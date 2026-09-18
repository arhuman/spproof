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

GOLANGCI_VERSION   := v2.1.6
GOVULNCHECK_VERSION := latest

.PHONY: audit bench build clean cover fulltest help release test tidy tools

## audit: vet, staticcheck and vulnerability scan
audit: cover
	@go vet ./...
	@which golangci-lint > /dev/null && golangci-lint run ./... || echo "golangci-lint not installed, skipping"
	@which staticcheck > /dev/null && staticcheck ./... || echo "staticcheck not installed, skipping"
	@which govulncheck > /dev/null && govulncheck ./... || echo "govulncheck not installed, skipping"

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

## tools: install development tools
tools:
	@go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	@go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
