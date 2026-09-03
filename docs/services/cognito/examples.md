---
title: "Cognito examples"
description: "Import users from a real AWS user pool into a local one, by CLI or over HTTP, including the status mapping and what does not come across."
section: "Service Reference"
tags:
  - cognito
  - docs
  - examples
  - services
---

# Cognito examples

Worked examples past the [Cognito quick start](../cognito.md#quick-start).

## Import users from a real AWS pool

```bash
overcast import cognito-users \
  --from-pool-id us-east-1_abc123 \
  --to-pool-id us-east-1_def456 \
  --from-profile my-aws-profile \
  --batch-size 100
```

| Flag             | Default | Description                                       |
| ---------------- | ------- | --------------------------------------------------- |
| `--from-pool-id` | (req)   | Source user pool ID in real AWS                   |
| `--to-pool-id`   | (req)   | Target user pool ID in Overcast                   |
| `--from-profile` |         | AWS profile for the source account                |
| `--from-region`  |         | AWS region (auto-detected if omitted)             |
| `--user`         |         | Import a single user by sub (UUID)                |
| `--max-users`    | `0`     | Limit total users (0 = unlimited)                 |
| `--batch-size`   | `100`   | Users per request to the server                   |
| `--endpoint`     |         | Overcast daemon URL (inherited from the root command) |

> [!IMPORTANT]
> Password hashes cannot be read out of AWS, so no password comes across. Every
> imported user that had one lands in `FORCE_CHANGE_PASSWORD` and must set a
> new password on first sign-in.

### Status mapping

| AWS status                                     | Becomes                 |
| ---------------------------------------------- | ------------------------- |
| `CONFIRMED`, `FORCE_CHANGE_PASSWORD`, `RESET_REQUIRED` | `FORCE_CHANGE_PASSWORD` |
| `UNCONFIRMED`                                  | `UNCONFIRMED`           |
| `DISABLED`, `ARCHIVED`, `COMPROMISED`          | `DISABLED`              |
| `EXTERNAL_PROVIDER`                            | Skipped, and reported in `errors` |

### What comes across, and what does not

- The original `sub` is preserved; any `sub` in the payload is overwritten.
- Groups a user belongs to are auto-created as stubs if they are not already in
  the target pool.
- Duplicate usernames are skipped and reported in `errors`.
- No password, confirmation code or TOTP secret is imported.

## Import over HTTP

The CLI is a wrapper around one endpoint, so a script or fixture loader can
post users directly:

```
POST /_overcast/cognito/user-pools/{poolId}/import-users
Content-Type: application/json
```

```json
{
  "users": [
    {
      "username": "jdoe",
      "sub": "a1b2c3d4-0000-0000-0000-000000000000",
      "enabled": true,
      "status": "CONFIRMED",
      "createdAt": "2024-01-01T00:00:00Z",
      "modifiedAt": "2024-01-01T00:00:00Z",
      "attributes": [{ "name": "email", "value": "jdoe@example.com" }],
      "groups": ["Admins"],
      "mfaEnabled": false
    }
  ]
}
```

```json
{ "imported": 1, "skipped": 0, "errors": [] }
```

## Related

- [Cognito](../cognito.md) — quick start and what works
- [Cognito limitations](./limitations.md) — the full divergence list
- [Cognito operations](./operations.md) — per-operation status
- [SES](../ses.md) — where user pool mail lands
