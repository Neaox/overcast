---
title: "ECR troubleshooting"
description: "Push and pull failures against the local registry, leaked registry containers after a kill, and how to reclaim the image storage volume."
section: "Service Reference"
tags:
  - docs
  - ecr
  - services
  - troubleshooting
---

# ECR troubleshooting

Back to [ECR](../ecr.md).

| Symptom | Cause | Fix |
| --- | --- | --- |
| `docker push` answers `405 Method Not Allowed` | There is no registry, so `repositoryUri` fell back to the API base URL | Start Docker and re-read the repository; the URI is re-minted on every read |
| `docker login` times out on a high-numbered port | The fixed port was taken, so the registry fell back to an ephemeral one the daemon cannot reach | Free `OVERCAST_ECR_REGISTRY_PORT` (default `4510`), or point it at another fixed port |
| `proxyconnect tcp: ... i/o timeout` on push | The registry was addressed by a hostname, which a daemon sends through a configured proxy | Use the `localhost:<port>` URI Overcast returns |
| Push fails with an HTTPS error against a remote daemon | The registry speaks plain HTTP, which a daemon only trusts for loopback | Add `<hostname>:<registryPort>` to the daemon's `insecure-registries` |
| `cdk deploy` re-pushes unchanged assets after a restart | The registry ran on an ephemeral port, so its images died with the container | Use the fixed port, which is backed by a named volume |
| A container image fails to pull at task or invoke time | The repository record outlived the image | Read the repository — reconciliation drops records the registry cannot serve — then push again |

## Reclaiming a leaked registry container

Shutting Overcast down removes its registry container. Killing it — `SIGKILL`, a
crash, a container runtime pulling the rug out — does not: the container runs
with `AutoRemove`, which fires when the *registry* exits, not when its owner
does. What is left behind is one container, one held port, and a registry whose
password no running process knows. Nothing resolves to it and nothing pushes to
it; the cost is the clutter.

This is confined to registries on an ephemeral port. A registry on the fixed port
is named after that port, so the next start finds the name, knows its holder can
only be a predecessor, and replaces it.

Overcast does not reclaim these automatically. A *running* registry Overcast does
not own is indistinguishable from one a second instance is using right now: same
image, same labels, same refusal of another instance's credentials. Removing a
live sibling's registry mid-push is a real failure; leaving a dead one running is
untidy. Any automatic sweep trades the second for a chance of the first.

An operator has the one piece of information Overcast does not — whether any
Overcast is running. With none running, every managed registry container is
leaked:

```bash
docker ps --filter label=overcast.service=ecr
```

```bash
docker rm -f $(docker ps -q --filter label=overcast.service=ecr)
```

Run those while an instance is up and you take its registry with you, so check
the first before running the second.

## Reclaiming the storage volume

A volume is not removed with its container — being outlived by one is the whole
point of it — so discarding the images means removing it deliberately. It carries
the same label the containers do:

```bash
docker volume ls --filter label=overcast.service=ecr
```

```bash
docker volume rm overcast-ecr-registry-data-4510
```

> [!CAUTION]
> This discards every image pushed to that registry. Nothing warns you and
> nothing rebuilds them: the next repository read drops the records, so the next
> `cdk deploy` re-pushes its assets and anything expecting a hand-pushed image
> gets a 404 at pull time.

Do it for a corrupt volume, a stale image you cannot otherwise displace, or to
reclaim the disk — not as routine cleanup. Unlike a leaked container, a volume
left behind by a killed Overcast is not a problem to fix: it holds no port and
runs no process, and the next instance on the same fixed port picks it up and
carries on with the images already in it.
