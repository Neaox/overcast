---
title: "S3 limitations"
description: "How S3 versioning, lifecycle rules, website configuration and event notifications diverge from AWS in Overcast, in full."
section: "Service Reference"
tags:
  - docs
  - limitations
  - s3
  - services
  - storage
---

# S3 limitations

The long-form behaviour behind [S3](../s3.md): what each configurable subsystem
really does, and where it stops.

## Versioning: version ids, delete markers and suspended buckets

A bucket has three versioning states.

| State | Behaviour |
| --- | --- |
| **Unversioned** | The initial state. Each key holds one object, a write replaces it, a delete removes it. `ListObjectVersions` reports each object as its key's `null` version, as AWS reports it |
| **Enabled** | Every write mints a version id, returned in `x-amz-version-id`, and the previous version becomes noncurrent. A delete with no `versionId` removes nothing — it adds a *delete marker* that becomes the current version. A delete naming a `versionId` removes that version permanently, and the newest survivor takes its place if it was current |
| **Suspended** | Not "off". Recorded versions are kept and stay addressable; new writes become the key's single `null` version, replacing any previous null version; a delete still leaves a delete marker, with version id `null` |

There is no transition back to unversioned, on AWS or here.

Objects stored **before** a bucket was versioned are the `null` version of their
key. They stay readable, appear in `ListObjectVersions` as `VersionId=null`, and
can be addressed with `?versionId=null`. Nothing has to be migrated by hand.

Reads distinguish AWS's two "not there" answers:

| Request | Answer |
| --- | --- |
| `GET`/`HEAD` of a key whose current version is a delete marker | `404 NoSuchKey` with `x-amz-delete-marker: true` |
| `GET`/`HEAD` naming a delete marker's own `versionId` | `405 MethodNotAllowed` with `x-amz-delete-marker: true` and `Allow: DELETE` |
| `GET`/`HEAD` naming a `versionId` that does not exist | `404 NoSuchVersion` |

`ListObjects` and `ListObjectsV2` never show delete markers or noncurrent
versions — only `ListObjectVersions` does. It returns keys ascending and, within
a key, most recently stored first, with `Version` and `DeleteMarker` elements
interleaved, and supports `prefix`, `delimiter`, `max-keys` and resumable
`key-marker`/`version-id-marker` pagination. A `version-id-marker` without a
`key-marker`, or one naming no version of the marker key, is refused with
`400 InvalidArgument` as AWS refuses it.

Version ids are opaque, URL-safe, fixed-width tokens. Nothing about their
internal structure is part of the contract.

## Lifecycle: version-aware actions

On a versioned bucket the same rules mean different things.

| Rule | Behaviour |
| --- | --- |
| `Expiration` | On the current version of a versioned object it **adds a delete marker** rather than deleting anything; on an unversioned bucket it deletes outright |
| `NoncurrentVersionExpiration` | Permanently removes versions noncurrent for `NoncurrentDays`. The clock starts when the version that *replaced* it was written. `NewerNoncurrentVersions` retains that many newer noncurrent versions first |
| `NoncurrentVersionTransition` | Marks noncurrent versions with a storage class on the same eligibility rules, and still respects the minimum transition size below. Delete markers are never transitioned — there are no bytes to move |
| `ExpiredObjectDeleteMarker` | Removes a delete marker that has become its key's *only* version. AWS refuses it alongside `Days` or `Date` in the same `Expiration`, and so does Overcast (`400 InvalidRequest`) |

Expiration wins over transition for a given version, as on AWS. The hourly
sweeper runs on the injected clock, so a test advances a mock clock rather than
waiting.

## Lifecycle: default minimum transition size

`PutBucketLifecycleConfiguration` accepts the
`x-amz-transition-default-minimum-object-size` header and echoes it on its own
response and on `GetBucketLifecycleConfiguration`. Omitting it stores nothing and
reads back as AWS's default, `all_storage_classes_128K`.

The setting is applied, not merely round-tripped:

| Value | Effect |
| --- | --- |
| `all_storage_classes_128K` (default) | An object under 128 KB is not transitioned to any storage class |
| `varies_by_storage_class` | An object under 128 KB still transitions to `GLACIER` or `DEEP_ARCHIVE`; every other class keeps the 128 KB floor |

A rule whose own `Filter` sets `ObjectSizeGreaterThan` or `ObjectSizeLessThan`
opts out of the default, as on AWS. A value outside the two documented ones is
refused with `400 InvalidArgument` rather than stored. Every
`PutBucketLifecycleConfiguration` replaces the configuration wholesale.

CloudFormation's
`AWS::S3::Bucket.LifecycleConfiguration.TransitionDefaultMinimumObjectSize`
dispatches through this header; S3 still owns the validation.

## Website: stored faithfully, served by nothing

`PutBucketWebsite` accepts the whole `WebsiteConfiguration` —
`RedirectAllRequestsTo`, `IndexDocument`, `ErrorDocument` and `RoutingRules`
with their `Condition` and `Redirect` members — and `GetBucketWebsite` returns
it unchanged. AWS's constraints are enforced:

- `RedirectAllRequestsTo` cannot be combined with any other element, and a
  configuration must carry either it or an `IndexDocument`.
- `Protocol` must be `http` or `https`; an `IndexDocument` `Suffix` must be
  non-empty and slash-free.
- A `Redirect` must name at least one destination field, and `ReplaceKeyWith`
  and `ReplaceKeyPrefixWith` are mutually exclusive.
- A `Condition` must carry at least one predicate.

Anything refused returns `400 InvalidArgument` and leaves the previous
configuration in place. Each Put replaces the whole document;
`DeleteBucketWebsite` removes it.

Two boundaries:

- **`HttpRedirectCode` values are not validated.** Real S3 rejects a code that is
  not a valid HTTP redirect status; Overcast stores whatever it is sent.
- **No website endpoint is served.** A stack that deploys one deploys it, but no
  request is redirected or answered with an index document.

## Notifications: EventBridge

`NotificationConfiguration` carries `EventBridgeConfiguration` alongside the
queue, topic and Lambda destinations. AWS models it as an element with no
content, so presence is the whole signal: while it is set, the bucket sends
**every** object event to the default event bus, with no event-type selection
and no key filter. Overcast omits it from
`GetBucketNotificationConfiguration` when it is not set, and clears it when a
later Put omits it.

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

`detail-type` is `Object Created` or `Object Deleted`; `reason` is the API
operation (`PutObject`, `CopyObject`, `CompleteMultipartUpload`,
`DeleteObject`); a delete carries `deletion-type`, which is `Permanently
Deleted` for a real removal and `Delete Marker Created` when a versioned bucket
wrote a tombstone instead. `object.version-id` is present for a bucket with
version history, and `object.sequencer` on every object event — the hex string
AWS documents consumers to compare when ordering two events for the same key.
Both appear as `versionId` and `sequencer` in the `Records[].s3.object` payload
delivered to SQS and Lambda.

The detail is **partial**: `request-id`, `requester` and `source-ip-address`
are omitted rather than invented. AWS's other
`detail-type` values — restore, storage-class, tagging and ACL events — have no
corresponding operation here and are never published.

CloudFormation's
`AWS::S3::Bucket.NotificationConfiguration.EventBridgeConfiguration` dispatches
through `PutBucketNotificationConfiguration`. CloudFormation spells it as an
`EventBridgeEnabled` flag whose only legal value is `true`; an explicit `false`
is refused: the S3 API has no spelling for it other than the element's
absence.

## Encryption

`PutBucketEncryption` stores AES256 and KMS rules and `GetBucketEncryption`
returns them, defaulting to SSE-S3 when none is set. Nothing is encrypted:
objects are stored as sent. Object-level SSE request headers
(`x-amz-server-side-encryption` and its SSE-C siblings) are ignored — they are
neither stored nor echoed on a later `GetObject` or `HeadObject`.

## Related

- [S3](../s3.md) — quick start and what works
- [S3 operations](./operations.md) — per-operation status
- [Host-routed addressing](../../networking/host-routing.md) — how a bucket subdomain resolves
