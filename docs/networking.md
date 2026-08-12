---
title: "Networking and host-based addressing"
description: "Path-style vs Host-routed AWS endpoints, the *.localhost.overcast.sh wildcard DNS option, and what to use offline."
section: "Getting Started"
tags:
  - docs
  - guide
  - networking
  - dns
---

# Networking and host-based addressing

Overcast listens on a single port (default `4566`) and dispatches every
request — regardless of service — to the same emulator process. Real AWS
services are split two ways depending on how a client addresses a resource:

- **Path-style**: the resource ID is in the URL path
  (`http://localhost:4566/restapis/{apiId}/{stage}/_user_request_/...`).
  This always works against Overcast with zero configuration and is what
  every AWS SDK falls back to when it isn't told a custom endpoint resolves
  a Host-based URL.
- **Host-routed (subdomain) style**: the resource ID (and often the region)
  is encoded in the **Host header** instead —
  `{apiId}.execute-api.{region}.amazonaws.com/{stage}/...`. Some AWS
  features are *only* reachable this way on real AWS (Lambda function URLs
  have no path-style equivalent at all).

Overcast supports both. This page covers the Host-routed side: what's
implemented, the DNS story that makes it work locally, and the tradeoffs.

---

## What works today

| Service                 | Host pattern                                    | Notes                                                                 |
| ------------------------ | ------------------------------------------------ | ---------------------------------------------------------------------- |
| API Gateway (REST v1)     | `{apiId}.execute-api.{region}.{base}/{stage}/...` | Stage is always the first path segment, same as real AWS.             |
| API Gateway (HTTP v2)     | `{apiId}.execute-api.{region}.{base}/...`         | `$default` stage: no stage segment. Named stages: `{stage}/...` prefix, resolved against your API's actual stages. |
| Lambda function URLs      | `{urlId}.lambda-url.{region}.{base}/...`          | No path-style equivalent — this is the only way to invoke a function URL, on Overcast and on real AWS alike. See [Lambda function URLs](#lambda-function-urls) below. |
| AppSync (GraphQL)         | `{apiId}.appsync-api.{region}.{base}/graphql`     | Also reachable at `/realtime` on the same host — Overcast colocates the GraphQL and realtime endpoints. |
| AppSync (subscriptions)   | `{apiId}.appsync-realtime-api.{region}.{base}`    | The host real AWS serves subscriptions on, and the one Amplify derives by substituting into the GraphQL URL. Routes to the same endpoint as `/realtime` above. |
| CloudFront                | `{distributionId}.cloudfront.{base}`              | Global, so there is no region segment. `DomainName` is minted on the hostname you reached Overcast on rather than the literal `cloudfront.net`. |
| S3 (virtual-hosted style) | `{bucket}.s3[.{region}].{base}/...` or `{bucket}.{base}/...` | Both forms are supported. The second is what an AWS SDK emits against a custom endpoint with path-style disabled, and the only form CDK's asset publisher uses. See [sdk-cli.md](./sdk-cli.md#s3-addressing-styles) and [cdk.md](./cdk.md#s3-asset-upload-fails-on-windows). |

Every Host-routed request is rewritten internally onto the same handlers
path-style requests use, so behavior (authorizers, stage variables,
integration dispatch, event publishing) is identical either way — pick
whichever addressing style your client/SDK produces.

**The Host is case-insensitive in every part** — the resource ID, the service
label, the region segment and the base domain alike — because a hostname is
(RFC 4343). So `E1PQRS2T3U4V5W.cloudfront.localhost.overcast.sh:4566` and the
all-lowercase form a browser actually sends reach the same distribution, and
`MyBucket.localhost:4566` reaches bucket `mybucket`. **Paths are
case-sensitive**, as they are on AWS: `/_cloudfront/{distributionId}/...` and
every other path-style route must match exactly.

### How Overcast decides who owns a Host

S3 virtual-hosted addressing and the host-routed services above share one
hostname space, so Overcast resolves them with a single rule, applied in order:

1. **`{bucket}.s3.{...}` / `{bucket}.s3-{region}.{...}` → S3.** The bucket is
   everything before the first `.s3.`, so bucket names containing dots stay
   addressable here.
2. **`{bucket}.{base}` → S3**, where `{base}` is `localhost`,
   `localhost.overcast.sh`, `localhost.localstack.cloud`, or your
   `OVERCAST_HOSTNAME`. Exception: if the part in front of the base carries a
   service label (`execute-api`, `lambda-url`, `appsync-api`,
   `appsync-realtime-api`, `cloudfront`) as its second or later dot-segment, it
   is a service address, not a bucket — rule 3 takes it.
3. **`{id}.{label}[.{region}].{base}` → the owning service**, for the labels in
   the table above.
4. **Anything else stays path-style** and reaches S3, which is the emulator's
   catch-all.

The order is fixed by this rule, not by internal configuration, so the same
Host always resolves the same way.

> **Reserved service labels.** A bucket name whose **second or later**
> dot-segment is `execute-api`, `lambda-url`, `appsync-api`,
> `appsync-realtime-api` or `cloudfront` cannot be addressed by rule 2 — `my.execute-api.localhost` is an API Gateway invoke.
> Use path-style (`localhost:4566/my.execute-api/key`) or the explicit form
> (`my.execute-api.s3.localhost`), both of which work.
>
> A bucket named *exactly* like a label is unaffected: `execute-api.localhost`
> is the bucket `execute-api`, because a host-routed address always has a
> non-empty resource ID in front of the label.
>
> Overcast warns at `CreateBucket` when a name carries a reserved label, naming
> both escapes. The bucket is still created — AWS accepts the name, so refusing
> it would fail a stack locally that deploys fine against AWS.

### Known AWS resource subdomains

AWS does not publish a single list of the hostnames that carry a resource ID.
The SDK endpoint rulesets (`endpoints.json`, per-service `endpoint-rules.json`)
and Smithy's `endpointPrefix` cover **control-plane** endpoints only — neither
contains `execute-api`, `lambda-url` or `appsync-api`. Those are documented
per-service, scattered across AWS's docs. This table is therefore the
centralised list, and it is maintained by hand.

| Form | Overcast | Notes |
| --- | --- | --- |
| `{apiId}.execute-api.{region}.{base}` | ✅ routed | API Gateway REST v1 and HTTP v2 invoke |
| `{urlId}.lambda-url.{region}.{base}` | ✅ routed | Lambda function URLs |
| `{apiId}.appsync-api.{region}.{base}` | ✅ routed | AppSync GraphQL |
| `{apiId}.appsync-realtime-api.{region}.{base}` | ✅ routed | AppSync subscriptions. Amplify derives this host by substituting into the GraphQL URL, so it must route even though Overcast serves both endpoints from one place |
| `{distributionId}.cloudfront.net` | ✅ routed | CloudFront distribution. Global, so there is no region segment |
| `{bucket}.s3[.{region}].{base}` | ✅ routed | S3 virtual-hosted style |
| `{bucket}.s3.dualstack.{region}.{base}` | ✅ routed | Matched by the `.s3.` rule |
| `{bucket}.s3-{region}.{base}` | ✅ routed | Legacy dash dialect, pre-2019 regions |
| `{bucket}.s3-accelerate.{base}` | ✅ routed | Transfer Acceleration. Routing only — no acceleration is emulated, and AWS forbids periods in bucket names used with it |
| `{bucket}.s3-website[-.]{region}.{base}` | ⚠️ routed | Reaches S3, but static website hosting behaviour is not implemented |
| `{ap}-{account}.s3-accesspoint.{region}.{base}` | ⚠️ routed | Reaches S3 treating the access point alias as a bucket name; access points are not modelled |
| `{bucket}.{base}` | ✅ routed | Bare form. No real-AWS equivalent, but what an SDK emits against a custom endpoint with path-style disabled, and the only form CDK's asset publisher uses |
| `{domain}.auth.{region}.amazoncognito.com` | ❌ | Cognito hosted UI. Not routed; use the path-style endpoint |
| `{id}.{hash}.{region}.rds.amazonaws.com` | ❌ | Engine data-plane endpoint |
| `{cluster}.{hash}.{region}.cache.amazonaws.com` | ❌ | Engine data-plane endpoint |
| `b-{n}.{cluster}.{hash}.kafka.{region}.amazonaws.com` | ❌ | Engine data-plane endpoint |
| `{domain}.{region}.es.amazonaws.com` | ❌ | Engine data-plane endpoint |

The engine data-plane endpoints are deliberately absent. Overcast emulates
those services' control planes, so there is no engine behind the hostname to
route to — and every one of those labels (`rds`, `cache`, `kafka`, `es`,
`auth`) is a bare common word. Registering one would make a bucket named
`my.cache` unaddressable in the bare form for no benefit. See the guardrail in
[internal/middleware/hostroute.go](https://github.com/Neaox/overcast/blob/main/internal/middleware/hostroute.go).

`{base}` is whatever hostname the request actually arrived on — Overcast
never hardcodes a domain. Point requests at `localhost`, an
`OVERCAST_HOSTNAME`-configured wildcard domain (below), or a Docker service
name; the response always echoes back the same base you called in.

### Lambda function URLs

`CreateFunctionUrlConfig` / `GetFunctionUrlConfig` / `UpdateFunctionUrlConfig`
/ `DeleteFunctionUrlConfig` / `ListFunctionUrlConfigs` are implemented under
the real API paths (`/2021-10-31/functions/{name}/url[s]`). The returned
`FunctionUrl` always uses the Host you called Overcast on:

```
http://<url-id>.lambda-url.<region>.<host>:<port>/
```

A few things are intentionally simplified relative to real AWS, consistent
with Overcast [not being a security boundary](../AGENTS.md#non-goals--decision-guide-for-agents):

- **`AuthType` (`NONE` / `AWS_IAM`) is stored and returned but never
  enforced.** Every Host-routed invocation runs as if `AuthType` were
  `NONE`, regardless of what was configured.
- **`Cors` is stored, returned, and reflected onto invoke responses**
  (`Access-Control-Allow-*` headers) — this is the one piece of CORS
  behavior actually applied, since it's cheap and matters for
  browser-based testing against a function URL.
- **`InvokeMode: RESPONSE_STREAM` is accepted but always behaves as
  `BUFFERED`** — there is no streaming function-URL invocation path in this
  emulator.
- **`Qualifier` is stored for API-shape correctness but not enforced against
  invocation** — Overcast's Lambda emulator already treats aliases/versions
  as metadata rather than separate executable snapshots (see
  `InvokeFunction`'s behavior), and function URLs follow the same rule.

---

## The `*.localhost.overcast.sh` wildcard DNS option

Host-routed addressing needs the Host header's subdomain to actually resolve
to wherever Overcast is listening. Three ways to get there, in order of
convenience vs. offline-friendliness:

1. **`localhost.overcast.sh` — recommended.** Set
   `OVERCAST_HOSTNAME=localhost.overcast.sh`. Every `*.localhost.overcast.sh`
   subdomain resolves to `127.0.0.1` through public DNS, so Host-routed URLs
   work with no hosts-file edits and behave identically on Linux, macOS and
   Windows. Overcast echoes the domain back in every URL it hands out.

   `localhost.localstack.cloud` and `localhost.floci.io` are recognised out of
   the box and work the same way, so a setup carried over from either tool
   keeps working — prefer `localhost.overcast.sh` for anything new.

2. **Plain `localhost` — the offline fallback.** `*.localhost` resolves to
   `127.0.0.1` with no network at all on Linux and macOS. **It does not on
   Windows**, where only `localhost` itself is in the hosts file — see the CDK
   S3 asset-upload troubleshooting in
   [cdk.md](./cdk.md#s3-asset-upload-fails-on-windows). Use this when option 1
   is unavailable and you are not on Windows.

3. **A hosts-file entry** for each specific subdomain you need, or a local
   DNS resolver (`dnsmasq`, `*.test` via `/etc/hosts`) that wildcard-resolves
   your own domain to `127.0.0.1`. More setup, but works fully offline, on
   every OS, and under restrictive network policies.

> **Caveat: public wildcard DNS needs internet access, and may be blocked.**
>
> - Option 1 needs a DNS lookup to a public resolver, so it will not work in
>   an offline or air-gapped environment. Use option 2 (Linux/macOS only) or
>   option 3 (any OS) there.
> - Some routers, corporate networks, and DNS filtering software implement
>   **DNS rebinding protection**, which blocks public hostnames from
>   resolving to private/loopback addresses like `127.0.0.1` — exactly what
>   `localhost.overcast.sh` and `localhost.localstack.cloud` do on purpose.
>   If Host-routed requests time out or fail to resolve, this is the first
>   thing to check (`nslookup localhost.overcast.sh` should return
>   `127.0.0.1`; if it returns nothing or errors, your network is filtering
>   it).
> - Plain `localhost` has neither problem, which is why it remains the
>   built-in default and the right choice for offline development on Linux and
>   macOS. A hosts-file entry (option 3) is the fallback that works
>   everywhere, including Windows and behind DNS filtering.

### Example: `docker compose` with a wildcard-DNS hostname

```yaml
services:
  overcast:
    image: ghcr.io/neaox/overcast
    ports:
      - "4566:4566" # AWS API endpoint
      - "4567:4567" # web management console
    environment:
      OVERCAST_HOSTNAME: localhost.overcast.sh
```

With this configuration, `CreateFunctionUrlConfig` returns URLs like
`http://a1b2c3....lambda-url.us-east-1.localhost.overcast.sh:4566/`, which
resolve via public DNS to `127.0.0.1` and route straight back into this same
container — see [performance.md](./performance.md#data-dir-placement-avoid-host-bind-mounts-on-docker-desktop)
for the matching `docker compose` pattern for the `/data` volume.

---

## Which host and port a URL carries, and why

Every URL Overcast hands out follows one rule: **the configured `OVERCAST_HOSTNAME` (when
set) on the port *you* reached Overcast on.** Your request is the only proof of a dialable
port — Overcast cannot see its own Docker port mapping — so with a remapped port
(`docker run -p 4652:4566`), host-side callers get URLs on `:4652` and containers started by
Overcast get `:4566`. Each party receives a URL that works *for them*. Values that cross the
container boundary mechanically — queue URLs baked into function environment by a deploy,
invoke payloads — are rewritten at the boundary.

Consequences worth knowing before they look like bugs:

- **The same resource shows different ports to different callers.** A stack output read from
  the host says `:4652`; the same output inside a Lambda says `:4566`. Both dial correctly.
  This is deliberate, not drift.
- **SQS queue URLs echo your exact origin** (no hostname substitution): SDKs dial the
  `QueueUrl` itself, so Overcast returns precisely what you just proved reachable.
- **The Cognito issuer also carries your port.** OIDC discovery requires `issuer` to match
  the URL you fetched the configuration from, and `jwks_uri` must be dialable by whoever
  validates the token. One consequence with a *remapped* port: a token minted from the host
  carries `:4652` in its `iss`, and a validator inside a container comparing that string
  literally against its own `:4566` issuer will report a mismatch. No single port can be
  dialable from both sides of a remap — if you hit this, publish the API 1:1
  (`-p 4566:4566`) and the issuer becomes identical everywhere. Overcast's own token
  validation is unaffected either way.
- **ECR `repositoryUri` is the exception**: it always uses the configured host and port,
  because the docker daemon — not your API client — is what dials it.
- **All of this presumes `OVERCAST_HOSTNAME`, if set, resolves to Overcast for every party.**
  The split-horizon names above do. `OVERCAST_HOSTNAME=localhost` does not (inside a
  container, `localhost` is the container) and remains the one configuration that silently
  breaks container callers.

How this is implemented, and which caller each address is minted for, is in
[docs/dev/networking.md](./dev/networking.md#5-the-addresses-overcast-hands-back).

---

## Data-plane endpoints — RDS, and anything else that is a container

Most names Overcast hands back point at **Overcast**. A few point at a
**container Overcast started**: an RDS instance's `Endpoint.Address`, an
ElastiCache node's address. The hostname rule is the same one — *the name is
minted on the endpoint your request arrived on* — but what has to happen for it
to resolve is different, and so is the port.

```
{dbInstanceIdentifier}.{region}.rds.{base}      # RDS DB instance
{dbClusterIdentifier}.cluster-rw.{region}.rds.{base}   # Aurora writer
{dbClusterIdentifier}.cluster-ro.{region}.rds.{base}   # Aurora reader
```

`{base}` is `OVERCAST_HOSTNAME` when set, otherwise the host you called Overcast
on — the same precedence every URL follows. With
`OVERCAST_HOSTNAME=localhost.overcast.sh`, a `Fn::GetAtt Endpoint.Address` comes
back as `mydb.ap-southeast-2.rds.localhost.overcast.sh`, and *that* is the value
a CDK stack bakes into an ECS task definition or a Secrets Manager secret.

**How it resolves inside a Lambda or ECS task.** Not through Overcast's DNS
server — that one answers "where is Overcast" (see
[the container-DNS notes](./dev/container-networking.md)). The engine container
carries its endpoint name as a **Docker network alias** on every network
emulated compute runs on — the shared data plane (`OVERCAST_NETWORK`, default
`overcast`), or the VPC network of its DB subnet group when it has one — and
Docker's embedded resolver answers from those aliases before forwarding anything
upstream. The alias set covers the name under *every* hostname Overcast could
mint it under, because the name a caller holds depends on the endpoint that
caller used.

**The port differs by caller, and this one is not cosmetic.** The engine listens
on 3306/5432 inside the Docker network; on the host it is reachable only through
a published port (`OVERCAST_RDS_PORT_BASE`, 33060 upwards, since 3306 is often
taken by a local install). So:

| Caller | `Endpoint.Address` | `Endpoint.Port` |
| --- | --- | --- |
| Lambda function, ECS task, any sibling container | the endpoint hostname | the engine port (3306/5432), as on AWS |
| The host (CLI, SDK, `cdk deploy`) | the endpoint hostname, or `127.0.0.1` when `{base}` has no wildcard DNS | the published host port |

Both pairs connect. Which one you were given is decided by the source address of
your request, since a split-horizon hostname is used from both sides of the
container boundary and cannot say which side you are on.

One consequence worth knowing before it looks like a bug: **a host-side deploy
bakes the host-side port into container environment.** `cdk deploy` runs on the
host, so `Fn::GetAtt Endpoint.Port` resolves to the published port, and a task
started from that template later reads it from inside the network where only
3306 is open. Applications that take a *host* and assume the standard port —
which is most of them, including the Bitnami images — are unaffected. If you
pass the port through explicitly, hard-code the engine's standard port rather
than `Endpoint.Port`; it is what real AWS would have returned anyway.

**When a name does not resolve, check the container first.** The alias exists
only while the engine container does. `docker ps --filter name=overcast-rds-`
should list it; if it is missing, the instance is `available` as metadata but
has nothing behind it, and the endpoint name resolves nowhere. The instance's
`EngineVersion` no longer has to be one Overcast advertises — the nearest image
family is used and the substitution is logged — so a missing container now means
Docker was unavailable or the image could not be pulled.

---

## The Docker networks Overcast uses

Everything Overcast starts as a container shares two Docker networks, and you
normally never have to think about either.

| Network | What it is for |
| --- | --- |
| `overcast` (`OVERCAST_NETWORK`) | The **data plane**: where resources reach each other. A Lambda function resolving an RDS endpoint, an ECS task reaching a cache node |
| `overcast_control` | Overcast's own channel to the containers it starts — the Lambda Runtime API, and the `AWS_ENDPOINT_URL` calls your function and task code make back into the emulator. Derived from `OVERCAST_NETWORK`; not separately configurable |

A resource created in a VPC additionally joins that VPC's network
(`overcast-vpc-*`). It keeps the shared data plane as well, so anything can
still reach it by name — Overcast does not yet restrict traffic by VPC.

**If you attach your own containers to Overcast's network** — a compose service
that needs to reach a database Overcast started, say — join `overcast`.

### Lambda, ECS and VPCs

Giving a function a `VpcConfig` (or a task an `awsvpc` configuration) is **real
connectivity**: the container joins that VPC's Docker network, takes an address
from its CIDR, and can reach the other resources in it by name.

What it does **not** do yet is take anything away. This is where Overcast and
AWS diverge, and the divergence runs in the direction that makes a local test
pass when the deployed thing will fail:

| | On AWS | In Overcast today |
| --- | --- | --- |
| A function **with** a `VpcConfig` reaching a resource outside that VPC | ✗ no route | ✓ works |
| A function **without** a `VpcConfig` reaching a private VPC resource | ✗ no route | ✓ works |
| A function in a VPC with no NAT gateway reaching the internet | ✗ | ✓ |
| Security groups restricting any of the above | ✓ enforced | ✗ stored, never applied |
| A function in a VPC calling the AWS APIs without a NAT or VPC endpoint | ✗ | ✓ **deliberately** — see below |

So a test that proves "my Lambda can reach my database" passes here whether or
not the VPC wiring is correct. If the wiring is what you are testing, that test
is not yet meaningful in Overcast — check it against AWS, or against the
[plan for enforcement](./plans/container-network-topology.md), which will make
these rows agree.

The last row stays divergent on purpose. Overcast's own API is what a container
calls for S3, SQS, DynamoDB and everything else, and it rides the same channel
as the Lambda Runtime API — so withholding it would not model a missing NAT
gateway, it would stop the function from starting at all. Read it as *"every VPC
has an interface endpoint for every service"*.

### "This used to work with `LAMBDA_NETWORK` set"

Overcast used to create one Docker network per emulator service:
`overcast_lambda`, `overcast_ecs`, `overcast_rds`, `overcast_elasticache`,
`overcast_msk`, `overcast_eks` and `overcast_efs`. That partition is gone, and
so are the seven environment variables that named those networks. It was the
reason a cache node could be reachable from a Lambda function and not from an
ECS task ([#872](https://github.com/Neaox/overcast/issues/872)) — whether any
two things could talk depended on which service happened to bridge the gap.

What to do:

- **You set one of the old variables.** They are no longer read. Set
  `OVERCAST_NETWORK` instead — one value, for the one network.
- **Your compose file joins `overcast_lambda`** (or another of the seven). Join
  `overcast` instead.
- **You have leftover `overcast_*` networks.** Overcast removes them at startup
  once nothing is attached. One that survives still has a container on it —
  `docker network inspect overcast_lambda` names it.

### "`DescribeVpcs` returns a VPC I did not create"

Each region now seeds a default VPC on first use, as every real AWS account
has. It is marked `isDefault`, uses AWS's own `172.31.0.0/16`, and is what
`Vpc.fromLookup(isDefault: true)` adopts.

`DescribeVpcs` honours `VpcId.N` and the `vpc-id` and `isDefault` filters, so a
lookup that names what it wants gets one VPC back. If you were relying on an
unfiltered list containing exactly your own VPCs, filter it.

You can delete it (`DeleteVpc`), as on AWS. Overcast will not seed another —
also as on AWS, where `CreateDefaultVpc` is the way back. Its backing network is
the shared data plane, so the delete removes the record and leaves the network
that every running container is attached to.

---

## See also

- [Container networking (internals)](./dev/container-networking.md) — which resolver answers what, and why a missing alias hangs rather than erroring
- [Using AWS SDKs and CLI](./sdk-cli.md) — endpoint configuration for every SDK
- [Using AWS CDK](./cdk.md) — the S3-specific virtual-hosted-addressing / Windows DNS issue
- [Lambda service reference](./services/lambda.md) — full endpoint coverage table
