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
localhost:<registryPort>/<accountId>/<repositoryName>
```

For example, `localhost:4510/000000000000/my-app`. The port is the registry
container's, not the API port — this URI is what a `docker push` targets, so it
has to name the registry rather than the emulator. The registry asks for a
fixed port (`OVERCAST_ECR_REGISTRY_PORT`, default `4510`, the same port
LocalStack serves its registry on) so the URI is normally the same from one run
to the next; if something else holds that port the registry falls back to an
ephemeral one and says so in the log. Without Docker there is no registry, and
the URI falls back to the API base URL — a `docker push` there answers
`405 Method Not Allowed`, because it is not a registry.

The URI is re-minted from the running registry every time a repository is read,
not stored with the repository. Repositories are persisted and the registry is
not, so a URI frozen at `CreateRepository` is a fact about the run that created
it: a `cdk bootstrap` performed before the registry was up would send every
later deploy's `docker push` at the API port, for as long as the repository
existed. It follows that a repository read while the registry is down reports
the fallback address, and reports the registry again once there is one.

`proxyEndpoint` from `GetAuthorizationToken` names the same address, and
`Fn::GetAtt Repo.RepositoryUri` returns this value rather than an
`amazonaws.com` one.

The host is `localhost` rather than `OVERCAST_HOSTNAME`, and deliberately so:
it is the address startup proved. `docker push`, `docker pull` and `docker
login` are all performed by the Docker daemon, never by the CLI that requested
them, and startup picks the port by having the daemon dial `localhost:<port>`
itself. Advertising anything else would be offering an address nobody checked.

The two are not interchangeable even when they name the same machine. Docker
trusts plain HTTP to `localhost` without configuration and bypasses proxies for
it; a hostname such as `localhost.overcast.sh` is an ordinary domain to a
daemon, so a machine with a proxy configured sends the push to the proxy —
`proxyconnect tcp: dial tcp 192.168.65.1:3128: i/o timeout` on Docker Desktop —
and it never reaches a registry that was listening the whole time.

When the daemon cannot be shown to reach the registry, `localhost` would be a
guess about someone else's machine, so `OVERCAST_HOSTNAME` stands: that is the
remote-daemon case, and the address to add to its `insecure-registries`.

At startup the registry's reachability is verified from the daemon's
own vantage (the Engine's distribution-inspect endpoint, which makes the daemon
contact the registry); if the daemon cannot reach it — a remote daemon, or a
proxy arrangement that does not loop published ports back — one warning names
the problem and the remediation instead of every later push and pull failing on
its own.

That probe carries this instance's own credentials, so it establishes *which*
registry is listening and not merely that one is. It matters when two Overcast
instances share a daemon: their ephemeral publishes can interleave across
address families, leaving one instance's `localhost:<port>` pointing at the
other's registry. An anonymous probe cannot tell the two apart, because every
authenticated registry refuses an anonymous request alike. With credentials the
answers separate — ours accepts them and reports the probe repository absent, a
sibling's rejects them — so a port answering as someone else's is passed over
rather than advertised. The fixed default port never had the ambiguity; this
affects only the ephemeral fallback.

## Authorization token

`GetAuthorizationToken` returns a token in AWS format: `base64("AWS:<password>")`.
When Docker is available, the same password is provisioned into the lazy-started
shared `registry:2` container via htpasswd auth, so the returned token can be used
for authenticated calls against the local registry endpoint. Token expiry is 12 hours.

## Asking whether an image is published

`DescribeImages` answers an `imageIds` entry it cannot resolve with
`ImageNotFoundException`, as real ECR does, rather than a 200 carrying a short
list. Only a call that named no `imageIds` returns an empty list, because only
a requested identifier can be missing.

The distinction decides whether anything is ever pushed. cdk-assets — the
publisher behind `cdk deploy` — treats *any* non-throwing `DescribeImages` as
"already published" and skips building and pushing the asset, so an emulator
that answers 200 for an absent tag reports a clean deploy over an empty
repository, and the ECS service or Lambda function that runs the asset fails at
pull time with a 404 from the registry.

For the same reason the image inventory follows the registry rather than
accumulating. When a repository is read, Overcast reconciles it against the
registry's manifests: a manifest the registry serves is recorded, and a record
Overcast created from an earlier push is dropped when the registry answers 404
for it. Reconciliation runs in both directions, which is what makes the answer
survivable — a restart that keeps the registry's storage (see
[Persistence](#persistence)) but loses the in-memory records rediscovers them
from the registry, and one that keeps the records but loses the storage drops
them. A record written by `PutImage` is left alone in either direction: it was
never in the registry, so the registry's silence says nothing about it.

## Persistence

Images pushed to the emulated ECR survive an Overcast restart. The registry
container's storage (`/var/lib/registry`) is a named Docker volume,
`overcast-ecr-registry-data-<port>`, labelled like every other resource Overcast
manages. The container itself stays disposable — it still runs with
`AutoRemove`, which reclaims only the *anonymous* volume it would otherwise
have been given.

This matters most under `cdk deploy`. cdk-assets asks `DescribeImages` for a
container asset's content-hash tag and skips both the build and the push when it
resolves, so a registry that came back empty made every deploy after a restart
rebuild and re-upload assets that had not changed.

Only the fixed-port claim (`OVERCAST_ECR_REGISTRY_PORT`, default `4510`) gets a
volume, because only it has something stable to name one after. The ephemeral
fallback's container name is deliberately random so that concurrent instances
cannot collide over it, and its port is whatever the daemon had spare — a volume
keyed to either would be a fresh orphan on every start that no later run could
find, and one well-known name shared between them would put two registry
processes on one filesystem, which corrupts it rather than sharing it. An
ephemeral registry's contents still die with its container.

Set `OVERCAST_ECR_REGISTRY_PERSIST=false` to go back to that behaviour on the
fixed port too — worth doing when the volume itself is the problem, though
discarding it is usually the shorter answer. See [Reclaiming the storage
volume](#reclaiming-the-storage-volume).

Repository metadata follows the state backend, not the volume, so a run on a
backend that keeps nothing (`OVERCAST_STATE=memory`) comes back with no
repositories even though the images are still there. Re-creating a repository is
enough for them to reappear: the first read reconciles it against the registry.

## Running an image from here

ECS resolves a task definition image addressed as
`{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}` — the form CDK
synthesises for a container asset — to this registry, and pulls it with the same
credentials `GetAuthorizationToken` returns. Lambda does the same for a
`PackageType=Image` function whose `Code.ImageUri` has that shape. See
[ECS § Images published to the emulated ECR](./ecs.md#images-published-to-the-emulated-ecr)
and
[Lambda § Container images published to the emulated ECR](./lambda.md#container-images-published-to-the-emulated-ecr).

## Limitations

- Push/pull via `docker push` / `docker pull` requires Docker daemon support.
  The registry speaks plain HTTP, which the daemon accepts for a loopback
  registry without configuration; only a setup that advertises the registry on
  a non-loopback hostname (`OVERCAST_HOSTNAME` pointing at a remote Overcast)
  needs an `insecure-registries` daemon entry for `<hostname>:<registryPort>`.
- Image content lives in the registry's Docker volume, not in Overcast state, so
  it is kept and reclaimed by Docker rather than by `OVERCAST_DATA_DIR`. Only
  the fixed-port claim gets one; an ephemeral registry's images still die with
  its container, and the first read of a repository afterwards drops the records
  the new registry cannot serve, so a publisher is told the truth and pushes
  again. See [Persistence](#persistence).
- The fallback to an ephemeral port is a degraded mode, not an equivalent one.
  Measured on Docker Desktop 29.6.2 for Windows, the daemon reaches a fixed
  publish and not an ephemeral one — `docker login localhost:5099` succeeds
  where `docker login localhost:62154` times out on the same registry image,
  though `docker port` reports both bindings as dual-stack. Overcast says so at
  startup ("the Docker daemon cannot reach the ECR registry it just
  published"); free `OVERCAST_ECR_REGISTRY_PORT`'s port, or point it at another
  fixed one, rather than working around the pull failures downstream.
- A registry on an *ephemeral* port can outlive an Overcast that was killed
  rather than shut down. See [Reclaiming a leaked registry
  container](#reclaiming-a-leaked-registry-container).
- Image content/layers are not stored in Overcast state; read APIs persist manifest metadata derived from `PutImage` calls and from manifests pushed into the local registry.
- Replication and public-registry APIs are not implemented.
- `DescribeImageScanFindings` is supported but always reports scanner-unavailable state with empty findings.

## Reclaiming a leaked registry container

Shutting Overcast down removes its registry container. Killing it — `SIGKILL`,
a crash, a container runtime pulling the rug out — does not: the container runs
with `AutoRemove`, which fires when the *registry* exits, not when its owner
does. What is left behind is one container, one held port, and a registry whose
password no running process knows. Nothing resolves to it and nothing pushes to
it; the cost is the clutter.

This is confined to registries on an ephemeral port, which is the fallback when
`OVERCAST_ECR_REGISTRY_PORT` (default `4510`) is already held. A registry on the
fixed port is named after that port, so the next start finds the name, knows its
holder can only be a predecessor, and replaces it. An ephemeral registry's name
is random precisely so that concurrent instances cannot collide over it, and
that is what leaves nothing to key a reclaim on.

Overcast does not reclaim these automatically, and the reason is worth stating.
A *running* registry that Overcast does not own is indistinguishable from one a
second Overcast instance is using right now: same image, same labels, same
refusal of another instance's credentials. Removing a live sibling's registry
mid-push is a real failure; leaving a dead one running is untidy. Any automatic
sweep trades the second for a chance of the first, so Overcast declines to
guess.

An operator has one piece of information Overcast does not — whether any
Overcast is running. With none running, every managed registry container is
leaked:

```bash
docker ps --filter label=overcast.service=ecr
```

```bash
docker rm -f $(docker ps -q --filter label=overcast.service=ecr)
```

Run those while an instance is up and you will take its registry with you, so
check the first before running the second.

## Reclaiming the storage volume

The section above is about ephemeral-port containers; this one is about the
fixed port's volume, and the two do not overlap. A volume is *not* removed with
its container — being outlived by one is the whole point of it — so discarding
the images means removing it deliberately. It carries the same
`overcast.service=ecr` label the containers do:

```bash
docker volume ls --filter label=overcast.service=ecr
```

```bash
docker volume rm overcast-ecr-registry-data-4510
```

**Removing the volume discards every image pushed to that registry.** Nothing
warns you, and nothing rebuilds them: the next read of a repository reconciles
against a registry that no longer has them and drops their records, so the next
`cdk deploy` re-pushes its assets and anything that expected an image someone
pushed by hand gets a 404 at pull time. Remove it when you want that — a corrupt
volume, a stale image you cannot otherwise displace, or reclaiming the disk —
not as routine cleanup.

Unlike a leaked container, a volume left behind by a killed Overcast is not a
problem to fix. It holds no port and runs no process; the next instance on the
same fixed port picks it up and carries on with the images already in it.

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

| Operation              | Status       | Notes                                                                                                                                        | AWS Docs                                                                                        |
| ---------------------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| `CreateRepository`     | ✅ Supported | Returns ARN, URI, and createdAt                                                                                                              | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_CreateRepository.html)     |
| `DescribeRepositories` | ✅ Supported | Lists all repos or filters by name                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeRepositories.html) |
| `DeleteRepository`     | ✅ Supported | Deletes the repository and all its image records; a repository still holding images raises RepositoryNotEmptyException unless `force` is set | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DeleteRepository.html)     |
| `DescribeRegistry`     | ✅ Supported | Returns registry metadata with empty replication rules                                                                                       | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeRegistry.html)     |

### Auth

| Operation               | Status       | Notes                                                                                        | AWS Docs                                                                                         |
| ----------------------- | ------------ | -------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `GetAuthorizationToken` | ✅ Supported | Returns `base64("AWS:<password>")` and the registry proxy endpoint; token expiry is 12 hours | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_GetAuthorizationToken.html) |

### Images

| Operation                   | Status       | Notes                                                                                                                                                                                           | AWS Docs                                                                                             |
| --------------------------- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `ListImages`                | ✅ Supported | Returns image IDs (tag + digest); reconciles local registry tags when Docker is available                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_ListImages.html)                |
| `DescribeImages`            | ✅ Supported | Returns image detail objects (digest, tags, media type); an imageIds entry that resolves to nothing raises ImageNotFoundException; reconciles local registry manifests when Docker is available | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeImages.html)            |
| `PutImage`                  | ✅ Supported | Stores an image manifest; generates a digest if none supplied                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_PutImage.html)                  |
| `BatchGetImage`             | ✅ Supported | Fetches manifests by tag or digest                                                                                                                                                              | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_BatchGetImage.html)             |
| `DescribeImageScanFindings` | ✅ Supported | Returns empty/not-scanned findings; no scan engine is emulated                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_DescribeImageScanFindings.html) |
| `BatchDeleteImage`          | ✅ Supported | Deletes images by tag or digest                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECR/latest/APIReference/API_BatchDeleteImage.html)          |

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
