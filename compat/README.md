# Overcast Compatibility Tests

Compatibility test suites that verify standard AWS tooling (SDKs, CLI, CDK,
IaC) works correctly against Overcast without modification.

Tests are used as a **coverage metric**: every service and operation is tested,
including those not yet implemented. Failures on unimplemented features are
expected and tracked — this is how we measure what's left to build, and how we
guard against regressions in what's already working.

> **Two rules CI enforces**, documented in full at
> [AGENTS.md § Baseline & uniformity policy](./AGENTS.md#baseline--uniformity-policy):
>
> 1. **No new failures.** [baseline.json](./baseline.json) records every test's
>    expected status; a result that gets worse, or a new failing test, fails the
>    build. Improvements are promoted automatically on `main`.
> 2. **Every SDK/CLI suite tests the same operations.** Add to
>    [suites/registry.json](./suites/registry.json) first, then implement in all
>    of them. Gaps must be declared in [parity-debt.json](./parity-debt.json),
>    and that file only shrinks.
>
> Check both locally before pushing:
>
> ```sh
> go run ./cmd/compat --compare-baseline --results-file compat-results.json
> go run ./cmd/compat --check-parity --results-file compat-results.json
> ```

> **Separation boundary:** everything in `compat/` is a black-box external
> observer of Overcast. Each suite uses its SDK, CLI, or CDK tool **without
> modification** — the only difference from talking to real AWS is the endpoint
> URL. Nothing in `compat/` imports from `internal/`, routes, middleware, or
> any other part of the Overcast server. The emulator has no knowledge that
> compat exists. This boundary must never be crossed.

---

## Quick start

**The runner manages its own Overcast instance.** Unless you pin `--endpoint`,
`cmd/compat` starts a throwaway emulator on a **free port**, waits for
`/_overcast/health`, runs against it, and stops it on exit. Ports 4566 (API) and 4567
(web UI) are never bound — those belong to your own instance
([AGENTS.md § Reserved ports](../AGENTS.md#reserved-ports--4566-and-4567-belong-to-the-user)) —
so a compat run never disturbs whatever you have running, and two runs can go
at once. The dashboard port is probed the same way.

Dashboard with a hot-reloading UI, opened in your browser:

```bash
go run ./cmd/compat --dev
```

Same thing through whichever entry point you prefer — all four are the same
code path, so they behave identically on Windows, macOS, and Linux:

| Entry point | Command |
| --- | --- |
| Go directly | `go run ./cmd/compat --dev` |
| Task (any OS) | `task compat-dev` |
| Make | `make compat-dev` |
| Shell wrapper | `compat/dev.sh` · `compat\dev.ps1` |

For a stable, pre-built (non-HMR) dashboard, swap `--dev` for
`--serve --interactive --build-ui --open` — or use `task compat-serve`,
`make compat-serve`, `compat/run.sh`, `compat\run.ps1`.

Headless runs, no UI:

```bash
go run ./cmd/compat --format agent
```

```bash
go run ./cmd/compat --suite go-sdk --format json
```

Target an instance you are already running (nothing is started or stopped for
you):

```bash
go run ./cmd/compat --endpoint http://localhost:4566
```

Run via Docker — no host Go or Node required:

```bash
docker compose -f compat/docker-compose.yml run --rm compat
```

```bash
docker compose -f compat/docker-compose.yml up dashboard
```

The second command serves the dashboard at `http://localhost:7777`; set
`COMPAT_PORT` if that port is taken (a published container port is the one
thing compat cannot pick for you). Add `--wait` to block until it is actually
serving. A cold start — no image layers, no caches — takes about a minute;
afterwards a restart is under ten seconds, because the Go module and build
caches, the suites' `node_modules`, and the UI build all live in named volumes
rather than in your working tree. `docker compose -f compat/docker-compose.yml
down -v` discards those caches and returns you to a cold start.

All eight suites run in the containerised dashboard, including the four that
build and run their own images (`java-sdk`, `dotnet-sdk`, `rust-sdk`) and the
Lambda tests. Both the emulator and the runner mount the host Docker socket, so
those images are **siblings on the host daemon** rather than a nested daemon;
each suite runner already detects it is inside a container and joins the
runner's network namespace so the sibling can reach `overcast`. Without the
socket the emulator's Lambda, ECS, RDS, ElastiCache and MSK support is
metadata-only, and a compat run measures the stub instead of Overcast — which
is why `OVERCAST_COMPAT_SKIP_DOCKER` now defaults to `0`. Set it to `1` to skip
the Docker-dependent tests, and `COMPAT_DOCKER_SOCK` if your socket is not at
`/var/run/docker.sock`.

Mounting the socket gives those containers control of your Docker daemon. That
is the same trust the dev container and `make docker-run` already assume, but
it is worth knowing; the compose file is for local development, not CI on
shared infrastructure.

First build of the Java, .NET and Rust suite images takes a few minutes; they
are cached on the host daemon afterwards and start in seconds.

### Useful flags

| Flag | Default | What it does |
| --- | --- | --- |
| `--dev` | off | `--serve --interactive --ui-dev --open` in one switch |
| `--endpoint` | — | Target your own instance; disables instance management |
| `--start-overcast` | `auto` | `auto` \| `always` \| `never` |
| `--port-base` | `4570` | First port considered when scanning (never 4566/4567) |
| `--port` | `:7777` | *Preferred* dashboard port; a free one is picked if taken |
| `--overcast-bin` | — | Binary to run. Naming one is honoured or the run fails; left unset, the search is `bin/overcast`, then `PATH` |
| `--overcast-image` | `ghcr.io/neaox/overcast:alpha` | Image to run. **Naming one selects the container**, even when a local binary exists; left unset, it is only the fallback for when no binary is found |
| `--overcast-host` | `localhost` | Hostname the suites use — e.g. `localhost.overcast.sh` for virtual-host-style S3 |
| `--overcast-ui` | off | Also expose the managed instance's own web UI |
| `--mount-docker-socket` | **on** | Bind-mount the host Docker socket into a managed container, which is what lets the instance run Lambda and ECS containers. `COMPAT_DOCKER_SOCK` sets the host path |
| `--build-ui` | off | Build the dashboard UI before serving it |
| `--ui-dir` | — | Serve the dashboard UI from a directory instead of the embedded build |

A managed instance runs with `OVERCAST_STATE=memory` and its own web UI
disabled, so it leaves nothing behind.

A managed instance gets the host Docker socket, because Lambda and ECS start
containers through it and without one every Lambda invoke fails
([#867](https://github.com/Neaox/overcast/issues/867)). Compat then asks the
instance's own `/_overcast/health` whether it found a daemon, and says so once at
startup if it did not. When the *machine* is the reason — nothing to mount, or
no daemon here at all — the tests that need one are skipped
(`OVERCAST_COMPAT_SKIP_DOCKER=1`), because they cannot run here and reporting
them as failures blames the emulator for it. When compat mounted the socket and
the instance still reports no daemon, they are left to fail: that answer is the
emulator's own. Either way an `OVERCAST_COMPAT_SKIP_DOCKER` you set yourself is
left alone, which is how the compose file and CI keep deciding it for
themselves.

For GitHub Actions, a dedicated workflow is provided at
`.github/workflows/compat.yml`. It runs on every push to `main`, every PR to
`main`, on release creation, and on manual dispatch. It uses the native
`ubuntu-latest` runner (no Docker image builds) for fast startup and
standard GHA caching. Results are uploaded as a build artifact and written to
the job summary.

The compose file starts Overcast, health-checks it, then runs the Go CLI which
spawns each suite subprocess. Suite failures are expected; the CLI exits `0`.
Only infrastructure failures (Overcast failed to start, subprocess crashed)
produce a non-zero exit code.

---

## Build Performance Notes

**Test harness optimization:** Rust suite builds use the `dev` profile (no optimization) for **fast test builds**, not `release` profile. Test code prioritizes build speed over runtime performance.

**First build vs. subsequent builds:**

| Suite       | First Build | Cached Build | Profile | Why slow on first build?      |
| ----------- | ----------- | ------------ | ------- | ----------------------------- |
| node-js-sdk | ~30s        | ~5s          | default | npm ci + TypeScript check     |
| python-sdk  | ~20s        | ~3s          | default | pip install + fast lang       |
| rust-sdk    | ~3-5 min    | ~15-30s      | dev     | LLVM compilation + large deps |
| java-sdk    | ~2-3 min    | ~10-20s      | default | Maven + JVM startup           |
| dotnet-sdk  | ~2-3 min    | ~15-30s      | default | NuGet restore + .NET runtime  |

**Why Rust is slower than Go/Node on first build:**

1. **Compiler design**: Rust uses LLVM (powerful, slow backend). Go has a simple, fast compiler optimized for speed.
2. **Type system**: Rust's trait system, lifetime checking, and generic specialization add heavy compile-time work that Go doesn't have.
3. **Dependencies**: AWS SDK Rust has macro-heavy transitive deps (syn, proc-macro2, quote). Each macro expansion adds compilation overhead.
4. **Even with opt-level=0**: Rust still performs complex borrow checking, trait resolution, and type inference at compile time.

**Optimizations in place:**

- ✅ **Dev profile** for test code (no optimization, fastest possible build)
- ✅ **BuildKit cache mounts** (persists cargo registry + build artifacts across rebuilds)
- ✅ **`.dockerignore`** excludes unnecessary files from build context
- ✅ **Dependency caching** (first install is slow, subsequent builds reuse cached deps)

**To rebuild faster:**

```bash
# BuildKit is auto-enabled in recent Docker versions; explicit enable if needed:
DOCKER_BUILDKIT=1 docker build -f compat/suites/rust-sdk/Dockerfile -t oc-rust-sdk:latest compat/suites

# Subsequent builds reuse cached dependencies — only recompile changed code (~15-30s)
```

**Why subsequent builds are much faster:**

1. Cargo registry cache is persisted across builds
2. Compiled dependency binaries are cached
3. `cargo build` detects unchanged dependencies and reuses their builds
4. Only source changes (`src/`) trigger recompilation

---

## Running Stable Tests Without Rebuilds

Once test code is stable (not changing frequently), you can run tests directly without Docker rebuilds. This is ideal for CI/testing scenarios where **only Overcast changes**, not the test code:

**Option 1: Run pre-built Docker image (recommended for CI)**

```bash
# Build once
docker build -f compat/suites/rust-sdk/Dockerfile -t oc-rust-sdk:stable compat/suites

# Run many times — uses cached image (instant, no rebuild)
docker run --rm --network host \
  -e OVERCAST_ENDPOINT=http://localhost:4566 \
  oc-rust-sdk:stable
```

**Option 2: Run host binary directly (recommended for local dev)**

Build locally once, then run against Overcast without Docker overhead:

```bash
# Build once (with cargo caching, ~1m 20s on first build, ~0.5s on subsequent)
cd compat/suites/rust-sdk && cargo build

# Run many times — cargo detects no changes, instant startup
OVERCAST_ENDPOINT=http://localhost:4566 ./target/debug/rust_sdk_compat
```

**Performance comparison:**

| Scenario                    | Build Time | Run Time | Use Case             |
| --------------------------- | ---------- | -------- | -------------------- |
| CI: Docker image + tests    | 1-2m (1×)  | ~10s     | Reproducible, shared |
| Docker: pre-built image     | 0 (cached) | ~10s     | Stability testing    |
| Host: direct binary         | 0 (cached) | ~1s      | Local dev, fast loop |
| Host: cargo check unchanged | 0.5s       | ~1s      | Frequent test runs   |

**Key insight:** Once test code stabilizes, subsequent test runs are **instant or sub-second** because:

- Cargo detects no source changes and skips recompilation
- Docker layer cache skips rebuilds
- Test failures are due to Overcast changes, not test code issues

This breaks the edit-compile-test loop for test harnesses: **edit once, test many times**.

---

## Suites

### SDK Tests

| Suite         | Language   | SDK / Tool      | Status     |
| ------------- | ---------- | --------------- | ---------- |
| `node-js-sdk` | TypeScript | AWS SDK JS v3   | ✅ active  |
| `python-sdk`  | Python 3   | boto3           | ✅ active  |
| `go-sdk`      | Go 1.24    | AWS SDK Go v2   | ✅ active  |
| `java-sdk`    | Java 17    | AWS SDK Java v2 | 🔜 planned |
| `dotnet-sdk`  | C#         | AWS SDK .NET v3 | ✅ active  |
| `rust-sdk`    | Rust       | AWS SDK Rust    | ✅ active  |
| `cli`         | Bash       | AWS CLI v2      | ✅ active  |

### Infrastructure as Code

| Suite       | Tool                        | Status     |
| ----------- | --------------------------- | ---------- |
| `cdk`       | AWS CDK v2 (TypeScript)     | 🔜 planned |
| `tofu`      | OpenTofu + AWS provider     | 🔜 planned |
| `terraform` | Terraform + AWS provider v6 | 🔜 planned |
| `pulumi`    | Pulumi AWS provider         | 🔜 planned |

---

## What to test

Every suite should cover all services implemented in Overcast at a minimum.
The table below shows what each suite currently covers. ✅ = tests exist
(may include expected failures for unimplemented ops), 🔜 = planned, — = out of scope.

| Service         | node-js-sdk | python | go  | java | rust | cli | cdk | tofu | terraform |
| --------------- | ----------- | ------ | --- | ---- | ---- | --- | --- | ---- | --------- |
| S3              | ✅          | ✅     | ✅  | 🔜   | 🔜   | ✅  | 🔜  | 🔜   | 🔜        |
| SQS             | ✅          | ✅     | ✅  | 🔜   | 🔜   | ✅  | 🔜  | 🔜   | 🔜        |
| DynamoDB        | ✅          | ✅     | ✅  | 🔜   | 🔜   | ✅  | 🔜  | 🔜   | 🔜        |
| SNS             | ✅          | ✅     | ✅  | 🔜   | 🔜   | ✅  | 🔜  | 🔜   | 🔜        |
| Lambda          | ✅          | ✅     | ✅  | 🔜   | 🔜   | ✅  | 🔜  | —    | —         |
| CloudWatch Logs | ✅          | ✅     | ✅  | 🔜   | —    | ✅  | —   | —    | —         |
| SES             | ✅          | ✅     | ✅  | 🔜   | —    | ✅  | —   | —    | —         |
| Secrets Manager | ✅          | ✅     | ✅  | 🔜   | 🔜   | ✅  | 🔜  | 🔜   | 🔜        |
| IAM             | ✅          | ✅     | ✅  | 🔜   | —    | ✅  | 🔜  | 🔜   | 🔜        |
| STS             | ✅          | ✅     | ✅  | 🔜   | 🔜   | ✅  | —   | —    | —         |
| KMS             | ✅          | ✅     | ✅  | 🔜   | 🔜   | ✅  | —   | 🔜   | 🔜        |
| SSM             | ✅          | ✅     | ✅  | 🔜   | 🔜   | ✅  | —   | 🔜   | 🔜        |
| EventBridge     | ✅          | ✅     | ✅  | 🔜   | —    | ✅  | 🔜  | —    | —         |
| Kinesis         | ✅          | ✅     | ✅  | 🔜   | —    | ✅  | —   | —    | —         |

---

## Architecture

```
[ Overcast emulator ]  ← the system under test; knows nothing about compat
        ↑ HTTP (port 4566)
        │
[ compat runner ]      ← spawns suite subprocesses, reads NDJSON from stdout
        │
  ┌─────┴──────┐
  │  suites    │  ← each suite is an independent subprocess / Docker image
  │ node-js-sdk│     that speaks only to the emulator via the AWS SDK
  │  python …  │
  └────────────┘
        │ aggregated RunReport (JSON)
        ↓
[ compat server ]      ← small HTTP service inside cmd/compat
        │               serves last run result + streams live NDJSON events
        ↓
[ compat-ui ]          ← Vite/React dashboard (compat/ui/)
                          reads from compat server only; never from Overcast
```

```
compat/
  README.md          ← you are here
  AGENTS.md          ← coding conventions for contributors and AI agents
  Makefile           ← make run / make ci / make json / make serve
  docker-compose.yml
  result.go          ← Go types for the NDJSON wire format
  runner.go          ← orchestrates suite subprocesses, aggregates results
  server.go          ← HTTP server: GET /events (SSE), GET /results, GET /

  suites/
    node-js-sdk/     ← TypeScript / AWS SDK JS v3 (active)
    python-sdk/      ← Python 3 / boto3 (planned)
    go-sdk/          ← Go / AWS SDK Go v2 (planned)
    java-sdk/        ← Java 17 / AWS SDK Java v2 (planned)
    dotnet-sdk/      ← C# / AWS SDK .NET v3 (planned)
    rust-sdk/        ← Rust / AWS SDK Rust (planned)
    cli/             ← Bash / AWS CLI v2 (planned)
    cdk/             ← TypeScript / AWS CDK v2 (planned)
    tofu/            ← HCL / OpenTofu (planned)
    terraform/       ← HCL / Terraform (planned)
    pulumi/          ← TypeScript / Pulumi AWS provider (planned)

  ui/                ← Vite + React dashboard
    package.json
    src/

cmd/compat/
  main.go            ← CLI: run suites and/or start the compat server
```

---

## Wire format (NDJSON)

Every suite runner emits **newline-delimited JSON** to stdout — one object per
line, four event types:

```
{"event":"run_start","suite":"node-js-sdk","started_at":"…","endpoint":"…","version":"1"}
{"event":"test_start","suite":"node-js-sdk","service":"s3","group":"s3-crud","test":"CreateBucket"}
{"event":"test_result","suite":"node-js-sdk","service":"s3","group":"s3-crud","test":"CreateBucket","status":"pass","duration_ms":42}
{"event":"test_result","suite":"node-js-sdk","service":"iam","group":"iam-users","test":"CreateUser","status":"unimplemented","duration_ms":120,"error":"NotImplemented: Unknown action: CreateUser"}
{"event":"run_end","suite":"node-js-sdk","passed":45,"failed":12,"skipped":2,"unimplemented":31,"duration_ms":5432}
```

| Field         | Type   | Description                                               |
| ------------- | ------ | --------------------------------------------------------- |
| `event`       | string | `run_start` \| `test_start` \| `test_result` \| `run_end` |
| `suite`       | string | Suite name, e.g. `"node-js-sdk"`                          |
| `service`     | string | AWS service, e.g. `"s3"`, `"iam"`                         |
| `group`       | string | Group within suite, e.g. `"s3-crud"`                      |
| `test`        | string | Test name                                                 |
| `status`      | string | `"pass"` \| `"fail"` \| `"skip"` \| `"unimplemented"`     |
| `duration_ms` | number | Wall-clock milliseconds                                   |
| `error`       | string | Error message (on `fail` and `unimplemented`)             |

Status semantics:

| Status          | Meaning                                                                 |
| --------------- | ----------------------------------------------------------------------- |
| `pass`          | Test passed against the emulator                                        |
| `fail`          | Test failed — the emulator returned a wrong response or an error        |
| `unimplemented` | Emulator returned 501 or `UnknownOperationException` / `NotImplemented` |
| `skip`          | Test skipped (e.g. Docker not available)                                |

**Rules:** emit to stdout only; one line per event; exit `0` always (suites
must not fail the process for expected test failures).

---

## Adding a new suite

1. Create `compat/suites/<name>/` — see the stub README in each planned suite
   directory for language-specific setup notes.
2. Emit the NDJSON wire format above to stdout.
3. Register the suite in `compat/runner.go`.
4. Update the Suites table above from 🔜 to ✅.

---

## Compat server

The Go CLI starts a small HTTP server (`--serve`, default port `7777`).

### `GET /events` — SSE stream

Uses **Server-Sent Events** to push individual result objects to connected
clients as they arrive from the suite subprocesses. Clients that connect
mid-run receive all buffered events since the run started.

### `GET /results` — last completed run

Returns the latest `RunReport` as a single JSON object. Useful for CI badge
generation and one-shot queries.

An interactive session has no single end — the dashboard submits work whenever
you ask it to — so the report is finalised each time the queue **drains**:
every batch done, nothing running. At that point results are merged into the
last report (a scoped re-run replaces just those suites), written to
`--results-file`, and summarised into `--agent-report-file`. So `--report`
and the results file reflect dashboard-triggered runs exactly as they do
batch ones.

### `POST /run` — trigger a run

Starts a new run (returns `202 Accepted` or `409 Conflict` if already running).
Accepts a JSON filter body:

```json
{ "service": "s3" }                          // re-run one service
{ "suite": "node-js-sdk" }                   // re-run one suite
{ "statuses": ["fail", "skip"] }             // re-run non-passing tests
{ "service": "s3", "group": "s3-crud" }      // re-run one group
```

### `GET /` — compat dashboard

Serves the dashboard UI. Where it comes from depends on how you started:

| Mode | Source of the UI |
| --- | --- |
| default | the `compat/ui/dist/` build embedded in the binary at compile time |
| `--build-ui` | a fresh build, served from `compat/ui/dist/` on disk |
| `--ui-dir DIR` | `DIR` on disk |
| `--ui-dev` / `--dev` | an external Vite dev server, with HMR |

## Compat UI

`compat/ui/` is a standalone Vite + React app. It:

- Opens an `EventSource` to `GET /events` and updates the compatibility matrix
  in real time as each `test_result` event arrives — no polling, no page reload.
- Falls back to `GET /results` to populate the matrix when loading a completed
  run.
- Never connects to Overcast directly.
- Is built and embedded into the `cmd/compat` binary as a static asset.
- In `--ui-dev`, proxies its API calls to the compat server named by
  `COMPAT_SERVER_URL` (the CLI sets it, since both ports are chosen at runtime).

## State management

Each test group creates and destroys its own resources using a `runId` prefix
(format: `oc-{8-hex}`). Teardown always runs in a `finally` block.
