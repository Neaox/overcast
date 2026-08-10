---
title: "ECR — Elastic Container Registry"
description: "Overcast emulates the ECR control-plane API (AmazonEC2ContainerRegistry_V20150921.*). All operations use AWS JSON 1.1 over HTTPS, dispatched via X-Amz-Target. RPC v2 CBOR is also..."
section: "Service Reference"
tags:
  - container
  - docs
  - ecr
  - elastic
  - registry
  - services
---

# ECR — Elastic Container Registry

Overcast emulates the ECR control-plane API (`AmazonEC2ContainerRegistry_V20150921.*`).
All operations use AWS JSON 1.1 over HTTPS, dispatched via `X-Amz-Target`.
RPC v2 CBOR is also supported via the Smithy RPC path
(`POST /service/ecr/operation/{Operation}`).

**Accepted wire protocols:** `awsJson1_1`, `rpcv2Cbor`

## Repository URI

Repositories are assigned a URI on the registry Overcast serves:

```
<hostname>:<registryPort>/<accountId>/<repositoryName>
```

For example, `localhost:4510/000000000000/my-app`. The port is the registry
container's, not the API port — this URI is what a `docker push` targets, so it
has to name the registry rather than the emulator. The registry asks for a
fixed port (`OVERCAST_ECR_REGISTRY_PORT`, default `4510`, the same port
LocalStack serves its registry on) so the URI is stable across restarts; if
something else holds that port the registry falls back to an ephemeral one and
says so in the log. Without Docker there is no registry, and the URI falls back
to the API base URL.

`proxyEndpoint` from `GetAuthorizationToken` names the same address, and
`Fn::GetAtt Repo.RepositoryUri` returns this value rather than an
`amazonaws.com` one.

The address is chosen for the party that actually dials it. `docker push`,
`docker pull` and `docker login` are all performed by the Docker daemon, never
by the CLI that requested them, so `localhost` here means the daemon's own
loopback — which is correct on native Linux and on Docker Desktop alike, and
regardless of where the client runs. Docker also trusts loopback registries
with plain HTTP automatically, so no `insecure-registries` configuration is
needed. At startup the registry's reachability is verified from the daemon's
own vantage (the Engine's distribution-inspect endpoint, which makes the daemon
contact the registry); if the daemon cannot reach it — a remote daemon, or a
proxy arrangement that does not loop published ports back — one warning names
the problem and the remediation instead of every later push and pull failing on
its own.

## Authorization token

`GetAuthorizationToken` returns a token in AWS format: `base64("AWS:<password>")`.
When Docker is available, the same password is provisioned into the lazy-started
shared `registry:2` container via htpasswd auth, so the returned token can be used
for authenticated calls against the local registry endpoint. Token expiry is 12 hours.

## Running an image from here

ECS resolves a task definition image addressed as
`{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}` — the form CDK
synthesises for a container asset — to this registry, and pulls it with the same
credentials `GetAuthorizationToken` returns. See
[ECS § Images published to the emulated ECR](./ecs.md#images-published-to-the-emulated-ecr).

## Limitations

- Push/pull via `docker push` / `docker pull` requires Docker daemon support.
  The registry speaks plain HTTP, which the daemon accepts for a loopback
  registry without configuration; only a setup that advertises the registry on
  a non-loopback hostname (`OVERCAST_HOSTNAME` pointing at a remote Overcast)
  needs an `insecure-registries` daemon entry for `<hostname>:<registryPort>`.
- Images live in the registry container, which is removed on shutdown, so a
  restart starts empty — repository metadata persists, image content does not.
- Image content/layers are not stored in Overcast state; read APIs persist manifest metadata derived from `PutImage` calls and from manifests pushed into the local registry.
- Replication and public-registry APIs are not implemented.
- `DescribeImageScanFindings` is supported but always reports scanner-unavailable state with empty findings.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| General  | 4            |
| Auth     | 1            |
| Images   | 6            |
| Policy   | 6            |
| Tags     | 3            |

---

## Endpoints

### General

| Operation              | Status       | Notes                                                  | AWS Docs                                                                                        |
| ---------------------- | ------------ | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------- |
| `CreateRepository`     | ✅ Supported | Returns ARN, URI, and createdAt                        | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_CreateRepository.html)     |
| `DescribeRepositories` | ✅ Supported | Lists all repos or filters by name                     | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeRepositories.html) |
| `DeleteRepository`     | ✅ Supported | Deletes the repository and all its image records       | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DeleteRepository.html)     |
| `DescribeRegistry`     | ✅ Supported | Returns registry metadata with empty replication rules | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeRegistry.html)     |

### Auth

| Operation               | Status       | Notes                                                                                        | AWS Docs                                                                                         |
| ----------------------- | ------------ | -------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `GetAuthorizationToken` | ✅ Supported | Returns `base64("AWS:<password>")` and the registry proxy endpoint; token expiry is 12 hours | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_GetAuthorizationToken.html) |

### Images

| Operation                   | Status       | Notes                                                                                                                 | AWS Docs                                                                                             |
| --------------------------- | ------------ | --------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `ListImages`                | ✅ Supported | Returns image IDs (tag + digest); reconciles local registry tags when Docker is available                             | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_ListImages.html)                |
| `DescribeImages`            | ✅ Supported | Returns image detail objects (digest, tags, media type); reconciles local registry manifests when Docker is available | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeImages.html)            |
| `PutImage`                  | ✅ Supported | Stores an image manifest; generates a digest if none supplied                                                         | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_PutImage.html)                  |
| `BatchGetImage`             | ✅ Supported | Fetches manifests by tag or digest                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_BatchGetImage.html)             |
| `DescribeImageScanFindings` | ✅ Supported | Returns empty/not-scanned findings; no scan engine is emulated                                                        | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeImageScanFindings.html) |
| `BatchDeleteImage`          | ✅ Supported | Deletes images by tag or digest                                                                                       | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_BatchDeleteImage.html)          |

### Policy

| Operation                | Status       | Notes                                                      | AWS Docs                                                                                          |
| ------------------------ | ------------ | ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `SetRepositoryPolicy`    | ✅ Supported | Stores arbitrary IAM policy text                           | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_SetRepositoryPolicy.html)    |
| `GetRepositoryPolicy`    | ✅ Supported | Retrieves stored policy; returns 400 if none set           | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_GetRepositoryPolicy.html)    |
| `DeleteRepositoryPolicy` | ✅ Supported |                                                            | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DeleteRepositoryPolicy.html) |
| `PutLifecyclePolicy`     | ✅ Supported | Stores lifecycle policy text for the repository            | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_PutLifecyclePolicy.html)     |
| `GetLifecyclePolicy`     | ✅ Supported | Retrieves stored lifecycle policy; returns 400 if none set | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_GetLifecyclePolicy.html)     |
| `DeleteLifecyclePolicy`  | ✅ Supported |                                                            | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DeleteLifecyclePolicy.html)  |

### Tags

| Operation             | Status       | Notes                                  | AWS Docs                                                                                       |
| --------------------- | ------------ | -------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported | Adds/merges tags onto a repository ARN | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes tag keys from a repository ARN | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported |                                        | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
