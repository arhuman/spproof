.DEFAULT_GOAL := help

BINARY      := spproof
CMD         := ./cmd/spproof
BUILD_DIR   := bin
COVER_MIN   := 75
COVER_FILE  := coverage.out

VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  := $(shell git show -s --format=%cI HEAD 2>/dev/null || echo unknown)
VERSION_PKG := github.com/arhuman/spproof/internal/version
LDFLAGS     := -s -w \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).GitCommit=$(COMMIT) \
	-X $(VERSION_PKG).BuildDate=$(BUILD_DATE)

GOLANGCI_VERSION    := v2.13.2
GOVULNCHECK_VERSION := v1.7.0

.PHONY: audit bench build clean cover fulltest help install release test tidy tools

## audit: vet, lint and vulnerability scan
# Each tool is installed when missing or when the copy on PATH does not match the
# pin, then run on its own unconditional line. A `&& run || echo skipping` guard
# would let a machine without the tools exit 0 having scanned nothing, and a bare
# presence test would accept an older analyzer set than CI runs.
# staticcheck is not invoked separately: golangci-lint v2 runs its analyzers.
audit: cover
	@go vet ./...
	@$(MAKE) --no-print-directory require-tools
	go mod verify
	golangci-lint run ./...
	govulncheck ./...

# require-tools installs a tool unless the installed version already equals the
# pin. Comparing the version, not merely the binary's presence, is the point:
# `which <tool> || install` leaves an older copy in place, so the local gate runs
# a weaker analyzer set than CI and passes on findings CI will reject.
.PHONY: require-tools
require-tools:
	@have=$$(golangci-lint --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
	if [ "v$$have" != "$(GOLANGCI_VERSION)" ]; then \
		echo "golangci-lint $${have:-absent} != $(GOLANGCI_VERSION), installing"; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION); \
	fi
	@have=$$(govulncheck -version 2>/dev/null | sed -n 's/^Scanner: govulncheck@v\(.*\)$$/\1/p' | head -1); \
	if [ "v$$have" != "$(GOVULNCHECK_VERSION)" ]; then \
		echo "govulncheck $${have:-absent} != $(GOVULNCHECK_VERSION), installing"; \
		go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION); \
	fi

## bench: run benchmarks
bench:
	@go test ./... -bench=. -benchmem -run '^$$'

## build: compile the binary with version metadata
build:
	@mkdir -p $(BUILD_DIR)
	@go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) $(CMD)

## clean: remove build and coverage artifacts
clean:
	@rm -rf $(BUILD_DIR) $(COVER_FILE)

## cover: run tests with coverage and enforce the minimum
cover:
	@go test -race -coverprofile=$(COVER_FILE) -covermode=atomic ./...
	@go tool cover -func=$(COVER_FILE) | tail -1
	@total=$$(go tool cover -func=$(COVER_FILE) | tail -1 | awk '{print $$3}' | tr -d '%'); \
	if [ $$(echo "$$total < $(COVER_MIN)" | bc -l) -eq 1 ]; then \
		echo "coverage $$total% is below the $(COVER_MIN)% minimum"; exit 1; \
	fi

## fulltest: run all tests with race detector and no cache
fulltest:
	@go test -race -count=1 ./...

## install: install spproof (from this checkout via Go, else the released binary)
install:
	@if command -v go > /dev/null 2>&1; then \
		echo "Installing spproof from this checkout"; \
		go install -ldflags "$(LDFLAGS)" $(CMD); \
	else \
		echo "Go not found, installing the released binary"; \
		./install.sh; \
	fi

## help: show this help
help:
	@echo "Usage: make [target]\n"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## release: derive the next version and tag it
release:
	@scripts/release.sh

## test: run unit tests
test:
	@go test ./...

## tidy: format code and tidy go.mod
tidy:
	@gofmt -w .
	@go mod tidy

## tools: install development tools at the pinned versions
tools: require-tools
