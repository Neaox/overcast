# compat/suites/pulumi — Pulumi (AWS Classic provider)

> **Status: planned.** This suite does not run yet. See the parent
> [compat/README.md](../../README.md) for the roadmap.

## What this suite will cover

Runs a full `pulumi up → spot-check → pulumi destroy` cycle using the Pulumi
AWS Classic provider pointed at Overcast. Planned resources mirror the `tofu`
and `terraform` suites:

- S3 bucket with versioning
- SQS queue + DLQ with redrive policy
- SNS topic + SQS subscription
- DynamoDB table with GSI and TTL
- IAM role for Lambda
- SSM parameters
- Secrets Manager secret

## Prerequisites

- Pulumi CLI (`pulumi`)
- Node.js 18+ (TypeScript Pulumi program)
- `aws` CLI (for credential setup and spot-checks)
- Overcast running on `http://localhost:4566`

## Environment variables

| Variable                  | Default                    | Description          |
| ------------------------- | -------------------------- | -------------------- |
| `OVERCAST_ENDPOINT`       | `http://localhost:4566`    | Emulator endpoint    |
| `AWS_ACCESS_KEY_ID`       | `test`                     | Fake credentials     |
| `AWS_SECRET_ACCESS_KEY`   | `test`                     | Fake credentials     |
| `AWS_DEFAULT_REGION`      | `us-east-1`                | AWS region           |
| `PULUMI_BACKEND_URL`      | `file://./state`           | Local file state backend |

## Wire format

This suite must emit NDJSON to stdout matching the format documented in
[compat/README.md](../../README.md#wire-format-ndjson). Each lifecycle phase
(up, each spot-check assertion, destroy) should map to a test.
