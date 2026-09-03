---
title: "Migrating from LocalStack"
description: "Swap the image line and keep the rest: the compose diff, the endpoint and hostname settings, and where the variable, endpoint and behavioural detail lives."
section: "Getting Started"
tags:
  - docs
  - from
  - localstack
  - migrating
  - migration
---

# Migrating from LocalStack

Overcast is a drop-in replacement for LocalStack: same port, same init-hook
layout, LocalStack's own environment variables honoured directly rather than
renamed, and no auth token or plan. Usually the image is the only line that
changes:

```yaml
# Before
services:
  localstack:
    image: localstack/localstack
    ports: ["4566:4566"]
    environment:
      SERVICES: s3,sqs,dynamodb
      DEBUG: 1

# After — the environment block carries over as-is; both variables are honoured
services:
  overcast:
    image: ghcr.io/overcast-sh/overcast:latest
    ports: ["4566:4566"]
    environment:
      DEBUG: 1
```

## The endpoint and the hostname

| Setting | What to do |
| --- | --- |
| `AWS_ENDPOINT_URL` | Leave it. Overcast listens on `4566` too |
| A `/_localstack/health` healthcheck | Leave it. Overcast serves it in LocalStack's shape |
| `/etc/localstack/init/<stage>.d/` scripts | Leave them. Both init trees are scanned |
| `OVERCAST_HOSTNAME` | Set it to `localhost.overcast.sh` if you use virtual-hosted S3 URLs. An existing `localhost.localstack.cloud` keeps working |
| `LOCALSTACK_AUTH_TOKEN` | Drop it whenever you like. It is recognised, logged once, and gated behind nothing |
| A LocalStack Testcontainers module | Replace it — see [Testcontainers](./testcontainers.md) |

## Where the detail is

| Question | Page |
| --- | --- |
| Which of my environment variables still mean something? | [LocalStack environment variables](./migration/environment-variables.md) |
| Which `/_localstack/` and `/_aws/` paths still answer? | [Endpoints and init hooks](./migration/endpoints.md) |
| What behaves differently once it is running? | [Behavioural differences](./migration/differences.md) |
| Item-by-item status for every port, URL, hostname and tool | [LocalStack compatibility matrix](./localstack-compatibility.md) |

## Coverage

For current coverage use the
[generated service index](./README.md#services) — every emulated service with
its operation count — and the per-service pages behind it for operation-level
detail. CloudFormation coverage is listed separately under
[supported resource types](./cdk/resource-types.md). Overcast has no plans, so
every service it emulates is in the one build.

## Troubleshooting

### "Connection refused" on port 4566

```bash
docker compose ps
curl http://localhost:4566/_overcast/health
```

### A bucket I just created does not exist

Two candidates: path-style addressing, or the state went with the last
container. Storage defaults to `memory` when no volume is mounted — see
[Storage and persistence](./storage.md) and
[S3 addressing](./migration/differences.md#s3-path-style-by-default-virtual-hosted-supported).

### Tests pass with LocalStack but fail here

1. Check the service page under [Services](./README.md#services) — the operation
   may not be emulated yet.
2. Re-run with `OVERCAST_LOG_LEVEL=debug` to see the request and the response.
3. Inspect stored state at `/_overcast/debug/state` (needs `OVERCAST_DEBUG=true`).
4. If the operation is listed ✅ Supported,
   [open a compatibility issue](https://github.com/overcast-sh/overcast/issues/new?template=compat_review.md)
   with a minimal reproduction.

## Related

- [LocalStack environment variables](./migration/environment-variables.md) — the alias and ignored-variable tables
- [Endpoints and init hooks](./migration/endpoints.md) — what `/_localstack/` and `/_aws/` paths map to
- [Behavioural differences](./migration/differences.md) — S3, SQS, Lambda, persistence and egress
- [LocalStack compatibility matrix](./localstack-compatibility.md) — every item, with its status
- [Using AWS SDKs and CLI](./sdk-cli.md) — endpoint and credential setup
- [Troubleshooting](./troubleshooting.md) — a symptom, and where its answer lives
