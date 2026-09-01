---
title: "Cognito — Amazon Cognito User Pools"
description: "Cognito User Pools (Identity Provider) uses the application/x-amz-json-1.1 protocol. Operations are identified by the X-Amz-Target header with the prefix..."
section: "Service Reference"
tags:
  - amazon
  - cognito
  - docs
  - pools
  - services
  - user
---

# Cognito — Amazon Cognito User Pools

Cognito User Pools (Identity Provider) uses the `application/x-amz-json-1.1`
protocol. Operations are identified by the `X-Amz-Target` header with the
prefix `AWSCognitoIdentityProviderService.`. RPC v2 CBOR is also supported via
the Smithy RPC path (`POST /service/cognito/operation/{Operation}`).

**Accepted wire protocols:** `awsJson1_1`, `rpcv2Cbor`

---

## User Pool operations

| Operation        | Status | Notes                                                                          |
| ---------------- | ------ | ------------------------------------------------------------------------------ |
| CreateUserPool   | ✅     | Returns Id and Arn; Id format `{region}_{8-char-hex}`; accepts email templates |
| DescribeUserPool | ✅     | Returns email templates, admin config, email configuration                     |
| DeleteUserPool   | ✅     | ResourceNotFoundException if not found                                         |
| UpdateUserPool   | ✅     | Updates VerificationMessageTemplate, AdminCreateUserConfig, EmailConfiguration |
| ListUserPools    | ✅     | Pagination via NextToken                                                       |

## User Pool Client operations

| Operation              | Status | Notes                                                                                                                  |
| ---------------------- | ------ | ---------------------------------------------------------------------------------------------------------------------- |
| CreateUserPoolClient   | ✅     | Returns ClientId (26-char hex); accepts AccessTokenValidity, IdTokenValidity, RefreshTokenValidity, TokenValidityUnits |
| DescribeUserPoolClient | ✅     | ResourceNotFoundException if not found                                                                                 |
| DeleteUserPoolClient   | ✅     | ResourceNotFoundException if not found                                                                                 |
| UpdateUserPoolClient   | ✅     | Updates client name, token validity                                                                                    |
| ListUserPoolClients    | ✅     | Pagination via NextToken                                                                                               |

## User operations

| Operation                 | Status | Notes                                                             |
| ------------------------- | ------ | ----------------------------------------------------------------- |
| AdminCreateUser           | ✅     | Bcrypt hashes password; sends email unless MessageAction=SUPPRESS |
| AdminDeleteUser           | ✅     | UserNotFoundException if not found                                |
| AdminGetUser              | ✅     | Returns attributes + status                                       |
| AdminSetUserPassword      | ✅     | Permanent=true sets status CONFIRMED                              |
| AdminConfirmSignUp        | ✅     | Confirms a UNCONFIRMED user                                       |
| AdminUpdateUserAttributes | ✅     | Merges attributes                                                 |
| AdminDeleteUserAttributes | ✅     | Removes named attributes from a user                              |
| AdminDisableUser          | ✅     | Sets Enabled=false; sign-in returns NotAuthorizedException        |
| AdminEnableUser           | ✅     | Re-enables a disabled user                                        |
| ListUsers                 | ✅     | Pagination via PaginationToken                                    |

## Auth / Token operations

| Operation                   | Status | Notes                                                                                        |
| --------------------------- | ------ | -------------------------------------------------------------------------------------------- |
| SignUp                      | ✅     | Sends confirmation email; returns UserSub                                                    |
| ConfirmSignUp               | ✅     | CodeMismatchException / ExpiredCodeException on failure; returns Session for USER_AUTH sign-in |
| ResendConfirmationCode      | ✅     | Generates and emails a new confirmation code                                                 |
| InitiateAuth                | ✅     | USER_PASSWORD_AUTH + REFRESH_TOKEN_AUTH; USER_AUTH with ConfirmSignUp Session; returns NEW_PASSWORD_REQUIRED or SOFTWARE_TOKEN_MFA |
| AdminInitiateAuth           | ✅     | USER_PASSWORD_AUTH + REFRESH_TOKEN_AUTH with UserPoolId; USER_AUTH with ConfirmSignUp Session |
| RespondToAuthChallenge      | ✅     | NEW_PASSWORD_REQUIRED and SOFTWARE_TOKEN_MFA challenges                                      |
| AdminRespondToAuthChallenge | ✅     | Same as above with admin credentials                                                         |
| ForgotPassword              | ✅     | Sends password-reset code by email                                                           |
| ConfirmForgotPassword       | ✅     | Validates reset code; sets new bcrypt password                                               |
| ChangePassword              | ✅     | Validates AccessToken + old password before setting new one                                  |
| GetUser                     | ✅     | Validates AccessToken; returns full user profile                                             |
| UpdateUserAttributes        | ✅     | Self-service; validates AccessToken; merges attributes                                       |
| DeleteUserAttributes        | ✅     | Self-service; validates AccessToken; removes named attributes                                |
| GlobalSignOut               | ✅     | Revokes access + id + refresh tokens for the user                                            |
| RevokeToken                 | ✅     | Revokes a specific refresh token                                                             |
| AssociateSoftwareToken      | ✅     | Issues a TOTP secret for the user; requires valid AccessToken                                |
| VerifySoftwareToken         | ✅     | Verifies a TOTP code and marks the secret verified                                           |
| SetUserMFAPreference        | ✅     | Enables/disables TOTP MFA for the calling user                                               |
| AdminSetUserMFAPreference   | ✅     | Same as above, admin version                                                                 |

## Group operations

| Operation                | Status | Notes                                          |
| ------------------------ | ------ | ---------------------------------------------- |
| CreateGroup              | ✅     | GroupExistsException if duplicate              |
| GetGroup                 | ✅     | ResourceNotFoundException if not found         |
| DeleteGroup              | ✅     | ResourceNotFoundException if not found         |
| UpdateGroup              | ✅     | Updates Description, Precedence, RoleArn       |
| ListGroups               | ✅     | Returns all groups for a pool                  |
| AdminAddUserToGroup      | ✅     | Idempotent                                     |
| AdminRemoveUserFromGroup | ✅     | No error if user is not in group               |
| AdminListGroupsForUser   | ✅     | Returns groups the user belongs to             |
| ListUsersInGroup         | ✅     | Returns users belonging to the specified group |

## User Pool Domain operations

| Operation              | Status | Notes                                                                 |
| ---------------------- | ------ | --------------------------------------------------------------------- |
| CreateUserPoolDomain   | ✅     | Associates a domain with the user pool's hosted UI                    |
| DescribeUserPoolDomain | ✅     | Returns domain details; empty DomainDescription when domain not found |
| DeleteUserPoolDomain   | ✅     | Removes the domain association from the pool                          |
| UpdateUserPoolDomain   | ✅     | Accepted; SSL certificate updates are inert in the emulator           |

---

## Notes

Each user pool exposes a JWKS endpoint used by API Gateways and libraries to validate tokens:

```
GET /{poolId}/.well-known/jwks.json
```

Access and ID tokens are RS256-signed JWTs. The signing key is lazily generated per pool (RSA-2048)
and stored in the emulator state so it survives restarts when a persistent backend is used.

- Target dispatch header: `X-Amz-Target: AWSCognitoIdentityProviderService.<Operation>`.
- Unimplemented operations return JSON `501 Not Implemented`.
- Pool IDs follow the `{region}_{8-char-hex}` format (e.g. `us-east-1_A1B2C3D4`).
- Passwords are stored using bcrypt (cost 10).
- Access and ID tokens are RS256-signed JWTs (standard 3-part format). Refresh, session, and MFA tokens are opaque hex strings.
- Each pool has a lazily-generated RSA-2048 signing key exposed at the JWKS endpoint.
- Email delivery uses the configured SMTP server (the built-in mock SMTP server by default).
- **Per-pool email templates:** `VerificationMessageTemplate` (with `EmailMessage`, `EmailSubject`, `EmailMessageByLink`, `EmailSubjectByLink`, `DefaultEmailOption`, `SmsMessage`), `AdminCreateUserConfig` (with `InviteMessageTemplate` containing `EmailMessage`, `EmailSubject`, `SMSMessage`; plus `AllowAdminCreateUserOnly` and `UnusedAccountValidityDays`), and `EmailConfiguration` (with `EmailSendingAccount`, `SourceArn`, `From`, `ReplyToEmailAddress`). Templates use `{username}` and `{####}` placeholders.
- TOTP MFA: RFC 6238, HMAC-SHA1, 30-second window, 6-digit codes. Clock skew tolerance: ±30 seconds.

## User import (emulator-only)

Users can be imported from a real AWS Cognito user pool into an Overcast pool.
Imported users are placed in `FORCE_CHANGE_PASSWORD` status because password
hashes cannot be extracted from AWS.

### CLI

```
overcast import cognito-users \
  --from-pool-id us-east-1_abc123 \
  --to-pool-id us-east-1_abc123 \
  --from-profile my-aws-profile \
  --batch-size 100
```

| Flag              | Default | Description                                       |
| ----------------- | ------- | ------------------------------------------------- |
| `--from-pool-id`  | (req)   | Source user pool ID in real AWS                   |
| `--to-pool-id`    | (req)   | Target user pool ID in Overcast                   |
| `--from-profile`  |         | AWS profile for source credentials                |
| `--from-region`   |         | AWS region (auto-detected if omitted)             |
| `--user`          |         | Import a single user by sub (UUID)                |
| `--max-users`     | 0       | Limit total users (0 = unlimited)                 |
| `--batch-size`    | 100     | Users per POST to the server                      |
| `--endpoint`      |         | Overcast daemon URL (inherited from root command) |

### HTTP endpoint

**`POST /_overcast/cognito/user-pools/{poolId}/import-users`**

Content-Type: `application/json`

```json
{
  "users": [
    {
      "username": "jdoe",
      "sub": "a1b2c3d4-...",
      "enabled": true,
      "status": "CONFIRMED",
      "createdAt": "2024-01-01T00:00:00Z",
      "modifiedAt": "2024-01-01T00:00:00Z",
      "attributes": [
        {"name": "email", "value": "jdoe@example.com"}
      ],
      "groups": ["Admins"],
      "mfaEnabled": false
    }
  ]
}
```

#### Response

```json
{
  "imported": 1,
  "skipped": 0,
  "errors": []
}
```

#### Status mapping

| AWS status              | Overcast status            |
| ----------------------- | -------------------------- |
| `CONFIRMED`             | `FORCE_CHANGE_PASSWORD`    |
| `FORCE_CHANGE_PASSWORD` | `FORCE_CHANGE_PASSWORD`    |
| `RESET_REQUIRED`        | `FORCE_CHANGE_PASSWORD`    |
| `UNCONFIRMED`           | `UNCONFIRMED`              |
| `DISABLED`              | `DISABLED`                 |
| `ARCHIVED`, `COMPROMISED` | `DISABLED`              |
| `EXTERNAL_PROVIDER`     | _skipped_                  |

#### Behaviour

- The original `sub` is preserved; any `sub` attribute in the payload is overwritten.
- Groups referenced by the user are auto-created as stubs if they don't already exist in the target pool.
- Duplicate usernames are skipped (reported in `errors`).
- No password, confirmation code, or TOTP secret is imported.

<!-- BEGIN overcast:capabilities -->

## Operations

All 70 listed operations are implemented.
Per-operation status, notes and AWS API links: [Cognito operations](cognito/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
