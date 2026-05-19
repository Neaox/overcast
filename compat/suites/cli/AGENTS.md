# AGENTS.md — cli suite

> Conventions for AI agents and contributors working in `compat/suites/cli/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules and separation boundary that apply to every suite.
> This file covers only cli-specific details.

---

## What this suite tests

Every AWS service operation reachable via the **AWS CLI v2**. It is the CLI
column of the compatibility matrix. Failures on unimplemented services are
correct and expected — they are the coverage gap metric, not bugs to silence.

---

## Runtime

| Item       | Value                                                     |
| ---------- | --------------------------------------------------------- |
| Language   | Go 1.24                                                   |
| AWS client | `aws` CLI v2 (pinned via Dockerfile base image tag)            |
| CI image   | Alpine + `aws` CLI pre-installed                               |

> SDK upgrade policy: [compat/AGENTS.md § SDK version pinning](../../AGENTS.md#sdk-version-pinning--upgrade-strategy).

---

## File layout

```
compat/suites/cli/
  AGENTS.md          ← you are here
  README.md          ← quick-start
  go.mod
  cmd/               ← binary entry point
  internal/
    awscli/          ← thin Run/RunOutput wrappers around the aws binary
    harness/         ← TestContext, TestFn
    groups/          ← one file per AWS service
      s3.go
      sqs.go
      ...
```

**One file per AWS service.** Never split a service across files.

---

## Group anatomy

```go
func MyService() ServiceGroup {
    g := &myGroup{}
    return ServiceGroup{
        Impls: map[string]harness.TestFn{
            "GroupName": map of operation → method,
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

`awscli.Run` returns an error if the CLI exits non-zero.
`awscli.RunOutput` returns the parsed JSON map + error.

---

## Naming conventions

| Element         | Convention                                                      |
| --------------- | --------------------------------------------------------------- |
| Group name      | `<service>-<feature>` (kebab-case), e.g. `s3-crud`, `iam-roles` |
| Resource prefix | `oc-<short>-{runId}` or `oc-{runId}-<short>`                    |
| Service file    | Lowercase service name: `s3.go`, `cloudwatch.go`                |
| Struct          | `type <service>Group struct{}`                                  |

---

## Teardown rules (cli-specific additions)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional Go/CLI specifics:

- Ignore errors on teardown calls with `//nolint:errcheck`.
- Use `awscli.RunOutput` when you need the JSON response to iterate over
  resources (e.g. listing uploads before aborting them).
- `teardownBucket` uses `list-objects-v2` — it does **not** handle versioned
  objects. Use `teardownVersioning` (or equivalent) for versioned buckets,
  which calls `list-object-versions` and deletes all versions and delete
  markers explicitly.
- `teardownMultipart` aborts incomplete uploads via `list-multipart-uploads`
  before deleting the bucket. Use it (not `teardownBucket`) for the
  `s3-multipart` group.

---

## What agents must NOT do

- Never call `time.Sleep` inside a test — use a poll loop with a max count.
- Never hard-code the endpoint — always use `t.Endpoint`.
- Never write to stdout inside a test — the runner parses stdout as NDJSON.
- Never add a setup function without a corresponding teardown.
- Never reuse `teardownBucket` for versioned or multipart groups — use the
  dedicated helpers.
