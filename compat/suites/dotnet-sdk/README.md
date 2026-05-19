# compat/suites/dotnet-sdk — .NET (AWS SDK .NET v3)

> **Status: planned.** This suite does not run yet. See the parent
> [compat/README.md](../../README.md) for the roadmap.

Explicitly listed in the Overcast project goals: *"Works with all official AWS
SDK clients (Go, JS/TS, Python, Java, **.NET**) without changes."*

## What this suite will cover

- All services already tested by `node-js-sdk`, cross-validated with the .NET SDK
- .NET-specific patterns (async/await, pagination with `AmazonS3Client.PaginateListObjectsV2`, etc.)

## Prerequisites

- .NET 8+ SDK (`dotnet`)
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
