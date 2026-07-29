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
| S3 (virtual-hosted style) | `{bucket}.s3[.{region}].{base}/...` or `{bucket}.{base}/...` | Both forms are supported. The second is what an AWS SDK emits against a custom endpoint with path-style disabled, and the only form CDK's asset publisher uses. See [sdk-cli.md](./sdk-cli.md#s3-path-style-addressing) and [cdk.md](./cdk.md#s3-asset-upload-fails-on-windows). |

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
container — see [performance.md](./performance.md#data-dir-placement--avoid-host-bind-mounts-on-docker-desktop)
for the matching `docker compose` pattern for the `/data` volume.

---

## See also

- [Using AWS SDKs and CLI](./sdk-cli.md) — endpoint configuration for every SDK
- [Using AWS CDK](./cdk.md) — the S3-specific virtual-hosted-addressing / Windows DNS issue
- [Lambda service reference](./services/lambda.md) — full endpoint coverage table
