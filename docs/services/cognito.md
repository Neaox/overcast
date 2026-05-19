# Cognito — Amazon Cognito User Pools

> AWS docs: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/Welcome.html

Cognito User Pools (Identity Provider) uses the `application/x-amz-json-1.1`
protocol. Operations are identified by the `X-Amz-Target` header with the
prefix `AWSCognitoIdentityProviderService.`. RPC v2 CBOR is also supported via
the Smithy RPC path (`POST /service/cognito/operation/{Operation}`).

**Accepted wire protocols:** `awsJson1_1`, `rpcv2Cbor`

---

## Summary

| Category      | ✅ Supported | ⚠️ Partial | 🚧 WIP | ❌ Unsupported |
| ------------- | ------------ | ---------- | ------ | -------------- |
| User Pools    | 5            | 0          | 0      | 0              |
| Pool Clients  | 5            | 0          | 0      | 0              |
| Users         | 10           | 0          | 0      | 0              |
| Auth / Tokens | 19           | 0          | 0      | 0              |
| Groups        | 9            | 0          | 0      | 0              |
| Domains       | 4            | 0          | 0      | 0              |

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
GET /{region}/{poolId}/.well-known/jwks.json
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

## Summary

| Category                    | ✅ Supported |
| --------------------------- | ------------ |
| User Pool operations        | 7            |
| User Pool Client operations | 5            |
| User operations             | 10           |
| Auth / Token operations     | 32           |
| Group operations            | 9            |
| User Pool Domain operations | 4            |

---

## Endpoints

### User Pool operations

| Operation              | Status       | Notes                                                                                                                                            | AWS Docs                                                                                                          |
| ---------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------- |
| `CreateUserPool`       | ✅ Supported | Returns Id and Arn; Id format {region}_{8-char-hex}; accepts SignInPolicy, email templates, UserAttributeUpdateSettings, and DeviceConfiguration | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_CreateUserPool.html)       |
| `DescribeUserPool`     | ✅ Supported | Returns SignInPolicy, email templates, admin config, email configuration, and UserAttributeUpdateSettings                                        | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_DescribeUserPool.html)     |
| `DeleteUserPool`       | ✅ Supported | ResourceNotFoundException if not found                                                                                                           | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_DeleteUserPool.html)       |
| `UpdateUserPool`       | ✅ Supported | Updates SignInPolicy, VerificationMessageTemplate, AdminCreateUserConfig, EmailConfiguration, UserAttributeUpdateSettings                        | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_UpdateUserPool.html)       |
| `ListUserPools`        | ✅ Supported | Pagination via NextToken                                                                                                                         | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ListUserPools.html)        |
| `SetUserPoolMfaConfig` | ✅ Supported | Stores MfaConfiguration and WebAuthnConfiguration for passkey sign-in; passkey cryptographic validation is intentionally partial                 | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_SetUserPoolMfaConfig.html) |
| `GetUserPoolMfaConfig` | ✅ Supported | Returns stored MfaConfiguration and WebAuthnConfiguration                                                                                        | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GetUserPoolMfaConfig.html) |

### User Pool Client operations

| Operation                | Status       | Notes                                                                                                                                                   | AWS Docs                                                                                                            |
| ------------------------ | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `CreateUserPoolClient`   | ✅ Supported | Returns ClientId (26-char hex); accepts and validates ExplicitAuthFlows, AccessTokenValidity, IdTokenValidity, RefreshTokenValidity, TokenValidityUnits | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_CreateUserPoolClient.html)   |
| `DescribeUserPoolClient` | ✅ Supported | ResourceNotFoundException if not found                                                                                                                  | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_DescribeUserPoolClient.html) |
| `DeleteUserPoolClient`   | ✅ Supported | ResourceNotFoundException if not found                                                                                                                  | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_DeleteUserPoolClient.html)   |
| `UpdateUserPoolClient`   | ✅ Supported | Updates client name, validates ExplicitAuthFlows, token validity                                                                                        | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_UpdateUserPoolClient.html)   |
| `ListUserPoolClients`    | ✅ Supported | Pagination via NextToken                                                                                                                                | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ListUserPoolClients.html)    |

### User operations

| Operation                   | Status       | Notes                                                                                            | AWS Docs                                                                                                               |
| --------------------------- | ------------ | ------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------- |
| `AdminCreateUser`           | ✅ Supported | Bcrypt hashes password; sends email unless MessageAction=SUPPRESS                                | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminCreateUser.html)           |
| `AdminDeleteUser`           | ✅ Supported | UserNotFoundException if not found                                                               | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminDeleteUser.html)           |
| `AdminGetUser`              | ✅ Supported | Returns attributes + status                                                                      | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminGetUser.html)              |
| `AdminSetUserPassword`      | ✅ Supported | Permanent=true sets status CONFIRMED                                                             | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminSetUserPassword.html)      |
| `AdminConfirmSignUp`        | ✅ Supported | Confirms a UNCONFIRMED user                                                                      | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminConfirmSignUp.html)        |
| `AdminUpdateUserAttributes` | ✅ Supported | Merges attributes; honors verification-before-update settings unless *_verified=true is supplied | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminUpdateUserAttributes.html) |
| `AdminDeleteUserAttributes` | ✅ Supported | Removes named attributes from a user                                                             | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminDeleteUserAttributes.html) |
| `AdminDisableUser`          | ✅ Supported | Sets Enabled=false; sign-in returns NotAuthorizedException                                       | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminDisableUser.html)          |
| `AdminEnableUser`           | ✅ Supported | Re-enables a disabled user                                                                       | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminEnableUser.html)           |
| `ListUsers`                 | ✅ Supported | Pagination via PaginationToken                                                                   | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ListUsers.html)                 |

### Auth / Token operations

| Operation                          | Status       | Notes                                                                                                                                                                                                                                                                                                     | AWS Docs                                                                                                                      |
| ---------------------------------- | ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| `SignUp`                           | ✅ Supported | Sends confirmation email; returns UserSub                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_SignUp.html)                           |
| `ConfirmSignUp`                    | ✅ Supported | CodeMismatchException / ExpiredCodeException on failure; returns Session for USER_AUTH sign-in                                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ConfirmSignUp.html)                    |
| `ResendConfirmationCode`           | ✅ Supported | Generates and emails a new confirmation code                                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ResendConfirmationCode.html)           |
| `InitiateAuth`                     | ✅ Supported | USER_PASSWORD_AUTH + USER_SRP_AUTH + REFRESH_TOKEN_AUTH + CUSTOM_AUTH; USER_AUTH with ConfirmSignUp Session, SELECT_CHALLENGE/PASSWORD/PASSWORD_SRP/WEB_AUTHN/EMAIL_OTP/SMS_OTP, or preferred PASSWORD/WEB_AUTHN/EMAIL_OTP/SMS_OTP; returns NEW_PASSWORD_REQUIRED, SOFTWARE_TOKEN_MFA, or DEVICE_SRP_AUTH | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_InitiateAuth.html)                     |
| `AdminInitiateAuth`                | ✅ Supported | USER_PASSWORD_AUTH + USER_SRP_AUTH + REFRESH_TOKEN_AUTH + CUSTOM_AUTH with UserPoolId; USER_AUTH with ConfirmSignUp Session, SELECT_CHALLENGE/PASSWORD/PASSWORD_SRP/WEB_AUTHN/EMAIL_OTP/SMS_OTP, or preferred PASSWORD/WEB_AUTHN/EMAIL_OTP/SMS_OTP                                                        | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminInitiateAuth.html)                |
| `RespondToAuthChallenge`           | ✅ Supported | SELECT_CHALLENGE/PASSWORD/PASSWORD_SRP/WEB_AUTHN/EMAIL_OTP/SMS_OTP, PASSWORD, PASSWORD_VERIFIER, CUSTOM_CHALLENGE, DEVICE_SRP_AUTH, DEVICE_PASSWORD_VERIFIER, WEB_AUTHN, EMAIL_OTP, SMS_OTP, NEW_PASSWORD_REQUIRED, and SOFTWARE_TOKEN_MFA challenges                                                     | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_RespondToAuthChallenge.html)           |
| `AdminRespondToAuthChallenge`      | ✅ Supported | Same as above with admin credentials                                                                                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminRespondToAuthChallenge.html)      |
| `ConfirmDevice`                    | ✅ Supported | Confirms a NewDeviceMetadata device key and stores remembered-device metadata; SRP verifier validation is intentionally partial                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ConfirmDevice.html)                    |
| `GetDevice`                        | ✅ Supported | Returns a confirmed device for the signed-in user                                                                                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GetDevice.html)                        |
| `ListDevices`                      | ✅ Supported | Lists confirmed devices for the signed-in user                                                                                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ListDevices.html)                      |
| `UpdateDeviceStatus`               | ✅ Supported | Marks a signed-in user's confirmed device as remembered or not_remembered                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_UpdateDeviceStatus.html)               |
| `ForgetDevice`                     | ✅ Supported | Removes a confirmed device for the signed-in user                                                                                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ForgetDevice.html)                     |
| `AdminGetDevice`                   | ✅ Supported | Returns a user's confirmed device                                                                                                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminGetDevice.html)                   |
| `AdminListDevices`                 | ✅ Supported | Lists a user's confirmed devices with pagination                                                                                                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminListDevices.html)                 |
| `AdminUpdateDeviceStatus`          | ✅ Supported | Marks a user's confirmed device as remembered or not_remembered                                                                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminUpdateDeviceStatus.html)          |
| `AdminForgetDevice`                | ✅ Supported | Removes a user's confirmed device                                                                                                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminForgetDevice.html)                |
| `ForgotPassword`                   | ✅ Supported | Sends password-reset code by email                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ForgotPassword.html)                   |
| `ConfirmForgotPassword`            | ✅ Supported | Validates reset code; sets new bcrypt password                                                                                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ConfirmForgotPassword.html)            |
| `ChangePassword`                   | ✅ Supported | Validates AccessToken + old password before setting new one                                                                                                                                                                                                                                               | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ChangePassword.html)                   |
| `GetUser`                          | ✅ Supported | Validates AccessToken; returns full user profile                                                                                                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GetUser.html)                          |
| `UpdateUserAttributes`             | ✅ Supported | Self-service; validates AccessToken; merges attributes or creates pending email/phone updates with CodeDeliveryDetailsList                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_UpdateUserAttributes.html)             |
| `VerifyUserAttribute`              | ✅ Supported | Verifies pending email/phone updates and sets *_verified=true                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_VerifyUserAttribute.html)              |
| `GetUserAttributeVerificationCode` | ✅ Supported | Sends or resends email/phone verification codes for the signed-in user                                                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GetUserAttributeVerificationCode.html) |
| `DeleteUserAttributes`             | ✅ Supported | Self-service; validates AccessToken; removes named attributes                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_DeleteUserAttributes.html)             |
| `GlobalSignOut`                    | ✅ Supported | Revokes access + id + refresh tokens for the user                                                                                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GlobalSignOut.html)                    |
| `RevokeToken`                      | ✅ Supported | Revokes a specific refresh token                                                                                                                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_RevokeToken.html)                      |
| `AssociateSoftwareToken`           | ✅ Supported | Issues a TOTP secret for the user; requires valid AccessToken                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AssociateSoftwareToken.html)           |
| `VerifySoftwareToken`              | ✅ Supported | Verifies a TOTP code and marks the secret verified                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_VerifySoftwareToken.html)              |
| `StartWebAuthnRegistration`        | ✅ Supported | Returns passkey CredentialCreationOptions for the signed-in user                                                                                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_StartWebAuthnRegistration.html)        |
| `CompleteWebAuthnRegistration`     | ✅ Supported | Registers passkey credential metadata; attestation validation is intentionally partial                                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_CompleteWebAuthnRegistration.html)     |
| `SetUserMFAPreference`             | ✅ Supported | Enables/disables TOTP MFA for the calling user                                                                                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_SetUserMFAPreference.html)             |
| `AdminSetUserMFAPreference`        | ✅ Supported | Same as above, admin version                                                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminSetUserMFAPreference.html)        |

### Group operations

| Operation                  | Status       | Notes                                          | AWS Docs                                                                                                              |
| -------------------------- | ------------ | ---------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `CreateGroup`              | ✅ Supported | GroupExistsException if duplicate              | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_CreateGroup.html)              |
| `GetGroup`                 | ✅ Supported | ResourceNotFoundException if not found         | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GetGroup.html)                 |
| `DeleteGroup`              | ✅ Supported | ResourceNotFoundException if not found         | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_DeleteGroup.html)              |
| `UpdateGroup`              | ✅ Supported | Updates Description, Precedence, RoleArn       | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_UpdateGroup.html)              |
| `ListGroups`               | ✅ Supported | Returns all groups for a pool                  | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ListGroups.html)               |
| `AdminAddUserToGroup`      | ✅ Supported | Idempotent                                     | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminAddUserToGroup.html)      |
| `AdminRemoveUserFromGroup` | ✅ Supported | No error if user is not in group               | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminRemoveUserFromGroup.html) |
| `AdminListGroupsForUser`   | ✅ Supported | Returns groups the user belongs to             | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminListGroupsForUser.html)   |
| `ListUsersInGroup`         | ✅ Supported | Returns users belonging to the specified group | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ListUsersInGroup.html)         |

### User Pool Domain operations

| Operation                | Status       | Notes                                                                 | AWS Docs                                                                                                            |
| ------------------------ | ------------ | --------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `CreateUserPoolDomain`   | ✅ Supported | Associates a domain with the user pool's hosted UI                    | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_CreateUserPoolDomain.html)   |
| `DescribeUserPoolDomain` | ✅ Supported | Returns domain details; empty DomainDescription when domain not found | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_DescribeUserPoolDomain.html) |
| `DeleteUserPoolDomain`   | ✅ Supported | Removes the domain association from the pool                          | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_DeleteUserPoolDomain.html)   |
| `UpdateUserPoolDomain`   | ✅ Supported | Accepted; SSL certificate updates are inert in the emulator           | [docs](https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_UpdateUserPoolDomain.html)   |

<!-- END overcast:capabilities -->
