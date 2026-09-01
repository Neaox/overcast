---
title: "Pipes operations"
description: "Every Pipes operation Overcast declares — 8 of 8 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - pipes
  - services
---

<!-- BEGIN overcast:capabilities -->

# Pipes operations

All 8 listed operations are implemented. Back to [Pipes](../pipes.md).

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| Pipes    | 5            |
| Tags     | 3            |

---

## Endpoints

### Pipes

| Operation      | Status       | Notes                                                                                                                                                       | AWS Docs                                                                                     |
| -------------- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `CreatePipe`   | ✅ Supported | Validates source/enrichment/target wiring up front, including a stream source's DeadLetterConfig destination; async state machine (CREATING→RUNNING)        | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_CreatePipe.html)   |
| `DescribePipe` | ✅ Supported | Returns the full pipe configuration and current state                                                                                                       | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_DescribePipe.html) |
| `UpdatePipe`   | ✅ Supported | Updates DesiredState, description, role and parameter blocks, re-validating wiring and DeadLetterConfig on a reconfiguring change (UPDATING→previous state) | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_UpdatePipe.html)   |
| `DeletePipe`   | ✅ Supported | Async deletion (DELETING→removed)                                                                                                                           | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_DeletePipe.html)   |
| `ListPipes`    | ✅ Supported | Lists all pipes as PipeSummary                                                                                                                              | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_ListPipes.html)    |

### Tags

| Operation             | Status       | Notes                               | AWS Docs                                                                                            |
| --------------------- | ------------ | ----------------------------------- | --------------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported | Adds/merges tags by pipe ARN        | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes tags by key from a pipe ARN | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Returns tags for a pipe ARN         | [docs](https://docs.aws.amazon.com/eventbridge/latest/pipes-reference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
