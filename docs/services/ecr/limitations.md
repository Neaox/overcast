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

Back to [ECR](../ecr.md).

## The repository URI

```
localhost:<registryPort>/<accountId>/<repositoryName>
```

The port is the **registry container's**, not the API port — the URI is what a
`docker push` targets. `proxyEndpoint` from `GetAuthorizationToken` names the
same address, and `Fn::GetAtt Repo.RepositoryUri` returns it rather than an
`amazonaws.com` one.

**It is re-minted on every read, not stored.** Repositories persist and the
registry does not, so a URI frozen at `CreateRepository` would be a fact about
the run that created it: a `cdk bootstrap` performed before the registry was up
would send every later deploy's push at the API port, for as long as the
repository existed. A repository read while the registry is down therefore
reports the fallback address, and reports the registry again once there is one.

### Why `localhost` and not `OVERCAST_HOSTNAME`

It is the address startup proved. `docker push`, `pull` and `login` are all
performed by the Docker daemon, never by the CLI that asked for them, and
startup picks the port by having the daemon dial `localhost:<port>` itself.

The two are not interchangeable even when they name the same machine. Docker
trusts plain HTTP to `localhost` without configuration and bypasses proxies for
it; a hostname such as `localhost.overcast.sh` is an ordinary domain to a
daemon, so a machine with a proxy configured sends the push to the proxy —
`proxyconnect tcp: dial tcp 192.168.65.1:3128: i/o timeout` on Docker Desktop —
and it never reaches a registry that was listening the whole time.

When the daemon cannot be shown to reach the registry, `localhost` would be a
guess about someone else's machine, so `OVERCAST_HOSTNAME` stands instead. That
is the remote-daemon case, and the address to add to the daemon's
`insecure-registries`.

### The startup reachability probe

Reachability is verified from the daemon's own vantage — the Engine's
distribution-inspect endpoint, which makes the daemon contact the registry. A
daemon that cannot reach it gets one warning naming the problem and the fix,
rather than every later push and pull failing on its own.

The probe carries this instance's credentials, so it establishes *which*
registry is listening and not merely that one is. That matters when two Overcast
instances share a daemon: ephemeral publishes can interleave across address
families, leaving one instance's `localhost:<port>` pointing at the other's
registry. An anonymous probe cannot tell them apart, because every authenticated
registry refuses an anonymous request alike. With credentials the answers
separate — ours accepts them and reports the probe repository absent, a
sibling's rejects them — so a port answering as someone else's is passed over.
The fixed default port never had the ambiguity; this affects only the ephemeral
fallback.

## The ephemeral-port fallback is degraded, not equivalent

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

This matters most under `cdk deploy`: cdk-assets asks `DescribeImages` for a
container asset's content-hash tag and skips both the build and the push when it
resolves, so a registry that came back empty made every deploy after a restart
rebuild and re-upload assets that had not changed.

**Only the fixed-port claim gets a volume**, because only it has something stable
to name one after. The ephemeral fallback's container name is deliberately random
so concurrent instances cannot collide over it, and its port is whatever the
daemon had spare — a volume keyed to either would be a fresh orphan on every
start, and one well-known name shared between them would put two registry
processes on one filesystem, which corrupts it rather than sharing it. An
ephemeral registry's contents still die with its container.

`OVERCAST_ECR_REGISTRY_PERSIST=false` goes back to that behaviour on the fixed
port too — worth doing when the volume itself is the problem, though discarding
it is usually the shorter answer.

Repository *metadata* follows the state backend, not the volume, so a run on
`OVERCAST_STATE=memory` comes back with no repositories even though the images
are still there. Re-creating the repository is enough for them to reappear: the
first read reconciles it against the registry.

## Asking whether an image is published

`DescribeImages` answers an `imageIds` entry it cannot resolve with
`ImageNotFoundException`, as real ECR does, rather than a `200` carrying a short
list. Only a call that named no `imageIds` returns an empty list, because only a
requested identifier can be missing.

The distinction decides whether anything is ever pushed. cdk-assets treats *any*
non-throwing `DescribeImages` as "already published" and skips building and
pushing the asset, so an emulator that answered `200` for an absent tag would
report a clean deploy over an empty repository — and the ECS service or Lambda
function that runs the asset would fail at pull time with a 404.

For the same reason the image inventory follows the registry rather than
accumulating. On every repository read, a manifest the registry serves is
recorded and a record the registry 404s is dropped. Reconciling in both
directions is what makes the answer survivable: a restart that keeps the
registry's storage but loses the in-memory records rediscovers them, and one
that keeps the records but loses the storage drops them. A record written by
`PutImage` is left alone either way — it was never in the registry, so the
registry's silence says nothing about it.
