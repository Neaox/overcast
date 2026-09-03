---
title: "Hostnames that resolve for every caller"
description: "Set OVERCAST_HOSTNAME to a name your shell and your containers both resolve: the wildcard DNS domain, the offline fallbacks, and what to use in Docker Compose."
section: "Networking"
tags:
  - compose
  - dns
  - docker
  - docs
  - networking
---

# Hostnames that resolve for every caller

Every URL Overcast hands out carries `OVERCAST_HOSTNAME` when it is set, so that
name has to resolve to Overcast for whoever receives the URL. One value works
for the host and for containers alike:

```sh
OVERCAST_HOSTNAME=localhost.overcast.sh
```

Every `*.localhost.overcast.sh` subdomain resolves to `127.0.0.1` through public
DNS, so host-routed URLs work with no hosts-file edits and behave the same on
Linux, macOS and Windows. `localhost.localstack.cloud` and `localhost.floci.io`
are recognised out of the box and work the same way, so a setup carried over
from either tool keeps working.

| Option | Resolves | Offline | Windows | Setup |
| --- | --- | --- | --- | --- |
| `localhost.overcast.sh` (recommended) | Every subdomain, via public DNS | ✗ | ✓ | One variable |
| Plain `localhost` | `*.localhost` on Linux and macOS only | ✓ | ✗ — only `localhost` itself is in the hosts file | None; it is the default |
| A hosts-file entry, or a local resolver (`dnsmasq`) | Whatever you list, or your own wildcard domain | ✓ | ✓ | One line per subdomain, or a resolver to run |

> [!WARNING]
> **Public wildcard DNS needs internet access, and may be blocked.** Routers,
> corporate networks and DNS filtering software often implement **DNS rebinding
> protection**, which stops a public hostname resolving to a loopback address —
> exactly what `localhost.overcast.sh` does on purpose. `nslookup
> localhost.overcast.sh` should answer `127.0.0.1`; anything else means your
> network is filtering it. Fall back to plain `localhost` (Linux and macOS) or a
> hosts-file entry (any OS).

Plain `localhost` on Windows is what breaks CDK's S3 asset upload — see
[CDK troubleshooting](../cdk/troubleshooting.md#s3-asset-upload-fails-on-windows).

## In Docker Compose

Inside a sibling container `localhost` is that container, so client-facing URLs
(SQS queue URLs, SNS unsubscribe links, RDS endpoints) built on `localhost`
point at the wrong process. Give both sides a wildcard-DNS name: it resolves to
`127.0.0.1` from the host and is remapped to Overcast inside every container
Overcast starts, so one URL works everywhere and host-routed addressing keeps
working with it.

```yaml
services:
  overcast:
    image: ghcr.io/overcast-sh/overcast
    ports:
      - "4566:4566" # AWS API endpoint
      - "4567:4567" # web management console
    environment:
      OVERCAST_HOSTNAME: localhost.overcast.sh

  app:
    build: .
    environment:
      AWS_ENDPOINT_URL: http://localhost.overcast.sh:4566
    depends_on:
      - overcast
```

`CreateFunctionUrlConfig` then returns
`http://a1b2c3….lambda-url.us-east-1.localhost.overcast.sh:4566/`, which resolves
via public DNS to `127.0.0.1` and routes straight back into this container.

**Offline, or behind DNS filtering:** use the Compose service name, which Compose
already resolves.

```yaml
services:
  overcast:
    image: ghcr.io/overcast-sh/overcast:latest
    environment:
      OVERCAST_HOSTNAME: overcast # SQS QueueUrl → http://overcast:4566/...
    ports:
      - "4566:4566"

  app:
    build: .
    environment:
      AWS_ENDPOINT_URL: http://overcast:4566
    depends_on:
      - overcast
```

> [!WARNING]
> A Compose service name resolves *only* on the Compose network. URLs Overcast
> hands out then fail from your own shell, from `cdk deploy`, and from a browser
> — including the web console's links. Add the name to your hosts file pointing
> at `127.0.0.1` if you need both, or prefer the wildcard-DNS option above.

`OVERCAST_SPLIT_HORIZON_HOSTS` adds hostnames to the set remapped to Overcast
inside the containers it starts, on top of the built-in
`localhost.overcast.sh`, `localhost.localstack.cloud` and `localhost.floci.io`.

## Related

- [What host and port a URL carries](./urls.md) — the port half of the same rule
- [Host-routed addressing](./host-routing.md) — what the subdomains are for
- [Networking troubleshooting](./troubleshooting.md) — when a name will not resolve
- [Networking and host-based addressing](../networking.md) — the rest of the addressing story
