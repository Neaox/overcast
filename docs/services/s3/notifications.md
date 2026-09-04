---
title: "S3 event notifications"
description: "What an Overcast bucket publishes to EventBridge, the envelope the events arrive in, which detail fields are omitted, and how CloudFormation spells the switch."
section: "Service Reference"
tags:
  - docs
  - eventbridge
  - notifications
  - s3
  - services
---

# S3 event notifications

What [S3](../s3.md) publishes when an object is written or deleted, and the
shape it arrives in.

## Turning it on

`NotificationConfiguration` carries `EventBridgeConfiguration` alongside the
queue, topic and Lambda destinations. AWS models it as an element with no
content, so presence is the whole signal: while it is set, the bucket sends
**every** object event to the default event bus, with no event-type selection
and no key filter. Overcast omits it from
`GetBucketNotificationConfiguration` when it is not set, and clears it when a
later Put omits it.

## The envelope

Object events go through EventBridge's own delivery path, so rule patterns,
input transformers, retries and dead-letter queues behave as they do for
`PutEvents`. The envelope follows AWS's documented S3 event:

```json
{
  "source": "aws.s3",
  "detail-type": "Object Created",
  "resources": ["arn:aws:s3:::my-bucket"],
  "detail": {
    "version": "0",
    "bucket": { "name": "my-bucket" },
    "object": { "key": "docs/hello.txt", "size": 11, "etag": "…" },
    "reason": "PutObject"
  }
}
```

| Field | Value |
| --- | --- |
| `detail-type` | `Object Created` or `Object Deleted` |
| `reason` | The API operation: `PutObject`, `CopyObject`, `CompleteMultipartUpload` or `DeleteObject` |
| `deletion-type` | On a delete: `Permanently Deleted`, or `Delete Marker Created` when a versioned bucket wrote a tombstone instead |
| `object.version-id` | Present for a bucket with version history |
| `object.sequencer` | On every object event — the hex string AWS documents consumers to compare when ordering two events for the same key |

`version-id` and `sequencer` appear as `versionId` and `sequencer` in the
`Records[].s3.object` payload delivered to SQS and Lambda.

## What is missing

The detail is **partial**: `request-id`, `requester` and `source-ip-address`
are omitted rather than invented. AWS's other `detail-type` values — restore,
storage-class, tagging and ACL events — have no corresponding operation here
and are never published.

## CloudFormation

`AWS::S3::Bucket.NotificationConfiguration.EventBridgeConfiguration` dispatches
through `PutBucketNotificationConfiguration`. CloudFormation spells it as an
`EventBridgeEnabled` flag whose only legal value is `true`; an explicit `false`
is refused: the S3 API has no spelling for it other than the element's
absence.

## Related

- [S3](../s3.md) — quick start and what works
- [S3 limitations](./limitations.md) — versioning, lifecycle, website and encryption
- [S3 operations](./operations.md) — per-operation status
- [EventBridge](../eventbridge.md) — the bus these events are delivered on
