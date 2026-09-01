---
title: "SSM Parameter Store — endpoint support"
description: "Generated for Overcast. See also: AWS SSM Parameter Store API Reference"
section: "Service Reference"
tags:
  - docs
  - endpoint
  - parameter
  - services
  - ssm
  - store
  - support
---

# SSM Parameter Store — endpoint support

> Generated for Overcast. See also: [AWS SSM Parameter Store API Reference](https://docs.aws.amazon.com/systems-manager/latest/APIReference/Welcome.html)

## Protocol

SSM Parameter Store accepts AWS JSON 1.1 requests at `POST /` with
`X-Amz-Target: AmazonSSM.<OperationName>` and Smithy RPC v2 CBOR requests at
`/service/ssm/operation/<OperationName>` with `Smithy-Protocol: rpc-v2-cbor`.

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
| UnlabelParameterVersion      | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_UnlabelParameterVersion.html)  |
| RemoveTagsFromResource       | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_RemoveTagsFromResource.html)   |
| GetServiceSetting            | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetServiceSetting.html)        |
| SendCommand                  | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_SendCommand.html)              |
| StartAutomationExecution     | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_StartAutomationExecution.html) |
| CreateDocument               | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_CreateDocument.html)           |
| RegisterDefaultPatchBaseline | ❌     | Returns 501                                                  | [link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_RegisterDefaultPatchBaseline.html) |

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

## Operations

11 of 18 listed operations are implemented.
Per-operation status, notes and AWS API links: [SSM operations](ssm/operations.md).

<!-- END overcast:capabilities -->

## Related

- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
