# compat/suites/terraform — Terraform

> **Status: planned — nothing is implemented.** This directory holds this
> README, [AGENTS.md](AGENTS.md) and a `.gitignore`. There is no Terraform
> configuration, no runner and no entry point.

## What exists today

Nothing that runs. Specifically:

- The suite is **not** in `compat/runner.go`'s suite table, so
  `go run ./cmd/compat` never spawns it and `--suite terraform` is rejected as
  an unknown name.
- It is **not** in the `all_suites` matrix in
  [.github/workflows/compat.yml](../../../.github/workflows/compat.yml), so CI
  neither builds nor runs it.
- It declares no groups in [compat/suites/registry.json](../registry.json), so
  it contributes no column to the compatibility matrix and no shard to
  `compat/baseline/`.

[AGENTS.md § Implementation checklist](AGENTS.md#implementation-checklist) is
the step-by-step guide for building it; the rest of this file describes what
that work is meant to produce.

## What this suite will cover

Runs a full `terraform init → validate → plan → apply → spot-check → destroy`
cycle using the AWS provider v6 pointed at Overcast. Planned resources mirror
the `tofu` suite:

- S3 bucket with versioning and lifecycle rules
- SQS queue + DLQ with redrive policy
- SNS topic + SQS subscription
- DynamoDB table with GSI and TTL
- IAM role for Lambda
- SSM parameters (String + SecureString)
- Secrets Manager secret
- S3 remote state + DynamoDB lock table (created pre-run)

## Prerequisites

- `terraform` CLI
- `aws` CLI (for credential setup and spot-checks)
- Overcast running on `http://localhost:4566`

## Environment variables

| Variable                  | Default                    | Description          |
| ------------------------- | -------------------------- | -------------------- |
| `OVERCAST_ENDPOINT`       | `http://localhost:4566`    | Emulator endpoint    |
| `AWS_ACCESS_KEY_ID`       | `test`                     | Fake credentials     |
| `AWS_SECRET_ACCESS_KEY`   | `test`                     | Fake credentials     |
| `AWS_DEFAULT_REGION`      | `us-east-1`                | AWS region           |

## Wire format

This suite must emit NDJSON to stdout matching the format documented in
[compat/README.md](../../README.md#wire-format-ndjson). Each lifecycle phase
(init, plan, apply, each spot-check assertion, destroy) should map to a test.
