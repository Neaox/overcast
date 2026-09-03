---
title: "What host and port a URL carries"
description: "Overcast mints every URL on the configured hostname and the port you reached it on, so the same resource shows different ports to the host and to a container. What follows from that."
section: "Networking"
tags:
  - docs
  - endpoints
  - networking
  - ports
  - urls
---

# What host and port a URL carries

Every URL Overcast hands out follows one rule: **the configured
`OVERCAST_HOSTNAME` (when set), on the port *you* reached Overcast on.** Your
request is the only proof of a dialable port — Overcast cannot see its own Docker
port mapping — so under a remapped port (`docker run -p 4652:4566`) host-side
callers get URLs on `:4652` and containers Overcast starts get `:4566`. Each
party receives a URL that works for them, and values that cross the container
boundary mechanically (queue URLs baked into a function's environment by a
deploy, invoke payloads) are rewritten at the boundary.

| Consequence | Why |
| --- | --- |
| The same resource shows different ports to different callers | A stack output read from the host says `:4652`; the same output inside a Lambda says `:4566`. Both dial correctly |
| SQS queue URLs echo your exact origin, with no hostname substitution | SDKs dial the `QueueUrl` itself, so Overcast returns precisely what you just proved reachable |
| The Cognito issuer carries your port | OIDC discovery requires `issuer` to match the URL the configuration was fetched from, and `jwks_uri` must be dialable by whoever validates the token |
| ECR's `repositoryUri` is the exception: it names the registry container's own address, never your origin | The docker daemon dials it, not your API client |

**A remapped port splits the Cognito issuer.** A token minted from the host
carries `:4652` in its `iss`, and a validator inside a container comparing that
string against its own `:4566` issuer reports a mismatch. No single port can be
dialable from both sides of a remap, so publish the API 1:1 (`-p 4566:4566`) and
the issuer becomes identical everywhere. Overcast's own token validation is
unaffected either way.

> [!IMPORTANT]
> All of this presumes `OVERCAST_HOSTNAME`, if set, resolves to Overcast for
> every party. The split-horizon names do; `OVERCAST_HOSTNAME=localhost` does
> not — inside a container `localhost` is the container — and it is the one
> setting that silently breaks container callers. See
> [Hostnames that resolve for every caller](./hostnames.md).

## Related

- [Hostnames that resolve for every caller](./hostnames.md) — choosing the name half
- [Data-plane endpoints](./data-plane-endpoints.md) — where the port differs for a different reason
- [Networking and host-based addressing](../networking.md) — the rest of the addressing story
