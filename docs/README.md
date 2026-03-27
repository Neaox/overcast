# Documentation

This directory contains documentation for the AWS emulator project.

## Contents

- [Service emulation reference](./services/) — per-service endpoint coverage tables

---

## Support level legend

Every endpoint in the service docs carries one of these statuses:

| Status | Meaning |
|--------|---------|
| ✅ Supported | Fully implemented. AWS SDK calls work as expected. |
| ⚠️ Partial | Implemented but with caveats. See the notes column for detail. |
| 🚧 WIP | Under active development. May be broken or incomplete. |
| ❌ Unsupported | Not implemented. Returns `501 Not Implemented`. |

Endpoints marked **Unsupported** return a well-formed AWS error response so
that SDKs surface a clear error rather than a connection failure:

```
HTTP 501 Not Implemented
x-emulator-unsupported: true

{
  "__type": "NotImplemented",
  "message": "This operation is not yet emulated. See https://github.com/your-org/overcast/docs/services/<service>.md"
}
```

---

## Services

| Service | Doc | Overall coverage |
|---------|-----|-----------------|
| S3 | [s3.md](./services/s3.md) | See summary table |
| SQS | [sqs.md](./services/sqs.md) | See summary table |
| SNS | [sns.md](./services/sns.md) | See summary table |
| Lambda | [lambda.md](./services/lambda.md) | See summary table |
| DynamoDB | [dynamodb.md](./services/dynamodb.md) | See summary table |

---

## Adding a new service

1. Create `docs/services/<service>.md` using the template below.
2. Add the service to the table above.
3. Create `internal/services/<service>/` and register it in the router.
4. Add integration tests under `tests/integration/<service>/`.

### Service doc template

```markdown
# <Service Name>

> AWS docs: https://docs.aws.amazon.com/...

## Summary

| Category | Supported | Partial | WIP | Unsupported |
|----------|-----------|---------|-----|-------------|
| ...      | N         | N       | N   | N           |

## Endpoints

### <Category name>

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| ...       | ✅/⚠️/🚧/❌ | | [link](...) |
```
