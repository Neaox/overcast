# AGENTS.md — tofu suite

> Conventions for AI agents and contributors planning or implementing
> `compat/suites/tofu/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules and separation boundary that apply to every suite.
> This file covers OpenTofu-specific details for agents building this suite
> from scratch.
>
> For quick-start, prerequisites, and env vars see [README.md](README.md).

---

## What this suite tests

End-to-end OpenTofu compatibility using the **AWS provider** pointed at
Overcast. It is the OpenTofu column of the compatibility matrix.

This suite is intentionally a near-mirror of the `terraform` suite — the same
`.tf` configuration, the same lifecycle phases, and the same spot-check
assertions. The only difference is the binary: `tofu` instead of `terraform`.
Differences between OpenTofu and Terraform behaviour that Overcast must handle
are exactly the gap this suite is designed to surface.

---

## Status

**Planned.** No implementation exists yet. Follow the implementation checklist
at the end of this file to build the suite from scratch.

---

## Runtime

| Item        | Value                                                      |
| ----------- | ---------------------------------------------------------- |
| IaC tool    | `tofu` CLI (OpenTofu, latest stable)                       |
| Provider    | `hashicorp/aws` v6+ (same provider as Terraform)           |
| Spot-checks | AWS CLI v2 (`aws` command)                                 |
| CI image    | Alpine + `tofu` + `aws` CLI pre-installed                  |
| Runner      | Go 1.24 (same harness as the `cli` and `terraform` suites) |

---

## File layout (planned)

```
compat/suites/tofu/
  AGENTS.md          ← you are here
  README.md          ← quick-start, prerequisites, env vars
  Dockerfile         ← Alpine + tofu + aws CLI; runs Go runner binary
  go.mod             ← runner dependencies (same pattern as terraform suite)

  cmd/
    runner/
      main.go        ← entry point; creates TestContext, runs all groups

  internal/
    tofucli/
      tofucli.go     ← thin wrappers: Init(), Plan(), Apply(), Destroy(), Output()
    harness/
      harness.go     ← TestContext, TestFn, ServiceGroup
    groups/
      main.go        ← wires all group registrations
      lifecycle.go   ← init, validate, plan, apply, spot-check, destroy groups
      spotcheck.go   ← spot-check assertions using awscli or SDK calls

  tf/
    main.tf          ← provider config; all planned resources (same as terraform suite)
    variables.tf     ← run_id variable (injected per run)
    outputs.tf       ← export resource names/ARNs for spot-checks
    backend.tf       ← S3 remote state + DynamoDB lock (created pre-run)
```

The `tf/` directory content is **identical** to `compat/suites/terraform/tf/`.
Do not diverge the resource definitions unless you specifically want to test
OpenTofu-specific HCL syntax — document any divergences explicitly.

---

## Group anatomy

Test groups map to OpenTofu lifecycle phases. Each phase is a `ServiceGroup`
with one or more `TestFn` entries. The Go runner pattern is the same as the
`terraform` suite with `terraform` replaced by `tofu`:

```go
// internal/groups/lifecycle.go
func (g *lifecycleGroup) init(ctx context.Context, t *harness.TestContext) error {
    return tofucli.Init(ctx, t.WorkDir)
}

func (g *lifecycleGroup) validate(ctx context.Context, t *harness.TestContext) error {
    return tofucli.Validate(ctx, t.WorkDir)
}

func (g *lifecycleGroup) apply(ctx context.Context, t *harness.TestContext) error {
    if err := tofucli.Apply(ctx, t.WorkDir, t.RunID); err != nil {
        return err
    }
    out, err := tofucli.Output(ctx, t.WorkDir)
    if err != nil {
        return err
    }
    t.Set("tf_outputs", out)
    return nil
}

func (g *lifecycleGroup) teardownApply(ctx context.Context, t *harness.TestContext) error {
    _ = tofucli.Destroy(ctx, t.WorkDir, t.RunID) //nolint:errcheck
    return nil
}
```

See [suites/terraform/AGENTS.md](../terraform/AGENTS.md) for the complete
`ServiceGroup` anatomy — the tofu suite follows the same Go pattern.

---

## Key types

The Go runner uses the same `TestContext`/`ServiceGroup`/`TestFn` pattern as
the `cli` and `terraform` suites. See [suites/cli/AGENTS.md](../cli/AGENTS.md)
for the full shape.

Additional tofu-specific fields on `TestContext`:

```go
type TestContext struct {
    Endpoint string
    Region   string
    RunID    string
    WorkDir  string // path to the tf/ directory
    // ... state bag (Get/Set)
}
```

---

## Naming conventions

| Element            | Convention                                                      |
| ------------------ | --------------------------------------------------------------- |
| Group name         | `tofu-<phase>` (kebab-case), e.g. `tofu-init`, `tofu-apply`     |
| Test name          | Title-case, e.g. `Init`, `Validate`, `Plan`, `Apply`, `Destroy` |
| TF resource prefix | `var.run_id` injected at apply time; e.g. `"${var.run_id}-s3"`  |
| Var name           | `run_id` (snake_case) — injected via `-var run_id={runID}`      |
| Output name        | snake_case, e.g. `bucket_name`, `queue_url`                     |

---

## Terraform variable injection

Resource names must be unique per run. Inject `run_id` as a variable — never
hard-code names in `.tf` files. This is identical to the `terraform` suite:

```hcl
# variables.tf
variable "run_id" {
  description = "Unique run identifier; prefix for all resource names."
  type        = string
}

# main.tf
resource "aws_s3_bucket" "compat" {
  bucket = "${var.run_id}-tofu-compat"
}
```

Pass to OpenTofu via: `tofu apply -var "run_id=${runID}"`.

---

## Teardown rules (OpenTofu-specific)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Teardown rules are **identical** to the `terraform` suite — see
[suites/terraform/AGENTS.md](../terraform/AGENTS.md#teardown-rules-terraform-specific).

Summary of key points:

- The `tofu-apply` group's teardown **must** call `tofu destroy -auto-approve
-var "run_id=..."` even if apply tests fail.
- Empty S3 buckets before `tofu destroy` — OpenTofu cannot delete non-empty
  buckets.
- The state backend bucket and DynamoDB lock table are managed separately from
  the main plan.

---

## What agents must NOT do

- Never import from `internal/`, `router/`, or any Overcast server source tree.
- Never hard-code the endpoint — configure the AWS provider via environment
  variables.
- Never run `tofu apply` or `tofu destroy` without `-auto-approve` — it will
  block on stdin in CI.
- Never leave an applied stack without a teardown that calls `tofu destroy`.
- Never hard-code resource names in `.tf` files — always use `var.run_id`.
- Never write to stdout inside a test function — the runner parses stdout as
  NDJSON.
- Never delete the state backend before calling `tofu destroy`.
- Never copy the `tofu` binary from the Terraform image — use the official
  OpenTofu distribution.

---

## Implementation checklist

When building this suite from scratch:

1. Create `go.mod` mirroring the `terraform` suite runner `go.mod`.
2. Implement `internal/harness/harness.go` (mirror the `terraform` suite
   harness, or share it if the modules are compatible).
3. Implement `internal/tofucli/tofucli.go` — identical to `terraform` suite's
   `tfcli.go` but invoking the `tofu` binary instead of `terraform`.
4. Copy `tf/` from the `terraform` suite verbatim (or symlink if the build
   system supports it). Document any intentional divergences.
5. Implement `internal/groups/lifecycle.go` with the phase groups (`init`,
   `validate`, `plan`, `apply`, `destroy`).
6. Implement `internal/groups/spotcheck.go` with the same assertions as the
   `terraform` suite.
7. Wire all groups in `cmd/runner/main.go`; emit NDJSON to stdout.
8. Create `Dockerfile` based on Alpine; install `tofu` from the official
   OpenTofu GitHub releases and `aws` CLI; build the Go runner binary.
9. Register the suite in `compat/runner.go` and `compat/suites/registry.json`.
10. Run the suite locally against a live Overcast instance and verify that
    the NDJSON output matches the `terraform` suite output shape.
