---
title: "ECR operations"
description: "Every ECR operation Overcast declares — 22 of 22 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - ecr
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# ECR operations

All 22 listed operations are implemented. Back to [ECR](../ecr.md).

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| General  | 5            |
| Auth     | 1            |
| Images   | 7            |
| Policy   | 6            |
| Tags     | 3            |

---

## Endpoints

### General

| Operation               | Status       | Notes                                                                                                                                        | AWS Docs                                                                                         |
| ----------------------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `CreateRepository`      | ✅ Supported | Returns ARN, URI, createdAt, imageTagMutability, imageScanningConfiguration, and encryptionConfiguration                                     | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_CreateRepository.html)      |
| `DescribeRepositories`  | ✅ Supported | Lists all repos or filters by name                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeRepositories.html)  |
| `DeleteRepository`      | ✅ Supported | Deletes the repository and all its image records; a repository still holding images raises RepositoryNotEmptyException unless `force` is set | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DeleteRepository.html)      |
| `DescribeRegistry`      | ✅ Supported | Returns registry metadata with empty replication rules                                                                                       | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeRegistry.html)      |
| `PutImageTagMutability` | ✅ Supported | Stores MUTABLE/IMMUTABLE and DescribeRepositories echoes it; not enforced against a repeat PutImage of the same tag                          | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_PutImageTagMutability.html) |

### Auth

| Operation               | Status       | Notes                                                                                        | AWS Docs                                                                                         |
| ----------------------- | ------------ | -------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `GetAuthorizationToken` | ✅ Supported | Returns `base64("AWS:<password>")` and the registry proxy endpoint; token expiry is 12 hours | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_GetAuthorizationToken.html) |

### Images

| Operation                       | Status       | Notes                                                                                                                                                                                           | AWS Docs                                                                                                 |
| ------------------------------- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `ListImages`                    | ✅ Supported | Returns image IDs (tag + digest); reconciles local registry tags when Docker is available                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_ListImages.html)                    |
| `DescribeImages`                | ✅ Supported | Returns image detail objects (digest, tags, media type); an imageIds entry that resolves to nothing raises ImageNotFoundException; reconciles local registry manifests when Docker is available | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeImages.html)                |
| `PutImage`                      | ✅ Supported | Stores an image manifest; generates a digest if none supplied                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_PutImage.html)                      |
| `BatchGetImage`                 | ✅ Supported | Fetches manifests by tag or digest                                                                                                                                                              | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_BatchGetImage.html)                 |
| `DescribeImageScanFindings`     | ✅ Supported | Returns empty/not-scanned findings; no scan engine is emulated                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeImageScanFindings.html)     |
| `BatchDeleteImage`              | ✅ Supported | Deletes images by tag or digest                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_BatchDeleteImage.html)              |
| `PutImageScanningConfiguration` | ✅ Supported | Stores scanOnPush and DescribeRepositories echoes it; no scan engine is emulated                                                                                                                | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_PutImageScanningConfiguration.html) |

### Policy

| Operation                | Status       | Notes                                                      | AWS Docs                                                                                          |
| ------------------------ | ------------ | ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `SetRepositoryPolicy`    | ✅ Supported | Stores arbitrary IAM policy text                           | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_SetRepositoryPolicy.html)    |
| `GetRepositoryPolicy`    | ✅ Supported | Retrieves stored policy; returns 400 if none set           | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_GetRepositoryPolicy.html)    |
| `DeleteRepositoryPolicy` | ✅ Supported |                                                            | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DeleteRepositoryPolicy.html) |
| `PutLifecyclePolicy`     | ✅ Supported | Stores lifecycle policy text for the repository            | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_PutLifecyclePolicy.html)     |
| `GetLifecyclePolicy`     | ✅ Supported | Retrieves stored lifecycle policy; returns 400 if none set | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_GetLifecyclePolicy.html)     |
| `DeleteLifecyclePolicy`  | ✅ Supported |                                                            | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DeleteLifecyclePolicy.html)  |

### Tags

| Operation             | Status       | Notes                                  | AWS Docs                                                                                       |
| --------------------- | ------------ | -------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported | Adds/merges tags onto a repository ARN | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes tag keys from a repository ARN | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported |                                        | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_ListTagsForResource.html) |

## Related

- [ECR](../ecr.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
