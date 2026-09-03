---
title: "ECR limitations"
description: "How a repository URI is derived and why it says localhost, what survives a restart, and how the image inventory tracks the registry rather than accumulating."
section: "Service Reference"
tags:
  - docs
  - ecr
  - limitations
  - services
---

# ECR limitations

How a repository URI is derived, what survives a restart, and what the image
inventory tracks, behind [ECR](../ecr.md).

## The repository URI

```
localhost:<registryPort>/<accountId>/<repositoryName>
```

The port is the **registry container's**, not the API port — the URI is what a
`docker push` targets. `proxyEndpoint` from `GetAuthorizationToken` names the
same address, and `Fn::GetAtt Repo.RepositoryUri` returns it rather than an
`amazonaws.com` one.

**It is re-minted on every read, not stored.** Repositories persist and the
registry does not, so a repository read while the registry is down reports the
fallback address, and reports the registry again once there is one.

### Why `localhost` and not `OVERCAST_HOSTNAME`

`docker push`, `pull` and `login` are performed by the Docker daemon, and a
daemon trusts plain HTTP to `localhost` without configuration while sending a
hostname such as `localhost.overcast.sh` through any proxy it has configured
(`proxyconnect tcp: dial tcp 192.168.65.1:3128: i/o timeout` on Docker
Desktop). Where the daemon cannot be shown to reach `localhost` — a remote
daemon — `OVERCAST_HOSTNAME` stands instead, and that address is the one to add
to the daemon's `insecure-registries`.

### The startup reachability probe

Startup has the daemon itself contact the registry, through the Engine's
distribution-inspect endpoint, and warns once when it cannot rather than letting
every later push and pull fail on its own. The probe carries this instance's
credentials, which is what tells this registry from a sibling's — see
[A port answering as another instance's registry](./troubleshooting.md#a-port-answering-as-another-instances-registry).

## When the fixed port is taken

If something else holds `OVERCAST_ECR_REGISTRY_PORT` (default `4510`), the
registry falls back to an ephemeral port and says so in the log. Measured on
Docker Desktop 29.6.2 for Windows, the daemon reaches a fixed publish and not an
ephemeral one — `docker login localhost:5099` succeeds where
`docker login localhost:62154` times out on the same registry image, though
`docker port` reports both bindings as dual-stack.

Overcast says so at startup ("the Docker daemon cannot reach the ECR registry it
just published"). Free the fixed port, or point the variable at another fixed
one, rather than working around the pull failures downstream.

## Persistence

Images pushed to the fixed port survive a restart. The registry container's
storage (`/var/lib/registry`) is the named Docker volume
`overcast-ecr-registry-data-<port>`, labelled `overcast.service=ecr` like every
other resource Overcast manages. The container itself stays disposable — it runs
with `AutoRemove`, which reclaims only the anonymous volume it would otherwise
have been given.

That is what stops `cdk deploy` re-uploading: cdk-assets asks `DescribeImages`
for a container asset's content-hash tag and skips both the build and the push
when it resolves.

**Only the fixed-port registry gets a volume.** The ephemeral fallback's
container name is random and its port is whatever the daemon had spare, so an
ephemeral registry's contents die with its container.
`OVERCAST_ECR_REGISTRY_PERSIST=false` goes back to that behaviour on the fixed
port too.

Repository *metadata* follows the state backend, not the volume, so a run on
`OVERCAST_STATE=memory` comes back with no repositories even though the images
are still there. Re-creating the repository is enough for them to reappear: the
first read reconciles it against the registry.

## Asking whether an image is published

`DescribeImages` answers an `imageIds` entry it cannot resolve with
`ImageNotFoundException`, as real ECR does, rather than a `200` carrying a short
list. Only a call that named no `imageIds` returns an empty list.

That decides whether anything is ever pushed: cdk-assets treats *any*
non-throwing `DescribeImages` as "already published" and skips building and
pushing the asset, so a `200` for an absent tag leaves an empty repository behind
a clean deploy and a 404 at pull time.

The image inventory follows the registry rather than accumulating. On every
repository read, a manifest the registry serves is recorded and a record the
registry 404s is dropped, so a restart that keeps the registry's storage but
loses the in-memory records rediscovers them, and one that keeps the records but
loses the storage drops them. A record written by `PutImage` is left alone either
way.

## Related

- [ECR](../ecr.md) — quick start and what works
- [ECR troubleshooting](./troubleshooting.md) — push failures, leaked containers, reclaiming storage
- [ECR operations](./operations.md) — per-operation status
- [Environment variable reference](../../configuration/reference.md) — `OVERCAST_ECR_REGISTRY_PORT` and the rest
