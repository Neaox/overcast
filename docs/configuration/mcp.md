---
title: "Exposing MCP"
description: "OVERCAST_MCP_REMOTE_EXPOSURE and OVERCAST_MCP_AUTH_TOKEN turn on bearer-token auth for /_overcast/mcp, and neither of them changes what Overcast binds."
section: "Reference"
tags:
  - configuration
  - docs
  - mcp
  - security
---

# Exposing MCP

`/_overcast/mcp` is a local surface. To let a non-local client reach it, declare
that and hand it a token — Overcast refuses to start with one and not the other:

```bash
OVERCAST_MCP_REMOTE_EXPOSURE=true OVERCAST_MCP_AUTH_TOKEN=$(openssl rand -hex 32) overcast serve
```

| Variable                       | Default | Effect                                                             |
| ------------------------------ | ------- | ------------------------------------------------------------------ |
| `OVERCAST_MCP_REMOTE_EXPOSURE` | `false` | Declares the endpoint will be reached remotely; requires the token |
| `OVERCAST_MCP_AUTH_TOKEN`      | —       | Bearer token every MCP request must present once set               |

Treat the token like any other credential.

## It does not open the port

Setting these two turns on bearer-token auth. What Overcast binds is
[`OVERCAST_LISTEN`](./ports.md), and if that exposes the port then the MCP
endpoint is exposed with it — so set both of these *before* moving the bind
address beyond localhost. Browser `Origin` checks (localhost origins only) are
enforced on MCP either way.

## Related

- [Environment variable reference](./reference.md)
- [Configuration](../configuration.md)
