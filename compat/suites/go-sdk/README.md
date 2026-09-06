# go-sdk suite

Runs the Overcast AWS compatibility matrix using the **AWS SDK for Go v2**, as
its own Go module (`compat/suites/go-sdk/go.mod`).

> **Status: implemented.** See [AGENTS.md](AGENTS.md) for code conventions.

Tests cover every AWS service the registry declares — including ones not yet
implemented in Overcast. Failures on unimplemented services are expected and
are the coverage metric, not a problem to fix.

---

## What it covers

Every operation this suite's service groups register, cross-validated with the
AWS SDK for Go v2: S3, SQS, DynamoDB, SNS, Lambda, CloudWatch Logs, SES, IAM,
STS, Secrets Manager, KMS, SSM, Kinesis, EventBridge, CloudFormation, EC2,
ECS, Cognito, AppSync, API Gateway (v1 and v2), CloudFront, RDS, Step
Functions, EventBridge Pipes, WAFv2, Shield, ElastiCache and EFS — `groups.All`
in [internal/groups/groups.go](internal/groups/groups.go) is the authoritative,
registration-order list. A registry test this suite has not implemented is
emitted as a `skip`, so the coverage gap is visible rather than silent.

The suite also loads [compat/suites/registry.generated.json](../registry.generated.json)
and runs the **generated** groups it declares for `go-sdk`. Those come from the
scenario IR under `compat/model/scenarios/`, but this suite does not interpret
it: the AWS SDK for Go v2 has no dynamic-dispatch API, so `cmd/compatgen` emits
Go source — `internal/groups/scenarios_<service>_gen.go` — which the ordinary
build compiles. Each emitted test builds a real typed input struct and calls a
real client method; the shared semantics live once in `internal/scenario`.
Never edit a `scenarios_*_gen.go` by hand: change the recipe or the emitter and
run `make generate-compat-model` from the repository root.

---

## Prerequisites

- Go 1.24 or newer (verified here against Go 1.27)
- Docker, for the tests the registry marks `requires: [docker]` — Lambda
  invocation and event-source-mapping delivery. Without a daemon, set
  `OVERCAST_COMPAT_SKIP_DOCKER=1` and those tests are skipped rather than
  failed.
- Overcast running somewhere reachable — see
  [compat/AGENTS.md § Running a session](../../AGENTS.md#running-a-session--ports-are-chosen-never-assumed)
  for why `4566`/`4567` are off-limits for a test instance you start yourself

No AWS credentials are needed: the clients are built with the fixed pair
`overcast`/`overcast`, which the emulator accepts without validating.

---

## Running the suite

### Locally (Go required)

```bash
cd compat/suites/go-sdk
go build ./...   # compiles the runner and both unit-test packages
go test ./...    # registry and registration unit tests; no emulator needed

# Start Overcast first (separate terminal), e.g.:
#   go run ./cmd/overcast serve

OVERCAST_ENDPOINT=http://localhost:4566 go run ./cmd/runner
```

PowerShell:

```powershell
cd compat/suites/go-sdk
go build ./...
go test ./...

$env:OVERCAST_ENDPOINT = "http://localhost:4566"
go run ./cmd/runner
```

`go test ./...` resolves this suite's real impl keys against the real
`registry.json` without touching a live instance, so a new group's keys can be
checked before running anything.

### Via Docker (no local Go required)

This suite ships no image of its own. It runs as a subprocess of the compat
runner container, which already carries Go. From the repo root:

```bash
OVERCAST_COMPAT_SUITE=go-sdk docker compose -f compat/docker-compose.yml run --rm compat
```

Arguments after the compose service name reach the container entrypoint rather
than the runner, which is why the suite selection is an environment variable —
see [compat/AGENTS.md § Running suites](../../AGENTS.md#running-suites-docker--ci).

### Via the Go CLI (recommended — runs all suites, or just this one)

```bash
# Starts its own Overcast instance on a free port and stops it afterwards:
go run ./cmd/compat
# or just this suite:
go run ./cmd/compat --suite go-sdk
# or against an instance you are already running:
go run ./cmd/compat --endpoint http://localhost:4566 --suite go-sdk
```

This is what CI runs. `cmd/compat` spawns `go run ./cmd/runner` in this
directory — see `defaultSuites` in [compat/runner.go](../../runner.go).

---

## Environment variables

| Variable                         | Default                 | Description                                                                                            |
| -------------------------------- | ----------------------- | ------------------------------------------------------------------------------------------------------ |
| `OVERCAST_ENDPOINT`              | `http://localhost:4566` | Overcast base URL                                                                                      |
| `OVERCAST_DEFAULT_REGION`        | `us-east-1`             | AWS region advertised to the SDK                                                                       |
| `OVERCAST_REGISTRY_PATH`         | `../registry.json`\*    | Override the path to `registry.json`                                                                   |
| `OVERCAST_COMPAT_RUN_ID`         | `oc-<pid in hex>`       | Prefix for resource names, so concurrent runs and the orphan sweep do not collide                      |
| `OVERCAST_COMPAT_SKIP_DOCKER`    | unset                   | Set to `1` to drop the `docker` capability, skipping every test the registry marks `requires: [docker]` |
| `OVERCAST_COMPAT_SERVICE`        | unset (all)             | Comma-separated AWS service names to run, e.g. `s3`                                                    |
| `OVERCAST_COMPAT_GROUPS`         | unset (all)             | Comma-separated group names to run                                                                     |
| `OVERCAST_COMPAT_TESTS`          | unset (all)             | Comma-separated test names to run within those groups                                                  |
| `OVERCAST_COMPAT_PARALLEL_SLOTS` | `8`                     | Max groups run concurrently in interactive mode                                                        |
| `OVERCAST_COMPAT_INTERACTIVE`    | unset                   | Set to `1` to serve the interactive command protocol instead of one batch run                          |

\* Resolved relative to the process working directory when unset, so the suite
finds it at `../registry.json` when run from `compat/suites/go-sdk/`.
`registry.generated.json` is always read from that file's own directory.

---

## Architecture

```
go-sdk/
  go.mod              ← its own module: aws-sdk-go-v2 plus one client per service
  README.md           ← you are here

  cmd/
    runner/main.go    ← entry point: merges impls, loads the registry, then runs
                        once or serves the OVERCAST_COMPAT_INTERACTIVE loop
    debugtest/main.go ← standalone request/response dumper with a hard-coded
                        endpoint; a debugging aid, not part of a compat run

  internal/
    clients/clients.go   ← Clients: one lazily-built AWS SDK client per service
    harness/harness.go   ← TestContext, TestCase, TestGroup, RunGroup, RunSuite,
                           IsUnimplemented, and the NDJSON emitters
    registry/registry.go ← loads registry.json and registry.generated.json,
                           merges and validates impl keys, builds TestGroups
    scenario/            ← the runtime the generated groups call into: the
                           context bag, the checks, error matching, eventually
                           and the six-field failure message
    groups/              ← one file per AWS service
      groups.go          ← the ServiceGroup type and All(), the registration point
      s3.go  sqs.go  dynamodb.go  …
      scenarios_gen.go  scenarios_<service>_gen.go   ← generated; do not edit
```

The group list is **not** defined here. It comes from the shared cross-suite
registry at [compat/suites/registry.json](../registry.json), the single source
of truth for which groups and tests exist across every suite. `cmd/runner`
loads it, collects this suite's implementations keyed `group:Test`, and builds
the groups from it.

### Key types (`internal/harness`)

| Type / function        | Purpose                                                                                    |
| ---------------------- | ------------------------------------------------------------------------------------------ |
| `TestFn`               | `func(context.Context, *TestContext) error` — return `nil` to pass, an error to fail      |
| `TestContext`          | `Endpoint`, `Region`, `RunID`, `Log()`, plus a `Set`/`Get`/`GetString` bag for group state |
| `TestGroup`/`TestCase` | What the registry builds: a named group of tests with optional setup and teardown          |
| `RunSuite`             | Runs every group and emits NDJSON to stdout                                                |
| `IsUnimplemented(err)` | Reports whether the emulator answered HTTP 501                                             |

---

## Adding a new group

1. Add the group and its tests to [compat/suites/registry.json](../registry.json).
   Nothing runs until it is declared there, and every other suite immediately
   shows the new tests as skips — which is the point.
2. Open (or create) `internal/groups/<service>.go` — one file per AWS service.
3. Register `group:Test` keys in `Impls`, plus the group's `Setup`/`Teardown`.
4. For a new file, add its constructor to `All()` in `internal/groups/groups.go`.
5. Run `go test ./...`, which resolves the registrations against the real
   registry and so catches a mis-keyed one without a run.

See [AGENTS.md](AGENTS.md) for the exact shape and the teardown rules.
