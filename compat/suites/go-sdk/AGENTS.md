# AGENTS.md — go-sdk suite

> Conventions for AI agents and contributors working in `compat/suites/go-sdk/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules, the separation boundary, and the `group:test`
> implementation-key rules that apply to every suite.
> This file covers only go-sdk-specific details.
>
> For quick-start, prerequisites, env vars and architecture see
> [README.md](README.md).

---

## What this suite tests

Every AWS service operation reachable via the **AWS SDK for Go v2**. It is the
Go SDK column of the compatibility matrix. Failures on unimplemented services
are correct and expected — they are the coverage gap metric, not bugs to
silence.

The suite mirrors the other SDK suites' service and operation coverage
(`registry.json` is the shared contract — see
[compat/AGENTS.md § Uniformity](../../AGENTS.md#2-uniformity--the-registry-is-the-contract))
while exercising Go-specific SDK behaviour: `context.Context` propagation, the
pointer-wrapping input structs, and the `aws.To*` helpers that unwrap a
response.

---

## Status

**Implemented.** `groups.All` wires 28 service group constructors covering S3,
SQS, DynamoDB, SNS, Lambda, CloudWatch Logs, SES, IAM, STS, Secrets Manager,
KMS, SSM, Kinesis, EventBridge, CloudFormation, EC2, ECS, Cognito, AppSync,
API Gateway (v1 and v2), CloudFront, RDS, Step Functions, EventBridge Pipes,
WAFv2, Shield, ElastiCache and EFS. It runs in the compat CI matrix
(`.github/workflows/compat.yml`) alongside every other suite.

---

## Runtime

| Item       | Value                                                                                        |
| ---------- | ---------------------------------------------------------------------------------------------- |
| Language   | Go 1.24 or newer — its own module (`go.mod`), which also names a `toolchain`                   |
| AWS client | `github.com/aws/aws-sdk-go-v2` plus one `service/*` module per client, pinned in `go.mod`      |
| CI image   | None of its own — GitHub Actions installs Go from the root `go.mod` and runs the suite as a subprocess; the compose path uses `.devcontainer/Dockerfile`, which already carries Go |

> SDK upgrade policy: [compat/AGENTS.md § SDK version pinning](../../AGENTS.md#sdk-version-pinning--upgrade-strategy).
> `go.mod` is the source of truth for every pinned version; do not restate one
> here.

---

## File layout

```
compat/suites/go-sdk/
  AGENTS.md          ← you are here
  README.md          ← quick-start, prerequisites, env vars, architecture
  go.mod / go.sum    ← its own module

  cmd/
    runner/main.go    ← entry point: merges impls, loads the registry, then runs
                        once or serves the OVERCAST_COMPAT_INTERACTIVE loop
    debugtest/main.go ← request/response dumper with a hard-coded endpoint; a
                        debugging aid, not part of a compat run

  internal/
    clients/clients.go   ← Clients: one lazily-built AWS SDK client per service
    harness/harness.go   ← TestContext, TestCase, TestGroup, RunGroup, RunSuite,
                           IsUnimplemented, NDJSON emitters, stdin command loop
    registry/registry.go ← loads registry.json + registry.generated.json, merges
                           and validates impl keys, builds TestGroups
    registry/registry_test.go ← loader unit tests
    groups/
      groups.go        ← the ServiceGroup type and All(), the registration point
      groups_test.go   ← this suite's registrations vs. the real registry.json
      s3.go  sqs.go  dynamodb.go  …   ← one file per AWS service
```

**One file per AWS service.** Never split a service across files, and never
merge two services into one file.

---

## Group anatomy

A service file exports one constructor returning a `ServiceGroup`: three maps
(`Impls`, `Setup`, `Teardown`) that `cmd/runner` merges into the suite's
registrations at startup. This is the real `STS` constructor
(`internal/groups/sts.go`), trimmed:

```go
func STS(c *clients.Clients) ServiceGroup {
    g := &stsGroup{c: c}
    return ServiceGroup{
        Impls: map[string]harness.TestFn{
            "sts-identity:GetCallerIdentity": g.GetCallerIdentity,
            "sts-assume:AssumeRole":          g.AssumeRole,
            // … one entry per test, keyed "group:Test"
        },
        Setup: map[string]func(context.Context, *harness.TestContext) error{
            "sts-identity": g.setupIdentity,
        },
        Teardown: map[string]func(context.Context, *harness.TestContext) error{
            "sts-identity": g.noopTeardown,
        },
    }
}

type stsGroup struct{ c *clients.Clients }

func (g *stsGroup) cl() *sts.Client { return g.c.STS() }

func (g *stsGroup) GetCallerIdentity(ctx context.Context, t *harness.TestContext) error {
    resp, err := g.cl().GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
    if err != nil {
        return err
    }
    if aws.ToString(resp.Account) == "" {
        return fmt.Errorf("GetCallerIdentity: empty account")
    }
    return nil
}
```

Clients come from `g.c.S3()`, `g.c.SQS()` and so on — never construct an SDK
client inside a test function. Return `nil` to pass and an error to fail;
`cmd/runner` classifies an HTTP 501 as `unimplemented` rather than a failure.

---

## Key types

```go
// internal/harness/harness.go
type TestFn func(ctx context.Context, t *TestContext) error

type TestContext struct {
    Endpoint string
    Region   string
    RunID    string
    // plus an unexported state bag reached through Set/Get/GetString
}

func (t *TestContext) Set(key string, val any)
func (t *TestContext) Get(key string) (any, bool)
func (t *TestContext) GetString(key string) string // "" when absent
func (t *TestContext) Log(msg string)              // stderr only — stdout is NDJSON

type TestCase struct {
    Name    string
    Fn      TestFn
    Op      string   // AWS operation name for doc links ("false" suppresses one)
    Skip    string   // non-empty emits "skip" instead of running
    NA      string   // non-empty emits "na" — the SDK does not expose the
                     // operation; excluded from pass rates
    Depends []string // same-group tests that must pass first
}

type TestGroup struct {
    Suite, Service, Name string
    Tests                []TestCase
    Setup, Teardown      func(context.Context, *TestContext) error
}

func IsUnimplemented(err error) bool // true for HTTP 501
```

`ServiceGroup` (`internal/groups/groups.go`) is what each service file returns.
Its `Name` field labels the file a duplicate-key error came from, so a
collision points at the two files rather than only the key they disagree about;
`All()` applies it through `named(...)` so the labels sit beside the
registration order they describe.

---

## Naming conventions

| Element         | Convention                                                                                                                                                                 |
| --------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Impl map key    | `<group>:<Test>` — **always group-qualified**, never bare (see [compat/AGENTS.md § Implementation keys](../../AGENTS.md#implementation-keys--grouptest-and-a-bad-key-aborts-the-run)) |
| Group name      | `<service>-<feature>` (kebab-case), e.g. `s3-crud`, `iam-roles`                                                                                                              |
| Test name       | PascalCase AWS operation name, e.g. `CreateBucket`, `AssumeRole`                                                                                                            |
| Resource prefix | `{t.RunID}-<short>`, e.g. `{runID}-s3crud`                                                                                                                                  |
| Service file    | Lowercase service name: `s3.go`, `cloudwatch.go`                                                                                                                            |
| Struct          | `type <service>Group struct{ c *clients.Clients }`                                                                                                                          |
| Context key     | snake_case string, e.g. `"s3_bucket"`, `"kms_key_id"`                                                                                                                       |

---

## Inter-test state

Use `t.Set` / `t.GetString` to pass data between sequential tests in a group:

```go
// in setup:
t.Set("s3_bucket", bucket)

// in a later test or in teardown:
bucket := t.GetString("s3_bucket")
```

A fresh `TestContext` is created per group, so never rely on inter-group
state, and never stash an SDK client in the bag — it already lives in
`clients.Clients`.

---

## Teardown rules (go-sdk-specific additions)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional Go SDK specifics:

- Suppress teardown errors with `//nolint:errcheck`; one failed delete must
  never abort the deletes after it.
- `emptyAndDeleteBucket` (in `s3.go`) handles versioned objects, delete
  markers **and** incomplete multipart uploads. Always use it; never call
  `DeleteBucket` directly without emptying first.
- `teardownBucket(contextKey)` is a factory returning a teardown function that
  reads the bucket name from `t.GetString(contextKey)`.
- `harness.IsUnimplemented(err)` reports HTTP 501 — use it to tolerate an
  unimplemented operation in teardown rather than reporting a cleanup failure.
- Always store created resource IDs with `t.Set(key, value)` during setup so
  teardown can read them back with `t.GetString(key)`.

---

## Error messages

Return an error carrying the operation, what was expected, and enough context
to diagnose the failure without re-running it:

```go
return fmt.Errorf("ListObjectsV2: key %q not found in bucket %s (runID=%s)", key, bucket, t.RunID)
```

Lead with the AWS operation name so the NDJSON `error` field is readable in
the dashboard without opening the source.

---

## Generated groups and the `ScenarioBackend` hook

`internal/registry` loads `registry.json` and its machine-written sibling
`registry.generated.json` (concatenated, hand-written groups first — see
[compat/AGENTS.md § registry.json](../../AGENTS.md#registryjson--canonical-test-matrix)),
validates this suite's impl keys against the result, and builds the
`TestGroup` list the harness runs.

A **generated** group (one with `"generated": true`) is meant to be executed
by an interpreter reading the group's scenario IR rather than by a registered
impl. `registry.ScenarioBackend` is the extension point for that interpreter:
it is consulted after the qualified and bare impl-key lookups and before the
not-implemented sentinel.

**Nothing implements `ScenarioBackend` in this suite yet**, and
`registry.generated.json` currently declares no groups, so this has no
observable effect today. Once the generator emits a group scoped to `go-sdk`,
a generated test with no backend reports as a hard **failure** naming the
group, not a skip. Do not add a backend speculatively; wait until there is a
real interpreter to wire in.

---

## Registration tests

`internal/groups/groups_test.go` resolves this suite's real registrations
against the real `registry.json` without starting a run:

- `TestRegisteredImplsResolveAgainstRegistry` — every impl key resolves to a
  real `group:test` pair.
- `TestRegisteredImplsHaveNoDuplicateKeys` — no two service files register the
  same key; last-writer-wins would otherwise drop one implementation silently.
- `TestRegisteredImplsHaveNoBareKeys` — every key is group-qualified. A bare
  key becomes ambiguous the moment a second group declares the same test name.

`internal/registry/registry_test.go` covers the loader itself — merge,
validate, dependency ordering, and `registry.generated.json` handling —
independently of this suite's registrations.

Run both with `go test ./...` from this directory. Neither needs a live
Overcast instance.

---

## Adding a new group

1. Add the group and its tests to [compat/suites/registry.json](../registry.json)
   first (see [compat/AGENTS.md § Uniformity](../../AGENTS.md#2-uniformity--the-registry-is-the-contract)),
   or confirm another suite has already declared them.
2. Create or open `internal/groups/<service>.go`.
3. Register every test under a **group-qualified** key in `Impls`, and its
   setup/teardown in `Setup`/`Teardown`.
4. For a new service, add the constructor to `All()` in `groups.go` and a
   lazily-initialised client to `internal/clients/clients.go`; add the SDK
   module to `go.mod`.
5. Run `go test ./...` — the registration tests fail on a mis-keyed
   registration without needing a run.
6. Run the suite against a live instance (see
   [README.md § Running the suite](README.md#running-the-suite)) and check the
   NDJSON output.

---

## What agents must NOT do

- Never import from `internal/`, `router/`, or any Overcast server source tree
  — see [compat/AGENTS.md § Separation boundary](../../AGENTS.md#separation-boundary--non-negotiable).
- Never call `time.Sleep` inside a test — use a poll loop with a max count.
- Never hard-code the endpoint — always use the injected client from `g.c`.
- Never construct SDK clients directly in test functions.
- Never register an impl key without the `group:` qualifier — the registration
  tests reject it.
- Never add a setup function without a corresponding teardown.
- Never call `DeleteBucket` without first emptying the bucket via
  `emptyAndDeleteBucket`.
- Never schedule KMS key deletion without first deleting any aliases pointing
  to that key.
- Never write to stdout inside a test — the runner parses stdout as NDJSON;
  use `t.Log()` (stderr) for diagnostics.
