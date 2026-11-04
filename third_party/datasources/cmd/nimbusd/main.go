// Command nimbusd starts the integrated Nimbus Go host. It is not wired to a
// production launchd job yet; every listener and database path must be supplied
// explicitly by flags or NIMBUSD_* environment variables.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Ricaardo/nimbus-os/datasources/app/nimbuscore"
)

func main() {
	dataAddr := flag.String("data-listen", os.Getenv("NIMBUSD_DATA_ADDR"), "data listener address (required)")
	dataplaneAddr := flag.String("dataplane-listen", os.Getenv("NIMBUSD_DATAPLANE_ADDR"), "dataplane listener address (required)")
	controlDB := flag.String("control-db", os.Getenv("NIMBUSD_CONTROL_DB_PATH"), "control SQLite database path (required)")
	flag.Parse()

	app, err := nimbuscore.New(nimbuscore.Config{
		DataListenAddr:      *dataAddr,
		DataplaneListenAddr: *dataplaneAddr,
		ControlDBPath:       *controlDB,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer app.Close()

	fmt.Printf("nimbusd data listening on http://%s\n", app.DataAddr())
	fmt.Printf("nimbusd dataplane listening on http://%s\n", app.DataplaneAddr())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
