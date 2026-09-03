---
title: "Lambda limitations"
description: "Where Overcast's Lambda diverges from AWS, in one table, with the page carrying the detail behind each row: concurrency, execution environments, event delivery, logging and runtimes."
section: "Service Reference"
tags:
  - docs
  - lambda
  - limitations
  - services
---

# Lambda limitations

Every divergence from AWS behind [Lambda](../lambda.md), and the page that
carries the detail.

## Divergences

| Area | Divergence | Detail |
| --- | --- | --- |
| Async invocation | Retries, destinations and DLQ all work; only the *exhausted*-concurrency retry-to-queue case differs from AWS | [Event delivery](./async.md) |
| Partial batch responses | `ReportBatchItemFailures` is honoured; an unreadable response fails the whole batch | [Event delivery](./async.md) |
| Concurrency quotas | Account-wide quotas and requests-per-second limits are not emulated; only reserved concurrency is enforced | [Concurrency](./concurrency.md) |
| Cold-start latency | Not simulated | [Execution environments](./execution-environments.md) |
| Runtime environment validation | Minimal | [Runtimes](./runtimes.md) |
| Extension telemetry (Logs API / Telemetry API) | HTTP destinations only | [Logging](./logging.md) |
| SnapStart | Not emulated — no restore records; `platform.runtimeDone` reports only `responseLatency` | [Logging](./logging.md) |
| `LoggingConfig: {}` (explicitly empty) | `UpdateFunctionConfiguration` returns `501` | [Logging](./logging.md) |
| `TracingConfig` / `EphemeralStorage` / `KMSKeyArn` | Stored and returned, never enforced | Below |
| Resource policies | `AddPermission` stores and validates; statements are not evaluated at invoke time | Below |
| Update status | Every update but an image `UpdateFunctionCode` answers `Successful` rather than `InProgress` | Below |
| Unqualified `DeleteFunction` | Removes the function record; versions, aliases and version counters are left behind | Below |
| Tagging | Functions and event source mappings only; other taggable resources return `501` | Below |
| Pinned `Code.S3ObjectVersion` | Excluded from the reactive S3 code sync | Below |
| Reactive S3 code sync | Only moves a function onto bytes it is not already running | Below |
| VPC placement | Not restricted on a native Windows or macOS host | Below |

## Recorded but not honoured

`TracingConfig`, `EphemeralStorage` and `KMSKeyArn` are validated against AWS's
own constraints, stored, and returned by `GetFunction`,
`GetFunctionConfiguration`, `CreateFunction` and `UpdateFunctionConfiguration`, so
a template or SDK client reads back exactly what it set. None of them changes what
the function does.

| Setting | What it does not do |
| --- | --- |
| `TracingConfig` | No segment is recorded and no trace exists, whichever `Mode` is set; `Active` and `PassThrough` behave identically |
| `EphemeralStorage` | The size is not enforced — a function configured with 512 MB of `/tmp` gets whatever the Docker host gives it, normally far more |
| `KMSKeyArn` | An association only; environment variables are stored in plaintext, as all Overcast state is |

## Update status

AWS answers every update `LastUpdateStatus: InProgress` and settles a moment
later. Overcast answers `Successful`: a zip deployment and every
`UpdateFunctionConfiguration` are durably applied before the call returns, so
`aws lambda wait function-updated` returns on its first poll. The one update
that really is asynchronous — `UpdateFunctionCode`
pointing a `PackageType=Image` function at a new image — answers `InProgress` and
settles to `Successful`, or `Failed` with
`ImageAccessDenied`/`InvalidImage`/`InternalError`, when the pull does.

## The reactive S3 code sync

An unpinned function is moved onto new bytes when a new object lands at its
`S3Bucket`/`S3Key`. Two limits apply:

- A function whose code names a `Code.S3ObjectVersion` is pinned to that version
  and excluded from the sync, which matches AWS. Use `UpdateFunctionCode` to move
  it.
- The sync only moves a function onto bytes it is not already running. A
  `PutObject` re-uploading an unchanged asset, or one landing just before a
  `CreateFunction` reads the same key, is not a new deployment: `RevisionId` and
  `LastModified` stay put and the warm environment survives.

## Deleting a version, and where tags live

`DeleteFunction` means two different things depending on whether you pass a
qualifier — either as `?Qualifier=` or inside the function name (`my-function:2`):

| Request | Effect |
| --- | --- |
| `DELETE /functions/my-function` | Deletes the function, its package and its resource policies |
| `DELETE /functions/my-function?Qualifier=2` | Deletes **only** published version 2, its qualified policy and its provisioned concurrency |
| `?Qualifier=$LATEST` | `400 InvalidParameterValueException` — `$LATEST` only goes with the function |
| Qualifier naming a version an alias points at | `409 ResourceConflictException` naming the aliases |
| Qualifier naming an alias | `409 ResourceConflictException` — `DeleteFunction` never deletes an alias |
| Qualifier naming neither | `404 ResourceNotFoundException` |

A qualified delete never touches `$LATEST`, the function record, other versions,
aliases or unqualified policies, and it does not rewind the version counter — AWS
never reuses a version number.

Tags attach to the **unqualified** function ARN, never to a version or alias, so
`TagResource`, `UntagResource` and `ListTags` reject a qualified ARN with
`InvalidParameterValueException`. They take an ARN, not a bare function name.
Event source mappings are taggable through the same three operations, and
`CreateEventSourceMapping` accepts a `Tags` map, as CloudFormation sends on
every deploy carrying stack tags. Their tags are stored separately and
never appear in `EventSourceMappingConfiguration`, so `ListTags` is the only way
to read them back. Code signing configurations, capacity providers and network
connectors return `501` from the tag operations.

## VPC placement — `VpcConfig`

A function with a `VpcConfig` naming subnets is placed on that VPC's network and
nothing else. It can reach what is in the VPC with it and cannot reach a
container outside it, matching AWS, where placement subtracts rather than adds.

> [!IMPORTANT]
> **On a native Windows or macOS host the restriction is not applied.** It needs
> Overcast's DNS resolver, and that needs an `/etc/resolv.conf` those hosts do
> not have. There the function joins its VPC network *and* the shared data
> plane and reaches everything on both, so a test of your VPC wiring passes
> whether or not the wiring is correct. Run Overcast in a container to get the
> restriction. See [Networking § The Docker networks Overcast uses](../../networking/docker-networks.md).

Overcast's own API endpoint is the exception and stays reachable from every
function regardless of placement. `AWS_ENDPOINT_URL` and the Lambda Runtime API
ride a separate control plane, so calling S3 or DynamoDB from inside a VPC works
here without the NAT gateway or VPC endpoint AWS would need.

A database in a VPC with the function outside it never worked on AWS either.
Put the function in the VPC, or set `PubliclyAccessible` on the instance; the
refused connection is named rather than left to hang.

## Related

- [Lambda](../lambda.md) — quick start and what works
- [Lambda concurrency](./concurrency.md) — the instance and memory limits
- [Lambda execution environments](./execution-environments.md) — what retires a warm container
- [Lambda event delivery and retries](./async.md) — retries, destinations, batch failures
- [Lambda logging](./logging.md) — the JSON record vocabulary and its levels
- [Lambda runtimes](./runtimes.md) — identifiers, deprecation dates, images
- [Lambda troubleshooting](./troubleshooting.md) — throttles, layer errors, extension endpoints
- [Lambda operations](./operations.md) — per-operation status
- [Lambda, ECS and VPCs](../../networking/vpcs.md) — what VPC membership restricts
