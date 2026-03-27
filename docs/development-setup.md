# Development setup

This guide covers getting Overcast building, testing, and running on
Mac, Linux, and Windows. There are three approaches — pick the one that
fits your environment.

---

## TL;DR — which approach should I use?

| Your situation | Recommended approach |
|----------------|---------------------|
| Windows (any setup) | **Option A: Dev Container** |
| Mac or Linux, want simplest setup | **Option A: Dev Container** |
| Mac or Linux, prefer native tools | **Option B: Native** |
| No local Go, just Docker | **Option C: Docker Compose** |
| GitHub Codespaces | **Option A: Dev Container** (works automatically) |

---

## Option A: Dev Container (recommended — works identically on all platforms)

A Dev Container is a Docker-based development environment that VS Code connects
to transparently. You edit files on your host machine — they're mounted into a
Linux container where builds and tests run. The result is an identical
environment for every contributor regardless of OS.

**Why this is the best option for Windows:** no manual tool installation, no
WSL2 setup, no path or line-ending issues, race detector always works (Linux),
and the exact same workflow as Mac/Linux contributors.

### Prerequisites

1. [Docker Desktop](https://docs.docker.com/get-docker/) (Windows / Mac) or Docker Engine (Linux)
2. [VS Code](https://code.visualstudio.com/)
3. VS Code extension: **Dev Containers** (`ms-vscode-remote.remote-containers`)

### First-time setup

```
1. Clone the repository
2. Open the project folder in VS Code
3. VS Code will detect .devcontainer/devcontainer.json and show a notification:
   "Folder contains a Dev Container configuration file. Reopen in Container?"
4. Click "Reopen in Container"  (or: Ctrl+Shift+P → "Dev Containers: Reopen in Container")
5. First build takes ~2–3 minutes (downloads Go image, installs tools)
6. Subsequent opens take ~5 seconds
```

Once open, the integrated terminal is inside the container. Everything works:

```bash
make test          # run all tests with race detector
make run           # build and start the server on :4566
make check         # full pre-PR check (fmt + vet + lint + test)
```

Port 4566 is forwarded automatically — your AWS CLI or SDK on Windows can hit
`http://localhost:4566` as normal.

### Daily workflow in the Dev Container

```bash
# In the VS Code integrated terminal (which is inside the container):

make test-unit         # fast feedback during development
make test-integration  # full integration suite

# Run the server (port 4566 is forwarded to your host):
make run

# From a separate terminal on your host (Windows PowerShell, cmd, etc.):
aws --endpoint-url http://localhost:4566 s3 mb s3://test-bucket
```

### Useful VS Code commands in Dev Container mode

- `Ctrl+Shift+P → "Dev Containers: Rebuild Container"` — rebuild if the Dockerfile changes
- `Ctrl+Shift+P → "Dev Containers: Reopen Folder Locally"` — go back to native mode
- Test Explorer (beaker icon in sidebar) — run individual tests with a click
- Breakpoints work normally — the Go debugger runs inside the container

---

## Option B: Native (Mac / Linux)

Direct install on the host. Fastest for Mac/Linux contributors who already have
Go installed.

### Prerequisites

```bash
# Mac (Homebrew)
brew install go         # 1.23+
brew install golangci-lint
brew install go-task    # optional — make works on Mac/Linux

# Ubuntu/Debian
sudo apt-get install golang-go
# golangci-lint: https://golangci-lint.run/usage/install/
```

### Setup

```bash
git clone https://github.com/your-org/overcast
cd overcast

# Verify tools
go run ./scripts/check-tools.go

# Install Go dependencies
go mod tidy

# Run tests
make test

# Start server
make run
# → overcast listening on :4566
```

---

## Option C: Docker Compose (any platform, no local Go needed)

If you have Docker but not Go, you can build and test entirely inside containers.
Your source files are mounted — edits on the host are instantly visible.

```bash
# Run all tests (Linux container, race detector enabled):
docker compose -f docker-compose.dev.yml run --rm test

# Run unit tests only:
docker compose -f docker-compose.dev.yml run --rm test \
  go test -race -count=1 -timeout=60s ./internal/...

# Start the development server:
docker compose -f docker-compose.dev.yml up overcast
# → overcast listening on :4566 (port forwarded to your host)

# Rebuild after code changes:
docker compose -f docker-compose.dev.yml up --build overcast
```

This is also available as VS Code tasks: `Ctrl+Shift+P → "Tasks: Run Task" → "container: test all"`.

---

## Windows native (advanced)

Native Windows with Go installed directly. Works for `go build` and `go test`
(CGO is not needed — we use pure-Go SQLite). Makefile doesn't work natively;
use `task` instead.

### Prerequisites

1. [Go 1.23+](https://go.dev/dl/)
2. [Task](https://taskfile.dev/installation/) — `scoop install task` or `winget install Task.Task`
3. [golangci-lint](https://golangci-lint.run/usage/install/) — `scoop install golangci-lint`
4. [Git for Windows](https://git-scm.com/download/win) — with "Use Unix-style line endings" (LF)

### Setup

```powershell
git clone https://github.com/your-org/overcast
cd overcast

# Check tools
go run ./scripts/check-tools.go

# Install Go dependencies
go mod tidy

# Run tests (use task, not make)
task test

# Start server
task run
# → overcast listening on :4566
```

### Line endings on Windows

Configure Git before cloning to avoid CRLF issues:

```powershell
git config --global core.autocrlf false
git config --global core.eol lf
```

The `.gitattributes` file enforces LF in the repository. With `core.autocrlf false`
your working copy will also use LF, which is what Go tools expect.

---

## CI — GitHub Actions

CI runs on all three platforms automatically:

```
ubuntu-latest  → full test suite + race detector + lint + coverage
macos-latest   → full test suite + race detector
windows-latest → full test suite + race detector (amd64 only)
```

You don't need to test on all platforms locally — push your branch and CI will
catch any platform-specific issues. The recommended workflow is:

1. Develop and test locally using your preferred option above
2. Run `make check` (or `task check`) before pushing
3. CI validates on all three platforms

---

## Build tool reference

All three tools produce identical results:

| Target | make | task | go run (zero-install) |
|--------|------|------|-----------------------|
| Build | `make build` | `task build` | `go build ./cmd/overcast` |
| Run | `make run` | `task run` | `go run ./scripts/run.go` |
| Test | `make test` | `task test` | `go test -race ./...` |
| Unit test | `make test-unit` | `task test-unit` | `go test -race ./internal/...` |
| Integration | `make test-integration` | `task test-integration` | `go test -race ./tests/...` |
| Lint | `make lint` | `task lint` | `golangci-lint run ./...` |
| Format | `make fmt` | `task fmt` | `go fmt ./...` |
| Pre-PR check | `make check` | `task check` | run fmt + vet + lint + test manually |
| Container test | `make container-test` | `task container-test` | `docker compose -f docker-compose.dev.yml run --rm test` |

## Step debugging

Full step debugging with breakpoints is supported in all development setups.
See **[docs/debugging.md](./debugging.md)** for the complete guide.

Quick version for the Dev Container:
1. Click left of a line number to set a breakpoint (red dot appears)
2. Press **F5** → select "Debug: server (memory state)"
3. Send a request from another terminal — debugger pauses at your breakpoint
4. F10 = step over · F11 = step into · hover to inspect variables

---
