---
title: "SSM operations"
description: "Every SSM operation Overcast declares — 11 of 18 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - services
  - ssm
---

<!-- BEGIN overcast:capabilities -->

# SSM operations

11 of 18 listed operations are implemented. Back to [SSM](../ssm.md).

## Summary

| Category      | ✅ Supported | ❌ Unsupported |
| ------------- | ------------ | -------------- |
| General       | 10           |                |
| Parameters    |              | 2              |
| Tags          | 1            |                |
| Advanced/misc |              | 5              |

---

## Endpoints

### General

| Operation             | Status       | Notes                                                                                                                                                                                                | AWS Docs                                                                                             |
| --------------------- | ------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `AddTagsToResource`   | ✅ Supported |                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_AddTagsToResource.html)   |
| `DeleteParameter`     | ✅ Supported |                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DeleteParameter.html)     |
| `DeleteParameters`    | ✅ Supported |                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DeleteParameters.html)    |
| `DescribeParameters`  | ✅ Supported | ParameterFilters keys: Name, Type; options: Equals (the default), BeginsWith, Contains; an unimplemented key or option is refused, not ignored                                                       | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_DescribeParameters.html)  |
| `GetParameter`        | ✅ Supported |                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameter.html)        |
| `GetParameterHistory` | ✅ Supported |                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameterHistory.html) |
| `GetParameters`       | ✅ Supported |                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParameters.html)       |
| `GetParametersByPath` | ✅ Supported |                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetParametersByPath.html) |
| `ListTagsForResource` | ✅ Supported |                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_ListTagsForResource.html) |
| `PutParameter`        | ✅ Supported | Description, Tier, DataType, AllowedPattern and Policies are stored and echoed back rather than a fixed default; Policies is stored as given and expanded into ParameterInlinePolicy entries on read | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_PutParameter.html)        |

### Parameters

| Operation                 | Status         | Notes       | AWS Docs                                                                                                 |
| ------------------------- | -------------- | ----------- | -------------------------------------------------------------------------------------------------------- |
| `LabelParameterVersion`   | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_LabelParameterVersion.html)   |
| `UnlabelParameterVersion` | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_UnlabelParameterVersion.html) |

### Tags

| Operation                | Status       | Notes                         | AWS Docs                                                                                                |
| ------------------------ | ------------ | ----------------------------- | ------------------------------------------------------------------------------------------------------- |
| `RemoveTagsFromResource` | ✅ Supported | Removes tags from a parameter | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_RemoveTagsFromResource.html) |

### Advanced/misc

| Operation                      | Status         | Notes       | AWS Docs                                                                                                      |
| ------------------------------ | -------------- | ----------- | ------------------------------------------------------------------------------------------------------------- |
| `GetServiceSetting`            | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetServiceSetting.html)            |
| `CreateDocument`               | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_CreateDocument.html)               |
| `SendCommand`                  | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_SendCommand.html)                  |
| `StartAutomationExecution`     | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_StartAutomationExecution.html)     |
| `RegisterDefaultPatchBaseline` | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_RegisterDefaultPatchBaseline.html) |

## Related

- [SSM](../ssm.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
