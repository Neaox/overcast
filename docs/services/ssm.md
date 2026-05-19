# SSM Parameter Store — endpoint support

> Generated for Overcast. See also: [AWS SSM Parameter Store API Reference](https://docs.aws.amazon.com/systems-manager/latest/APIReference/Welcome.html)

## Summary

## Protocol

SSM Parameter Store accepts AWS JSON 1.1 requests at `POST /` with
`X-Amz-Target: AmazonSSM.<OperationName>` and Smithy RPC v2 CBOR requests at
`/service/ssm/operation/<OperationName>` with `Smithy-Protocol: rpc-v2-cbor`.

| Category      | ✅ Supported | ❌ Unsupported |
| ------------- | ------------ | -------------- |
| Parameters    | 8            | 0              |
| Tags          | 2            | 0              |
| Advanced/misc | 0            | 8              |
| **Total**     | **10**       | **8**          |

## Endpoint details

| Operation                    | Status | Notes                                                        | AWS docs                                                                                                  |
| ---------------------------- | ------ | ------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------- |
| PutParameter                 | ✅     | String, StringList, SecureString; versioning; Overwrite      | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_PutParameter.html)             |
| GetParameter                 | ✅     | Latest version; SecureString masked without WithDecryption   | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameter.html)             |
| GetParameters                | ✅     | Batch get; invalid names returned in InvalidParameters       | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameters.html)            |
| GetParametersByPath          | ✅     | Recursive + non-recursive; MaxResults + NextToken pagination | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParametersByPath.html)      |
| DescribeParameters           | ✅     | Name BeginsWith filter; pagination                           | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DescribeParameters.html)       |
| GetParameterHistory          | ✅     | All versions; pagination                                     | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameterHistory.html)      |
| AddTagsToResource            | ✅     | Tags on Parameter resources                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_AddTagsToResource.html)        |
| ListTagsForResource          | ✅     | Returns all tags for a parameter                             | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_ListTagsForResource.html)      |
| DeleteParameter              | ✅     | Single parameter deletion                                    | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DeleteParameter.html)          |
| DeleteParameters             | ✅     | Batch delete; invalid names returned in InvalidParameters    | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DeleteParameters.html)         |
| LabelParameterVersion        | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_LabelParameterVersion.html)    |
| RemoveTagsFromResource       | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_RemoveTagsFromResource.html)   |
| GetParametersByPathRecursive | ➡️     | Via GetParametersByPath with Recursive=true                  |                                                                                                           |
| PutParameters                | ❌     | Returns 501                                                  |                                                                                                           |
| RegisterDefaultPatchBaseline | ❌     | Returns 501                                                  |                                                                                                           |
| SendCommand                  | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_SendCommand.html)              |
| StartAutomationExecution     | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_StartAutomationExecution.html) |
| CreateDocument               | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_CreateDocument.html)           |

## SDK compatibility

| SDK                       | Tested |
| ------------------------- | ------ |
| AWS SDK for Go v2         | ❌     |
| AWS SDK for JavaScript v3 | ✅     |
| boto3 (Python)            | ❌     |
| AWS SDK for Java          | ❌     |
| AWS SDK for .NET          | ❌     |

## Notes

- **Versioning**: Every `PutParameter` (including overwrites) creates a new version. `GetParameterHistory` returns all versions.
- **SecureString**: When `WithDecryption: false`, the value is replaced with a masked placeholder. When `WithDecryption: true`, the plaintext is returned (no real KMS encryption).
- **Path hierarchy**: `GetParametersByPath` with `Recursive: false` returns only direct children of the given path. With `Recursive: true`, all descendants are returned.
- **Pagination**: `GetParametersByPath` and `DescribeParameters` support `MaxResults` + `NextToken` pagination.
- **Protocol**: Uses AWS JSON 1.1 (`X-Amz-Target: AmazonSSM.*`,
  `application/x-amz-json-1.1`) and Smithy RPC v2 CBOR.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| General  | 10           |

---

## Endpoints

### General

| Operation             | Status       | Notes | AWS Docs                                                                                             |
| --------------------- | ------------ | ----- | ---------------------------------------------------------------------------------------------------- |
| `AddTagsToResource`   | ✅ Supported |       | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_AddTagsToResource.html)   |
| `DeleteParameter`     | ✅ Supported |       | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DeleteParameter.html)     |
| `DeleteParameters`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DeleteParameters.html)    |
| `DescribeParameters`  | ✅ Supported |       | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DescribeParameters.html)  |
| `GetParameter`        | ✅ Supported |       | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameter.html)        |
| `GetParameterHistory` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameterHistory.html) |
| `GetParameters`       | ✅ Supported |       | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameters.html)       |
| `GetParametersByPath` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParametersByPath.html) |
| `ListTagsForResource` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_ListTagsForResource.html) |
| `PutParameter`        | ✅ Supported |       | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_PutParameter.html)        |

<!-- END overcast:capabilities -->
