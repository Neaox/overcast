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
| `/_lambda/url-invoke/{urlId}/*` | [lambda/service.go:1343](../../internal/services/lambda/service.go) | `/_overcast/lambda/url-invoke/{urlId}/*` |
| `/_cognito/{poolId}/…` — 23 routes (`oauth2/{authorize,token,userInfo,revoke}`, `login`, `logout`, `signup`, `confirm`, `new-password`, `mfa`, `forgot-password`, `reset-password`, `branding`, `debug/token`, `users/{username}/password`) | [cognito/service.go:103](../../internal/services/cognito/service.go) | `/_overcast/cognito/user-pools/{poolId}/…` — see §4.1 |

The corresponding rewrite sites are
[apigateway/handler_host_execute.go:50](../../internal/services/apigateway/handler_host_execute.go),
[appsync/handler_host_execute.go:39,50](../../internal/services/appsync/handler_host_execute.go),
[cloudfront/handler_proxy.go:623](../../internal/services/cloudfront/handler_proxy.go),
[elbv2/handler_proxy.go:48,81](../../internal/services/elbv2/handler_proxy.go),
[lambda/handler_url.go:404](../../internal/services/lambda/handler_url.go) and
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

### 3.1 Bug found while auditing the predicates — **fixed, phase 0**

`shouldBypassIAM` ([iam_enforce.go](../../internal/middleware/iam_enforce.go))
bypassed IAM enforcement for `/_*` **and** for `/api` + `/api/`. That second
arm was written for the console BFF, which has never passed through this
middleware: `IAMEnforce` is wired once, on the AWS mux
([router.go:169](../../internal/router/router.go)), and the UI is a separate
listener with its own handler ([cmd_serve.go:264](../../cmd/overcast/cmd_serve.go)).

It exempted two real things, and the second is the one the audit went looking
for:

- **An S3 bucket named `api`.** Three characters, all lowercase — a legal
  bucket name. With `OVERCAST_ENFORCE_IAM=true`, every object request against
  it reached the S3 fallback with no policy evaluated. This shipped in every
  release that has had IAM enforcement.
- **MSK's v2 cluster API**, once #894 bound it to `/api/v2/clusters`
  ([msk/service.go:330](../../internal/services/msk/service.go)) — the path the
  pinned kafka model gives it. `CreateClusterV2`, `ListClustersV2` and
  `DescribeClusterV2`: unsigned, unauthorized, 200. Unreleased at the time of
  writing; its fragment is still unconsumed in `.changelog/`.

Both are the same arm, and together they sharpen the argument for the gate in
§5 past what the MSK half alone showed. A prefix bypass written against path
space nobody owned was **already wrong before AWS modeled anything there** —
S3's fallback owns everything unclaimed, so an unreserved prefix is not empty,
it is S3's. `/_` is safe only because S3 bucket names cannot begin with an
underscore. Adding a prefix to this predicate therefore asserts two things: AWS
models no service under it, and S3 will not accept it as a bucket name.

Fixed as phase 0, standalone, failing test first — both halves covered, the
released one first.

#### What phase 0 does *not* fix

Removing the bypass closes the hole it opened: an unsigned request to any
`/api` path is now refused, because `isSignedIAMRequest` runs ahead of action
inference. The S3 half is enforced end to end — `requestIAMAction` names
`s3:GetObject`/`s3:PutObject` for a bucket named `api`, so signed requests are
evaluated too.

MSK was weaker, for reasons that predate this work and were unchanged by it.
Measured against `restOperation` at the time phase 0 landed:

| Request | Inferred action |
| --- | --- |
| `GET`/`POST /api/v2/clusters` | `msk:ListClustersV2` / `msk:CreateClusterV2` |
| `GET /api/v2/clusters/{arn}` (`DescribeClusterV2`) | none |
| `GET /v1/clusters` under an SDK's `kafka` credential scope | none |

Three separate defects sat behind that, none of them a namespace problem. All
are recorded here because the audit is what surfaced them.

1. **`kafka` vs `msk` — fixed separately, see below.** The generated registry is
   keyed `msk`, so `restOperation("kafka", …)` returned nothing — and
   `detectService` answered `kafka` for any MSK path the prefix switch does not
   claim, because that is what the SigV4 credential scope carries. Separately,
   the action Overcast *did* infer was `msk:ListClustersV2`, while AWS's real
   IAM prefix for MSK is `kafka:`, so a correctly-written policy would not have
   matched it.
2. **Fail-open on an unnamed operation — still open, and now much narrower.**
   `IAMEnforce` passes a signed request through when `requestIAMAction` returns
   `""` — deliberate, documented, and the right default for S3's sub-resource
   operations. With defect 1 fixed, MSK's v1 surface is named and policy-checked;
   what still reaches this branch is any request whose operation cannot be named
   at all, which is defect 3 for MSK and the S3 sub-resources everywhere else.
3. **A non-greedy URI label cannot hold an ARN — still open.** This is what the
   `DescribeClusterV2` row above measures, and it is unrelated to the other two.
   The model binds it to `/api/v2/clusters/{ClusterArn}`, a non-greedy label, but
   an MSK cluster ARN contains `/`. The SDK percent-encodes them; Go decodes
   `r.URL.Path`, so by the time `restOperation` sees the path the ARN has become
   three extra segments and the label cannot match. Passing `r.URL.EscapedPath()`
   matches, verified:

   ```
   /api/v2/clusters/arn:aws:kafka:…:cluster/demo/uuid-1        -> ""
   /api/v2/clusters/arn%3Aaws%3Akafka%3A…%3Acluster%2Fdemo%2Fuuid-1 -> "DescribeClusterV2"
   ```

   It affects every modeled binding whose non-greedy label takes an ARN, not
   only MSK's — the same class `isBedrockRuntimeInferencePath` already works
   around in `detectService` — so the fix belongs in `restOperation` and needs
   checking against every service, which is why it is not folded in here.

#### Update: defect 1 fixed

Both halves of `kafka` vs `msk` are fixed, along with the same defect in five
other services the original note did not reach. `detectService` step 3 now maps
the credential scope's signing name to an Overcast service key, and
`requestIAMAction` builds the action from AWS's IAM action prefix rather than
from Overcast's key. Both mappings are in
[`internal/middleware/serviceidentity.go`](../../internal/middleware/serviceidentity.go);
they are deliberately two mappings rather than one inverted, because CloudWatch
signs as `monitoring` and authorizes as `cloudwatch:`.

The audit's own framing turned out to understate it. The scope-to-key mismatch
was live for AppRegistry's `/attribute-groups` as well as MSK's v1 surface, and
for the two Query-protocol services whose signing name differs — CloudWatch and
ELBv2 — where it cost them their resource resolvers rather than their action.
The action-prefix half was live for ten services.

The measured table above now reads:

| Request | Inferred action |
| --- | --- |
| `GET`/`POST /api/v2/clusters` | `kafka:ListClustersV2` / `kafka:CreateClusterV2` |
| `GET /api/v2/clusters/{arn}` (`DescribeClusterV2`) | none — defect 3 |
| `GET /v1/clusters` under an SDK's `kafka` credential scope | `kafka:ListClusters` |

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
([logger.go:354](../../internal/middleware/logger.go)); the path is the only
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
| `internalService`, a 9-arm switch ([logger.go:338](../../internal/middleware/logger.go)) | `strings.Split(path, "/")[2]` |
| `detectService`'s internal arms | one `/_overcast/` arm |
| 16 `/_*` entries in `registeredRouteClassification` | 1 |
| `HasPrefix(path, "/_")` in `notready.go`, `iam_enforce.go`, and `logger.go` (×2, the `internalService` attribution pair at :216 and :451) | `HasPrefix(path, router.InternalPrefix)` |

A single exported `router.InternalPrefix = "/_overcast/"` replaces those
literals. That constant is the enforceable version of the rule.

Note which `logger.go` sites those are. The two in the row above ask *"who owns
this path"*; `isOperationalPollPath`, in the same file, asks *"was this polled
or did a client do it"* — a different question with a different answer, and the
next section is about not confusing them.

### What the move must NOT delete — the two allowlists

**An earlier draft of this plan listed `trace.internalPaths` and
`isInternalPath`'s special cases here, to be replaced by "one prefix test".
That was wrong, and it would have been a silent regression.**

`/_` currently does double duty. It means *"a path Overcast invented"* — the
thing this plan collapses — and it is also the accidental carrier of a second,
unrelated distinction: *"not a real client's request"*. Those are different
sets, and only the first is about naming.

Two predicates depend on the second, and neither is a prefix test:

| Predicate | Shape today | Decides |
| --- | --- | --- |
| [`trace.isInternalPath`](../../internal/trace/trace.go) | allowlist: 8 exact paths + `/_debug/*` | whether a request spends user-trace ring-buffer budget, and whether it shows in the trace UI |
| [`middleware.isOperationalPollPath`](../../internal/middleware/logger.go) | allowlist: `/_health` + `/_debug*` | whether the request logs at TRACE instead of INFO |

Everything **not** in those allowlists is treated as a real client's request —
which today silently includes the whole emulated data plane, because those
routes happen to sit on first segments (`/_appsync`, `/_apigateway`,
`/_cognito`, `/_cloudfront`, `/_elb`, `/_lambda/url-invoke`) that nobody added
to an allowlist. An AppSync GraphQL call, an API Gateway invoke, a Lambda
function URL request and a Cognito hosted-UI login are all requests a user
made and expects to find in the trace list.

Move every one of them to `/_overcast/…` and collapse the predicate to a
prefix test, and all of them are reclassified as internal noise: no error, no
failing build — they just stop appearing in the trace UI, and the first
symptom is somebody unable to find a request they know they made.

**Guard landed ahead of the move**
(`TestIsInternalPathSeparatesPollingFromClientTraffic`,
`TestIsOperationalPollPath`): both allowlists now pin the data-plane paths as
client traffic at their current spellings, and the second test is new —
`isOperationalPollPath` had no coverage at all. Verified to discriminate by
collapsing `isInternalPath` to a prefix test, which fails all eight data-plane
cases. Every phase must keep them passing at whatever path the routes land on.

### How the distinction gets carried afterwards — decide before phase 5

Three options, to settle before the data-plane routes move:

1. **Re-root the allowlists.** Zero behaviour change, smallest diff, keeps two
   hand-maintained lists that drift — a new data-plane route is misclassified
   by omission, which is how this got fragile in the first place.
2. **Sub-namespace by kind**, `/_overcast/x/…` for data plane. Restores a
   path-shaped signal so every predicate stays a prefix test, but puts a
   cryptic segment into user-facing URLs (Cognito hosted UI, function URLs)
   and revives the objection in §4.1.
3. **Mark it on the route, not in the path** — *recommended*. "Is this a real
   client's request?" is a property of the route, not of its spelling, and the
   codebase already works this way in one place:
   [logger.go:189 and :428](../../internal/middleware/logger.go) consult
   `HostClaimFromContext(…).Kind == HostClaimHostRoute` to spot host-routed
   data-plane traffic and refuse to name an AWS operation for it. Generalise
   that to a marker stamped by the data-plane route groups at registration, and
   the predicates stop reading paths altogether. It also covers the case the
   host claim misses today: reaching the same route directly by path rather
   than through host addressing.

Option 3 additionally makes it possible to tighten two predicates that are
conflated *today*, independent of this plan: `notready.go` and `iam_enforce.go`
exempt `/_appsync/{id}/graphql` from the storage-migrating gate and from IAM
enforcement purely because it starts with `/_`. Whether that is right is a
design question this plan does not settle — but after the move it should be a
decision, not an inheritance.

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
| **0** | ✅ **Done.** Fix the `/api` IAM bypass (§3.1). Standalone, failing test first. | Independent of the move, and a live hole. |
| **0b** | ✅ **Done.** Pin the internal-vs-client-traffic classification in both allowlists, data-plane paths included (§5). No routes move. | The one regression in this plan that fails silently. Cheaper to write down before the move than to diagnose after it. |
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

No open questions remain. Phase 0 (the `/api` IAM bypass, §3.1) is done; the
next action is phase 1, landing gate 9 with today's violations pre-loaded in
`nonManifestRoutes` so no route moves before the rule is enforced.
