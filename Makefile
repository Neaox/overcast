# Makefile — works on Mac and Linux.
# Windows users: use `task <target>` (Taskfile.yml) or `go run ./scripts/<script>`.
# Both tools produce identical results.
#
# Run `make help` for a list of targets.

BINARY    := overcast
BUILD_DIR := ./bin
GO        := go
GOFLAGS   := -trimpath
LDFLAGS   := -w -s

.PHONY: help setup build run test test-unit test-integration test-coverage \
        bench lint fmt vet tidy check docker docker-run clean

## help: print this help message
help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "Windows: use 'task <target>' (Taskfile.yml) — all targets are equivalent."
	@echo "Any platform: use 'go run ./scripts/<script>' for zero-install scripts."
	@echo ""
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## setup: verify all dev tools are installed (cross-platform)
setup:
	$(GO) run ./scripts/check-tools.go

## build: compile the binary for the current platform
build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/overcast

## run: build and run with dev defaults (uses cross-platform Go script)
run:
	$(GO) run ./scripts/run.go

## dev-server: watch Go sources and hot-reload the server (requires air)
dev-server:
	air

## test: run all tests (unit + integration, with race detector where supported)
test:
	$(GO) test -race -count=1 -timeout=120s ./...

## test-unit: run unit tests only (fast — no server startup)
test-unit:
	$(GO) test -race -count=1 -timeout=60s ./internal/...

## test-integration: run integration tests only
test-integration:
	$(GO) test -race -count=1 -timeout=120s ./tests/...

## test-coverage: run tests and generate HTML coverage report
test-coverage:
	$(GO) test -race -count=1 -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report written to coverage.html"

## bench: run benchmarks with memory allocation reporting
bench:
	$(GO) test -bench=. -benchmem -count=3 ./...

## lint: run golangci-lint (install: https://golangci-lint.run/usage/install/)
lint:
	golangci-lint run ./...

## fmt: format all Go source files
fmt:
	$(GO) fmt ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## tidy: tidy and verify go.mod / go.sum
tidy:
	$(GO) mod tidy
	$(GO) mod verify

## check: run all pre-PR checks (fmt + vet + lint + test)
check: fmt vet lint test

## docker: build the Docker image
docker:
	docker build -t overcast:dev .

## docker-run: run the Docker image with debug logging
docker-run:
	docker run --rm -p 4566:4566 -e OVERCAST_LOG_LEVEL=debug overcast:dev

## clean: remove build artefacts
clean:
	$(GO) clean ./...
	@rm -rf $(BUILD_DIR) coverage.out coverage.html

# ---- Container-based development (cross-platform) --------------------------
# These targets work identically on Mac, Linux, and Windows.
# They require Docker but not a local Go installation.

## container-test: run all tests inside a Linux container (cross-platform)
container-test:
	docker compose -f docker-compose.dev.yml run --rm test

## container-test-unit: run unit tests inside a container
container-test-unit:
	docker compose -f docker-compose.dev.yml run --rm test \
		go test -race -count=1 -timeout=60s ./internal/...

## container-test-integration: run integration tests inside a container
container-test-integration:
	docker compose -f docker-compose.dev.yml run --rm test \
		go test -race -count=1 -timeout=120s ./tests/...

## dev: start the development server with source mounted (rebuilds on --build)
dev:
	docker compose -f docker-compose.dev.yml up overcast

## dev-build: force rebuild of the dev server
dev-build:
	docker compose -f docker-compose.dev.yml up --build overcast
