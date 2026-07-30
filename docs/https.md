---
title: "HTTPS and HTTP/2"
description: "Serve the API and web console over browser-trusted HTTPS with two commands — unlocking HTTP/2 so the console stays responsive under load."
section: "Getting Started"
tags:
  - docs
  - guide
  - https
  - tls
  - http2
  - certificates
---

# HTTPS and HTTP/2

Serve the Overcast API **and** the web console over browser-trusted HTTPS with
two commands. The payoff is not the padlock — it is **HTTP/2 for the web
console**, which keeps the UI responsive under load.

## Quick start

```bash
# 1. Once per machine: create the local CA, install it into the system
#    trust store, and mint the server certificate. Your OS will ask you to
#    approve the CA install — approving that prompt is the only manual step.
overcast https enable

# 2. Start the daemon with TLS on:
OVERCAST_TLS=auto overcast serve
```

Then open **<https://localhost.overcast.sh:4567>** — web console over
HTTPS + HTTP/2, no browser warnings. The API is on
`https://localhost.overcast.sh:4566` (and plain `https://localhost:4566`).

Expected prompts during `overcast https enable`:

- **Windows** — a certificate-store confirmation dialog ("Do you want to
  install this certificate?"). The CA goes into the *current user's* Root
  store; no administrator shell needed.
- **macOS** — a keychain authorisation prompt. The CA goes into your login
  keychain.
- **Linux** — writing the system CA bundle needs root: run
  `sudo overcast https enable` (or see [Doing it manually](#doing-it-manually)
  for Firefox/Chromium, which read their own NSS store).

Re-running `overcast https enable` is safe — it reports what is already in
place. `overcast https status` shows the current state, and
`overcast https disable` removes the CA from the trust store again.

Running Overcast in Docker instead? See [Docker](#docker) below — the daemon
serves its own CA certificate, so trusting it is one command with no shared
volume.

## Docker

The container mints its CA inside the container, where the host cannot read
it — so the daemon serves the CA **certificate** (public half only, never
the key) at `GET /_overcast/ca.pem`, and the host-side CLI fetches and
installs it. Two commands total:

```bash
docker run -d -e OVERCAST_TLS=auto \
  -v overcast-data:/data \
  -p 4566:4566 -p 4567:4567 \
  ghcr.io/neaox/overcast:alpha

overcast https enable --endpoint http://localhost:4566
```

Then open **<https://localhost.overcast.sh:4567>**. The `http://` spelling
is fine — the CLI notices the daemon answers TLS and fetches over https
(unverified for this one bootstrap fetch; the payload is validated as a CA
certificate, and the endpoint must be loopback — see below). The daemon also
logs this exact command at startup when it detects it is containerized.

With docker-compose:

```yaml
services:
  overcast:
    image: ghcr.io/neaox/overcast:alpha
    environment:
      OVERCAST_TLS: auto
    ports:
      - "4566:4566"
      - "4567:4567"
    volumes:
      - overcast-data:/data

volumes:
  overcast-data:
```

Both images work the same way: the **console** image serves the web UI on
4567 and the API on 4566 over TLS + HTTP/2; the **slim** image has no web UI
but its API listener does TLS + HTTP/2 identically (skip the `4567` port
mapping). Both images' health checks handle TLS.

**Persist `/data`.** The named volume above is not optional decoration: the
CA lives under the data dir, so without a volume every container recreation
mints a **fresh CA** — the one you installed goes stale, browsers show
warnings again, and `AWS_CA_BUNDLE` paths stop verifying. With the volume
the CA survives recreations and the trust install keeps working. If you do
recreate without a volume, re-trusting is the same one command again:
`overcast https enable --endpoint http://localhost:4566` (it fetches the new
CA and installs it alongside; `overcast https disable --endpoint ...` removes
one when you're done with it).

How the `--endpoint` flow works, precisely:

- The CLI fetches `/_overcast/ca.pem`, **validates the payload is actually a
  CA certificate**, and caches it under `<data dir>/ca-remote/<host_port>/`
  — deliberately separate from the local CA's `<data dir>/ca`, so
  `status`/`disable --endpoint` find exactly the CA that install used and a
  fetched CA can never be confused with a locally-minted one.
- **Loopback endpoints only, by default.** Installing a root CA fetched from
  another machine is a trust decision — that host could then impersonate any
  TLS site to you — so a non-loopback `--endpoint` (including names like
  `localhost.overcast.sh` that merely *resolve* to 127.0.0.1) is refused
  unless you add `--trust-remote`.
- The same flag works on the lower-level commands:
  `overcast trust install|status|uninstall --endpoint http://localhost:4566`.
- No `overcast` binary on the host? Fetch the cert yourself and use the
  [manual install commands](#doing-it-manually):
  `curl -ko rootCA.pem https://localhost:4566/_overcast/ca.pem` (the UI port
  mirrors it at `/api/ca.pem`).

## What you're living with over plain HTTP

Browsers cap **HTTP/1.1 at 6 connections per origin** — and localhost is not
exempt. Browsers also never negotiate cleartext HTTP/2 (h2c). So over plain
HTTP, everything the web console does competes for those 6 sockets:

- the live event feed (SSE) holds one connection open permanently,
- every running Lambda invocation with a progress stream holds another,
- S3 uploads/downloads and the dashboard's polling take the rest.

The symptoms are easy to recognise: **the UI stops responding to clicks while
several Lambdas run**, navigation appears to hang mid-load, tabs sharing the
origin make it worse — and everything springs back the moment streams finish.
Nothing is slow server-side; the browser is queueing requests waiting for a
free socket.

The emulator API itself is fine over plain HTTP for SDKs and the CLI — they
are not subject to the browser cap, use keep-alive, and can even use h2c
(Overcast's plain listener accepts HTTP/2 prior-knowledge connections).

## What switching to HTTPS gains

- **HTTP/2 via ALPN** — one multiplexed connection per origin. SSE streams
  and invoke progress streams no longer consume sockets; the console stays
  responsive no matter how much is in flight.
- **Trusted names** — the certificate covers `localhost.overcast.sh` and
  `*.localhost.overcast.sh`. These are real public DNS records that resolve
  to `127.0.0.1` (they are live now), so no hosts-file edits.
- **Production parity** — SDK clients or tools that insist on TLS endpoints
  can now point at Overcast unchanged.

## How it works

### What `enable` / `trust install` actually does

The mkcert model: Overcast keeps a **per-machine local CA** under
`<data dir>/ca` (default `~/.overcast/data/ca`):

| File            | What it is                                              |
| --------------- | ------------------------------------------------------- |
| `rootCA.pem`    | CA certificate (public — this is what gets installed)   |
| `rootCA-key.pem`| CA private key — **never leaves your machine**          |
| `cert.pem`      | Current server certificate (leaf), minted from the CA   |
| `key.pem`       | Its private key                                         |

`overcast https enable` (or the lower-level `overcast trust install`) puts
`rootCA.pem` — only the certificate, never the key — into the OS trust store.
The CA is valid for 10 years; leaves are minted for 825 days (the maximum
Apple platforms accept) and re-minted automatically ~30 days before expiry or
whenever the required names change. Re-minting a leaf never touches the CA,
so the trust-store install stays valid.

The leaf covers `localhost`, `127.0.0.1`, `::1`, then for each wildcard DNS
domain (`localhost.overcast.sh`, `localhost.localstack.cloud`,
`localhost.floci.io`) the apex, `*.<domain>`, and `*.s3.<domain>` (S3
virtual-hosted buckets), plus `OVERCAST_HOSTNAME` and any
`OVERCAST_SPLIT_HORIZON_HOSTS`. TLS wildcards match exactly one label, so
deeper host-routed names with a variable middle (e.g.
`{id}.execute-api.{region}.…`) are not covered — use path-style addressing
for those over TLS.

### Offline behaviour

`localhost.overcast.sh` needs public DNS to resolve, so it won't work on a
plane. Nothing else degrades: use **<https://localhost:4567>** instead — the
certificate's SANs cover `localhost`, `127.0.0.1`, and `::1`, and the CA is
already trusted locally, so HTTPS + HTTP/2 keep working entirely offline.

### Pointing AWS SDKs and the CLI at it

To use the TLS API endpoint from SDK clients, hand them the CA certificate:

```bash
export AWS_ENDPOINT_URL=https://localhost:4566
export AWS_CA_BUNDLE=~/.overcast/data/ca/rootCA.pem   # AWS CLI + boto3
export NODE_EXTRA_CA_CERTS=~/.overcast/data/ca/rootCA.pem  # Node.js SDK
export SSL_CERT_FILE=~/.overcast/data/ca/rootCA.pem   # many other tools
```

Init hook scripts get `AWS_ENDPOINT_URL` and `AWS_CA_BUNDLE` set
automatically when TLS is on.

### Turning it off

```bash
overcast https disable   # removes the CA from the trust store
```

…and unset `OVERCAST_TLS`. The CA key material on disk is kept, so a later
`enable` reuses it and nothing needs re-minting. Delete `<data dir>/ca` if
you want the key material gone too.

### WSL

Running the daemon inside WSL with the browser on Windows is a split-trust
situation: `sudo overcast https enable` inside WSL installs the CA into the
**Linux** trust store only, which curl/SDKs inside WSL use — the Windows
browser never sees it. Install the same CA certificate into the Windows
current-user store as well. The CA lives at `~/.overcast/data/ca/rootCA.pem`
inside WSL, and Windows executables are callable from a WSL shell, so the
smoothest path is one line from inside WSL:

```bash
certutil.exe -user -addstore Root "$(wslpath -w ~/.overcast/data/ca/rootCA.pem)"
```

(or from a Windows shell, using the UNC path:
`certutil.exe -user -addstore Root \\wsl$\<distro>\home\<user>\.overcast\data\ca\rootCA.pem`;
or, with a Windows `overcast.exe` installed,
`overcast.exe https enable --endpoint http://localhost:4566` — WSL2's
localhost forwarding carries the fetch). Approve the confirmation dialog.
WSL2's localhost forwarding then makes <https://localhost:4567> — and
`localhost.overcast.sh`, which resolves to `127.0.0.1` — work from the
Windows browser directly.

### Limitations

- `overcast serve --bridge` (the mDNS port-80 proxy) is skipped while TLS is
  enabled — it proxies plain HTTP.
- The web dev server (`pnpm run dev` in `web/`) has its own mkcert-based
  HTTPS flow and is unaffected by any of this.
- On platforms without a trust-store backend (FreeBSD and other non
  Windows/macOS/Linux systems), certificate minting and TLS serving work
  exactly the same — only the automatic trust-store install is missing.
  `overcast https enable` / `overcast trust install` report this; install
  `rootCA.pem` by hand instead (see [Doing it manually](#doing-it-manually)).

## Lambda functions and ECS tasks — wired automatically

Containers Overcast starts trust its TLS with no configuration. When TLS is
on, every function and task container receives:

- an **https** `AWS_ENDPOINT_URL` (the API listener serves only TLS, so an
  http endpoint would be a hard failure, not a degraded mode);
- the trust root at `/opt/overcast/ca.pem` — the local CA in auto mode, your
  own certificate chain in explicit mode — injected by the same mechanism as
  function code, so it works when Overcast itself runs in Docker;
- `AWS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE` and
  `REQUESTS_CA_BUNDLE` pointing at it, which covers the AWS CLI, botocore,
  and the Go, JavaScript, Ruby and python-requests stacks.

The one caveat is the **Java SDK**, which reads only its own truststore and
ignores those variables. Java function code that calls back into Overcast
over TLS needs the CA imported into its truststore (`keytool -importcert
-file /opt/overcast/ca.pem …`) — or keep Overcast on plain HTTP for
Java-heavy stacks.

## Doing it manually

For users who want control — or whose environment blocks trust-store writes.

**Mint without touching the trust store.** `OVERCAST_TLS=auto overcast serve`
creates the CA and leaf on first run even if you never install anything; you
then own distribution of `rootCA.pem`. Inspect what was minted:

```bash
openssl x509 -in ~/.overcast/data/ca/cert.pem -noout -subject -enddate -ext subjectAltName
```

**Install the CA by hand:**

- Windows (current user):
  `certutil -user -addstore Root %USERPROFILE%\.overcast\data\ca\rootCA.pem`
- macOS (login keychain):
  `security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db ~/.overcast/data/ca/rootCA.pem`
- Linux (Debian/Ubuntu/Alpine):
  `sudo cp ~/.overcast/data/ca/rootCA.pem /usr/local/share/ca-certificates/overcast-local-ca.crt && sudo update-ca-certificates`
- Linux (Fedora/RHEL):
  `sudo cp ~/.overcast/data/ca/rootCA.pem /etc/pki/ca-trust/source/anchors/ && sudo update-ca-trust extract`
- Linux (Arch, p11-kit):
  `sudo cp ~/.overcast/data/ca/rootCA.pem /etc/ca-certificates/trust-source/anchors/ && sudo trust extract-compat`
- Firefox / Chromium on Linux read the **NSS user database**, not the system
  bundle (needs `certutil` from `libnss3-tools`):
  `certutil -d sql:$HOME/.pki/nssdb -A -t "C,," -n "overcast local CA" -i ~/.overcast/data/ca/rootCA.pem`
  (Firefox: use the profile directory under `~/.mozilla/firefox/*.default*`
  instead of `~/.pki/nssdb`, or import via Settings → Certificates.)

**Bring your own certificate instead.** Skip the local CA entirely and point
Overcast at any cert/key pair (mkcert output, a corporate-issued cert, …):

```bash
OVERCAST_TLS_CERT=/certs/cert.pem OVERCAST_TLS_KEY=/certs/key.pem overcast serve
```

This serves both the API and the web UI, exactly like `auto` mode. Put the
full chain in the cert file if a private CA issued it — the web console's
backend verifies against that file plus the system roots.
`OVERCAST_TLS=auto` and `OVERCAST_TLS_CERT`/`KEY` are mutually exclusive.

**Verify:**

```bash
curl --cacert ~/.overcast/data/ca/rootCA.pem https://localhost:4566/_health
curl --cacert ~/.overcast/data/ca/rootCA.pem -sso /dev/null -w '%{http_version}\n' https://localhost:4567/
# → 2
```

…or open <https://localhost.overcast.sh:4567>, check the padlock, and look
for `h2` in the DevTools Network panel's Protocol column.

## Why not a publicly-trusted certificate, like localhost.localstack.cloud uses?

A publicly-trusted wildcard cert for `*.localhost.overcast.sh` would need its
private key shipped to every user — and a **downloadable private key is a
compromised key** under CA/Browser Forum rules, revocable as soon as anyone
reports it. Certificates distributed that way can (and do) get revoked
without warning. Overcast deliberately uses a per-machine local CA instead:
nothing to revoke, no expiry-day surprises, and it works fully offline.
