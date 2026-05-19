# compat/suites/tofu — OpenTofu

> **Status: planned.** This suite does not run yet. See the parent
> [compat/README.md](../../README.md) for the roadmap.

## What this suite will cover

Runs a full `tofu init → validate → plan → apply → spot-check → destroy` cycle
using the AWS provider pointed at Overcast. Planned resources:

- S3 bucket with versioning and lifecycle rules
- SQS queue + DLQ with redrive policy
- SNS topic + SQS subscription
- DynamoDB table with GSI and TTL
- IAM role for Lambda
- SSM parameters (String + SecureString)
- Secrets Manager secret
- S3 remote state + DynamoDB lock table (created pre-run)

## Prerequisites

- `tofu` CLI (OpenTofu)
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
