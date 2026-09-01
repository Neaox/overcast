---
title: "ECR — Elastic Container Registry"
description: "AWS's ECR control-plane API in front of a real Docker registry, so docker push and pull work locally and ECS and Lambda can run what you pushed."
section: "Service Reference"
tags:
  - container
  - docs
  - ecr
  - registry
  - services
---

# ECR — Elastic Container Registry

AWS's ECR control-plane API in front of a real Docker registry, so a push
lands bytes that ECS and Lambda can actually run.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

URI=$(aws ecr create-repository --repository-name my-app \
  --query 'repository.repositoryUri' --output text)   # localhost:4510/000000000000/my-app

aws ecr get-login-password | docker login --username AWS --password-stdin "${URI%%/*}"

docker tag my-app:latest "$URI:latest"
docker push "$URI:latest"

aws ecr describe-images --repository-name my-app
```

## What works

| Area | Behaviour |
| --- | --- |
| Repositories | Full CRUD, policies, lifecycle policy and tag operations. |
| Registry | A shared `registry:2` container, started lazily, on port `OVERCAST_ECR_REGISTRY_PORT` (default `4510`). |
| Authentication | `GetAuthorizationToken` returns `base64("AWS:<password>")`, valid 12 hours; the same password is provisioned into the registry via htpasswd. |
| Image inventory | Reconciled against the registry on every repository read, in both directions — a manifest the registry serves is recorded, and a record it 404s is dropped. |
| Persistence | Images on the fixed port survive a restart in the named volume `overcast-ecr-registry-data-<port>`. |
| Running what you pushed | ECS task definitions and `PackageType=Image` Lambda functions resolve `{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}` to this registry. |

## Differences from AWS

| Area | Overcast | AWS |
| --- | --- | --- |
| Repository URI host | `localhost:<registryPort>` — the address startup proved the Docker daemon can reach | `{account}.dkr.ecr.{region}.amazonaws.com` |
| Transport | Plain HTTP | HTTPS |
| Image scanning | `DescribeImageScanFindings` always reports scanner-unavailable with empty findings | Real findings |
| Replication, public registries | Not implemented | Supported |
| Image storage | A Docker volume, reclaimed by Docker rather than by `OVERCAST_DATA_DIR` | Managed by AWS |

Why the URI is re-minted on every read, why it says `localhost` rather than
`OVERCAST_HOSTNAME`, and what happens when the fixed port is taken:
[Limitations](ecr/limitations.md).

## Gotchas

> [!IMPORTANT]
> Without Docker there is no registry, and `repositoryUri` falls back to the API
> base URL. A `docker push` there answers `405 Method Not Allowed`, because it is
> not a registry. `CreateRepository` marks that response with
> `x-overcast-emulation-limitation`, which becomes the resource's
> `ResourceStatusReason` inside a CloudFormation deploy.

> [!CAUTION]
> Removing the storage volume discards every image pushed to that registry, with
> no warning and nothing to rebuild them from. See
> [Troubleshooting](ecr/troubleshooting.md).

<!-- BEGIN overcast:capabilities -->

## Operations

All 22 listed operations are implemented.
Per-operation status, notes and AWS API links: [ECR operations](ecr/operations.md).

<!-- END overcast:capabilities -->

## Related

- [Limitations](ecr/limitations.md) — repository URIs, persistence, image inventory
- [Troubleshooting](ecr/troubleshooting.md) — leaked containers, reclaiming storage, push failures
- [ECS](./ecs.md) and [Lambda](./lambda.md) — what runs the images
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
