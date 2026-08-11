# One namespace for every non-canonical URL: `/_overcast/`

> Status: **agreed** — audit complete and open questions decided (§4.3, §5, §8);
> no code changed yet.
> Scope: `internal/router/`, `internal/middleware/`, `internal/services/{apigateway,appsync,cloudfront,cognito,ecs,elbv2,lambda,rds}/`,
> `internal/trace/`, `internal/bff/`, `web/`, `compat/ui/`, `Dockerfile`,
> `docker-compose*.yml`, `docs/`.
> Related: [Making the pinned manifest the enforced source of truth](./manifest-enforcement.md)
> — this plan adds enforcement point 9 to that table, and is the half that
> manifest gates structurally cannot cover.

## 1. The rule

> **Every path Overcast serves on the AWS API listener is either a binding the
> pinned AWS manifest models, or it lives under `/_overcast/`. There is no
> third category.**

The `_` is a property of the namespace, not of the endpoint inside it, so it
appears exactly once and at the front: `/_health` becomes **`/_overcast/health`**,
never `/_overcast/_health`. Everything below the prefix is an ordinary path.

Two consequences make this worth enforcing rather than merely writing down:

- **`/_overcast/` becomes the only reserved prefix.** S3 bucket names cannot
  begin with `_`, which is the entire reason the `/_`-prefix convention works
  at all — but today that guarantee is spent across sixteen separate first
  segments (`/_health`, `/_debug`, `/_mcp`, `/_lambda`, `/_cognito`, …), each
  of which some future AWS service could plausibly want. Collapsing to one
  removes the question permanently.
- **Three hand-maintained prefix switches collapse into one string constant.**
  `internalService`, `detectService`'s internal arms, and
  `registeredRouteClassification`'s sixteen `/_*` entries all exist only to
  answer "which service owns this internal path". After the move the answer is
  the path's second segment, and the switches delete.

## 2. What is served today

Taken by walking the real router (`chi.Walk` over `router.New`, the same walk
`TestDetectServiceClassifiesEveryRegisteredRouteFamily` uses) and cross-checking
against every `/_`-literal in the tree.

### 2.1 Router-owned roots

| Current | Registered at | Target |
| --- | --- | --- |
| `GET /_health` | [router.go:974](../../internal/router/router.go) | `/_overcast/health` |
| `GET /_metrics` | [router.go:235](../../internal/router/router.go) | `/_overcast/metrics` |
| `GET /_topology` | [router.go:977](../../internal/router/router.go) | `/_overcast/topology` |
| `GET /_/info` | [router.go:240](../../internal/router/router.go) | `/_overcast/info` |
| `GET /_events` | [router.go:224](../../internal/router/router.go) | `/_overcast/events` |
| `GET /_events/request/{requestId}` | [router.go:225](../../internal/router/router.go) | `/_overcast/events/request/{requestId}` |
| `GET /_internal/domains/watch` | [router.go:231](../../internal/router/router.go) | `/_overcast/domains/watch` |
| `/_debug` + `/_debug/*` (only when `cfg.Debug`) | [router.go:275](../../internal/router/router.go) | `/_overcast/debug/*` |
| `/_mcp` + `/_mcp/*` (excluded by `slim`) | [mcp_routes.go:47](../../internal/router/mcp_routes.go) | `/_overcast/mcp` |
| `GET /_overcast/init`, `/_overcast/init/{stage}` | [router.go:204](../../internal/router/router.go) | unchanged |
| `GET /_overcast/ca.pem` | [trust/remote.go:35](../../internal/hostbridge/trust/remote.go) | unchanged |
| `/_overcast/inbox/*` | [router.go:577](../../internal/router/router.go) | `/_overcast/ses/inbox/*` — see §4.2 |

`/_/info` is the sharpest illustration of the problem: it is the only route
using a `/_/` root, it was named before any convention existed, and nothing
today would catch a second one.

### 2.2 Service-owned data plane

These are the rewrite targets of host-addressed requests — the paths a browser
or `curl` actually reaches when a client-facing URL is resolved. They also
appear in URLs Overcast *mints* and hands back to callers, which is what makes
them the highest-risk group to move (§6).

| Current | Registered at | Target |
| --- | --- | --- |
| `/_apigateway/execute-api/{apiId}/{region}/*` | [apigateway/service.go:273](../../internal/services/apigateway/service.go) | `/_overcast/apigateway/execute-api/{apiId}/{region}/*` |
| `/_appsync/{apiId}/graphql`, `/_appsync/{apiId}/realtime` | [appsync/service.go:241](../../internal/services/appsync/service.go) | `/_overcast/appsync/{apiId}/…` |
| `/_cloudfront/{distId}/*` | [cloudfront/service.go:223](../../internal/services/cloudfront/service.go) | `/_overcast/cloudfront/{distId}/*` |
| `/_elb`, `/_elb/*` | [elbv2/service.go:102](../../internal/services/elbv2/service.go) | `/_overcast/elb/*` |
| `/_lambda/url-invoke/{urlId}/*` | [lambda/service.go:1323](../../internal/services/lambda/service.go) | `/_overcast/lambda/url-invoke/{urlId}/*` |
| `/_cognito/{poolId}/…` — 23 routes (`oauth2/{authorize,token,userInfo,revoke}`, `login`, `logout`, `signup`, `confirm`, `new-password`, `mfa`, `forgot-password`, `reset-password`, `branding`, `debug/token`, `users/{username}/password`) | [cognito/service.go:103](../../internal/services/cognito/service.go) | `/_overcast/cognito/user-pools/{poolId}/…` — see §4.1 |

The corresponding rewrite sites are
[apigateway/handler_host_execute.go:50](../../internal/services/apigateway/handler_host_execute.go),
[appsync/handler_host_execute.go:39,50](../../internal/services/appsync/handler_host_execute.go),
[cloudfront/handler_proxy.go:623](../../internal/services/cloudfront/handler_proxy.go),
[elbv2/handler_proxy.go:48,81](../../internal/services/elbv2/handler_proxy.go),
[lambda/handler_url.go:412](../../internal/services/lambda/handler_url.go) and
[cognito/handler_managed_login.go:254,1341,1770](../../internal/services/cognito/handler_managed_login.go).
Each must move in the same commit as the route it rewrites to.

### 2.3 Service-owned admin and observability

| Current | Target |
| --- | --- |
| `/_lambda/instances`, `/_lambda/runtimes`, `/_lambda/layers/{layerName}/versions/{versionNumber}/metadata` | `/_overcast/lambda/…` |
| `/_ecs/tasks/{taskArn}/logs/{container}`, `/_ecs/clusters/{cluster}/tasks` | `/_overcast/ecs/…` |
| `/_rds/instances/{instanceId}/logs` | `/_overcast/rds/…` |
| `/_overcast/cognito/user-pools/{poolId}/import-users` | unchanged |
| `/_overcast/ses/identities` (×3) | unchanged |
| `/_overcast/secretsmanager/secrets…` (×6) | unchanged |
| `/_overcast/sns/topics…` (×2) | unchanged |
| `/_overcast/pipes/{wiring,deliveries}` | unchanged |
| `/_overcast/eventbridge/{deliveries,rule-targets}` | unchanged |

Nine of the fifteen admin route groups are already in the right place. The
shape they established — `/_overcast/<service>/<resource>` — is the shape §4
generalises to everything.

## 3. Non-`/_` paths: the part you asked me to check

You were right that this is nearly a closed set, but not entirely. Walking
every route the router registers and subtracting the manifest-modeled bindings
leaves exactly five things.

**Real violations of the rule:**

1. **`/{region}/{poolId}/.well-known/jwks.json`** and
   **`/{region}/{poolId}/.well-known/openid-configuration`**
   ([cognito/service.go:94](../../internal/services/cognito/service.go)).
   AWS serves these at `https://cognito-idp.{region}.amazonaws.com/{poolId}/.well-known/jwks.json`
   — **no region segment in the path**. The leading `{region}` is an Overcast
   invention with no manifest backing, and because it is a required segment,
   the AWS-shaped path is *not served at all*. Any JWT library pointed at a
   real-looking issuer URL gets a 404 that S3 answers. This is both a
   namespace violation and an AWS-fidelity gap. **Decided: serve the AWS
   shape** — see §4.3.

2. **`/restapis/{restApiId}/{stageName}/_user_request_/*`**
   ([apigateway/service.go:186](../../internal/services/apigateway/service.go)).
   A LocalStack-compatible execute-api path, non-canonical but nested inside a
   modeled prefix. **Decided: permanent named exception.** Its only purpose is
   byte-identical compatibility with a URL LocalStack documents; moving it
   deletes the feature rather than relocating it. Overcast already serves the
   same capability at the namespaced `/_overcast/apigateway/execute-api/…` for
   host-addressed callers, so the exception costs nothing. Its
   `nonManifestRoutes` reason string must say all of that, because the `_` in
   the middle of the path is exactly what a future reader will try to "fix".

**Not violations, recorded so the gate does not re-litigate them:**

3. **`GET /favicon.ico`** ([router.go:195](../../internal/router/router.go)) —
   204, to suppress browser noise. The browser chooses this path; we cannot
   move it. Named exception.

4. **`POST /`**, **`GET /`**, **`/*`**, **`POST /service/{service}/operation/{operation}`**
   ([router.go:982–988](../../internal/router/router.go)) — the `awsQuery`,
   `awsJson`, S3 and Smithy RPC v2 protocol bindings. Canonical by
   construction: they are protocol roots, not operation URIs, so they never
   appear as a manifest `URI` and must be allowlisted explicitly.

5. **`/{accountID:[0-9]+}/…` (SQS path-style queue URLs)** — a real AWS URL
   shape that the manifest expresses as a queue *endpoint*, not an operation
   `URI`. Canonical; allowlisted with that reason.

**Out of scope, stated so the boundary is explicit:** the console BFF's
`/api/*` surface ([bff.go:123](../../internal/bff/bff.go)) is served on the
web listener, not the AWS API listener. Different origin, different port,
different contract — the rule does not reach it.

`overcast-mcp`'s own `/_health`
([cmd/overcast-mcp/main.go:40](../../cmd/overcast-mcp/main.go)) is a separate
binary on a separate listener, so it has no collision to avoid. **Decided:
move it to `/_overcast/health` anyway.** One convention beats two, and the
cost is a one-line change plus its probe. Rides along in phase 2.

### 3.1 Bug found while auditing the predicates

`shouldBypassIAM` ([iam_enforce.go:1401](../../internal/middleware/iam_enforce.go))
bypasses IAM enforcement for `/_*` **and** for `/api` + `/api/`. That second
arm was written for the console BFF — but on the AWS listener, `/api/v2/clusters`
is **MSK's modeled v2 cluster API** ([msk/service.go:330](../../internal/services/msk/service.go),
landed in #894). Every MSK v2 operation currently skips IAM enforcement
entirely.

This predates the namespace work and is independently fixable — the `/api`
arm simply does not belong on this listener — but it is the clearest possible
argument for the gate in §5: a prefix bypass written against a path space
nobody owned became a security hole the moment AWS modeled a service there.
**Recommend fixing it first, as a standalone PR with its own test**, rather
than folding it into phase 1.

## 4. Design decisions

### 4.1 The Cognito collision, and why the shape is `/_overcast/<service>/<resource>/…`

Moving `/_cognito/{poolId}/…` naively to `/_overcast/cognito/{poolId}/…`
collides with the existing `/_overcast/cognito/user-pools/{poolId}/import-users`.
chi resolves it (static segments beat wildcards) and no real pool ID is
`user-pools`, so nothing breaks — but a namespace whose correctness depends on
a routing tie-break is not a namespace worth enforcing.

**Decision: `/_overcast/<service>/<resource-collection>/{id}/…`.** Cognito's
managed login moves to `/_overcast/cognito/user-pools/{poolId}/…`, which is
both collision-free and the same shape as the admin route already there.
Applied consistently:

- `/_overcast/lambda/instances`, `/_overcast/lambda/runtimes`,
  `/_overcast/lambda/functions/{name}/url-invoke/*`
- `/_overcast/ecs/clusters/{cluster}/tasks`,
  `/_overcast/ecs/tasks/{taskArn}/logs/{container}`
- `/_overcast/rds/instances/{instanceId}/logs`
- `/_overcast/appsync/apis/{apiId}/graphql`
- `/_overcast/cloudfront/distributions/{distId}/*`

The alternative — a kind-based split like `/_overcast/admin/…` vs
`/_overcast/x/…` — was rejected: it adds a segment that carries no routing
information and forces a judgement call ("is a task-log endpoint admin or data
plane?") on every new route.

### 4.2 `/_overcast/inbox` → `/_overcast/ses/inbox`

`internalService` already attributes `/_overcast/inbox` to SES
([logger.go:336](../../internal/middleware/logger.go)); the path is the only
thing that disagrees. Moving it is what lets that whole function delete.

### 4.3 Cognito discovery documents

**Decided: serve the AWS shape.**

`GET /{poolId}/.well-known/jwks.json` and
`GET /{poolId}/.well-known/openid-configuration`, matching
`https://cognito-idp.{region}.amazonaws.com/{poolId}/.well-known/…` exactly.
Both go in `nonManifestRoutes` as canonical-by-shape: AWS models no SDK
operation for OIDC discovery, so no manifest row can ever cover them, and the
gate must not read that absence as an invention.

**The region-prefixed `/{region}/{poolId}/.well-known/…` is deleted, not
relocated.** It carries no information: a Cognito pool ID is `{region}_{suffix}`,
so the pool ID already names its own region — which is precisely how
`poolRegionMiddleware` ([cognito/service.go:103](../../internal/services/cognito/service.go))
resolves the region for the whole managed-login subtree today. The two
discovery handlers switch to that same middleware and read the region from the
pool ID instead of from a path segment. Keeping a relocated duplicate would
add an `/_overcast/` route whose only distinction from the canonical one is a
segment that restates the segment beside it.

Flagging the reversal cost, since this is a deletion rather than a move: if
some caller does depend on the region-prefixed form, restoring it is a
three-line route registration under
`/_overcast/cognito/user-pools/{poolId}/.well-known/…`. Nothing in this plan
forecloses it.

Registering `/{poolId}/.well-known/*` does put two label-rooted routes at the
top level — this and SQS's `/{accountID:[0-9]+}/…`. They do not overlap
(`.well-known` is a literal second segment, and a pool ID is not all-digits),
but phase 6 needs a routing test that says so, not just a chi tie-break that
happens to work.

### 4.4 No aliases, no deprecation window

Alpha, and you said so. A redirect layer would have to live in the same three
predicates this change exists to delete. Clean break, one breaking-change
CHANGELOG fragment.

## 5. Enforcement — the point of the exercise

### Gate 9: every registered route is modeled or namespaced

New: `internal/router/pathnamespace_dev_test.go` (dev build tag, so it can use
`walkRegisteredRoutes` from [routeinventory_dev.go](../../internal/router/routeinventory_dev.go)
and see the dispatch-mounted sub-routers too). For every registered route:

1. starts with `/_overcast/` → pass;
2. matches a `URI` in `awsapi.Operations` for a service Overcast serves → pass;
3. appears in `nonManifestRoutes`, a map of pattern → reason → pass;
4. otherwise → **fail**, naming the pattern and the two ways to fix it.

`nonManifestRoutes` ends up holding exactly five groups, each carrying its
reason as a string, not a comment — so `go test` output explains itself:

| Entry | Reason recorded |
| --- | --- |
| `/favicon.ico` | browser-chosen path; we do not control it |
| `POST /`, `GET /`, `/*`, `POST /service/{service}/operation/{operation}` | protocol roots (`awsQuery`, `awsJson`, S3, Smithy RPC v2) — never a manifest `URI` |
| `/{accountID:[0-9]+}/…` | SQS path-style queue URL: a real AWS shape the manifest expresses as an endpoint, not an operation |
| `/{poolId}/.well-known/{jwks.json,openid-configuration}` | AWS-shaped OIDC discovery; AWS models no SDK operation for it (§4.3) |
| `/restapis/{restApiId}/{stageName}/_user_request_/*` | LocalStack URL compatibility; the mid-path `_` is deliberate (§3, violation 2) |

It is a ratchet in the same sense as `unservedBindings` and
`protocolAsymmetries`: adding an entry is legal and reviewable, and the review
question is always "why is this not `/_overcast/`?"

This runs route → model. `modelbinding_dev_test.go` runs model → route. Neither
implies the other, and only this direction catches an invented path.

Wire it into `make aws-models-check` alongside the existing
`go test -tags dev ./internal/router`, and add row 9 to
[manifest-enforcement.md](./manifest-enforcement.md) §"What is enforced now".

### What the move lets us delete

| Today | After |
| --- | --- |
| `internalService`, a 9-arm switch ([logger.go:320](../../internal/middleware/logger.go)) | `strings.Split(path, "/")[2]` |
| `detectService`'s internal arms | one `/_overcast/` arm |
| 16 `/_*` entries in `registeredRouteClassification` | 1 |
| `HasPrefix(path, "/_")` in `notready.go`, `iam_enforce.go`, `logger.go` (×2) | `HasPrefix(path, router.InternalPrefix)` |
| `internalPaths` map + `isInternalPath`'s special cases ([trace.go:572](../../internal/trace/trace.go)) | one prefix test |

A single exported `router.InternalPrefix = "/_overcast/"` replaces every
literal. That constant is the enforceable version of the rule.

### Where the rule gets written down

- `CONTRIBUTING.md` — endpoint checklist, as a hard requirement.
- `AGENTS.md` — the guardrail list.
- `.agents/skills/new-feature/SKILL.md` and `.agents/skills/code-review/SKILL.md`.

## 6. Blast radius, measured

| Surface | Files | Notes |
| --- | --- | --- |
| Go non-test | ~88 | includes doc-comment mentions; ~30 carry a real literal |
| Go tests | 44 | 344 occurrences |
| `web/src`, `web/api/src` | 34 | see below |
| `compat/ui/src` | 2 | `use-overcast-events.ts`, `server-log-panel.tsx` |
| `docs/`, root `*.md`, `.agents/` | 38 | incl. `migration-from-localstack.md`, `networking.md`, `https.md`, `debugging.md` |
| Container healthchecks | 4 | `Dockerfile:231`, `docker-compose.yml:27`, `docker-compose.dev.yml:124`, `compat/docker-compose.yml` |

The web split matters for sequencing. `web/api/src/routes/*.ts` and
`internal/bff/bff.go` are **proxies**: their own `/api/*` surface is unchanged,
only the upstream target strings move — 12 files, mechanical. But
`web/src/services/`, `web/src/features/metrics/`, `web/src/features/debug/`
and `web/src/hooks/use-{health,metrics}.ts` call the daemon **directly**, so
the SPA breaks the instant the daemon moves. Web and Go must land in the same
commit, per phase.

Also in the blast radius, outside the repo: any user's `docker-compose.yml`
healthcheck, `curl http://localhost:4566/_health` in a script, and any Cognito
app config holding a `/_cognito/{poolId}/oauth2/…` redirect URI. That last one
is why phase 4 is separate and loud.

## 7. Phasing

One PR per phase; Go, web, docs and tests in the same commit.

| # | Content | Why here |
| --- | --- | --- |
| **0** | Fix the `/api` IAM bypass (§3.1). Standalone, failing test first. | Independent of the move, and a live hole. |
| **1** | Gate 9 (§5) landed **with every current violation in `nonManifestRoutes`**, plus `router.InternalPrefix`. No routes move. | The rule is enforced from day one; every later phase shrinks the ratchet instead of racing it. |
| **2** | Router roots: `/_health`, `/_metrics`, `/_topology`, `/_/info`, `/_events`, `/_internal/domains/watch`, plus `overcast-mcp`'s own `/_health`. Predicates, web, 4 healthchecks, `cmd/compat/launch.go`. | Highest-traffic, lowest-risk: no minted URLs. |
| **3** | `/_debug/*` → `/_overcast/debug/*`; `/_mcp` → `/_overcast/mcp`. | Both build-tag-gated; isolated from service code. |
| **4** | Service admin: `/_lambda/*`, `/_ecs/*`, `/_rds/*`, and `/_overcast/inbox` → `/_overcast/ses/inbox`. | Console-only consumers. |
| **5** | Service data plane: `/_apigateway`, `/_appsync`, `/_cloudfront`, `/_elb`, `/_lambda/url-invoke`, `/_cognito`. Rewrite sites, URL-minting code, `docs/networking.md`. | The only phase that changes URLs Overcast hands to callers. |
| **6** | Cognito discovery to the AWS shape (§4.3), with the top-level label-route overlap test. Delete `internalService`, collapse `detectService`, empty the ratchet down to the five allowlist groups, update `manifest-enforcement.md`. | The payoff. |

Phase 1 is the one that must land; 2–6 are then individually revertible.

Phase 6 is the only phase that changes behaviour rather than naming — it makes
a URL work that does not work today. It is last because the gate and the
namespace should be settled before AWS fidelity moves underneath them, and
because it is the phase most worth reverting alone if it goes wrong.

## 8. Decisions on record

| Question | Decision |
| --- | --- |
| Where does the `_` go? | Once, at the front. `/_overcast/health`, not `/_overcast/_health` (§1). |
| Cognito OIDC discovery | Serve the AWS shape `/{poolId}/.well-known/…`; delete the region-prefixed form rather than relocating it (§4.3). |
| `overcast-mcp`'s `/_health` | Move to `/_overcast/health` — one convention, not two (§3). |
| `_user_request_` | Permanent documented exception, with the reason in the allowlist string (§3, violation 2). |
| Aliases / deprecation window | None. Alpha; clean break, one breaking-change fragment (§4.4). |

No open questions remain. The next action is phase 0 — the `/api` IAM bypass
(§3.1) — which is independent of everything else here.
