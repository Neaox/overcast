---
title: "Bedrock — Amazon Bedrock Runtime"
description: "Amazon Bedrock Runtime uses the REST JSON protocol. Routes are served under the /_bedrock/ path prefix."
section: "Service Reference"
tags:
  - amazon
  - bedrock
  - docs
  - runtime
  - services
---

# Bedrock — Amazon Bedrock Runtime

> AWS docs: https://docs.aws.amazon.com/bedrock/latest/APIReference/

Amazon Bedrock Runtime uses the REST JSON protocol. Routes are served under the
`/_bedrock/` path prefix.

---

## Notes

- REST routes are prefixed with `/_bedrock/` (e.g. `POST /_bedrock/model/{modelId}/invoke`).
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Returns canned text responses — no actual model inference is performed.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category  | ✅ Supported |
| --------- | ------------ |
| Inference | 2            |

---

## Endpoints

### Inference

| Operation     | Status       | Notes                                        | AWS Docs                                                                                     |
| ------------- | ------------ | -------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `InvokeModel` | ✅ Supported | POST /model/{modelId}/invoke — invokes model | [docs](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModel.html) |
| `Converse`    | ✅ Supported | POST /model/{modelId}/converse — chat API    | [docs](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html)    |

<!-- END overcast:capabilities -->
