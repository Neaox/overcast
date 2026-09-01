---
title: "SQS — Simple Queue Service"
description: "SQS supports AWS JSON 1.0, AWS Query, and Smithy RPC v2 CBOR. JSON and Query requests share the root endpoint; the action is identified by the Action query parameter or the..."
section: "Service Reference"
tags:
  - docs
  - queue
  - service
  - services
  - simple
  - sqs
---

# SQS — Simple Queue Service

SQS supports AWS JSON 1.0, AWS Query, and Smithy RPC v2 CBOR. JSON and Query
requests share the root endpoint; the action is identified by the `Action`
query parameter or the `X-Amz-Target` header in SDK requests. RPC v2 CBOR
requests use `/service/AmazonSQS/operation/<Operation>` with
`Smithy-Protocol: rpc-v2-cbor`.

Queue URLs are returned in the form `http://localhost:4566/<account-id>/<queue-name>`.
For local use, `<account-id>` defaults to `000000000000`.

## Queue URLs and endpoint resolution

AWS SDKs resolve the SQS endpoint from the `QueueUrl`, not from the endpoint you
configured. The JS v3 client's `queueUrlMiddleware` replaces the resolved
endpoint with the queue URL's origin whenever the two differ, and .NET and Java
v1 use the queue URL as the request URI outright — a leftover from the Query
protocol, where a queue was addressed by its URL.

**`AWS_ENDPOINT_URL` does not protect against this.** It is resolved through the
endpoint ruleset's `Endpoint` parameter and never becomes the client's
`endpoint` config field, which is what the middleware checks. Only an endpoint
passed explicitly to the client suppresses the override:

```js
// Both of these keep the client pinned to Overcast:
new SQSClient({ endpoint: process.env.AWS_ENDPOINT_URL });
new SQSClient({ useQueueUrlAsEndpoint: false });
```

The practical consequence is that a queue URL is only usable by a caller that
can dial its origin. Overcast therefore mints queue URLs **per request**, on the
origin the caller reached it on: a host CLI hitting `localhost:4566` gets
`localhost:4566` URLs, and a Lambda container calling in on Overcast's container
address gets that address. `OVERCAST_HOSTNAME` is the fallback for callers with
no usable origin, not an override.

Queue URLs minted elsewhere are always accepted — only the queue name is read
from the URL, so a queue created on one origin can be addressed from another.

For URLs that cross the boundary out-of-band — a CDK deploy run on the host
baking `queue.queueUrl` into a function's environment — see
[Lambda: reaching Overcast from function code](lambda.md#reaching-overcast-from-function-code).

---

---

## Known limitations

- Visibility timeout clocks are wall-clock based. They may drift slightly under
  high load in the in-memory backend.
- Message attribute data types `Binary` and `Number` are stored but not validated.
- SQS → Lambda event source mapping requires the Lambda service; see `lambda.md`.

<!-- BEGIN overcast:capabilities -->

## Operations

19 of 21 listed operations are implemented.
Per-operation status, notes and AWS API links: [SQS operations](sqs/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
