---
title: "Multi-container networking"
description: "Set OVERCAST_HOSTNAME to the Docker Compose service name so client-facing URLs (queue URLs, endpoints) resolve from sibling containers, not just the host."
section: "Networking"
tags:
  - docs
  - networking
  - docker
  - compose
  - hostname
---

# Multi-container networking

When running Overcast inside Docker Compose alongside application containers,
client-facing URLs (e.g. SQS queue URLs, SNS unsubscribe links, RDS endpoints)
default to `localhost` — which won't resolve from a sibling container.

Set `OVERCAST_HOSTNAME` to the Docker Compose service name so returned URLs
are reachable across the network:

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

For the full addressing model — path-style vs. Host-routed endpoints, the
`*.localhost.overcast.sh` wildcard DNS option, and how VPC-scoped resources
resolve between sibling containers — see
[Networking and host-based addressing](./networking.md).
