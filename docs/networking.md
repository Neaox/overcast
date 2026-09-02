---
title: "Networking and host-based addressing"
description: "Path-style vs Host-routed AWS endpoints, the *.localhost.overcast.sh wildcard DNS option, reaching Overcast from sibling containers, and how VPCs isolate emulated compute."
section: "Networking"
tags:
  - docs
  - guide
  - networking
  - dns
  - docker
  - compose
  - vpc
---

# Networking and host-based addressing

Overcast listens on a single port (default `4566`) and dispatches every request
to the same process, whichever service it is for. Everything below follows from
one question: **what name does a caller use, and does that name resolve for
them?**

| If you are here about                                          | Go to                                                                       |
| -------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| An API Gateway / Lambda URL / AppSync / S3 hostname            | [What works today](#what-works-today)                                       |
| A subdomain that will not resolve, on Windows or offline       | [The `*.localhost.overcast.sh` wildcard DNS option](#the-localhostovercastsh-wildcard-dns-option) |
| A URL that works from your shell but not from a container      | [Docker Compose and sibling containers](#docker-compose-and-sibling-containers) |
| A port that differs depending on who asks                      | [Which host and port a URL carries](#which-host-and-port-a-url-carries-and-why) |
| An RDS or ElastiCache endpoint that will not connect           | [Data-plane endpoints](#data-plane-endpoints--rds-and-anything-else-that-is-a-container) |
| A Lambda that cannot reach a database                          | [Lambda, ECS and VPCs](#lambda-ecs-and-vpcs)                                 |

Real AWS splits addressing two ways, and Overcast supports both:

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
path-style requests use, so behaviour (authorizers, stage variables,
integration dispatch, event publishing) is identical either way — pick
whichever addressing style your client/SDK produces.

**The Host is case-insensitive in every part** — the resource ID, the service
label, the region segment and the base domain alike — because a hostname is
(RFC 4343). So `E1PQRS2T3U4V5W.cloudfront.localhost.overcast.sh:4566` and the
all-lowercase form a browser actually sends reach the same distribution, and
`MyBucket.localhost:4566` reaches bucket `mybucket`. **Paths are
case-sensitive**, as they are on AWS: `/_overcast/cloudfront/distributions/{distributionId}/...` and
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

> [!IMPORTANT]
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
[internal/middleware/hostroute.go](https://github.com/overcast-sh/overcast/blob/main/internal/middleware/hostroute.go).

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

A few things are intentionally simplified relative to real AWS, because
Overcast is a development tool and not a security boundary:

- **`AuthType` (`NONE` / `AWS_IAM`) is stored and returned but never
  enforced.** Every Host-routed invocation runs as if `AuthType` were
  `NONE`, regardless of what was configured.
- **`Cors` is stored, returned, and reflected onto invoke responses**
  (`Access-Control-Allow-*` headers) — this is the one piece of CORS
  behaviour actually applied, since it's cheap and matters for
  browser-based testing against a function URL.
- **`InvokeMode: RESPONSE_STREAM` is accepted but always behaves as
  `BUFFERED`** — there is no streaming function-URL invocation path in this
  emulator.
- **`Qualifier` is stored for API-shape correctness but not enforced against
  invocation** — Overcast's Lambda emulator already treats aliases/versions
  as metadata rather than separate executable snapshots (see
  `InvokeFunction`'s behaviour), and function URLs follow the same rule.

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

> [!WARNING]
> **Public wildcard DNS needs internet access, and may be blocked.**
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

---

## Docker Compose and sibling containers

Running Overcast in Compose alongside your own containers changes one thing:
client-facing URLs (SQS queue URLs, SNS unsubscribe links, RDS endpoints)
default to `localhost`, and inside a sibling container `localhost` is that
container. Set `OVERCAST_HOSTNAME` to a name both sides resolve.

**Best: a wildcard-DNS hostname.** It resolves to `127.0.0.1` from the host and
is remapped to Overcast inside every container Overcast starts, so one URL works
everywhere — and Host-routed addressing (function URLs, virtual-hosted S3) keeps
working too.

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
via public DNS to `127.0.0.1` and routes straight back into this same container.

**Offline, or behind DNS filtering: the Compose service name.** Use the service
name Compose already resolves for you:

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
> hands out then do not work from your own shell, from `cdk deploy`, or from a
> browser — including the web console's links. Add the name to your hosts file
> pointing at `127.0.0.1` if you need both, or prefer the wildcard-DNS option
> above.

Use `OVERCAST_SPLIT_HORIZON_HOSTS` to have additional hostnames remapped to
Overcast inside the containers it starts, on top of the built-in
`localhost.overcast.sh`, `localhost.localstack.cloud` and `localhost.floci.io`.

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

---

## Data-plane endpoints — RDS, and anything else that is a container

Most names Overcast hands back point at **Overcast**. A few point at a
**container Overcast started**: an RDS instance's `Endpoint.Address`, an
ElastiCache node's address. The hostname rule is the same one — *the name is
minted on the endpoint your request arrived on* — but what has to happen for it
to resolve is different, and so is the port.

```
{dbInstanceIdentifier}.{region}.rds.{base}      # RDS DB instance
{dbClusterIdentifier}.cluster.{region}.rds.{base}      # Aurora writer
{dbClusterIdentifier}.cluster-ro.{region}.rds.{base}   # Aurora reader
```

`{base}` is `OVERCAST_HOSTNAME` when set, otherwise the host you called Overcast
on — the same precedence every URL follows. With
`OVERCAST_HOSTNAME=localhost.overcast.sh`, a `Fn::GetAtt Endpoint.Address` comes
back as `mydb.ap-southeast-2.rds.localhost.overcast.sh`, and *that* is the value
a CDK stack bakes into an ECS task definition or a Secrets Manager secret.

**How it resolves inside a Lambda or ECS task.** Not through Overcast's DNS
server — that one answers "where is Overcast". The engine container
carries its endpoint name as a **Docker network alias** on every network
emulated compute runs on — the shared data plane (`OVERCAST_NETWORK`, default
`overcast`), or the VPC network of its DB subnet group when it has one — and
Docker's embedded resolver answers from those aliases before forwarding anything
upstream. The alias set covers the name under *every* hostname Overcast could
mint it under, because the name a caller holds depends on the endpoint that
caller used.

**The port differs by caller, and this one is not cosmetic.** The engine listens
on 3306/5432 inside the Docker network; on the host it is reachable only through
a published port (`RDS_PORT_BASE`, 33060 upwards, since 3306 is often
taken by a local install). So:

| Caller | `Endpoint.Address` | `Endpoint.Port` |
| --- | --- | --- |
| Lambda function, ECS task, any sibling container | the endpoint hostname | the engine port (3306/5432), as on AWS |
| The host (CLI, SDK, `cdk deploy`) | the endpoint hostname, or `127.0.0.1` when `{base}` has no wildcard DNS | the published host port |

**Aurora's cluster endpoints follow the same table**, because they name the same
thing. A cluster has no container of its own: `Endpoint` and `ReaderEndpoint`
both point at the writer member's engine, so `DescribeDBClusters` answers with
that instance's address and port, on the rules above. Both names — not only the
writer's — are registered as aliases on the writer's container, so
`cluster.clusterEndpoint.hostname` in a CDK stack resolves from inside a task
exactly as the instance endpoint does.

That last part is where Overcast diverges from AWS on purpose. On AWS the reader
endpoint load-balances across the Aurora Replicas and serves the writer only
when the cluster has none. Overcast gives every cluster member its own engine
container with its own storage — there is no shared Aurora volume to replicate
from — so a reader endpoint spread across the replicas would answer from an
empty database. It points at the writer instead: reads are not distributed, but
they return the data that was written.

The names themselves drop AWS's account-specific hash, as every Overcast
endpoint name does: AWS's `{cluster}.cluster-{hash}.…` and
`{cluster}.cluster-ro-{hash}.…` reduce to the two above. Overcast minted
`cluster-rw` for the writer until 0.0.1-alpha.37 — a label AWS has never used.
A cluster created by an older Overcast keeps answering to the name in its stored
record, so an upgrade in place does not strand one; a cluster created after it
answers only to `cluster`.

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
| `overcast-vpc-<vpc-id>` | One VPC's **plane** — where the resources in that VPC reach each other |
| `overcast-vpc-<vpc-id>-egress` | Only under `OVERCAST_VPC_EGRESS=routed`: that VPC's **route out**, joined by the containers whose subnet's route table has a `0.0.0.0/0` route. Created on the first placement that earns one — see [`routed`](#routed-egress-from-your-route-tables) |

A resource created in a VPC joins that VPC's network (`overcast-vpc-<vpc-id>`,
under your `OVERCAST_NETWORK` name)
**instead of** the shared one, so only things in the same VPC can reach it by
name — see [Lambda, ECS and VPCs](#lambda-ecs-and-vpcs) for what that costs and
how to opt out.

**If you attach your own containers to Overcast's network** — a compose service
that needs to reach a database Overcast started, say — join `overcast`.

### Egress modes

`OVERCAST_VPC_EGRESS` decides whether the containers Overcast starts can reach
anything outside your machine.

| Mode | What a VPC-attached container can reach | Use it when |
| --- | --- | --- |
| `open` (default) | Everything: other resources in its VPC, Overcast's own APIs, and the internet — including real AWS endpoints and third-party APIs | Normal development, and any stack whose functions call something outside the emulator. This is what LocalStack, Moto and SAM CLI do |
| `none` | Its VPC, and Overcast's own APIs. Nothing outside the machine — outbound connections fail with `ENETUNREACH` | Deterministic CI, air-gapped hosts, and proving a stack has no hidden external dependency |
| `routed` | Exactly what its subnet's route table says: a `0.0.0.0/0` route to an attached internet gateway or an available NAT gateway grants egress, and no default route withholds it — outbound connections then fail with `ENETUNREACH`. **Run Overcast in a container**: natively on Docker Desktop it cannot withhold, and says so | Catching a missing NAT gateway locally instead of in a deploy, and any stack whose public/private subnet split is the thing you are testing |

It is one setting for the whole topology rather than a flag per network,
because a container sits on two Docker networks at once and takes its default
route from whichever of them is routable. Isolating one and not the other
settles nothing.

**A VPC network is still `--internal` when its VPC has no internet gateway**,
under `open` as before. That costs `open` nothing: the container is also on the
control plane, which `open` leaves routable, so it has egress either way. The
flag stays honest about your template instead of being flattened. What changed
is that it no longer *decides* egress on its own — which is what used to make a
private subnet behind a NAT gateway indistinguishable from an isolated one.
Under `routed`, every VPC plane is `--internal` whatever the gateway says, and
the route out is a second network per VPC.

So `docker network inspect` can show `Internal: true` for a network whose
containers plainly reach the internet. When that surprises you, three places say
why, and all three say the same thing:

```sh
overcast network status     # "… — egress via overcast_control"
docker network inspect overcast-vpc-<id> --format '{{index .Labels "overcast.network.egress"}}'
```

and the startup log's `vpc network isolation` line, which names the mode and the
route out for every VPC network as it is created.

**Invocations keep working in `none`.** The Lambda Runtime API and
`AWS_ENDPOINT_URL` calls back into the emulator are not egress — they reach a
server on this machine — so functions still run, and only what leaves the
machine is withheld.

**On Docker Desktop, `none` cannot isolate the control plane.** Containers
there reach Overcast at your host's own address, which `--internal` would cut
off, stranding every invocation at INIT. Overcast leaves that one network
routable, says so at startup, and reports it in `/_overcast/health` — so the
stack is *not* hermetic. Run Overcast in a container, or against a native Linux
Docker daemon, for the whole of `none`.

### `routed`: egress from your route tables

`OVERCAST_VPC_EGRESS=routed` is the AWS-faithful mode. Egress is decided **per
subnet**, from the route table associated with it — the one thing local
emulation can offer here that LocalStack, Moto and SAM CLI do not, all of which
give a VPC-attached function full egress and model no VPC networking at all.

> [!IMPORTANT]
> **Run Overcast in a container for this mode.** On Docker Desktop with
> Overcast running natively — and on any native Windows or macOS host —
> `routed` **cannot withhold egress**: every container has a route out
> whatever its route table says, so a subnet with no `0.0.0.0/0` route still
> reaches the internet and the missing NAT gateway this mode exists to catch
> goes uncaught. Overcast says so rather than pretending: **two warnings in the
> startup log**, which is where to look, and a `routed-egress-not-enforced`
> advisory in `GET /_overcast/debug/metrics` when `OVERCAST_DEBUG=true`.
> Running Overcast **in a container**, or against a **native Linux Docker
> daemon**, is what makes the mode enforceable.
>
> Two host limits cause it, and both the warnings and the advisory name
> whichever applies:
>
> - The control plane cannot be isolated where containers reach the Lambda
>   Runtime API at the host's own address — `--internal` would strand every
>   invocation at INIT. The same limit [`none` has](#egress-modes).
> - VPC placement is not enforced where Overcast's DNS resolver cannot start
>   (no `/etc/resolv.conf` on those hosts), so a VPC-placed container also
>   joins the routable shared data plane — see
>   [Two things this does not restrict](#two-things-this-does-not-restrict).

| The subnet's `0.0.0.0/0` route | What its containers get |
| --- | --- |
| → an internet gateway **attached to this VPC** | A route out |
| → a NAT gateway that exists and is available | A route out |
| → a gateway that is detached, deleted or gone | Nothing — a blackhole on AWS, and here |
| → anything else (a virtual private gateway, a peering connection, an instance) | Nothing — none of those reaches the internet through anything Overcast runs |
| absent | Nothing. Outbound connections fail with `ENETUNREACH` |

A subnet with no explicit route-table association uses its VPC's main table,
exactly as on AWS. A container placed in several subnets gets a route out when
**any** of them grants one: on AWS such a function reaches the internet from
some of its ENIs and not others, which is not a state one container can be in,
and granting is the reading that does not make a working stack fail locally.

**How it is carried.** The VPC's plane stays one `--internal` bridge that every
container in the VPC joins, whatever its subnets route to — an isolated
database and a NAT-routed function in one VPC reach each other on AWS, and they
could not if each egress class were its own bridge. A container whose subnet
grants egress *also* joins a second, routable bridge,
`overcast-vpc-<vpc-id>-egress`, and takes its default route from there.

That shape is what makes a route-table change safe on a hot path:

- **A container placed after the change** gets the answer its route table gives
  now. Nothing to restart.
- **A container already running** is moved on or off the egress network in
  place, by one `docker network connect` or `disconnect`. Its plane, its
  address, its DNS names and its control-plane connection are all untouched, so
  an in-flight invocation keeps its Runtime API. That is the AWS shape too: a
  route-table change reroutes an ENI in place.

`CreateRoute`, `DeleteRoute`, `AssociateRouteTable`, `DisassociateRouteTable`,
`DeleteRouteTable`, `CreateNatGateway`, `DeleteNatGateway`, `AttachInternetGateway`
and `DetachInternetGateway` each revisit every container in the VPC. Each move
is logged with the subnet and the route table that decided it. A move Docker
refuses does not fail the API call — AWS never refuses a route for a reason
like a daemon's — but is logged at `error`, raised as an advisory in
`GET /_overcast/debug/metrics` (with `OVERCAST_DEBUG=true`), and retried at the
next start.

**Resources outside a VPC are unaffected.** They sit on the shared data plane,
which `routed` leaves routable, because that is what they get on AWS. So does
anything in a **default VPC**, whose subnets are public on AWS.

> [!NOTE]
> `routed` isolates the control plane, as `none` does. A routable control plane
> would hand every container a route out whatever its route table said — which
> is exactly what the measurements behind
> [#1571](https://github.com/overcast-sh/overcast/issues/1571) found.

### The address-pool ceiling

`routed` needs a second Docker network per VPC, and Docker's own default
address pools stretch to about **31 networks in total** on a stock daemon —
shared with every other tool on the machine. Doubling the per-VPC count against
that ceiling is how a run ends in `all predefined address pools have been fully
subnetted`.

So the egress networks never draw on those pools. Each is pinned to a `/24`
carved from `OVERCAST_VPC_EGRESS_POOL`, which defaults to `198.18.0.0/16` — the
RFC 2544 benchmarking range, never routed on the internet, and untouched by
both Docker's defaults and the `remapped` VPC strategy's `100.64.0.0/10`.

| | |
| --- | --- |
| VPCs with egress the default pool supports | **256** |
| Address per VPC | One `/24`, allocated once and kept on the VPC's record, so a restart brings the network back at the same range |
| When one is created | On the first placement whose subnet grants a route out. A VPC whose subnets all route nowhere never gets one |
| When one is removed | With its VPC, and at startup when no VPC names it — including every one left behind by a `routed` run after you switch back to `open` or `none` |

Set a wider range if you need more:

```sh
OVERCAST_VPC_EGRESS_POOL=198.18.0.0/15   # 512 VPCs
```

It must be an IPv4 CIDR between `/8` and `/24`, and is validated at startup in
every mode — so a pool written for a `routed` deployment is not found to be
malformed on the day you switch to it. Running out names the pool and how to
widen it, and **fails the placement** rather than quietly starting a container
without the egress its template grants.

### Control-plane isolation

**Deprecated.** `OVERCAST_CONTROL_PLANE_INTERNAL=auto|true|false` pins the
`overcast_control` network's isolation on top of the mode above. It still works
and still wins where it is set, and setting it logs a deprecation notice.

Prefer the mode: `OVERCAST_VPC_EGRESS=none` for what `true` meant, `open` for
what `false` meant — applied to every network rather than to one. Egress is a
property of the whole topology, and pinning a single network never settled it.

### Network state verification

Docker's create-network call returns an existing network **unchanged**. It
applies no isolation, no subnet, no driver option. So a network created by an
older Overcast, a different egress mode, or by hand keeps every setting it was
born with, while `docker network ls` says the name is present and everything
looks fine.

Overcast therefore checks, on every start, that each network it reuses is in
the exact state it would have created it in. Not just the isolation flag —
every field:

| Checked | Why it matters |
| --- | --- |
| `driver` | A network of the right name under the wrong driver behaves nothing like the one asked for |
| `internal` | Decides whether containers on it reach anything outside the machine |
| IPv6 | Changes which addresses containers get, and which Overcast's resolver can answer with |
| IPAM subnet and gateway | Only when Overcast pinned them — a VPC network takes its range from the VPC's CIDR |
| Driver options | `enable_icc`, `enable_ip_masquerade`. A network with masquerading off looks routable and behaves isolated |
| `overcast.network.spec-hash` | The identity of the whole desired state. **A network with no such label is treated as mismatched** — it predates this check, and those are the networks that have actually been wrong |

What happens next depends on the network. **The two planes** (`overcast` and
`overcast_control`) and **per-VPC networks** are repaired differently, because
only one of them can move its containers across:

| | The planes | Per-VPC networks |
| --- | --- | --- |
| Nothing attached | Removed and recreated to match | Removed and recreated to match |
| Containers attached | **Left alone.** Warned at startup naming every differing field and every attached container, `/_overcast/health` marked **degraded**, console advisory raised, `overcast network reset` named as the fix | **Rebuilt under them.** Each container is disconnected, the network is recreated, and each is reconnected at the address and DNS aliases it had. Connections across the VPC bridge drop; the control-plane connection does not, so an in-flight invocation keeps its Runtime API |
| Owned by another Overcast instance | Left alone, always | Left alone, always |
| Owned by another tool (`docker compose` and friends) | Left alone, always | Left alone, always |

A plane carries every container Overcast has started, so rebuilding it under
them would sever the Runtime API mid-invocation — the repair has to wait for a
moment somebody chose. A VPC network carries only that VPC's resources and
Overcast knows how to put them back, which is what makes the automatic rebuild
safe there.

> **On the first start after upgrading**, every VPC network on the machine
> mismatches — none carries a spec-hash label yet — so each is rebuilt once,
> dropping open connections across its VPC bridge. Stop your stack before
> upgrading if that matters, or expect one reconnect.

An instance never removes a network it cannot prove it created: every network
Overcast creates carries the identity of the instance that created it, and a
network carrying another tool's ownership labels is left alone whatever its
name.

### `overcast network status` and `overcast network reset`

```sh
overcast network status              # what differs, and on which fields
overcast network reset --dry-run     # exactly what a reset would do
overcast network reset               # do it
```

`reset` rebuilds each network that differs: it stops the containers **Overcast**
started, disconnects containers it did not start and leaves them running,
removes the network, and recreates it to spec. A network already in the right
state is left alone unless you pass `--force`. Name one or more networks to
narrow it.

## Lambda, ECS and VPCs

Giving a function a `VpcConfig` (or a task an `awsvpc` configuration) puts the
container in that VPC: it joins that VPC's Docker network, takes an address from
its CIDR, and reaches the other resources in it by name.

**It also takes away everything outside that VPC**, which is what naming a VPC
means on AWS.

| | On AWS | In Overcast |
| --- | --- | --- |
| A function **with** a `VpcConfig` reaching a resource outside that VPC | ✗ no route | ✗ refused |
| A function **without** a `VpcConfig` reaching a resource inside one | ✗ no route | ✗ refused |
| Two resources in the same VPC reaching each other | ✓ | ✓ |
| A function in a VPC with no NAT gateway reaching the internet | ✗ | ✓ by default; ✗ with `OVERCAST_VPC_EGRESS=none` — see [Egress modes](#egress-modes) |
| Security groups restricting any of the above | ✓ enforced | ✗ stored, never applied |
| A function in a VPC calling the AWS APIs without a NAT or VPC endpoint | ✗ | ✓ **deliberately** — see below |

"Refused" means what it says. Overcast will not answer a name the caller cannot
reach, and the log names both sides:

```
refusing a data-plane name the caller cannot reach
  name:            mydb.us-east-1.rds.localhost.overcast.sh
  target:          rds mydb
  caller:          lambda api-handler
  target_networks: [overcast-vpc-vpc-0abc]
  caller_networks: [overcast_control overcast]
```

That is better than AWS gives you, where the same mistake is a connection that
times out several minutes later with nothing to point at.

### If this just started failing

Your stack is describing something that would not work deployed either. Three
ways out, all of them AWS's own fields rather than Overcast settings — so the
fix that works here is the fix that works on AWS:

| Situation | Fix |
| --- | --- |
| A function or task should be in the VPC | Give it a `VpcConfig` / `awsvpcConfiguration` naming a subnet in that VPC |
| A database should be reachable from outside its VPC | `PubliclyAccessible: true` on the instance |
| A task should be reachable from outside its VPC | `assignPublicIp: ENABLED` in its `awsvpcConfiguration` |

If none of those is what you want, the honest answer is that the two things
genuinely cannot talk on AWS, and the local failure has told you so early.

### Two things this does not restrict

**Overcast's own API stays reachable from inside any VPC.** A container calls it
for S3, SQS, DynamoDB and everything else, and it rides the same channel as the
Lambda Runtime API — withholding it would not model a missing NAT gateway, it
would stop the function from starting at all. Read it as *"every VPC has an
interface endpoint for every service"*.

**On a native Windows or macOS host, nothing is restricted at all.** The
restriction is only safe where a forbidden connection fails by name, and that
needs Overcast's DNS resolver, which needs `/etc/resolv.conf` to find upstream
servers. There is no such file on those hosts, so the resolver does not start,
and rather than let a forbidden connection hang with no explanation Overcast
keeps the old permissive behaviour. Run Overcast in a container — the
recommended setup — to get the restriction and the diagnostics together.

### What is still not enforced

Docker network membership expresses "in this VPC or not", and nothing finer. So
what a VPC lets *through* is not modelled:

- **Security groups and NACLs.** Stored and returned; never applied. There is no
  port- or source-level filtering between two containers that share a network.
- **Subnets within a VPC.** One flat network per VPC — no public/private
  distinction. Everything in a VPC reaches everything else in it, whichever
  subnets they are in.
- **Subnet-level internet access**, unless you ask for it. Under the default
  `open` a private subnet behind a NAT gateway and a fully isolated one get the
  same answer. Set [`OVERCAST_VPC_EGRESS=routed`](#routed-egress-from-your-route-tables)
  to have each subnet's route table decide.
- **Two VPCs with the same CIDR**, under the default `shared` strategy, are one
  Docker network and therefore not isolated from each other. `strict` and
  `remapped` give real separation — see
  [`OVERCAST_EC2_VPC_STRATEGY`](./services/ec2.md).

### "This used to work with `LAMBDA_NETWORK` set"

Overcast used to create one Docker network per emulator service:
`overcast_lambda`, `overcast_ecs`, `overcast_rds`, `overcast_elasticache`,
`overcast_msk`, `overcast_eks` and `overcast_efs`. That partition is gone, and
so are the seven environment variables that named those networks. It was the
reason a cache node could be reachable from a Lambda function and not from an
ECS task ([#872](https://github.com/overcast-sh/overcast/issues/872)) — whether any
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

## Related

- [Using AWS SDKs and CLI](./sdk-cli.md) — endpoint configuration for every SDK
- [Using AWS CDK](./cdk.md) — the S3-specific virtual-hosted-addressing / Windows DNS issue
- [Lambda service reference](./services/lambda.md) — full endpoint coverage table
