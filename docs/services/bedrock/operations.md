---
title: "Bedrock operations"
description: "Every Bedrock operation Overcast declares — 2 of 2 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - bedrock
  - docs
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# Bedrock operations

All 2 listed operations are implemented. Back to [Bedrock](../bedrock.md).

## Summary

| Category  | 🧊 Inert |
| --------- | -------- |
| Inference | 2        |

---

## Endpoints

### Inference

| Operation     | Status   | Notes                                                                                                                                                                                                                                                                                                | AWS Docs                                                                                     |
| ------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `InvokeModel` | 🧊 Inert | POST /model/{modelId}/invoke — accepted and answered 200; the required payload is not parsed and no model runs. The response body is an opaque blob whose format belongs to the model, which Overcast does not emulate, so it returns one self-describing field rather than any model family's shape | [docs](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModel.html) |
| `Converse`    | 🧊 Inert | POST /model/{modelId}/converse — accepted and answered with a complete ConverseResponse, every @required member present; no inference runs, so the assistant text is canned and the token counts and latency are zero                                                                                | [docs](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html)    |

## Related

- [Bedrock](../bedrock.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
