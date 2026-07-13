# STS — endpoint support

> Generated for Overcast. See also: [AWS STS API Reference](https://docs.aws.amazon.com/STS/latest/APIReference/welcome.html)

## Summary

| Category         | ✅ Supported | ❌ Unsupported |
| ---------------- | ------------ | -------------- |
| Identity         | 1            | 0              |
| Temp credentials | 4            | 0              |
| SAML/OpenID      | 0            | 2              |
| Federation       | 0            | 1              |
| Misc             | 0            | 3              |
| **Total**        | **5**        | **6**          |

## Endpoint details

| Operation                  | Status | Notes                                      | AWS docs                                                                                        |
| -------------------------- | ------ | ------------------------------------------ | ----------------------------------------------------------------------------------------------- |
| GetCallerIdentity          | ✅     | Returns fake account/user ARN              | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetCallerIdentity.html)          |
| GetSessionToken            | ✅     | Returns fake temporary credentials         | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetSessionToken.html)            |
| AssumeRole                 | ✅     | Returns fake credentials + AssumedRoleUser | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html)                 |
| AssumeRoleWithWebIdentity  | ✅     | Returns fake credentials                   | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithWebIdentity.html)  |
| GetFederationToken         | ✅     | Returns fake credentials + FederatedUser   | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetFederationToken.html)         |
| AssumeRoleWithSAML         | ❌     | Returns NotImplemented                     | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithSAML.html)         |
| AssumeRoot                 | ❌     | Returns NotImplemented                     | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoot.html)                 |
| DecodeAuthorizationMessage | ❌     | Returns NotImplemented                     | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_DecodeAuthorizationMessage.html) |
| GetAccessKeyInfo           | ❌     | Returns NotImplemented                     | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetAccessKeyInfo.html)           |
| GetDelegatedAccessToken    | ❌     | Returns NotImplemented                     | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetDelegatedAccessToken.html)    |
| GetWebIdentityToken        | ❌     | Returns NotImplemented                     | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetWebIdentityToken.html)        |

## SDK compatibility

| SDK                       | Tested |
| ------------------------- | ------ |
| AWS SDK for Go v2         | ❌     |
| AWS SDK for JavaScript v3 | ✅     |
| boto3 (Python)            | ❌     |
| AWS SDK for Java          | ❌     |
| AWS SDK for .NET          | ❌     |

## Notes

- **Credentials**: All temporary credential operations return freshly generated fake credentials (ASIA-prefixed access key, random secret and session token).
- **No credential validation**: Credentials are not stored or validated; they are single-use fake values.
- **Protocol**: Uses AWS Query protocol (form-encoded POST body, XML responses).

<!-- BEGIN overcast:capabilities -->

## Summary

| Category    | ✅ Supported | ❌ Unsupported |
| ----------- | ------------ | -------------- |
| General     | 5            |                |
| Unsupported |              | 6              |

---

## Endpoints

### General

| Operation                   | Status       | Notes | AWS Docs                                                                                       |
| --------------------------- | ------------ | ----- | ---------------------------------------------------------------------------------------------- |
| `AssumeRole`                | ✅ Supported |       | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html)                |
| `AssumeRoleWithWebIdentity` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithWebIdentity.html) |
| `GetCallerIdentity`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetCallerIdentity.html)         |
| `GetFederationToken`        | ✅ Supported |       | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetFederationToken.html)        |
| `GetSessionToken`           | ✅ Supported |       | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetSessionToken.html)           |

### Unsupported

| Operation                    | Status         | Notes                  | AWS Docs                                                                                        |
| ---------------------------- | -------------- | ---------------------- | ----------------------------------------------------------------------------------------------- |
| `AssumeRoleWithSAML`         | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithSAML.html)         |
| `AssumeRoot`                 | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoot.html)                 |
| `DecodeAuthorizationMessage` | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_DecodeAuthorizationMessage.html) |
| `GetAccessKeyInfo`           | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetAccessKeyInfo.html)           |
| `GetDelegatedAccessToken`    | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetDelegatedAccessToken.html)    |
| `GetWebIdentityToken`        | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetWebIdentityToken.html)        |

<!-- END overcast:capabilities -->
