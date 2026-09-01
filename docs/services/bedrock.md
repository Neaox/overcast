---
title: "Bedrock — Amazon Bedrock Runtime"
description: "Amazon Bedrock Runtime uses the REST JSON protocol, served at AWS's own /model/{modelId}/ inference paths. No model is invoked."
section: "Service Reference"
tags:
  - amazon
  - bedrock
  - docs
  - runtime
  - services
---

# Bedrock — Amazon Bedrock Runtime

Amazon Bedrock Runtime uses the REST JSON protocol, served at the paths AWS
binds it to: `POST /model/{modelId}/invoke` and `POST /model/{modelId}/converse`.
No model is invoked — both answer from a canned response.

---

## Notes

- `modelId` may be a model ID, an inference-profile ID, or an ARN. It is a
  single path segment, so an SDK percent-encodes an ARN's separators; the route
  matches either form.
- **`Converse`** returns a complete `ConverseResponse` — every member the AWS
  model marks required, with the canned assistant text in
  `output.message.content[0].text`. Because nothing is inferred, the token
  counts and `metrics.latencyMs` are all zero.
- **`InvokeModel`** takes and returns an opaque payload whose format belongs to
  the *model*, not to Bedrock. Overcast emulates no model, so it answers with a
  single `overcastEmulator` field rather than imitating one family's shape and
  being wrong for the rest. Use `Converse` where you need a response shape AWS
  itself defines.
- The streaming and token-counting siblings
  (`InvokeModelWithResponseStream`, `ConverseStream`, `CountTokens`) are not
  emulated and return a JSON `501 Not Implemented`, as does any other
  unimplemented operation.

<!-- BEGIN overcast:capabilities -->

## Operations

All 2 listed operations are implemented.
Per-operation status, notes and AWS API links: [Bedrock operations](bedrock/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/bedrock/latest/APIReference/)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
