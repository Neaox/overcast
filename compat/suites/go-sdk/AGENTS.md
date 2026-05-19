# AGENTS.md — go-sdk suite

> Conventions for AI agents and contributors working in `compat/suites/go-sdk/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules and separation boundary that apply to every suite.
> This file covers only go-sdk-specific details.

---

## What this suite tests

Every AWS service operation reachable via the **AWS SDK for Go v2**. It is the
Go SDK column of the compatibility matrix. Failures on unimplemented services
are correct and expected.

---

## Runtime

| Item       | Value                                                         |
| ---------- | ------------------------------------------------------------- |
| Language   | Go 1.24                                                       |
| AWS client | `github.com/aws/aws-sdk-go-v2` (v1.41.5 + `config/*`, `service/*` sub-packages, pinned via `go.mod`) |
| CI image   | `golang:1.24-alpine`                                                                                |

> SDK upgrade policy: [compat/AGENTS.md § SDK version pinning](../../AGENTS.md#sdk-version-pinning--upgrade-strategy).

---

## File layout

```
compat/suites/go-sdk/
  AGENTS.md          ← you are here
  README.md          ← quick-start
  go.mod
  cmd/               ← binary entry point
  internal/
    clients/         ← Clients struct; lazy-init per-service clients
    harness/         ← TestContext, TestFn, IsUnimplemented()
    groups/          ← one file per AWS service
      s3.go
      sqs.go
      ...
```

**One file per AWS service.** Never split a service across files.

---

## Group anatomy

```go
func MyService(c *clients.Clients) ServiceGroup {
    g := &myGroup{c: c}
    return ServiceGroup{
        Impls: map[string]harness.TestFn{
            "OperationName": g.OperationName,
        },
        Setup: map[string]func(context.Context, *harness.TestContext) error{
            "group-name": g.setupGroupName,
        },
        Teardown: map[string]func(context.Context, *harness.TestContext) error{
            "group-name": g.teardownGroupName,
        },
    }
}
```

Clients are obtained from `g.c.S3()`, `g.c.SQS()`, etc. — never construct
clients directly inside a test function.

---

## Naming conventions

| Element         | Convention                                                      |
| --------------- | --------------------------------------------------------------- |
| Group name      | `<service>-<feature>` (kebab-case), e.g. `s3-crud`, `iam-roles` |
| Resource prefix | `oc-{runId}-<short>` (e.g. `oc-{runId}-s3crud`)                 |
| Context key     | snake_case string, e.g. `"s3_bucket"`, `"kms_key_id"`           |
| Service file    | Lowercase service name: `s3.go`, `cloudwatch.go`                |
| Struct          | `type <service>Group struct{ c *clients.Clients }`              |

---

## Teardown rules (go-sdk-specific additions)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional Go SDK specifics:

- Suppress teardown errors with `//nolint:errcheck`.
- `emptyAndDeleteBucket` (in `s3.go`) handles versioned objects, delete
  markers, AND incomplete multipart uploads. Always use it; never call
  `DeleteBucket` directly without first emptying.
- `teardownBucket(contextKey)` is a factory that returns a teardown function
  reading the bucket name from `t.GetString(contextKey)`.
- `harness.IsUnimplemented(err)` returns true for HTTP 501 — use it to
  tolerate unimplemented operations gracefully in teardown when needed.
- Always store created resource IDs in `t.Set(key, value)` during setup so
  teardown can read them via `t.GetString(key)`.

---

## What agents must NOT do

- Never call `time.Sleep` inside a test — use a poll loop with a max count.
- Never hard-code the endpoint — always use the injected client from `g.c`.
- Never construct SDK clients directly in test functions.
- Never add a setup function without a corresponding teardown.
- Never call `DeleteBucket` without first emptying the bucket via
  `emptyAndDeleteBucket`.
- Never schedule KMS key deletion without first deleting any aliases pointing
  to that key.
