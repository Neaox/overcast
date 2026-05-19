# compat/suites/rust — Rust (AWS SDK Rust)

> **Status: planned.** This suite does not run yet. See the parent
> [compat/README.md](../../README.md) for the roadmap.

## What this suite will cover

- Core services: S3, SQS, DynamoDB, SNS, Lambda, STS, KMS, Secrets Manager, SSM

## Prerequisites

- Rust stable toolchain (`rustup`)
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
[compat/README.md](../../README.md#wire-format-ndjson). See the `node-js-sdk`
suite for a reference implementation.
