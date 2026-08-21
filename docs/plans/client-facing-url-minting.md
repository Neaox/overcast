---
title: Client-facing URL minting — one rule, tested against every service
description: Every URL Overcast hands a caller must be dialable by that caller. The repo had four base-URL precedences; this plan documents each service's requirements, tests the unified rule against them, and records which special cases are genuinely required.
---

# Client-facing URL minting — one rule, tested against every service

**Status:** implemented (alpha.26) · **Supersedes:** the open finding in
`client-base-url-port-vs-stability.md` (that plan recorded the initial,
incorrect port-vs-stability framing and was deleted 2026-08-21 once superseded)
**Related:** #318 (SQS per-caller), #331 (published port), #351/#353 (CFN output origins),
#378 (host-routed grammar), the alpha.26 Cognito issuer fix.

## Problem

With `OVERCAST_HOSTNAME=localhost.overcast.sh` and the API port remapped (`-p 4652:4566`),
measured on a real instance:

| Advertised URL | Port | Dialable by the caller who received it |
| --- | --- | --- |
| SQS `QueueUrl` | 4652 | ✅ |
| AppSync `uris.GRAPHQL` | **4566** | ❌ |
| API Gateway v2 `apiEndpoint` | **4566** | ❌ |
| Lambda `FunctionUrl` | **4566** | ❌ |
| Cognito issuer / discovery endpoints | **4566** | ❌ (a host OIDC client cannot fetch JWKS) |

Not exotic config: `scripts/run-test-instance.sh` remaps the port, and the CDK instructions
say to set `OVERCAST_HOSTNAME=localhost.overcast.sh`.

## Research: the four precedences that coexisted

| Implementation | Host | Port | Scheme | Consumers |
| --- | --- | --- | --- | --- |
| `serviceutil.ClientBaseURL` | cfg.Hostname, else caller | **cfg.Port**, else caller | cfg TLS, else caller | AppSync `uris`, Cognito issuer/discovery/managed login, `HostRoutedURL` → `apiEndpoint`, `FunctionUrl` |
| SQS `queueURLBase` | **caller verbatim** | caller | caller | queue URLs on the wire |
| CFN `clientBaseURL` | cfg.Hostname, else caller | **caller** | caller | stack-output re-minting (#351/#353) |
| Cognito `issuerBase` (typed CBOR path) | cfg.Hostname → `ExternalBaseURL` | **cfg.Port** | cfg TLS | typed-path issuer — a hand-kept mirror of `ClientBaseURL` |

Plus `cfg.ExternalBaseURL()` (all-config), the only deliberately caller-independent base:
stored state and no-caller fallbacks.

`cfg.PublishedPort` was referenced by none of them.

## The rule

**A client-facing URL is minted from the configured hostname (when set) on the caller's
port; scheme is https if Overcast serves TLS or the caller arrived over https.**

- The hostname is the operator's assertion that one name resolves for every party (#351) —
  true for the split-horizon defaults. `OVERCAST_HOSTNAME=localhost` remains the documented
  misconfiguration it always was (`docs/dev/manual-testing.md`).
- The port is the caller's because their request is the only *proof* of a dialable port;
  Overcast cannot see its own port mapping. `cfg.Port` is the fallback when a request
  carries no port (background work, synthetic internal dispatch), and such values are
  re-rendered per caller at read time or rewritten at the container boundary.
- Stored state stays canonical (`ExternalBaseURL()`), re-rendered on the way out.
- AWS-observable behaviour governs URL *shape* (#378's grammar); reachability is the local
  allowance a dev tool must make.

One core implementation: `serviceutil.ClientBaseURLFromOrigin(cfg, origin)`, with
`ClientBaseURL(cfg, r)` as its request-shaped wrapper. Context-shaped callers (middleware
`ClientEndpointFromContext`) pass the stamped origin.

## Per-service requirements and constraints — the rule tested against each

| Service / URL | Requirement | Constraint that could break | Verdict |
| --- | --- | --- | --- |
| **AppSync** `uris.GRAPHQL/REALTIME` | dialable by holder; crosses parties (CDK `Fn::GetAtt GraphQLUrl` passes it into templates and env) | host-routed form needs a base that can carry subdomains — gated by `SupportsHostRouting` (#378); REALTIME must share GRAPHQL's host (colocated) | **standard rule** |
| **API Gateway v2** `apiEndpoint`, **Lambda** `FunctionUrl` | dialable by holder; crosses parties via `Fn::GetAtt` | no path-style fallback exists, so these are host-routed unconditionally; the port must therefore be right | **standard rule** |
| **CloudFormation** stack outputs | dialable by whoever calls `DescribeStacks` | must only re-origin values recognisably Overcast's own — never ARNs, ECR URIs, third-party endpoints (#353) | **standard rule** (its private helper already implemented it; now delegates) |
| **Cognito** issuer + OIDC discovery | discovery: OIDC Discovery 1.0 §4.3 — `issuer` MUST equal the URL config was retrieved from → **per-caller is spec-required**, config-port violated it for every remapped-port caller | `iss` is embedded in a **durable signed artifact** (the JWT) that outlives the request and can cross the container boundary — unique among all minted URLs; and two dispatch paths (JSON + Smithy CBOR) must mint identical strings | **special handling documented, same rule**: (1) Overcast's own validation extracts the pool ID from the issuer *path* and must never become a literal string comparison — that accommodation is what makes per-caller `iss` safe, guarded by comment + test; (2) both paths now call the same function, so typed==JSON holds by construction |
| **SQS** queue URLs (wire) | dialable by holder; JS/.NET/Java SDKs dial the `QueueUrl` itself; the URL is also an *input parameter* clients send back | the caller's verbatim origin is the one base carrying **zero** resolution assumptions — they just dialed it. Substituting even a well-configured hostname adds an assumption for no reachability gain, and amplifies the `OVERCAST_HOSTNAME=localhost` misconfiguration to container callers | **deliberate divergence, kept**: verbatim caller origin, canonical fallback. Documented in code so a future DRY pass doesn't "unify" it |
| **SNS** notification `UnsubscribeURL` | must be dialable by whoever received the notification — it is the documented way to unsubscribe, and webhook handlers follow it | minted during **asynchronous** fan-out, on a goroutine that outlives the publishing request. There *is* a caller, but not at the point the envelope is built, so reading the origin at delivery time is too late | **standard rule, origin carried explicitly** (#797): the Publish handler reads the stamped origin while its request is in scope and passes it into `fanOut`. A publish with no HTTP caller behind it (a scheduler firing, internal dispatch, a real-AWS-hostname request) carries an empty origin and takes the rule's canonical fallback. Passing it as an argument rather than reading it back out of the fan-out context is deliberate: the context happens to carry it today, but only because every publish path hands `fanOut` a `context.WithoutCancel` of its own request — a property nothing enforces |
| **ECR** `repositoryUri` | `docker push/pull` must reach the registry | scheme-less `host:port/name` consumed by the **docker daemon**, not the API caller — a per-caller value would churn the login/push target mid-session and the daemon is not the caller anyway | **deferred, canonical kept**: documented constraint; revisit only if remapped-port docker workflows surface |
| **CloudFront** `DomainName` | resolvable by holder | minted as `{id}.cloudfront.{host}` on the caller's hostname already | **standard rule** |
| **RDS** `Endpoint.Address`, Aurora `Endpoint`/`ReaderEndpoint` | resolvable *and connectable* by the holder; crosses parties constantly (`Fn::GetAtt` into ECS task env and Secrets Manager) | it names a **container Overcast started**, not Overcast, so the rule's usual "point at us" answer is wrong twice over: the name must resolve through Docker's embedded resolver (network aliases, not our DNS server), and the engine port inside the network is not the published port on the host | **standard rule for the hostname, extended for the port**: hostname per the rule; port per caller side (engine port for a sibling container, published port for the host), decided by source address since a split-horizon name cannot distinguish them. Aliases are registered for the name under *every* mintable hostname, because which one a caller holds depends on their endpoint. See docs/networking.md § Data-plane endpoints |
| **S3** presigned / virtual-hosted URLs | — | minted client-side by SDKs from *their* endpoint; Overcast only needs to accept every arriving Host form (#371 case-folding, hostroute grammar) | out of scope — server-side accept, not minting |
| **Web console** | browser must reach the API | browser-origin based; published port via `#328`'s BFF split | out of scope — separate plumbing |

**Summary: one special case kept (SQS verbatim echo), one documented accommodation
(Cognito path-based validation), one deferral (ECR), one extension for data-plane
endpoints (RDS).** Everything else is the standard rule.

**Still outstanding: ElastiCache mints the same way RDS used to** —
`cfg.ExternalHostname()` at create time, with the address overwritten by the
container's IP once it starts (`internal/services/elasticache/handler.go`). The
helpers RDS now uses are service-local; lifting them into `serviceutil` and
applying them there is the follow-up.

## The issuer, re-examined

The earlier finding said per-caller minting "breaks" the Cognito issuer. Fresh look: it
broke a *test*, and the test pinned behaviour that violates OIDC Discovery §4.3 for
remapped-port callers. What the alpha.26 issuer fix actually protects survives intact:

- **Hostname**: still authoritative — the "sibling container handed 127.0.0.1" failure
  cannot return.
- **Scheme**: still config-asserted TLS; upgrades, never downgrades.
- **Overcast's own validation**: `poolIDFromIssuer` reads the path segment; caller-agnostic.
- **Typed == JSON**: now one function instead of a hand-kept mirror.

**Irreducible caveat, documented rather than "solved":** with a remapped port there exists
no single host:port dialable from both sides of the container boundary, so a token minted
on one side fails a *literal* cross-boundary `iss` comparison on the other — under every
possible policy, including the old one (which additionally broke host-side discovery). On
the default 1:1 mapping the question disappears. If this bites a user, the log-visible
symptom is an issuer mismatch naming two ports; this paragraph is the explanation.

## Container-boundary consequence

Per-caller ports create one new mechanical hazard: a host-side deploy can bake a
split-horizon URL carrying the **published** port into function/task env or invoke
payloads. Inside a container the name resolves, but that port is bound only on the host —
so `containerendpoint.RewriteURLs` now also re-points `{split-horizon-host}:{published}` at
the listen port, preserving the hostname (both the `://name:port` origin form and the
`.name:port` virtual-hosted form). Loopback rewriting is unchanged.

## Dialability statement

Every URL Overcast mints is dialable by the party it was minted for, provided a configured
`OVERCAST_HOSTNAME` resolves to Overcast for every party (the split-horizon defaults do).
Mechanical cross-boundary routes — env, payloads — are rewritten; the residue is human
copy-paste of a URL across a port remap, which no rewrite can see.

## Methodology for new services

1. Never build a client-facing URL from `r.Host` directly, nor from config alone; call
   `serviceutil.ClientBaseURL` / `ClientBaseURLFromOrigin`.
2. Persist canonical (`ExternalBaseURL()`); re-render per caller on the way out.
3. Host-routed shapes only via `serviceutil.HostRoutedURL` + `SupportsHostRouting`.
4. If a minted value can be baked into container env or payloads, make sure
   `containerendpoint` can recognise and rewrite its origin.
5. Identifiers are not URLs — never re-origin ARNs, ECR URIs, third-party endpoints.
6. A deliberate divergence from the rule gets a code comment naming this document and the
   functional reason, so a tidy-up cannot silently revert it.
7. Verify per `docs/dev/manual-testing.md`: real SDK, **remapped** port, no explicit
   endpoint, from inside a container where relevant.

## TLS across the container boundary

Found in alpha.26 pre-release testing, immediately after OVERCAST_TLS landed: with the API
listener serving only TLS, containers still received `http://` endpoints, and every SDK call
from every Lambda failed with Go's plaintext rejection surfaced as deserialization garbage.

The rule extends across the boundary in three parts, all keyed on `cfg.TLSEnabled()`:

1. **Scheme follows the listener** — `containerendpoint.BaseURL` is the one place the
   container-facing scheme is decided; both the Lambda and ECS runtimes build their endpoint
   through it.
2. **The minting CA travels with the code** — `Mapper.CABundleTar` is injected via
   `CopyToContainer` (a dockerized Overcast has no host path to bind-mount), and
   `Mapper.CABundleEnv` points `AWS_CA_BUNDLE` / `NODE_EXTRA_CA_CERTS` / `SSL_CERT_FILE` /
   `REQUESTS_CA_BUNDLE` at it. Java's SDK reads only its truststore — documented caveat in
   docs/https.md, not fixable from environment.
3. **Rewrites target a SAN-covered name** — under TLS, `RewriteURLs` substitutes the named
   client endpoint rather than the raw address: the leaf's SANs cover Overcast's names, never
   a container-network IP, so an address target would trade a scheme error for a certificate
   error.

Verified live under OVERCAST_TLS=auto with a remapped port: every bare-SDK call from inside a
function passes over https, and plain-HTTP mode is byte-identical to before (no CA env, http
endpoint).

## Performance

Two paths change; neither is the routing hot path (which stays allocation-free and untouched):

- **`ClientBaseURLFromOrigin`** adds one `url.Parse` per *minted URL* — a response-path cost
  paid a handful of times per API call (CreateQueue, DescribeStacks, token mint, discovery
  fetch), never per routed request. Measured (`BenchmarkClientBaseURLFromOrigin`, AMD Ryzen 9
  5900X, Go 1.24 in Docker): **274 ns/op, 196 B, 3 allocs** per minted URL.
- **`RewriteURLs`** gains up to `2 × len(split-horizon names)` `strings.Contains` scans, and it
  runs on **every invoke payload** — so the patterns are precomputed once in
  `WithPublishedPort` (they are pure config), the extension is skipped entirely when no
  published port differs, and the miss path allocates nothing. Measured
  (`BenchmarkRewriteURLs_payloadMiss`, ~1 KiB payload with no Overcast origins — the shape of
  nearly every real payload): **313 ns/op, 0 B, 0 allocs**.

## Verification

- Unit: core precedence (host/port/scheme × origin present/portless/absent × TLS);
  delegation equivalence; the container-boundary rewrite forms; every retargeted assertion
  carries its spec reason in the diff.
- Full `go test ./...` — scoped runs are insufficient here (the audit doc's warning; it was
  proved right when the Cognito collision only appeared in a full run).
- Live, `OVERCAST_HOSTNAME=localhost.overcast.sh -p 4652:4566`: every URL in the symptom
  table carries `:4652` and answers a real request; OIDC discovery fetched on `:4652` has a
  matching `issuer` and fetchable `jwks_uri`; the in-Lambda probe passes; a baked `:4652`
  split-horizon env URL is rewritten to `:4566` and works.
- CDK lifecycle suite stays 35/0.
