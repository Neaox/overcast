---
title: "LocalStack compatibility matrix"
description: "Every part of a LocalStack setup — ports, endpoints, hostnames, container conventions, client tooling and behavioural conventions — with its status against Overcast."
section: "Reference"
tags:
  - docs
  - localstack
  - compatibility
  - reference
  - migration
---

# LocalStack compatibility matrix

Everything a LocalStack setup touches, item by item, with what it does here —
measured against LocalStack's interface as it stands after its March 2026
edition change, not against any edition's service list ([why](#not-in-scope)).
Start at [Migrating from LocalStack](./migration-from-localstack.md) if you want
the short version; this page is the audit behind it.

| Status | Means |
| ------ | ----- |
| **Works** | Carries over untouched |
| **Aliased** | Overcast answers LocalStack's own name or URL |
| **Differs** | Works, but not identically — the cell says how |
| **Gap** | Does not work; the linked issue tracks it |
| **No equivalent** | The concept does not exist here; the alternative is named |

---

## Ports

| Item | Status | Notes |
| ---- | ------ | ----- |
| Edge port `4566` | Works | Same default. `AWS_ENDPOINT_URL` needs no change |
| `EDGE_PORT`, `GATEWAY_LISTEN` | Aliased | `GATEWAY_LISTEN` takes one port; LocalStack's own default names two |
| Gateway on `443` | Differs | TLS is served on the same port via `OVERCAST_TLS` — see [HTTPS](./https.md) |
| External service ports `4510-4559` | Differs | Per-service bases instead of one pool. [#1548](https://github.com/overcast-sh/overcast/issues/1548) |
| Web console `4567` | Works | Overcast-only; LocalStack has no equivalent |

## Endpoints

Full mapping in
[Migrating from LocalStack § Endpoint mapping](./migration-from-localstack.md#endpoint-mapping).

| Item | Status | Notes |
| ---- | ------ | ----- |
| `/_localstack/health` | Aliased | Served in LocalStack's shape, plus an `emulator` field |
| `/_localstack/init`, `/init/{stage}` | Aliased | Byte-identical: the shapes already matched |
| `POST /_localstack/state/reset` | Aliased | Returns `{"status":"reset"}`; LocalStack returns nothing |
| `/_localstack/info`, `/diagnose`, `/config`, `/usage`, `/plugins` | No equivalent | The 404 names the `/_overcast/debug/*` endpoint to use instead |
| `/_localstack/state/save`, `/load` | No equivalent | Persistence is incremental, not snapshot-based |
| `GET`/`DELETE /_aws/ses` | Aliased | LocalStack's shape, from the same inbox as `/_overcast/ses/inbox/messages`; `Region` and `RawData` are omitted — the capture does not hold them |
| `/_aws/sqs/messages` | Aliased | XML `ReceiveMessageResponse`, or JSON under `Accept: application/json`; `?QueueUrl=`, `?QueueName=&QueueRegion=` and the `/{region}/{account}/{queue}` path form; `ShowInvisible`, `ShowDelayed` |
| `/_aws/sns/sms-messages`, `/platform-endpoint-messages`, `/_aws/lambda/runtimes` | Gap | [#1545](https://github.com/overcast-sh/overcast/issues/1545). The 404 names the `/_overcast/` endpoint that has the data |
| `/_aws/sns/subscription-tokens/{arn}`, `DELETE /_aws/dynamodb/expired` | No equivalent | The token is not exposed; TTL expiry has no manual trigger. Split out of [#1545](https://github.com/overcast-sh/overcast/issues/1545) |
| `/restapis/{id}/{stage}/_user_request_/` | Works | LocalStack's API Gateway invoke URL, served verbatim |
| `/_aws/execute-api/{id}/{stage}/` | Gap | [#1545](https://github.com/overcast-sh/overcast/issues/1545). The host-routed form below usually makes it unnecessary |

## Hostnames and DNS

Every row here is verified against a running instance.

| Item | Status | Notes |
| ---- | ------ | ----- |
| `localhost.localstack.cloud` | Works | A built-in wildcard base, alongside `localhost.overcast.sh` |
| `*.localhost.localstack.cloud` | Works | Split-horizon: remapped inside containers Overcast starts |
| `s3.localhost.localstack.cloud` | Works | Recognised as the service endpoint, not a bucket named `s3` |
| `{bucket}.s3.localhost.localstack.cloud` | Works | Virtual-hosted S3 |
| `{bucket}.localhost.localstack.cloud` | Works | The bare form too — no `s3.` label needed |
| `{id}.execute-api.localhost.localstack.cloud` | Works | Region segment optional, as LocalStack omits it |
| `{id}.lambda-url.{region}.localhost.localstack.cloud` | Works | |
| `LOCALSTACK_HOST`, `HOSTNAME_EXTERNAL` | Aliased | Both map to `OVERCAST_HOSTNAME` |
| Built-in DNS server on `53` | Works | `OVERCAST_DNS`; `DNS_ADDRESS=0` turns it off |
| `DNS_RESOLVE_IP`, `DNS_SERVER` | No equivalent | Resolves to Overcast's own address; forwards to the system resolver |
| Transparent `*.amazonaws.com` interception | No equivalent | Point `AWS_ENDPOINT_URL` at Overcast instead |

## Environment variables

The full table, alias by alias, is in
[Migrating from LocalStack § Environment variables](./migration-from-localstack.md#environment-variables).
In summary: the ones with a genuine Overcast equivalent are read directly as
aliases, and every other documented LocalStack variable is recognised and inert
— never rejected, and named in a startup log line with the reason it does
nothing.

## Container conventions

| Item | Status | Notes |
| ---- | ------ | ----- |
| `ports: ["4566:4566"]` | Works | |
| `/var/run/docker.sock` mount | Works | Needed for Lambda, ECS, RDS and the rest of the container-backed services |
| `DOCKER_HOST` | Works | Read when `LAMBDA_DOCKER_SOCKET` is unset — Colima, Rancher Desktop, Podman, rootless |
| Volume at `/var/lib/localstack` | Works | Adopted as the state directory when it is the only volume mounted |
| Init hooks in `/etc/localstack/init/{stage}.d/` | Works | Both that tree and `/etc/overcast/init/` are scanned |
| `awslocal` in the image | Works | Same wrapper; needs the `aws` CLI present |
| `Ready.` readiness log line | Works | Printed verbatim once every listener is bound, after Overcast's own `overcast ready` line |
| `/usr/local/bin/docker-entrypoint.sh` | Aliased | LocalStack's entrypoint path, symlinked to Overcast's — what the Java Testcontainers module execs |
| `HEALTHCHECK` | Differs | Probes `/_overcast/health`; a compose healthcheck on `/_localstack/health` also works |
| `VOLUME /var/lib/localstack` | No equivalent | Overcast declares no volume, so a volume-less run stays ephemeral by default |
| `LOCALSTACK_AUTH_TOKEN` | Works | Recognised and inert: nothing here is auth-gated |
| `LAMBDA_DOCKER_NETWORK`, `MAIN_DOCKER_NETWORK` | No equivalent | Both name the network containers join; Overcast puts everything it starts on `OVERCAST_NETWORK` and the control plane derived from it. Recognised and inert — see [why they are not aliased](./migration-from-localstack.md#why-lambda_docker_network-is-inert-rather-than-aliased) |
| Egress from compute in a VPC | Works | Both give a VPC-attached Lambda full egress by default. Overcast can also withhold it, which LocalStack cannot: `OVERCAST_VPC_EGRESS=none` makes every network it creates `--internal`, and `routed` decides per subnet from its route table. Both need Overcast running in a container to withhold fully — on Docker Desktop the control plane has to stay routable — see below |

**Egress matches LocalStack by default, and did not always.** LocalStack has no
concept of network isolation: `VpcConfig` is metadata, subnets and gateways are
records, and every Lambda lands on the same shared network with the host's own
egress. Overcast 0.0.1-alpha.37 through the release before this one withheld
egress on some hosts, so a stack that reached an external API under LocalStack
could fail with `ENETUNREACH` — with nothing in the configuration explaining
why, because the answer came from the host.

`OVERCAST_VPC_EGRESS` replaces that with a setting you choose. `open`, the
default, is LocalStack's behaviour: every container reaches the internet. So a
migration needs nothing here.

What Overcast adds is two directions LocalStack cannot express at all — its
container-client API takes only a network name:

- **`OVERCAST_VPC_EGRESS=none`** makes every network it creates `--internal`,
  so nothing the emulator starts reaches anything outside the machine. Use it
  for deterministic CI, or to prove a stack has no hidden external dependency.
- **`OVERCAST_VPC_EGRESS=routed`** reads the route tables. A subnet whose
  `0.0.0.0/0` route names an attached internet gateway or an available NAT
  gateway gives its containers egress; one with no default route does not, and
  outbound connections fail with `ENETUNREACH`. LocalStack, Moto and SAM CLI
  all give a VPC-attached function full egress whatever the template says, so a
  missing NAT gateway surfaces in a deploy rather than locally.

**Both need Overcast running in a container.** On Docker Desktop, with Overcast
running outside one, the control plane is the exception: isolating it would
sever the Lambda Runtime API, so it stays routable and containers keep a route
out. Under `none` that means the stack is not hermetic; under `routed` it means
egress is granted where the route tables withhold it. A startup warning says so
either way, and `/_overcast/health` reports it. Run Overcast in a container, or
against a native Linux Docker daemon, for the whole of either mode.

See [Egress modes](./networking/egress.md) and
[`routed`](./networking/routed-egress.md).

Persistence is the row worth reading twice. The published image defaults to
**in-memory** state: `OVERCAST_STATE=auto` resolves to a durable backend only
when it finds a mounted volume, an explicitly configured data directory, or an
existing database. A LocalStack compose file that mounts `/var/lib/localstack`
gets persistence; one that mounts nothing does not, and a container restart is
a wipe. Set `OVERCAST_STATE` explicitly to decide rather than infer — see
[Storage and persistence](./storage.md).

## Client tooling

| Item | Status | Notes |
| ---- | ------ | ----- |
| `awslocal` | Works | Sets `--endpoint-url` to `localhost:4566` |
| `cdklocal` | Works | See the [CDK guide](./cdk.md) for the asset-publishing caveat on Windows |
| `tflocal` | Works | Its `S3_HOSTNAME` default resolves and is recognised |
| `samlocal` | Works | Sets `AWS_ENDPOINT_URL` and nothing else |
| Overcast's Testcontainers module (Go) | Works | See [Testcontainers](./testcontainers.md) |
| Generic-container recipe, any language | Works | Wait on `/_overcast/health` or `/_localstack/health` |
| LocalStack Testcontainers modules | Differs | All five start the Overcast image; each has its own rule about which tag you may name — see [Testcontainers](./testcontainers.md#using-the-localstack-testcontainers-modules) |

## Behavioural conventions

| Item | Status | Notes |
| ---- | ------ | ----- |
| Account `000000000000` | Works | |
| Credentials `test`/`test` | Works | Any credentials are accepted; signatures are not verified unless you ask |
| Default region `us-east-1` | Works | `DEFAULT_REGION` is an alias |
| S3 path-style | Works | The default here, where LocalStack prefers virtual-hosted |
| S3 virtual-hosted | Works | Both forms, with or without the `s3.` label |
| S3 presigned URL host | Works | Signing is client-side; the host is whatever endpoint you configured |
| SQS queue URLs | Differs | Minted on the origin the caller reached — LocalStack's `dynamic` strategy, not its `standard` default |
| `SQS_ENDPOINT_STRATEGY` | No equivalent | Drop it; see the row above |
| Lambda reaching the gateway | Works | Containers resolve the split-horizon names through Overcast's own resolver |
| `x-amz-request-id` on every response | Differs | Always present here; LocalStack omits it on some errors |
| `x-localstack` response header | No equivalent | |
| SigV4 verification | Differs | Off by default, both here and there. `OVERCAST_SIGV4_VALIDATE=true` turns it on |
| IAM enforcement | Works | `ENFORCE_IAM` is an alias of `OVERCAST_ENFORCE_IAM` |

## Not in scope

LocalStack restructured its editions on 23 March 2026: the published image
requires an auth token to start, the free plan is "Hobby" and limited to
non-commercial use, and most services — ECS, ECR, RDS, ElastiCache, CloudFront,
ELB, Cognito, EKS, AppSync, Athena, Glue and MSK among them — sit on the paid
Base or Ultimate plans, as does local state persistence. Overcast emulates every
one of those services in its single build, so "the same services as the free
edition" would understate it and "the same services as LocalStack" would
overstate it. Neither is a target this page measures against.

This page measures Overcast against LocalStack's *interface* — the ports, URLs,
variables and conventions your setup is written against. For what Overcast
actually emulates, use the [service index](./README.md#services); a carried-over
`LOCALSTACK_AUTH_TOKEN` is recognised, logged once at startup as inert, and
gates nothing.
