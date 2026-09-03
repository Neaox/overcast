---
title: "Behavioural differences from LocalStack"
description: "What changes once the container is running: editions and plans, egress, S3 addressing, SQS queue URLs, Lambda execution, persistence and request IDs."
section: "Getting Started"
tags:
  - docs
  - differences
  - localstack
  - migration
---

# Behavioural differences from LocalStack

The deliberate divergences behind
[Migrating from LocalStack](../migration-from-localstack.md), so you know what
to expect once the container is up. Item-by-item status for every port, URL,
hostname and client tool is in the
[compatibility matrix](../localstack-compatibility.md).

| Area | On LocalStack | On Overcast |
| --- | --- | --- |
| Editions | One image for every plan, an auth token to start it, most services on a paid plan | No plans, no token. Every emulated service is in the one build |
| Egress | A Docker network is never isolated, so a VPC function always reaches the internet | The same by default, and `OVERCAST_VPC_EGRESS` can withhold it or follow your route tables |
| S3 addressing | Virtual-hosted style needs an `s3.` prefix on your endpoint | Path-style by default; both virtual-hosted forms work with no prefix |
| SQS queue URLs | `SQS_ENDPOINT_STRATEGY` picks the host | The URL is minted on the origin the caller reached |
| Lambda execution | Docker, or a local executor | Docker, on the official AWS base images |
| Persistence | Enabled when `DATA_DIR` is set | `auto` infers it the same way |
| Request IDs | Omitted from some error responses | `x-amz-request-id` (or `x-amzn-requestid`) on every response, errors included |
| SigV4 signatures | Verified | Accepted, not verified, until `OVERCAST_SIGV4_VALIDATE=true` |
| IAM policies | Enforced in strict mode | Stored, never enforced, until `OVERCAST_ENFORCE_IAM=true` |

## Editions and plans

LocalStack restructured its editions on 23 March 2026: one image for every
plan, an auth token required to start it, and a free "Hobby" plan for
non-commercial use that leaves most services, and local state persistence, to
the paid Base and Ultimate plans. Overcast has no plans, so a setup that leaned
on a paid-plan service — ECS, RDS, Cognito, CloudFront and the others named in
the [matrix](../localstack-compatibility.md#not-in-scope) — migrates the same
way as one using S3 and SQS, and persistence needs nothing beyond a mounted
volume (see [Storage and persistence](../storage.md)).

A carried-over `LOCALSTACK_AUTH_TOKEN` is recognised and inert: startup logs it
once by name, nothing is gated behind it, and it can stay or go. What has no
equivalent here was never a plan question — snapshot save/load,
`SQS_ENDPOINT_STRATEGY` and the `/_aws/*` inspection endpoints are each listed
in the matrix with the alternative.

## Egress

LocalStack never isolates a Docker network, so a Lambda in a VPC reaches the
internet there whatever its subnets look like. Overcast's default,
`OVERCAST_VPC_EGRESS=open`, is the same — a migration needs nothing here.
`none` withholds egress from everything Overcast starts and `routed` decides it
per subnet from the route table; both are described in
[Egress modes](../networking/egress.md) and
[`routed`](../networking/routed-egress.md).

Two notes if you are moving from an Overcast between 0.0.1-alpha.37 and this
release: egress on your machine may have been withheld and is not any more, and
`OVERCAST_CONTROL_PLANE_INTERNAL` is deprecated in favour of the mode. If you
set it to `false` to restore LocalStack's behaviour, drop it — that is now the
default.

## S3: path-style by default, virtual-hosted supported

Overcast returns path-style URLs (`http://localhost:4566/bucket/key`). Both
virtual-hosted forms work too — `bucket.s3.<base>` and the bare `bucket.<base>` —
and neither needs an `s3.` prefix on your endpoint. What they do need is for the
bucket subdomain to resolve: set `OVERCAST_HOSTNAME=localhost.overcast.sh`, and
an existing `localhost.localstack.cloud` keeps working — see
[Hostnames that resolve for every caller](../networking/hostnames.md).

To force path-style instead of configuring a hostname:

```bash
aws configure set s3.addressing_style path                                  # AWS CLI
# boto3: boto3.client('s3', config=Config(s3={'addressing_style': 'path'}))
```

> [!WARNING]
> CDK's asset publisher always uses virtual-hosted style and ignores
> `forcePathStyle`, so on Windows it needs the hostname, not the setting — see
> [CDK § S3 asset upload fails on Windows](../cdk/troubleshooting.md#s3-asset-upload-fails-on-windows).

## SQS: queue URLs follow the caller

There is no `SQS_ENDPOINT_STRATEGY` equivalent. Overcast mints each queue URL on
the origin the caller reached it on, so a host CLI gets `localhost:4566` and a
Lambda container gets an address it can dial. This matters because AWS SDKs
resolve the SQS endpoint from the `QueueUrl` and ignore `AWS_ENDPOINT_URL` when
doing so — see
[SQS § Queue URLs and endpoint resolution](../services/sqs.md#queue-urls-and-endpoint-resolution).
Queue URLs carried over from a LocalStack setup keep resolving on both sides of
the container boundary, because `localhost.localstack.cloud` is remapped to
Overcast inside the containers it starts. Drop `SQS_ENDPOINT_STRATEGY`.

## Lambda: Docker-based execution

Functions run in containers built on the official AWS base images
(`public.ecr.aws/lambda/<runtime>`), so the Docker socket has to be reachable —
see `LAMBDA_DOCKER_SOCKET` and `OVERCAST_NETWORK` in the
[configuration reference](../configuration/reference.md). Without Docker,
functions can still be created and managed; invocations degrade to a built-in
Node.js runtime for simple handlers.

## Persistence: auto-detected, like LocalStack's `DATA_DIR` presence

LocalStack enables persistence when `DATA_DIR` is set. Overcast's default,
`auto`, infers it the same way — see
[Storage and persistence § The auto default](../storage.md#the-auto-default).
Set `OVERCAST_STATE` explicitly for a specific backend regardless of what is
mounted.

A volume carried over at LocalStack's own path is read where it is. Overcast
keeps state in `/data`, but a compose file migrated line by line still mounts
`/var/lib/localstack` — so when that is the only volume mounted, it becomes the
state directory and a startup line says so. Mount `/data`, or set
`OVERCAST_DATA_DIR`, to choose otherwise; either wins.

```yaml
volumes:
  - "./volume:/var/lib/localstack" # works unchanged
  - "/var/run/docker.sock:/var/run/docker.sock"
```

> [!WARNING]
> **Not true of the `overcast-slim` image or the `overcastd` binaries.** Both are
> built without SQLite, so `auto` there always resolves to `memory` — a mounted
> volume gives you no persistence at all, and nothing announces it beyond the
> startup log. Replacing a LocalStack container that had `DATA_DIR` set? Use the
> full `ghcr.io/overcast-sh/overcast` image, or add `OVERCAST_STATE=wal`. See
> [Builds without SQLite](../storage.md#builds-without-sqlite).

## Related

- [Migrating from LocalStack](../migration-from-localstack.md) — the image swap and the rest of the move
- [LocalStack environment variables](./environment-variables.md) — the alias and ignored-variable tables
- [Endpoints and init hooks](./endpoints.md) — the paths that carry over
- [LocalStack compatibility matrix](../localstack-compatibility.md) — every item, with its status
- [Egress modes](../networking/egress.md) — what `OVERCAST_VPC_EGRESS` changes
