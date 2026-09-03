---
title: "Overcast in Docker over HTTPS"
description: "Keep the CA on the host and let the container mint leaves from it, so one trust install survives every recreation — with docker run, Compose, or no overcast CLI on the host at all."
section: "Networking"
tags:
  - certificates
  - docker
  - docs
  - https
  - tls
---

# Overcast in Docker over HTTPS

Approving a root certificate is a per-machine, permanent act, and a container's
CA lasts exactly as long as the container. Keep the two apart — **the host owns
the CA, the container mints leaves from it** — and you approve one prompt ever.
Turning TLS on in the first place is [HTTPS and HTTP/2](../https.md).

## The host owns the CA

Point `OVERCAST_CA_DIR` at the host's CA directory and mount it read-only:

```bash
overcast https enable            # once per machine: mint the CA, approve the
                                 # OS prompt. Never needed again.

docker run -d -e OVERCAST_TLS=auto \
  -e OVERCAST_CA_DIR=/ca -v ~/.overcast/data/ca:/ca:ro \
  -p 4566:4566 -p 4567:4567 \
  ghcr.io/overcast-sh/overcast:latest
```

Then open **<https://localhost.overcast.sh:4567>**. Recreate the container,
`down -v` it, upgrade the image, run five of them at once — every one serves a
certificate chaining to the root this machine already trusts, and you are never
prompted again.

Mount it `:ro`. The daemon reads the CA to sign leaves and has no reason to
rewrite your machine's trust anchor. Leaf certificates are normally cached next
to the CA; on a read-only mount that caching is skipped and the daemon re-mints
at startup instead, which costs about a millisecond.

With docker-compose, give the CA its own volume so `down` cannot take it (or
mount the host CA as above, which is better still):

```yaml
services:
  overcast:
    image: ghcr.io/overcast-sh/overcast:latest
    environment:
      OVERCAST_TLS: auto
      OVERCAST_CA_DIR: /ca
    ports:
      - "4566:4566"
      - "4567:4567"
    volumes:
      - overcast-data:/data
      - overcast-ca:/ca        # or ~/.overcast/data/ca:/ca:ro

volumes:
  overcast-data:
  overcast-ca:
```

Both images work the same way: the **console** image serves the web console on
4567 and the API on 4566 over TLS + HTTP/2; the **slim** image has no web
console but its API listener does TLS + HTTP/2 identically (skip the `4567`
port mapping). Both images' health checks handle TLS.

## No overcast CLI on the host?

Then the container mints the CA and the host fetches it: the daemon serves the
CA **certificate** (public half only, never the key) at
`GET /_overcast/ca.pem`, and `overcast https enable --endpoint` installs it.

```bash
docker run -d -e OVERCAST_TLS=auto \
  -e OVERCAST_CA_DIR=/ca -v overcast-ca:/ca \
  -p 4566:4566 -p 4567:4567 \
  ghcr.io/overcast-sh/overcast:latest

overcast https enable --endpoint https://localhost:4566
```

Keep `OVERCAST_CA_DIR` on a named volume even here. Without it every recreation
mints a **fresh** CA: the one you installed goes stale, browsers warn again, and
`AWS_CA_BUNDLE` paths stop verifying. Re-trusting is the same command again
(`overcast https disable --endpoint ...` removes a CA you are done with), but a
named volume saves re-approving a root certificate on a schedule set by your
container lifecycle.

Use the `https://` spelling. A container serving TLS answers a plain-HTTP dial
with `http: TLS handshake error ...: client sent an HTTP request to an HTTPS
server`. The `http://` spelling still works — the CLI notices the daemon answers
TLS and retries over https — it just costs you a confusing line in the log. The
daemon logs the correct command at startup when it detects it is containerised.

### What `--endpoint` does

| Step | Detail |
| --- | --- |
| Fetch | `GET /_overcast/ca.pem`, with the payload validated as a CA certificate before anything is installed |
| Cache | Under `<data dir>/ca-remote/<host_port>/`, separate from the local CA's `<data dir>/ca`, so `status`/`disable --endpoint` find exactly the CA that install used |
| Restrict | Loopback endpoints only. A non-loopback `--endpoint` needs `--trust-remote` |
| Apply to | `overcast trust install`, `trust status` and `trust uninstall` take the same flag; either scheme is accepted |

Installing a root CA fetched from another machine is a trust decision: that host
could then impersonate any TLS site to you. Names that merely *resolve* to
127.0.0.1, such as `localhost.overcast.sh`, count as non-loopback for this check
and need `--trust-remote` too.

With no `overcast` binary on the host at all, fetch the certificate and install
it yourself with the [manual install commands](./manual-trust.md):
`curl -ko rootCA.pem https://localhost:4566/_overcast/ca.pem` (the UI port
mirrors it at `/api/ca.pem`).

## From the web console

**Settings → HTTPS & certificates** prepares the certificates inside the
container, then hands you the host-side one-liner — or a CA certificate download
plus the manual install commands if the CLI isn't installed on the host —
followed by the restart-and-switch steps. When the CA lives inside the container
it says so, and shows how to hand the daemon a CA that outlives it.

## Related

- [Installing the CA by hand](./manual-trust.md) — the per-platform trust-store commands
- [How the local CA works](./how-it-works.md) — what is minted, and which names it covers
- [HTTPS and HTTP/2](../https.md) — the two-command setup
- [Environment variable reference](../configuration/reference.md) — `OVERCAST_CA_DIR`, `OVERCAST_TLS`
