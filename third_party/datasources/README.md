# nimbus-datasources

Go-first, **one independent package per data source**. Upper layers consume them:

- **Go consumers** (guanfu, news) `import` the packages directly (typed, no HTTP hop).
- **Python consumers** (equity-screener, nimbus, market-pulse) reach them via the
  resident Go gateway `cmd/datagw` (8821), fronted by the data-access facade (8800)
  — same HTTP contract as the legacy Python data-gateway, so apps are unchanged.

```
internal/httpx/   shared retry HTTP client (the only cross-package util)
envelope/         standard {provider,endpoint,params,retrieved_at,raw_hash,data} wrapper
yahoo/            quotes + daily history (chart v8) — replaces 5 scattered impls
binance/          spot ticker/klines + funding / open interest / long-short
okx/              ticker/candles + funding / open interest / long-short
deribit/          DVOL implied-vol index + option mark IV & greeks
internal/dataplaneapp/ shared route and lifecycle assembly for the data core
cmd/datagw/       compatibility HTTP gateway entrypoint (port 8821)
cmd/nimbusd/      phase-0 compatibility entrypoint for the same data core
```

Each source package is self-contained (own types, calls `internal/httpx`, no
cross-package deps). Adding a source = new package + a route in
`internal/dataplaneapp`.

## Run the gateway

Choose one entrypoint; both expose the same phase-0 data plane and must not bind
the same address at the same time:

```
go build -o bin/datagw ./cmd/datagw && DATAGW_PORT=8821 ./bin/datagw
# Or use the future consolidated daemon entrypoint:
go build -o bin/nimbusd ./cmd/nimbusd && DATAGW_PORT=8821 ./bin/nimbusd
```

Resident via launchd: `com.nimbus.datagw`.

## Local consumer wiring (Go)

Sibling repos import via a go.mod replace:

```
require github.com/Ricaardo/nimbus-datasources v0.0.0
replace github.com/Ricaardo/nimbus-datasources => ../datasources
```
