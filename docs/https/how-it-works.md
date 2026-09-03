---
title: "How the local CA works"
description: "What overcast https enable mints and how long it lasts, which names the certificate covers, working offline, what Lambda and ECS containers get, and why Overcast uses a per-machine CA."
section: "Networking"
tags:
  - certificates
  - docs
  - https
  - http2
  - tls
---

# How the local CA works

What `overcast https enable` puts on disk, which names it covers, and what the
containers Overcast starts do with it. To just turn TLS on, see
[HTTPS and HTTP/2](../https.md).

## What `enable` mints

Overcast keeps a **per-machine local CA** under `<data dir>/ca` (default
`~/.overcast/data/ca`):

| File | What it is |
| --- | --- |
| `rootCA.pem` | CA certificate (public — this is what gets installed) |
| `rootCA-key.pem` | CA private key — **never leaves your machine** |
| `cert.pem` | Current server certificate (leaf), minted from the CA |
| `key.pem` | Its private key |

`overcast https enable` (or the lower-level `overcast trust install`) puts
`rootCA.pem` — only the certificate, never the key — into the OS trust store.

| Lifetime | Value |
| --- | --- |
| CA validity | 10 years |
| Leaf validity | 825 days, the maximum Apple platforms accept |
| Leaf re-mint | Automatic, ~30 days before expiry or whenever the required names change |

Re-minting a leaf never touches the CA, so the trust-store install stays valid.

## Which names the certificate covers

The leaf covers `localhost`, `127.0.0.1` and `::1`, then for each wildcard DNS
domain (`localhost.overcast.sh`, `localhost.localstack.cloud`,
`localhost.floci.io`) the apex, `*.<domain>` and `*.s3.<domain>` for S3
virtual-hosted buckets, plus `OVERCAST_HOSTNAME` and any
`OVERCAST_SPLIT_HORIZON_HOSTS`.

> [!WARNING]
> TLS wildcards match exactly one label, so a host-routed name with a variable
> middle (`{id}.execute-api.{region}.…`) is not covered. Use path-style
> addressing for those over TLS.

## Offline

`localhost.overcast.sh` needs public DNS to resolve, so it will not work on a
plane. Use **<https://localhost:4567>** instead: the certificate's SANs cover
`localhost`, `127.0.0.1` and `::1`, and the CA is already trusted locally, so
HTTPS and HTTP/2 keep working entirely offline.

## Pointing AWS SDKs and the CLI at it

Hand the client the CA certificate:

```bash
export AWS_ENDPOINT_URL=https://localhost:4566
export AWS_CA_BUNDLE=~/.overcast/data/ca/rootCA.pem   # AWS CLI + boto3
export NODE_EXTRA_CA_CERTS=~/.overcast/data/ca/rootCA.pem  # Node.js SDK
export SSL_CERT_FILE=~/.overcast/data/ca/rootCA.pem   # many other tools
```

Init hook scripts get `AWS_ENDPOINT_URL` and `AWS_CA_BUNDLE` set automatically
when TLS is on.

## Lambda functions and ECS tasks

Containers Overcast starts trust its TLS with no configuration. When TLS is on,
every function and task container receives:

| What | Value |
| --- | --- |
| `AWS_ENDPOINT_URL` | An **https** URL. The API listener serves only TLS, so an http endpoint would be a hard failure rather than a degraded mode |
| `/opt/overcast/ca.pem` | The trust root: the local CA in auto mode, your own chain in explicit mode. Injected by the same mechanism as function code, so it works when Overcast itself runs in Docker |
| `AWS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE` | All pointing at that file, which covers the AWS CLI, botocore, and the Go, JavaScript, Ruby and python-requests stacks |

> [!WARNING]
> The **Java SDK** reads only its own truststore and ignores those variables.
> Java function code that calls back into Overcast over TLS needs the CA
> imported (`keytool -importcert -file /opt/overcast/ca.pem …`) — or keep
> Overcast on plain HTTP for Java-heavy stacks.

## Why the console wants HTTP/2

Browsers cap **HTTP/1.1 at 6 connections per origin**, localhost included, and
never negotiate cleartext HTTP/2. Over plain HTTP the console's live event feed
holds one socket permanently, every Lambda invocation with a progress stream
holds another, and S3 transfers plus dashboard polling take the rest. The
symptoms:

- the UI stops responding to clicks while several Lambdas run,
- navigation appears to hang mid-load, and tabs sharing the origin make it worse,
- everything springs back the moment the streams finish.

Nothing is slow server-side; the browser is queueing requests waiting for a free
socket. HTTP/2 over ALPN replaces the six sockets with one multiplexed
connection.

## The console's own setup endpoints

The console calls `GET /_overcast/tls/status` and `POST /_overcast/tls/setup` on
the API port, through its own backend. The setup endpoint refuses cross-origin
browser requests: only pages served from the daemon's own names (loopback,
`localhost.overcast.sh`, …) may trigger a trust-store install, so a hostile web
page cannot make your OS pop certificate prompts.

## Limitations

- `overcast serve --bridge` (the mDNS port-80 proxy) is skipped while TLS is
  enabled — it proxies plain HTTP.
- The web dev server (`pnpm run dev` in `web/`) has its own mkcert-based HTTPS
  flow and is unaffected by any of this.

## Why not a publicly-trusted certificate?

A publicly-trusted wildcard certificate for `*.localhost.overcast.sh` would need
its private key shipped to every user, and a **downloadable private key is a
compromised key** under CA/Browser Forum rules — revocable as soon as anyone
reports it, and certificates distributed that way do get revoked without
warning. A per-machine local CA has nothing to revoke, no expiry-day surprises,
and works fully offline.

## Related

- [Overcast in Docker over HTTPS](./docker.md) — where the CA should live when the daemon is containerised
- [Installing the CA by hand](./manual-trust.md) — per-platform trust stores, WSL, your own certificate
- [HTTPS and HTTP/2](../https.md) — the two-command setup
- [Using AWS SDKs and CLI](../sdk-cli.md) — endpoint configuration for every SDK
