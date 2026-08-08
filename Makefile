# Makefile — works on Mac and Linux.
# Windows users: use `task <target>` (Taskfile.yml) or `go run ./scripts/<script>`.
# Both tools produce identical results.
#
# Run `make help` for a list of targets.

BINARY    := overcast
BUILD_DIR := ./bin
GO        := go
AWS_MODELS_REVISION ?= $(shell sed -n 's/^revision=//p' models/aws/VERSION)
GOFLAGS   := -trimpath
VERSION   := $(shell cat VERSION)
LDFLAGS   := -w -s -X main.version=$(VERSION)
ACTIONLINT_VERSION := v1.7.7
# golangci-lint v2.x — v2 config schema (.golangci.yml declares `version: "2"`).
# v2.8.0 is the newest release that still builds with the Go 1.24 toolchain in
# go.mod; v2.10.0+ requires Go 1.25. Keep in sync with .devcontainer/Dockerfile
# and CONTRIBUTING.md.
GOLANGCI_LINT_VERSION := v2.8.0

.PHONY: help setup build build-web build-slim build-cross \
        build-linux-amd64 build-linux-arm64 \
        build-darwin-amd64 build-darwin-arm64 \
        build-windows-amd64 \
        build-slim-linux-amd64 build-slim-linux-arm64 \
        build-slim-darwin-amd64 build-slim-darwin-arm64 \
        build-slim-windows-amd64 \
        run test test-unit test-integration test-coverage \
        ci-local ci-local-web ci-local-go \
        bench bench-startup lint lint-go lint-web lint-actions fmt vet tidy check verify aws-models-check docker docker-slim docker-console docker-run clean \
        compat compat-build compat-serve compat-dev compat-docker compat-report compat-registry-check \
        generate-caps check-caps generate-aws-operations aws-models-check docs docs-index docs-check supportmeta-check check-binary-symbols

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

## build: compile the overcast binary for the current platform (includes embedded web UI)
build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/overcast

## build-mcp: compile the workspace MCP server binary
build-mcp:
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/overcast-mcp ./cmd/overcast-mcp

## build-web: build the web UI (run before build if assets are stale)
build-web:
	cd web && VITE_BUNDLED=true pnpm run build

## build-slim: compile the slim binary (no web UI, no SQLite) for the current platform
build-slim:
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -tags slim,nosqlite -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/overcastd ./cmd/overcast

## build-cross: compile release binaries for all supported platforms
build-cross: build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64 \
             build-slim-linux-amd64 build-slim-linux-arm64 build-slim-darwin-amd64 build-slim-darwin-arm64 build-slim-windows-amd64

## build-linux-amd64: compile overcast for Linux x86-64
build-linux-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux  GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64        ./cmd/overcast

## build-linux-arm64: compile overcast for Linux ARM64 (Raspberry Pi, AWS Graviton, etc.)
build-linux-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux  GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-arm64        ./cmd/overcast

## build-darwin-amd64: compile overcast for macOS Intel
build-darwin-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-amd64       ./cmd/overcast

## build-darwin-arm64: compile overcast for macOS Apple Silicon
build-darwin-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-arm64       ./cmd/overcast

## build-windows-amd64: compile overcast for Windows x86-64 (console .exe)
build-windows-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe ./cmd/overcast

## build-slim-linux-amd64: compile slim overcastd for Linux x86-64
build-slim-linux-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux  GOARCH=amd64 $(GO) build $(GOFLAGS) -tags slim,nosqlite -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/overcastd-linux-amd64        ./cmd/overcast

## build-slim-linux-arm64: compile slim overcastd for Linux ARM64
build-slim-linux-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux  GOARCH=arm64 $(GO) build $(GOFLAGS) -tags slim,nosqlite -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/overcastd-linux-arm64        ./cmd/overcast

## build-slim-darwin-amd64: compile slim overcastd for macOS Intel
build-slim-darwin-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -tags slim,nosqlite -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/overcastd-darwin-amd64       ./cmd/overcast

## build-slim-darwin-arm64: compile slim overcastd for macOS Apple Silicon
build-slim-darwin-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -tags slim,nosqlite -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/overcastd-darwin-arm64       ./cmd/overcast

## build-slim-windows-amd64: compile slim overcastd for Windows x86-64
build-slim-windows-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -tags slim,nosqlite -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/overcastd-windows-amd64.exe ./cmd/overcast

## run: build and run with dev defaults (uses cross-platform Go script)
run:
	$(GO) run ./scripts/run.go

## dev-server: watch Go sources and hot-reload the server (requires air)
dev-server:
	air

## test: run all tests (unit + integration, with race detector where supported)
test:
	$(GO) test -race -count=1 -timeout=1200s ./...

## test-unit: run unit tests only (fast — no server startup)
test-unit:
	$(GO) test -race -count=1 -timeout=900s ./internal/...

## test-integration: run integration tests only
test-integration:
	$(GO) test -race -count=1 -timeout=600s ./tests/...

## test-coverage: run tests and generate HTML coverage report
test-coverage:
	$(GO) test -race -count=1 -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report written to coverage.html"

## bench: run benchmarks with memory allocation reporting
bench:
	$(GO) test -bench=. -benchmem -count=3 ./...

## bench-startup: measure cold-start time across all storage backends (pre-release gate)
bench-startup:
	$(GO) run ./scripts/bench-startup.go

## lint: run all linters (Go/emulation, web UI, GitHub Actions)
lint: lint-go lint-web lint-actions

## lint-go: run golangci-lint for Go/emulation code (pinned via go run — no install needed)
lint-go:
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...
	@bash scripts/verify-changed.sh --record go

## lint-web: run web UI linting
lint-web:
	cd web && pnpm run lint

## lint-actions: lint GitHub Actions workflows
lint-actions:
	$(GO) run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) .github/workflows/*.yml

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

## verify: run the required-CI checks this branch touches (cached — a no-op if nothing changed)
verify:
	bash scripts/verify-changed.sh $(ARGS)

## ci-local: run the whole CI pipeline locally (Go via Docker — no host Go needed)
ci-local:
	bash scripts/ci-local.sh $(ARGS)

## ci-local-web: web stages only (lint, typecheck, vitest, build)
ci-local-web:
	bash scripts/ci-local.sh --web-only

## ci-local-go: Go stages only (docs index, vet, build, unit tests)
ci-local-go:
	bash scripts/ci-local.sh --go-only

## generate-caps: generate internal/capabilities/all.gen.go from per-service capabilities_dev.go files
generate-caps:
	$(GO) run -tags dev ./cmd/capgen --generate

## check-caps: verify capabilities declarations match handler operation registrations
check-caps:
	$(GO) run -tags dev ./cmd/capgen --check

## generate-aws-operations: regenerate the AWS operation manifest from a pinned local api-models-aws checkout
## Set AWS_MODELS_DIR to its models directory. The checked-out commit must match models/aws/VERSION.
generate-aws-operations:
	@test -n "$(AWS_MODELS_DIR)" || (echo "ERROR: set AWS_MODELS_DIR to api-models-aws/models" && exit 1)
	@test -n "$(AWS_MODELS_REVISION)" || (echo "ERROR: set AWS_MODELS_REVISION to the api-models-aws commit" && exit 1)
	$(GO) run ./cmd/awsmodelgen -models "$(AWS_MODELS_DIR)" -output internal/awsapi/manifest.gen.go -source-revision "$(AWS_MODELS_REVISION)"

## aws-models-check: validate the committed AWS operation corpus and generated runtime ownership indexes
## Set AWS_MODELS_DIR to additionally prove the committed manifest matches that pinned checkout byte-for-byte.
aws-models-check:
	$(GO) test -count=1 ./cmd/awsmodelgen ./internal/awsapi ./internal/protocol/codec ./tests/integration/router
	$(GO) run -tags dev ./cmd/capgen --check-model
	@if [ -n "$(AWS_MODELS_DIR)" ]; then \
		$(GO) run ./cmd/awsmodelgen -models "$(AWS_MODELS_DIR)" -output internal/awsapi/manifest.gen.go -source-revision "$(AWS_MODELS_REVISION)" -check; \
	fi

## docs-index: regenerate the committed docs search/navigation index (run after editing docs/, then commit)
docs-index:
	$(GO) run ./scripts/docs-index.go --write-nav --write-search-index

## docs: regenerate sentinel-bracketed capability tables in docs/services/*.md
docs: generate-caps
	$(GO) run -tags dev ./cmd/capgen --write-docs
	$(GO) run ./scripts/docs-index.go --write-nav --write-search-index

## docs-check: verify docs capability tables, all.gen.go, and STATUS.md are up to date, and every doc has frontmatter (CI gate)
docs-check: check-caps
	$(GO) run -tags dev ./cmd/capgen --generate
	@git diff --exit-code internal/capabilities/all.gen.go \
		|| (echo "ERROR: internal/capabilities/all.gen.go is stale. Run: make generate-caps" && exit 1)
	$(GO) run -tags dev ./cmd/capgen --write-docs
	$(GO) run ./scripts/docs-index.go --check
	@git diff --exit-code README.md STATUS.md docs/README.md docs/cdk.md docs/services/ docs/generated/service-support.json \
		|| (echo "ERROR: README.md, STATUS.md, docs/README.md, docs/cdk.md, docs/services/, or docs/generated/service-support.json are stale. Run: make docs" && exit 1)

## supportmeta-check: alias for docs-check (manifest schema, registry parity, docs parity, generated artifacts)
supportmeta-check: docs-check

## check-binary-symbols: verify no capability symbols leak into the production binary
check-binary-symbols:
	bash scripts/check-binary-symbols.sh

## docker: build both Docker images (console + slim)
docker: docker-console docker-slim

## docker-console: build the Docker image with web console (default target)
docker-console:
	docker build -t overcast:dev .

## docker-slim: build the slim Docker image (no web UI, no SQLite, for CI)
docker-slim:
	docker build --target slim --build-arg NOSQLITE=1 -t overcast-slim:dev .

## docker-run: run the Docker image with debug logging (mounts Docker socket for Lambda)
docker-run:
	docker run --rm -p 4566:4566 \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-e OVERCAST_LOG_LEVEL=debug overcast:dev

## clean: remove build artefacts
clean:
	$(GO) clean ./...
	@rm -rf $(BUILD_DIR) coverage.out coverage.html

# ---- Compat dashboard -------------------------------------------------------
# Every target manages its own throwaway Overcast instance on free ports —
# 4566/4567 stay yours. Pass ARGS='--endpoint http://localhost:4566' to target
# an instance you are already running.

## compat: run the compat suites headlessly (starts its own Overcast instance)
compat:
	$(GO) run ./cmd/compat $(ARGS)

## compat-build: build the compat UI and embed it into the compat binary
compat-build:
	@echo "Building compat UI…"
	cd compat/ui && npm run build
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/compat ./cmd/compat

## compat-serve: start the compat dashboard with a freshly built UI
compat-serve:
	$(GO) run ./cmd/compat --serve --interactive --build-ui --open $(ARGS)

## compat-dev: start the compat dashboard with a hot-reloading UI
compat-dev:
	$(GO) run ./cmd/compat --dev $(ARGS)

## compat-docker: run the compat suites entirely in containers (no host Go or Node)
compat-docker:
	docker compose -f compat/docker-compose.yml run --rm compat

## compat-report: print an agent-friendly summary of the last compat run (reads compat-results.json)
compat-report:
	$(GO) run ./cmd/compat --report

## compat-registry-check: validate compat/suites/registry.json against registry.schema.json
compat-registry-check:
	python3 scripts/validate-compat-registry.py

# ---- Container-based development (cross-platform) --------------------------
# These targets work identically on Mac, Linux, and Windows.
# They require Docker but not a local Go installation.

# These go through scripts/container-test.sh rather than calling docker compose
# directly: it derives the container's CPU cap from the host and hands it to the
# Compose file, which cannot compute one itself. See that script's header.

## container-test: run all tests inside a Linux container (cross-platform)
container-test:
	bash scripts/container-test.sh

## container-test-unit: run unit tests inside a container
container-test-unit:
	bash scripts/container-test.sh -race -count=1 -timeout=900s ./internal/...

## container-test-integration: run integration tests inside a container
container-test-integration:
	bash scripts/container-test.sh -race -count=1 -timeout=600s ./tests/...

## dev: start the development server with source mounted (rebuilds on --build)
dev:
	docker compose -f docker-compose.dev.yml up overcast

## dev-build: force rebuild of the dev server
dev-build:
	docker compose -f docker-compose.dev.yml up --build overcast
