---
title: "Lambda runtimes"
description: "Which Lambda runtime identifiers Overcast accepts, what each rejection means, how AWS's deprecation dates are observed, and which runtimes have no execution image."
section: "Service Reference"
tags:
  - docs
  - lambda
  - runtimes
  - services
---

# Lambda runtimes

Which runtime identifiers [Lambda](../lambda.md) accepts, and what a rejection
means.

Every runtime identifier comes from one table, so request validation, the
runtime-to-image mapping, AWS's deprecation dates and the web console's create
wizard cannot disagree. Identifiers are the modelled `Runtime` enum from the
pinned AWS Smithy model; deprecation, block-create and block-update dates come
from AWS's published runtime lifecycle table.

## What each rejection means

| Situation | Response |
| --- | --- |
| Not a value of the modelled `Runtime` enum, and not a runtime-version ARN | `400 InvalidParameterValueException` listing every modelled value |
| Modelled, but past AWS's deprecation phase for the operation | `400 InvalidParameterValueException` naming the recommended successor |
| Modelled and accepted by AWS, but Overcast has no execution image for it | `501 NotImplemented` — an emulator gap; nothing is persisted |

The third case is the important one: a missing execution image is **not** a bad
request, so Overcast never invents a `400` for it. The check applies to `Zip`
packages; an `Image` function brings its own.

## Deprecation is observed in three steps

AWS retires a runtime in three steps and Overcast observes each against the
current date:

| Date | Effect |
| --- | --- |
| Deprecation date | Support ends; the runtime stays deployable and invokable |
| Block function create | `CreateFunction` starts refusing it |
| Block function update | `UpdateFunctionConfiguration` starts refusing it |

So a deprecated runtime is not automatically refused — `python3.9` lost support in
December 2025 but stays deployable until its block-create date.

## Which runtimes have an image

Overcast maps an official base image to every runtime AWS still accepts for
`CreateFunction`, across Node.js, Python, Java (including the Amazon Linux 2023
variants), .NET, Ruby and the `provided` custom runtimes. Runtimes AWS has already
blocked from `CreateFunction` — `go1.x`, `java8`, `python3.7` and older,
`nodejs14.x` and older, the `dotnetcore` family, `ruby2.x` and `provided` — carry
no image, so the `400` above is the only response they can produce.

`LAMBDA_SEED_RUNTIME_IMAGES=true` pre-pulls only the images for runtimes AWS still
supports; a deprecated-but-deployable runtime is pulled on demand at its first
cold start. `GET /_overcast/lambda/runtimes` returns the catalogue with each
runtime's `supported`, `deprecated`, `createBlocked` and `updateBlocked` flags.

## Related

- [Lambda](../lambda.md) — quick start and what works
- [Lambda limitations](./limitations.md) — the divergence table
- [Lambda examples](./examples.md) — container images, layers, extensions
- [Lambda troubleshooting](./troubleshooting.md) — throttles, layer errors, extension endpoints
- [Configuration reference](../../configuration.md) — every `LAMBDA_*` environment variable
