# STS — endpoint support

> Generated for Overcast. See also: [AWS STS API Reference](https://docs.aws.amazon.com/STS/latest/APIReference/welcome.html)

## Summary

| Category         | ✅ Supported | ❌ Unsupported |
| ---------------- | ------------ | -------------- |
| Identity         | 1            | 0              |
| Temp credentials | 4            | 0              |
| SAML/OpenID      | 0            | 2              |
| Federation       | 0            | 2              |
| Misc             | 0            | 2              |
| **Total**        | **5**        | **6**          |

## Endpoint details

| Operation                  | Status | Notes                                      | AWS docs                                                                                        |
| -------------------------- | ------ | ------------------------------------------ | ----------------------------------------------------------------------------------------------- |
| GetCallerIdentity          | ✅     | Returns fake account/user ARN              | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetCallerIdentity.html)          |
| GetSessionToken            | ✅     | Returns fake temporary credentials         | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetSessionToken.html)            |
| AssumeRole                 | ✅     | Returns fake credentials + AssumedRoleUser | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html)                 |
| AssumeRoleWithWebIdentity  | ✅     | Returns fake credentials                   | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithWebIdentity.html)  |
| GetFederationToken         | ✅     | Returns fake credentials + FederatedUser   | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetFederationToken.html)         |
| AssumeRoleWithSAML         | ❌     | Returns 501                                | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithSAML.html)         |
| DecodeAuthorizationMessage | ❌     | Returns 501                                | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_DecodeAuthorizationMessage.html) |
| GetAccessKeyInfo           | ❌     | Returns 501                                | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetAccessKeyInfo.html)           |
| GetServiceBearerToken      | ❌     | Returns 501                                | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetServiceBearerToken.html)      |
| AssumeRoot                 | ❌     | Returns 501                                | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoot.html)                 |
| TagSession                 | ❌     | Returns 501                                | [link](https://docs.aws.amazon.com/STS/latest/APIReference/API_TagSession.html)                 |

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

| Category | ✅ Supported |
| -------- | ------------ |
| General  | 5            |

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

<!-- END overcast:capabilities -->
