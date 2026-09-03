---
title: "Bedrock — Amazon Bedrock Runtime"
description: "Quick start, the two Runtime operations and the modelId forms accepted, and what a canned response means for token counts, latency and streaming."
section: "Service Reference"
tags:
  - amazon
  - bedrock
  - docs
  - runtime
  - services
---

# Bedrock — Amazon Bedrock Runtime

`Converse` and `InvokeModel` answer from a canned response. No model runs, so
this exercises your wiring — not your prompts.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws bedrock-runtime converse \
  --model-id anthropic.claude-3-5-sonnet-20241022-v2:0 \
  --messages '[{"role":"user","content":[{"text":"hello"}]}]'
```

Any credentials work; with none configured, run `eval "$(overcast env)"` first
— see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).

The reply is a complete `ConverseResponse` — every member the AWS model marks
required, with fixed assistant text in `output.message.content[0].text`.

## What works

| Area          | Behaviour                                                                                         |
| ------------- | ------------------------------------------------------------------------------------------------- |
| `Converse`    | `POST /model/{modelId}/converse`; full response shape, so an SDK deserialises it without complaint |
| `InvokeModel` | `POST /model/{modelId}/invoke`; answers `{"overcastEmulator": …}`                                  |
| `modelId`     | A model ID, an inference-profile ID, or an ARN — percent-encoded or not                            |

## Differences from AWS

| Area               | On AWS                                            | Overcast                                                 |
| ------------------ | ------------------------------------------------- | -------------------------------------------------------- |
| Inference          | The named model runs                              | Nothing runs; the text is fixed regardless of the prompt |
| Usage metrics      | Real token counts and `metrics.latencyMs`         | All zero                                                 |
| `InvokeModel` body | The model's own schema                            | One `overcastEmulator` field                             |
| Streaming          | `InvokeModelWithResponseStream`, `ConverseStream` | Not implemented — `501 Not Implemented`                  |
| `CountTokens`      | Counts tokens for a model                         | Not implemented — `501 Not Implemented`                  |

## Gotchas

> [!TIP]
> `InvokeModel`'s body is opaque on AWS too — its schema belongs to the model,
> not to Bedrock. Use `Converse` where you need a response shape AWS itself
> defines.

<!-- BEGIN overcast:capabilities -->

## Operations

All 2 listed operations are implemented.
Per-operation status, notes and AWS API links: [Bedrock operations](bedrock/operations.md).

<!-- END overcast:capabilities -->

## Related

- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/bedrock/latest/APIReference/)
