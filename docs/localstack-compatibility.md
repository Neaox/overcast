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
Start at [Migrating from LocalStack](./migration-from-localstack.md) for the
short version.

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

| Item | Status | Notes |
| ---- | ------ | ----- |
| Every documented LocalStack variable | Aliased or inert | One with an Overcast equivalent is read as an alias; every other is recognised, does nothing, and says so in a startup log line. Full table: [Migrating from LocalStack § Environment variables](./migration-from-localstack.md#environment-variables) |

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
| Egress from compute in a VPC | Works | `OVERCAST_VPC_EGRESS=open`, the default, matches LocalStack: a VPC-attached Lambda has full egress. `none` and `routed` withhold it, which LocalStack cannot express; both need Overcast in a container — see [Egress modes](./networking/egress.md) and [`routed`](./networking/routed-egress.md) |
| Persistence by default | Differs | The image mounts no volume, so `OVERCAST_STATE=auto` resolves to memory and a restart is a wipe. Set `OVERCAST_STATE` to decide rather than infer — see [Storage and persistence](./storage.md#the-auto-default) |

## Client tooling

| Item | Status | Notes |
| ---- | ------ | ----- |
| `awslocal` | Works | Sets `--endpoint-url` to `localhost:4566` |
| `cdklocal` | Works | See [CDK troubleshooting](./cdk/troubleshooting.md#s3-asset-upload-fails-on-windows) for the asset-publishing caveat on Windows |
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
one of those services in its single build.

The rows above compare interfaces — the ports, URLs, variables and conventions
your setup is written against — rather than service lists. For what Overcast
actually emulates, use the [service index](./README.md#services); a carried-over
`LOCALSTACK_AUTH_TOKEN` is recognised, logged once at startup as inert, and
gates nothing.

## Related

- [Migrating from LocalStack](./migration-from-localstack.md) — the short version, with the steps
- [Testcontainers](./testcontainers.md) — the LocalStack modules against this image
- [Service reference](./services/README.md) — what each service supports
