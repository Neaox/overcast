---
title: Wildcard hostnames do not resolve to Overcast inside containers
description: /etc/hosts cannot express wildcards, so *.localhost.overcast.sh resolves to 127.0.0.1 — the container itself — inside a Lambda. Virtual-hosted S3 and API Gateway URLs are unreachable from function code. Found during alpha.26 release smoke testing.
---

# Wildcard hostnames do not resolve to Overcast inside containers

**Status:** open · **Found:** 2026-07-29, alpha.26 pre-release smoke testing
**Severity:** virtual-hosted S3, API Gateway, AppSync and Lambda function URLs are undialable from
inside a Lambda or ECS container. Pre-existing — **not** an alpha.26 regression, and not a release
blocker for it.
**Related:** #318 (per-caller queue URLs), #353 (stack output origins), `internal/containerendpoint`

## Symptom

Overcast rewrites `/etc/hosts` in every container it starts so its split-horizon hostnames point
back at it. That works for the exact names, and only for the exact names. Measured from inside a
real Lambda container (`dns.promises.lookup`, Overcast at `172.18.0.2`):

| Name | Resolves to | |
| --- | --- | --- |
| `localhost.overcast.sh` | `172.18.0.2` | ✅ Overcast |
| `foo.localhost.overcast.sh` | `127.0.0.1` | ❌ the container itself |
| `mybucket.s3.localhost.overcast.sh` | `127.0.0.1` | ❌ |
| `abc123.execute-api.us-east-1.localhost.overcast.sh` | `127.0.0.1` | ❌ |
| `localhost.localstack.cloud` | `172.18.0.2` | ✅ |
| `mybucket.s3.localhost.localstack.cloud` | `127.0.0.1` | ❌ |

The injected entries were present and correct throughout:

```
172.18.0.2	localhost.overcast.sh
172.18.0.2	localhost.localstack.cloud
172.18.0.2	localhost.floci.io
```

So this is not a bug in `containerendpoint` — it is doing exactly what it was asked to. It is a
limit of the mechanism.

## Mechanism

`/etc/hosts` is an exact-match table. It has no wildcard syntax, and never has. A subdomain
therefore misses the table entirely and falls through to public DNS, where the split-horizon names
resolve to `127.0.0.1` by design — which, inside a container, is that container.

That last part is what makes the failure nasty rather than obvious: the lookup **succeeds**. Code
gets a connection refused (or, worse, reaches the runtime API) instead of a clean `ENOTFOUND`.

Two facts make enumeration a dead end rather than merely ugly:

1. **The names are unbounded and dynamic.** Bucket names, API IDs and function-URL IDs are created
   *after* a container starts. Overcast cannot know them at `--add-host` time, and Docker fixes a
   container's `/etc/hosts` at creation.
2. **`ExtraHosts` is create-time only.** Retrofitting an entry into a running container would mean
   rewriting a file the daemon owns, inside Docker Desktop's VM on macOS and Windows. Not viable.

For completeness, since the file size was raised as the concern: **it is not the constraint.**
Lookup cost is linear in entry count, measured inside a glibc container with the target deliberately
placed last:

| Entries | File size | Cost per lookup |
| --- | --- | --- |
| 0 | 0.2 KiB | 15 µs |
| 100 | 4.4 KiB | 24 µs |
| 1 000 | 43 KiB | 84 µs |
| 5 000 | 219 KiB | 349 µs |
| 20 000 | 888 KiB | 1 408 µs |

A hundred entries costs ~9 µs per lookup. Even a thousand is affordable. Enumeration fails on
*coverage*, not on speed — so "write more entries" cannot be made to work no matter how many we
are willing to write.

## What does work

Docker's embedded DNS server (`127.0.0.11`) forwards anything it cannot answer to the resolvers
configured by `--dns` (`HostConfig.Dns`). Pointing that at a resolver Overcast owns gives real
wildcard matching.

Verified end to end with a throwaway DNS shim on a user-defined network — it answered the magic
suffixes with a sentinel `10.99.99.99` and forwarded the rest:

| Name | Answered by | |
| --- | --- | --- |
| `localhost.overcast.sh` | shim | ✅ apex |
| `mybucket.s3.localhost.overcast.sh` | shim | ✅ wildcard |
| `abc.execute-api.us-east-1.localhost.overcast.sh` | shim | ✅ wildcard |
| `deep.a.b.c.localhost.localstack.cloud` | shim | ✅ arbitrary depth |
| `ocdns-shim` (a container name) | Docker embedded DNS | ✅ still works |
| `example.com` | forwarded upstream | ✅ still works |

The critical detail is that **`--dns` does not replace Docker's embedded DNS.** The client
container's `resolv.conf` was still:

```
nameserver 127.0.0.11
options ndots:0
```

Docker keeps its own resolver in front and uses ours as the upstream. Container-name and
service-discovery resolution are therefore unaffected, which removes the main risk of the approach.
Note also that Docker's own table is exact-match too, so the embedded DNS cannot serve wildcards
itself — a real resolver is required.

## Suggested fix

1. Add a small DNS responder to Overcast (`internal/dns`), listening on UDP/53 — and TCP/53 for
   truncation — on the container network.
2. Answer `A` for the apex **and** any subdomain of every split-horizon host: the built-in list in
   `internal/config/config.go` (`localhost.overcast.sh`, `localhost.localstack.cloud`,
   `localhost.floci.io`), plus `OVERCAST_HOSTNAME` and `OVERCAST_SPLIT_HORIZON_HOSTS`. Match the
   apex as well as `*.` — a suffix test that requires a leading dot silently drops the apex.
3. Forward everything else to the daemon's upstream resolvers. Never `NXDOMAIN` a name we do not
   own; that would break the internet from inside every function.
4. Set `HostConfig.Dns` at container create in `internal/services/lambda/container_runtime.go:390`
   and `internal/services/ecs/handler_tasks.go:257`, from the same `containerendpoint.Mapper` that
   supplies `ExtraHosts()` today.
5. **Keep `ExtraHosts` as-is.** It is a correct, zero-dependency fast path for the apex names and a
   fallback if the resolver is unavailable. This is additive.

### Open questions for whoever picks this up

- **Port 53 binding.** Overcast must be reachable on 53 from the container network. Inside Docker
  this is free; for the native binary it needs a privileged port, so the resolver should be
  opt-out (or auto-disabled when the bind fails, falling back to today's behaviour). Do not make
  a failed 53 bind fatal to startup.
- **Which address to answer with.** The same container-routable address `ExtraHosts` already
  resolves — reuse `containerendpoint`, do not re-derive.
- **Port, not just host.** DNS returns an address, never a port. A virtual-hosted URL still has to
  carry Overcast's *listen* port for a container caller. That is the existing per-caller minting
  problem (#318, #353) and is not solved by this; the two must agree.
- **Interaction with the container-name idea.** Using Docker's container name for
  `AWS_ENDPOINT_URL` (filed separately) does *not* subsume this: a container name cannot take a
  subdomain, so `bucket.s3.<container-name>` can never work. A resolver is strictly more general,
  and would make `localhost.overcast.sh` the better default endpoint value.

## Reproducing

```sh
# From inside a Lambda started by Overcast:
node -e "require('dns').promises.lookup('mybucket.s3.localhost.overcast.sh').then(console.log)"
# => { address: '127.0.0.1' }   # the container itself, not Overcast
```

## Scope check

The server side is already built: `internal/middleware/hostroute.go` parses virtual-hosted S3 and
`execute-api` Hosts, and `internal/middleware/region.go` documents the
`<id>.execute-api.<region>.localhost.localstack.cloud` form. Those paths work today for host-side
callers, whose public DNS wildcard resolves to `127.0.0.1` correctly.

So this is not new functionality — it is an existing, working feature that is simply unreachable
from inside a container. Fixing resolution turns it on for function code with no service changes.
