# AGENTS.md — terraform suite

> Conventions for AI agents and contributors planning or implementing
> `compat/suites/terraform/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules and separation boundary that apply to every suite.
> This file covers Terraform-specific details for agents building this suite
> from scratch.
>
> For quick-start, prerequisites, and env vars see [README.md](README.md).

---

## What this suite tests

End-to-end Terraform compatibility using the **AWS provider v6** pointed at
Overcast. It is the Terraform column of the compatibility matrix.

Like the `tofu` suite (which uses OpenTofu), this suite does not test
individual API operations. It verifies that Terraform can plan, apply a
multi-resource configuration, and destroy it cleanly.

---

## Status

**Planned.** No implementation exists yet. Follow the implementation checklist
at the end of this file to build the suite from scratch.

---

## Runtime

| Item        | Value                                                |
| ----------- | ---------------------------------------------------- |
| IaC tool    | `terraform` CLI (HashiCorp Terraform, latest stable) |
| Provider    | `hashicorp/aws` v6+                                  |
| Spot-checks | AWS CLI v2 (`aws` command)                           |
| CI image    | Alpine + `terraform` + `aws` CLI pre-installed       |
| Runner      | Go 1.24 (same harness as the `cli` suite)            |

---

## File layout (planned)

```
compat/suites/terraform/
  AGENTS.md          ← you are here
  README.md          ← quick-start, prerequisites, env vars
  Dockerfile         ← Alpine + terraform + aws CLI; runs Go runner binary
  go.mod             ← runner dependencies (same pattern as cli suite)

  cmd/
    runner/
      main.go        ← entry point; creates TestContext, runs all groups

  internal/
    tfcli/
      tfcli.go       ← thin wrappers: Init(), Plan(), Apply(), Destroy(), Output()
    harness/
      harness.go     ← TestContext, TestFn, ServiceGroup
    groups/
      main.go        ← wires all group registrations
      lifecycle.go   ← init, plan, apply, spot-check, destroy groups
      spotcheck.go   ← spot-check assertions using awscli or SDK calls

  tf/
    main.tf          ← provider config; all planned resources
    variables.tf     ← run_id variable (injected per run)
    outputs.tf       ← export resource names/ARNs for spot-checks
    backend.tf       ← S3 remote state + DynamoDB lock (created pre-run)
```

---

## Group anatomy

Test groups map to Terraform lifecycle phases. Each phase is a `ServiceGroup`
with one or more `TestFn` entries.

```go
// internal/groups/lifecycle.go
package groups

import (
    "context"

    "github.com/your-org/overcast/compat/suites/terraform/internal/harness"
    "github.com/your-org/overcast/compat/suites/terraform/internal/tfcli"
)

func Lifecycle() harness.ServiceGroup {
    g := &lifecycleGroup{}
    return harness.ServiceGroup{
        Impls: map[string]harness.TestFn{
            "terraform-init:Init":    g.init,
            "terraform-plan:Plan":    g.plan,
            "terraform-apply:Apply":  g.apply,
            "terraform-destroy:Destroy": g.destroy,
        },
        Setup: map[string]func(context.Context, *harness.TestContext) error{
            "terraform-init":    g.setupInit,
            "terraform-apply":   g.setupApply,
        },
        Teardown: map[string]func(context.Context, *harness.TestContext) error{
            "terraform-apply":   g.teardownApply,
        },
    }
}

type lifecycleGroup struct{}

func (g *lifecycleGroup) init(ctx context.Context, t *harness.TestContext) error {
    return tfcli.Init(ctx, t.WorkDir)
}

func (g *lifecycleGroup) plan(ctx context.Context, t *harness.TestContext) error {
    return tfcli.Plan(ctx, t.WorkDir, t.RunID)
}

func (g *lifecycleGroup) apply(ctx context.Context, t *harness.TestContext) error {
    if err := tfcli.Apply(ctx, t.WorkDir, t.RunID); err != nil {
        return err
    }
    // Stash outputs for spot-check
    out, err := tfcli.Output(ctx, t.WorkDir)
    if err != nil {
        return err
    }
    t.Set("tf_outputs", out)
    return nil
}

func (g *lifecycleGroup) teardownApply(ctx context.Context, t *harness.TestContext) error {
    _ = tfcli.Destroy(ctx, t.WorkDir, t.RunID) //nolint:errcheck
    return nil
}
```

---

## Key types

The Go runner uses the same `TestContext`/`ServiceGroup`/`TestFn` pattern as
the `cli` suite. See [suites/cli/AGENTS.md](../cli/AGENTS.md) for the full
shape.

Additional Terraform-specific fields on `TestContext`:

```go
type TestContext struct {
    Endpoint string
    Region   string
    RunID    string
    WorkDir  string // path to the terraform module directory (tf/)
    // ... state bag (Get/Set)
}
```

---

## Naming conventions

| Element            | Convention                                                                 |
| ------------------ | -------------------------------------------------------------------------- |
| Group name         | `terraform-<phase>` (kebab-case), e.g. `terraform-init`, `terraform-apply` |
| Test name          | Title-case, e.g. `Init`, `Plan`, `Apply`, `BucketExists`, `Destroy`        |
| TF resource prefix | `var.run_id` injected at apply time; e.g. `"${var.run_id}-s3"`             |
| Var name           | `run_id` (snake_case) — injected via `-var run_id={runID}`                 |
| Output name        | snake_case, e.g. `bucket_name`, `queue_url`                                |

---

## Terraform variable injection

Resource names must be unique per run. Inject `run_id` as a Terraform
variable — never hard-code resource names in `.tf` files:

```hcl
# variables.tf
variable "run_id" {
  description = "Unique run identifier; prefix for all resource names."
  type        = string
}

# main.tf
resource "aws_s3_bucket" "compat" {
  bucket = "${var.run_id}-tf-compat"
}
```

Pass to Terraform via: `terraform apply -var "run_id=${runID}"`.

---

## Teardown rules (Terraform-specific)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional Terraform specifics:

- The `terraform-apply` group's teardown **must** call `terraform destroy
-auto-approve -var "run_id=..."` even if apply tests fail.
- Before `terraform destroy` can delete a non-empty S3 bucket, the bucket must
  be emptied. Add a pre-destroy step that empties the bucket via the AWS CLI
  or SDK, then let Terraform destroy the (now empty) bucket resource.
- The S3 remote-state bucket and DynamoDB lock table are created **before** the
  main run (in a setup step) and deleted **after** the main teardown; they must
  not be part of the main Terraform plan.
- If `terraform plan` produces no diff after resources already exist, that is
  not a test failure — it means the prior run's teardown did not complete. Log
  a warning and re-apply.

---

## What agents must NOT do

- Never import from `internal/`, `router/`, or any Overcast server source tree.
- Never hard-code the endpoint — configure the AWS provider via environment
  variables (`AWS_ENDPOINT_URL_*`) or `endpoints {}` block.
- Never run `terraform apply` or `terraform destroy` without `-auto-approve` —
  it will block on stdin in CI.
- Never leave an applied stack without a teardown that calls `terraform destroy`.
- Never hard-code resource names in `.tf` files — always use `var.run_id`.
- Never write to stdout inside a test function — the runner parses stdout as
  NDJSON; use the logger for diagnostics.
- Never delete the state backend (S3 bucket + DynamoDB table) before calling
  `terraform destroy` — Terraform needs the backend to destroy resources.

---

## Implementation checklist

When building this suite from scratch:

1. Create `go.mod` with `github.com/aws/aws-sdk-go-v2/*` and any CLI-helper
   utilities (mirror the `cli` suite `go.mod`).
2. Implement `internal/harness/harness.go` (mirror the `cli` suite harness).
3. Implement `internal/tfcli/tfcli.go` — thin wrappers that call `terraform`
   binary via `exec.CommandContext`, capture stdout/stderr, return errors.
4. Write `tf/main.tf`, `tf/variables.tf`, `tf/outputs.tf`, `tf/backend.tf`
   with all planned resources (see README.md for the full list).
5. Implement `internal/groups/lifecycle.go` with the phase groups.
6. Implement `internal/groups/spotcheck.go` with read assertions that verify
   key resources exist (using AWS CLI or SDK).
7. Wire all groups in `cmd/runner/main.go`; emit NDJSON to stdout.
8. Create `Dockerfile` based on Alpine; install `terraform` and `aws` CLI;
   build the Go runner binary.
9. Register the suite in `compat/runner.go` and `compat/suites/registry.json`.
10. Run the suite locally against a live Overcast instance and verify output.
