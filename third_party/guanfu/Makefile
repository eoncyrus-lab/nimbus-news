.PHONY: build test run clean vet mcp all similar snapshot install

# Binary name
BIN := bin/guanfu
MCP_BIN := bin/guanfu-mcp
SIMILAR_BIN := bin/guanfu-similar

# Build flags — embed git version info so `guanfu --version` works on local builds.
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/Ricaardo/guanfu/pkg/version.Version=$(VERSION) \
	-X github.com/Ricaardo/guanfu/pkg/version.Commit=$(COMMIT) \
	-X github.com/Ricaardo/guanfu/pkg/version.Date=$(DATE)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/guanfu

test:
	go test ./... -count=1

vet:
	go vet ./...

run: build
	GUANFU_NO_HISTORY=1 $(BIN)

run-json: build
	GUANFU_NO_HISTORY=1 $(BIN) --json

clean:
	rm -f $(BIN) $(MCP_BIN) $(SIMILAR_BIN)

# Install to $GOPATH/bin
install:
	go install ./cmd/guanfu

# MCP server
mcp:
	go build -ldflags "$(LDFLAGS)" -o $(MCP_BIN) ./cmd/guanfu-mcp

similar:
	go build -ldflags "$(LDFLAGS)" -o $(SIMILAR_BIN) ./cmd/guanfu-similar

all: vet test build mcp similar

snapshot:
	goreleaser release --snapshot --clean
