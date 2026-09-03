---
title: "LocalStack environment variables"
description: "Which LocalStack variables Overcast reads as aliases, which are recognised and do nothing, and what happens when an alias and its Overcast spelling disagree."
section: "Getting Started"
tags:
  - docs
  - configuration
  - localstack
  - migration
---

# LocalStack environment variables

The full alias and ignored-variable tables behind
[Migrating from LocalStack](../migration-from-localstack.md). Carry your
`environment:` block over unchanged; rename at your leisure, or never.

## Aliases

Every variable below is read directly, with the Overcast spelling it maps to.

| LocalStack                            | Overcast                            | Status                                              |
| ------------------------------------- | ----------------------------------- | ---------------------------------------------------- |
| `LOCALSTACK_HOST`                     | `OVERCAST_HOSTNAME`                 | Alias. Accepts the `hostname[:port]` form            |
| `HOSTNAME_EXTERNAL`                   | `OVERCAST_HOSTNAME`                 | Alias, chained after `LOCALSTACK_HOST`               |
| `EDGE_PORT`                           | `OVERCAST_PORT`                     | Alias                                                |
| `GATEWAY_LISTEN`                      | `OVERCAST_LISTEN` + `OVERCAST_PORT` | Alias. Accepts `<ip>:<port>[,…]`, one port only      |
| `DATA_DIR`                            | `OVERCAST_DATA_DIR`                 | Alias. Counts as an explicit data dir for `auto`     |
| `DEFAULT_REGION`                      | `OVERCAST_DEFAULT_REGION`           | Alias                                                |
| `DEBUG=1`                             | `OVERCAST_LOG_LEVEL=debug`          | Alias. `DEBUG=0` is a no-op                          |
| `LS_LOG`                              | `OVERCAST_LOG_LEVEL`                | Alias. `trace-internal` → `trace`, `warning` → `warn` |
| `PERSISTENCE=1`                       | `OVERCAST_STATE=persistent`         | Alias. `PERSISTENCE=0` is a no-op                    |
| `ENFORCE_IAM`                         | `OVERCAST_ENFORCE_IAM`              | Alias                                                |
| `LAMBDA_RUNTIME_ENVIRONMENT_TIMEOUT`  | `LAMBDA_INIT_TIMEOUT_SECONDS`       | Alias                                                |
| `LAMBDA_REMOVE_CONTAINERS=0`          | `LAMBDA_KEEP_CONTAINERS=true`       | Alias, inverted. Same default either way             |
| `DNS_ADDRESS=0`                       | `OVERCAST_DNS=false`                | Alias. A bind address is a no-op                     |
| `DOCKER_HOST`                         | `LAMBDA_DOCKER_SOCKET`              | Read directly; `LAMBDA_DOCKER_SOCKET` still wins     |

The mappings came out of a full compatibility audit tracked in
[#1190](https://github.com/overcast-sh/overcast/issues/1190).

## Recognised and inert

Everything else LocalStack documents is never rejected, never silently missed,
and named in a startup log line with the reason it does nothing — so you can
drop it once you have seen it.

| Recognised, no effect                                     | Why                                             |
| --------------------------------------------------------- | ----------------------------------------------- |
| `SERVICES`, `EAGER_SERVICE_LOADING`                       | Every service is always loaded                  |
| `LOCALSTACK_AUTH_TOKEN`, `LOCALSTACK_API_KEY`, `ACTIVATE_PRO` | Nothing here is auth-gated                  |
| `SQS_ENDPOINT_STRATEGY`                                   | Queue URLs follow the caller — see [SQS](./differences.md#sqs-queue-urls-follow-the-caller) |
| `S3_SKIP_SIGNATURE_VALIDATION`                            | Signature checking is server-wide, not S3-only  |
| `IAM_SOFT_MODE`                                           | Policies are stored, never enforced, by default |
| `DISABLE_CORS_CHECKS` and the other CORS knobs            | CORS is already unconditionally permissive      |
| `LAMBDA_DOCKER_NETWORK`, `MAIN_DOCKER_NETWORK`            | Adjacent concept, opposite default — see below  |
| `LAMBDA_KEEPALIVE_MS`                                     | Idle-container lifetime is a fixed 15 minutes   |
| `LAMBDA_DOCKER_FLAGS`, `ECS_DOCKER_FLAGS`, `EC2_DOCKER_FLAGS`, `BATCH_DOCKER_FLAGS`, `LAMBDA_RUNTIME_EXECUTOR` | No flag pass-through; Docker is the only executor |
| `SNAPSHOT_*`                                              | Persistence here is incremental, not snapshot-based |
| `PROVIDER_OVERRIDE_*`                                     | One implementation per service                  |
| `MAIN_CONTAINER_NAME`, `DISABLE_EVENTS`, `SKIP_SSL_CERT_DOWNLOAD`, `ALLOW_NONSTANDARD_REGIONS`, `ENABLE_CONFIG_UPDATES` | Nothing to name, send, download, allow or update |

## When an alias and its Overcast name disagree

Setting both spellings to the *same* value is fine — that is the natural result
of migrating a compose file line by line. Setting them to **different** values
fails startup naming both, rather than silently preferring one. The same applies
to a `LOCALSTACK_HOST` or `GATEWAY_LISTEN` port that disagrees with
`OVERCAST_PORT`, and to a `GATEWAY_LISTEN` naming more than one distinct port:
there is no single `OVERCAST_PORT` for it to map to, so Overcast refuses rather
than dropping one of the binds.

The Docker images stay out of the way of that check. They bake exactly one
`OVERCAST_*` default, `OVERCAST_DATA_DIR=/data`, and mark it as the image's own
rather than user intent — so `DATA_DIR` overrides it instead of conflicting with
it, and a LocalStack `environment:` block carried over unchanged never trips a
conflict against something the image itself shipped.

## Why `LAMBDA_DOCKER_NETWORK` is inert rather than aliased

`LAMBDA_DOCKER_NETWORK` names a network Lambda containers join, defaulting to
Docker's built-in `bridge`; Overcast's `OVERCAST_NETWORK` names the single
shared network *every* container it starts joins so they can reach each other by
name. Aliasing them would strip Lambda containers of that network the moment a
migrated compose file's `LAMBDA_DOCKER_NETWORK: bridge` carried over. Set
`OVERCAST_NETWORK` directly if you deliberately want a non-default network.

`MAIN_DOCKER_NETWORK` is the same concept from LocalStack's other side — the
network LocalStack itself is on, which it also falls back to for Lambda — and
is inert here for the same reason.

## Related

- [Migrating from LocalStack](../migration-from-localstack.md) — the image swap and the rest of the move
- [Endpoint and init-hook mapping](./endpoints.md) — the paths that carry over
- [Behavioural differences](./differences.md) — what changes once it is running
- [Configuration reference](../configuration/reference.md) — every `OVERCAST_*` variable with its default
- [LocalStack compatibility matrix](../localstack-compatibility.md) — every item, with its status
