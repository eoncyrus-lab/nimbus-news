package nimbuscore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/envelope"
	"github.com/Ricaardo/nimbus-os/datasources/internal/controlstore"
)

func TestConfigRequiresExplicitLocations(t *testing.T) {
	valid := Config{DataListenAddr: "127.0.0.1:0", DataplaneListenAddr: "127.0.0.1:0", ControlDBPath: filepath.Join(t.TempDir(), "control.db")}
	cases := []Config{
		{DataplaneListenAddr: valid.DataplaneListenAddr, ControlDBPath: valid.ControlDBPath},
		{DataListenAddr: valid.DataListenAddr, ControlDBPath: valid.ControlDBPath},
		{DataListenAddr: valid.DataListenAddr, DataplaneListenAddr: valid.DataplaneListenAddr},
	}
	for _, config := range cases {
		if app, err := New(config); err == nil {
			app.Close()
			t.Fatalf("New(%+v) succeeded without explicit config", config)
		}
	}
}

func TestListenAddressesNormalizeOnlyToLoopbackLiterals(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "[::]:0", ":0"} {
		t.Run("reject "+address, func(t *testing.T) {
			if normalized, err := normalizeListenAddr(address); err == nil {
				t.Fatalf("normalizeListenAddr(%q) = %q", address, normalized)
			}
		})
	}
	for address, want := range map[string]string{
		"127.0.0.1:0": "127.0.0.1:0",
		"[::1]:0":     "[::1]:0",
		"localhost:0": "127.0.0.1:0",
		"LOCALHOST:0": "127.0.0.1:0",
	} {
		t.Run("allow "+address, func(t *testing.T) {
			normalized, err := normalizeListenAddr(address)
			if err != nil {
				t.Fatalf("normalizeListenAddr(%q): %v", address, err)
			}
			if normalized != want {
				t.Fatalf("normalizeListenAddr(%q) = %q, want %q", address, normalized, want)
			}
		})
	}
}

func TestListenAddressRejectsHostnamesWithoutDNSLookup(t *testing.T) {
	originalResolver := net.DefaultResolver
	var dialed atomic.Bool
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dialed.Store(true)
			return nil, errors.New("unexpected DNS lookup")
		},
	}
	t.Cleanup(func() { net.DefaultResolver = originalResolver })

	for _, address := range []string{"example.com:0", "localhost.evil.invalid:0"} {
		if normalized, err := normalizeListenAddr(address); err == nil {
			t.Fatalf("normalizeListenAddr(%q) = %q", address, normalized)
		}
	}
	if dialed.Load() {
		t.Fatal("hostname rejection performed a DNS lookup")
	}
}

func TestLocalhostBindsConcreteLoopbackAddresses(t *testing.T) {
	app, err := New(Config{
		DataListenAddr: "localhost:0", DataplaneListenAddr: "LOCALHOST:0",
		ControlDBPath: filepath.Join(t.TempDir(), "control.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	for _, address := range []string{app.DataAddr(), app.DataplaneAddr()} {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			t.Fatal(err)
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatalf("bound address %q is not a concrete loopback IP", address)
		}
	}
	if app.config.DataListenAddr != "127.0.0.1:0" || app.config.DataplaneListenAddr != "127.0.0.1:0" {
		t.Fatalf("normalized config = %+v", app.config)
	}
}

func TestConfigChecksBothListenerAddresses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	for _, config := range []Config{
		{DataListenAddr: "0.0.0.0:0", DataplaneListenAddr: "127.0.0.1:0", ControlDBPath: path},
		{DataListenAddr: "127.0.0.1:0", DataplaneListenAddr: "[::]:0", ControlDBPath: path},
	} {
		if err := validateConfig(config); err == nil {
			t.Fatalf("validateConfig(%+v) succeeded", config)
		}
	}
}

func TestLiveAndReadyGoldenResponses(t *testing.T) {
	app := newTestApp(t, filepath.Join(t.TempDir(), "control.db"))
	defer app.Close()

	live := httptest.NewRecorder()
	app.dataServer.Handler.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/live", nil))
	if live.Code != http.StatusOK || live.Body.String() != "{\"live\":true}\n" {
		t.Fatalf("live golden: code=%d body=%q", live.Code, live.Body.String())
	}
	ready := httptest.NewRecorder()
	app.dataServer.Handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusOK || ready.Body.String() != "{\"ready\":true}\n" {
		t.Fatalf("ready golden: code=%d body=%q", ready.Code, ready.Body.String())
	}

	if err := app.store.Close(); err != nil {
		t.Fatal(err)
	}
	unready := httptest.NewRecorder()
	app.dataServer.Handler.ServeHTTP(unready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if unready.Code != http.StatusServiceUnavailable || unready.Body.String() != "{\"ready\":false,\"error\":\"control database unavailable\"}\n" {
		t.Fatalf("unready golden: code=%d body=%q", unready.Code, unready.Body.String())
	}
	stillLive := httptest.NewRecorder()
	app.dataServer.Handler.ServeHTTP(stillLive, httptest.NewRequest(http.MethodGet, "/live", nil))
	if stillLive.Code != http.StatusOK || stillLive.Body.String() != "{\"live\":true}\n" {
		t.Fatalf("live must not probe DB: code=%d body=%q", stillLive.Code, stillLive.Body.String())
	}
}

func TestSnapshotControl(t *testing.T) {
	app := newTestApp(t, filepath.Join(t.TempDir(), "control.db"))
	defer app.Close()

	var destination bytes.Buffer
	meta, err := app.SnapshotControl(context.Background(), &destination)
	if err != nil {
		t.Fatalf("SnapshotControl() error = %v", err)
	}
	if meta.Size != int64(destination.Len()) || meta.Size == 0 || meta.SHA256 == "" || meta.SchemaVersion != 2 {
		t.Fatalf("SnapshotControl() meta = %+v, bytes=%d", meta, destination.Len())
	}

	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := os.WriteFile(path, destination.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var quickCheck string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&quickCheck); err != nil || quickCheck != "ok" {
		t.Fatalf("snapshot quick_check = %q, error=%v", quickCheck, err)
	}
}

func TestHostStrictLegacy(t *testing.T) {
	app := newTestApp(t, filepath.Join(t.TempDir(), "control.db"))
	defer app.Close()

	strict := httptest.NewRecorder()
	app.dataServer.Handler.ServeHTTP(strict, httptest.NewRequest(http.MethodGet, "/v1/quote", nil))
	observation, err := envelope.DecodeObservationEnvelope[any](strict.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if strict.Code != http.StatusBadRequest || observation.Status != envelope.ObservationStatusError || observation.Error == nil || observation.Error.Code != "invalid_request" {
		t.Fatalf("strict host response: code=%d observation=%+v", strict.Code, observation)
	}

	legacy := httptest.NewRecorder()
	app.dataServer.Handler.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/quote", nil))
	if legacy.Code != http.StatusBadRequest || legacy.Body.String() != "{\"error\":\"symbol required\"}\n" {
		t.Fatalf("legacy host response: code=%d body=%q", legacy.Code, legacy.Body.String())
	}
}

func TestHostWriterLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	first := newTestApp(t, path)
	defer first.Close()
	second, err := New(testConfig(path))
	if second != nil {
		second.Close()
		t.Fatal("second host unexpectedly opened the control database")
	}
	if !errors.Is(err, controlstore.ErrWriterActive) {
		t.Fatalf("writer conflict error = %v", err)
	}
}

func TestContextCancelClosesListenersAndControlStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	app := newTestApp(t, path)
	dataAddr := app.DataAddr()
	dataplaneAddr := app.DataplaneAddr()
	cancel, runErr := runTestApp(t, app)
	waitForLive(t, dataAddr)
	waitForLive(t, dataplaneAddr)
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run after cancel: %v", err)
	}
	for _, addr := range []string{dataAddr, dataplaneAddr} {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Fatalf("listener %s still accepts connections", addr)
		}
	}

	reopened := newTestApp(t, path)
	if err := reopened.Close(); err != nil {
		t.Fatalf("reopen after context cancellation: %v", err)
	}
}

func TestHostControlRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	app := newTestApp(t, path)
	cancel, runErr := runTestApp(t, app)
	waitForLive(t, app.DataAddr())

	ctx := context.Background()
	job, created, err := app.store.CreateJob(ctx, controlstore.NewJob{
		Kind: "recovery-test", Producer: "nimbuscore-test", TraceID: "trace-1", IdempotencyKey: "job-1",
	}, []controlstore.NewStep{{Name: "persist-failure"}})
	if err != nil || !created {
		t.Fatalf("CreateJob: created=%v err=%v", created, err)
	}
	if _, err := app.store.StartJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	stepID := lookupStepID(t, path, job.ID)
	step, err := app.store.StartStep(ctx, job.ID, stepID, "worker-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.FailStepAndJob(ctx, step.ID, step.LeaseToken, controlstore.Failure{
		Code: "terminal", Message: "expected failure", Retryable: false,
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-runErr; err != nil {
		t.Fatal(err)
	}

	reopened := newTestApp(t, path)
	defer reopened.Close()
	recovered, err := reopened.store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != controlstore.JobFailed || recovered.Failure == nil || recovered.Failure.Code != "terminal" {
		t.Fatalf("recovered job = %+v", recovered)
	}
}

func newTestApp(t *testing.T, path string) *App {
	t.Helper()
	app, err := New(testConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func testConfig(path string) Config {
	return Config{
		DataListenAddr: "127.0.0.1:0", DataplaneListenAddr: "127.0.0.1:0", ControlDBPath: path,
	}
}

func runTestApp(t *testing.T, app *App) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() { errs <- app.Run(ctx) }()
	return cancel, errs
}

func waitForLive(t *testing.T, addr string) {
	t.Helper()
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + addr + "/live")
		if err == nil {
			var body struct {
				Live bool `json:"live"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil && body.Live {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("host %s did not become live", addr)
}

func lookupStepID(t *testing.T, path, jobID string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var stepID string
	if err := db.QueryRow(`SELECT id FROM step_runs WHERE job_id = ?`, jobID).Scan(&stepID); err != nil {
		t.Fatal(err)
	}
	return stepID
}
