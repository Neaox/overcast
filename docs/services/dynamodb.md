---
title: "DynamoDB"
description: "DynamoDB accepts AWS JSON 1.0 and Smithy RPC v2 CBOR. JSON operations are identified by the X-Amz-Target header (e.g. DynamoDB_20120810.PutItem); CBOR operations use..."
section: "Service Reference"
tags:
  - docs
  - dynamodb
  - services
---

# DynamoDB

DynamoDB accepts AWS JSON 1.0 and Smithy RPC v2 CBOR. JSON operations are
identified by the `X-Amz-Target` header (e.g. `DynamoDB_20120810.PutItem`);
CBOR operations use `/service/DynamoDB/operation/<Operation>` with
`Smithy-Protocol: rpc-v2-cbor`.

All data types are supported in the request/response format. The emulator
stores items in their DynamoDB JSON wire format internally to avoid
serialisation round-trip issues.

---

## Tables are region-scoped

A DynamoDB table is a regional resource, and Overcast models it as one. A table
created in `us-east-1` is invisible from `eu-west-1`: `ListTables` does not
report it, `DescribeTable` answers `ResourceNotFoundException`, and the same
table name can exist independently in both regions with entirely separate
items, GSI index entries, `ItemCount` and stream records. Deleting the table in
one region leaves the same-named table in the other untouched.

The region comes from the request, exactly as on AWS — the SigV4 credential
scope, a regional endpoint hostname, or `OVERCAST_DEFAULT_REGION` when the
request names none. A create in one region and a list in another therefore
correctly disagree; that is the emulated behaviour, not a bug.

> **Upgrading an existing database.** Before this, item rows, GSI index entries
> and stream records were keyed by table name alone, so same-named tables in
> different regions shared them. A startup migration rewrites existing rows to
> the region their table was created in, which is already recorded on disk, so
> single-region data carries over untouched. If the same table name existed in
> two regions, those regions genuinely shared one set of rows and nothing on
> disk says which write came from where: the rows are assigned to the
> alphabetically first of those regions and the others start out empty.

---

## Known limitations

- **GSI consistency**: real DynamoDB GSIs are eventually consistent; the emulator is immediately consistent — items are visible in GSI queries the instant they are written. Asking for a strongly consistent read on a GSI (`ConsistentRead=true` with a GSI `IndexName`) is still rejected with a `ValidationException`, exactly as AWS does, so code written against the emulator cannot come to depend on a read mode AWS has no way to serve.
- **TTL expiry** is not enforced in real-time. Items with expired TTL are removed by a background sweeper (runs hourly), not lazily on read.
- **PartiQL** (`ExecuteStatement`, `ExecuteTransaction`, `BatchExecuteStatement`) is explicitly out of scope for v1.
- **Every other modeled DynamoDB operation** — global tables, backups, exports and imports, resource policies, contributor insights, PartiQL — answers `501 Not Implemented` with `x-emulator-unsupported: true`, in DynamoDB's own AWS JSON 1.0 error envelope. Only an `X-Amz-Target` naming no AWS operation at all gets `400 UnknownOperationException`. The endpoint tables below name the global-table operations explicitly; the rest follow the same rule without being listed one by one.

<!-- BEGIN overcast:capabilities -->

## Operations

21 of 28 listed operations are implemented.
Per-operation status, notes and AWS API links: [DynamoDB operations](dynamodb/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
