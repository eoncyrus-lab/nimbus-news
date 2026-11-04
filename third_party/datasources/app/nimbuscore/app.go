// Package nimbuscore provides the integrated Go application host. It owns the
// data-plane listeners and the single-writer control database as one lifecycle.
package nimbuscore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/controlstore"
	"github.com/Ricaardo/nimbus-os/datasources/internal/dataplaneapp"
)

const (
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 5 * time.Second
	readyTimeout      = 2 * time.Second
)

// Config contains every deployment-specific location used by the integrated
// host. Callers must provide all values; this package does not guess production
// ports or a production control database path.
type Config struct {
	DataListenAddr      string
	DataplaneListenAddr string
	ControlDBPath       string
}

// App owns both HTTP listeners and the control store's unique writer lock.
type App struct {
	config Config
	store  *controlstore.Store

	dataListener      net.Listener
	dataplaneListener net.Listener
	dataServer        *http.Server
	dataplaneServer   *http.Server

	alive atomic.Bool

	runMu      sync.Mutex
	runStarted bool
	shutdown   sync.Once
	closeErr   error
}

// ControlSnapshotMeta describes a standalone control database snapshot.
type ControlSnapshotMeta struct {
	Size          int64
	SHA256        string
	SchemaVersion int
}

// New validates the explicit configuration, acquires the unique control-store
// writer lock, and binds both listeners. Any conflict is a startup failure and
// all resources acquired before the failure are released.
func New(config Config) (*App, error) {
	normalizedConfig, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	config = normalizedConfig
	store, err := controlstore.Open(config.ControlDBPath, controlstore.Options{})
	if err != nil {
		return nil, fmt.Errorf("nimbuscore: open control store: %w", err)
	}
	dataListener, err := net.Listen("tcp", config.DataListenAddr)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("nimbuscore: listen data %q: %w", config.DataListenAddr, err)
	}
	dataplaneListener, err := net.Listen("tcp", config.DataplaneListenAddr)
	if err != nil {
		_ = dataListener.Close()
		_ = store.Close()
		return nil, fmt.Errorf("nimbuscore: listen dataplane %q: %w", config.DataplaneListenAddr, err)
	}

	app := &App{
		config:            config,
		store:             store,
		dataListener:      dataListener,
		dataplaneListener: dataplaneListener,
	}
	dataHandler := dataplaneapp.NewDataHandler()
	dataplaneHandler := dataplaneapp.NewDataplaneHandler(dataHandler)
	app.dataServer = &http.Server{
		Addr: config.DataListenAddr, Handler: app.withHealth(dataHandler), ReadHeaderTimeout: readHeaderTimeout,
	}
	app.dataplaneServer = &http.Server{
		Addr: config.DataplaneListenAddr, Handler: app.withHealth(dataplaneHandler), ReadHeaderTimeout: readHeaderTimeout,
	}
	app.alive.Store(true)
	return app, nil
}

func validateConfig(config Config) error {
	_, err := normalizeConfig(config)
	return err
}

func normalizeConfig(config Config) (Config, error) {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"control database path", config.ControlDBPath},
	} {
		if strings.TrimSpace(field.value) == "" {
			return Config{}, fmt.Errorf("nimbuscore: %s is required", field.name)
		}
	}
	dataAddr, err := normalizeListenAddr(config.DataListenAddr)
	if err != nil {
		return Config{}, fmt.Errorf("nimbuscore: data listen address: %w", err)
	}
	dataplaneAddr, err := normalizeListenAddr(config.DataplaneListenAddr)
	if err != nil {
		return Config{}, fmt.Errorf("nimbuscore: dataplane listen address: %w", err)
	}
	config.DataListenAddr = dataAddr
	config.DataplaneListenAddr = dataplaneAddr
	return config, nil
}

func normalizeListenAddr(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", fmt.Errorf("address is required")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid address %q: %w", address, err)
	}
	if host == "" {
		return "", fmt.Errorf("wildcard address %q is forbidden", address)
	}
	if strings.EqualFold(host, "localhost") {
		return net.JoinHostPort("127.0.0.1", port), nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", fmt.Errorf("hostname %q is forbidden", host)
	}
	if !ip.IsLoopback() {
		return "", fmt.Errorf("non-loopback address %q is forbidden", address)
	}
	return net.JoinHostPort(ip.String(), port), nil
}

// DataAddr returns the bound data listener address. It is especially useful
// when tests explicitly configure port zero.
func (a *App) DataAddr() string {
	return a.dataListener.Addr().String()
}

// DataplaneAddr returns the bound facade-dialect listener address.
func (a *App) DataplaneAddr() string {
	return a.dataplaneListener.Addr().String()
}

// Ready performs a real query against the control database.
func (a *App) Ready(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nimbuscore: readiness context is required")
	}
	return a.store.Probe(ctx)
}

// SnapshotControl writes a consistent control database image without exposing
// the database handle, its path, or an arbitrary SQL capability.
func (a *App) SnapshotControl(ctx context.Context, destination io.Writer) (ControlSnapshotMeta, error) {
	meta, err := a.store.Snapshot(ctx, destination)
	if err != nil {
		return ControlSnapshotMeta{}, fmt.Errorf("nimbuscore: snapshot control: %w", err)
	}
	return ControlSnapshotMeta{
		Size:          meta.Size,
		SHA256:        meta.SHA256,
		SchemaVersion: meta.SchemaVersion,
	}, nil
}

// Run serves both listeners until ctx is cancelled or a listener fails. It
// gracefully drains HTTP requests before releasing the database writer lock.
// An App can be run only once.
func (a *App) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nimbuscore: run context is required")
	}
	a.runMu.Lock()
	if a.runStarted {
		a.runMu.Unlock()
		return fmt.Errorf("nimbuscore: app already run")
	}
	if !a.alive.Load() {
		a.runMu.Unlock()
		return fmt.Errorf("nimbuscore: app is closed")
	}
	a.runStarted = true
	a.runMu.Unlock()

	errs := make(chan error, 2)
	go func() { errs <- a.dataServer.Serve(a.dataListener) }()
	go func() { errs <- a.dataplaneServer.Serve(a.dataplaneListener) }()

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !isServerClosed(err) {
			serveErr = err
		}
	}
	closeErr := a.closeGracefully()
	if serveErr != nil {
		serveErr = fmt.Errorf("nimbuscore: serve: %w", serveErr)
	}
	return errors.Join(serveErr, closeErr)
}

// Close gracefully releases listeners and the control database. It is safe to
// call more than once and may be used when New succeeds but Run is not started.
func (a *App) Close() error {
	return a.closeGracefully()
}

func (a *App) closeGracefully() error {
	a.shutdown.Do(func() {
		a.alive.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		dataShutdownErr := a.dataServer.Shutdown(ctx)
		dataplaneShutdownErr := a.dataplaneServer.Shutdown(ctx)
		dataListenerErr := ignoreListenerClosed(a.dataListener.Close())
		dataplaneListenerErr := ignoreListenerClosed(a.dataplaneListener.Close())
		storeErr := a.store.Close()
		a.closeErr = errors.Join(dataShutdownErr, dataplaneShutdownErr, dataListenerErr, dataplaneListenerErr, storeErr)
		if a.closeErr != nil {
			a.closeErr = fmt.Errorf("nimbuscore: close: %w", a.closeErr)
		}
	})
	return a.closeErr
}

func (a *App) withHealth(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, _ *http.Request) {
		live := a.alive.Load()
		code := http.StatusOK
		if !live {
			code = http.StatusServiceUnavailable
		}
		writeHealthJSON(w, code, struct {
			Live bool `json:"live"`
		}{Live: live})
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		if err := a.Ready(ctx); err != nil {
			writeHealthJSON(w, http.StatusServiceUnavailable, struct {
				Ready bool   `json:"ready"`
				Error string `json:"error"`
			}{Ready: false, Error: "control database unavailable"})
			return
		}
		writeHealthJSON(w, http.StatusOK, struct {
			Ready bool `json:"ready"`
		}{Ready: true})
	})
	mux.Handle("/", next)
	return mux
}

func writeHealthJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func isServerClosed(err error) bool {
	return err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed)
}

func ignoreListenerClosed(err error) error {
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
