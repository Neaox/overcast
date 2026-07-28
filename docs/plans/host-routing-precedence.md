# Host-addressing precedence — S3 virtual-hosted vs. host-routed services

> Status: in progress. Branch `claude/api-gateway-url-routing-4050e7`.
> Scope: `internal/middleware/` (host classification), `internal/router/router.go`
> (middleware wiring), `internal/services/s3/` (reserved-label diagnostic),
> `docs/networking.md`.
> Related: [AWS API operation coverage](./aws-api-operation-coverage.md) — the
> reserved-label set converges on that plan's generated manifest (see §6).

## 1. Problem

Every Host-routed invoke URL that uses a base Overcast can actually resolve —
`localhost`, `localhost.overcast.sh`, `localhost.localstack.cloud` — is broken.
Only `.amazonaws.com` works, and that base does not resolve to the emulator
without a hosts-file entry.

Verified end-to-end against `helpers.NewTestServer`, REST v1 mock integration
deployed to stage `test`, `GET /test/hello`:

| Host | Status |
| --- | --- |
| `{id}.execute-api.us-east-1.amazonaws.com` | 200 `{"message":"hello world"}` |
| `{id}.execute-api.us-east-1.localhost.overcast.sh:4566` | 403 `Missing Authentication Token` |
| `{id}.execute-api.localhost.overcast.sh:4566` | 403 `Missing Authentication Token` |
| `{id}.execute-api.us-east-1.localhost:4566` | 403 `Missing Authentication Token` |
| `{id}.execute-api.localhost:4566` | 403 `Missing Authentication Token` |
| `{id}.execute-api.us-east-1.localhost.localstack.cloud:4566` | 403 `Missing Authentication Token` |

AppSync and Lambda function URLs fail identically. Lambda is the sharpest case:
`buildFunctionURL` ([internal/services/lambda/handler_url.go](../../internal/services/lambda/handler_url.go))
correctly mints `http://{urlId}.lambda-url.{region}.{OVERCAST_HOSTNAME}:4566/`,
so Overcast hands back a URL it then refuses to serve. That exact URL is the
worked example in [networking.md](../networking.md).

## 2. Root cause

Two middlewares both claim the same request.

`S3VirtualHostFor` runs at [router.go:117](../../internal/router/router.go),
`HostDispatch` at [router.go:124](../../internal/router/router.go).
`defaultVirtualHostBases` is unconditionally `localhost`,
`localhost.overcast.sh`, `localhost.localstack.cloud`, and
`extractS3BucketFromHost` **suffix**-matches, taking everything before the base
as the bucket:

```go
if strings.HasSuffix(hostname, suffix) {
    candidate := strings.TrimSuffix(hostname, suffix)
```

So `abc123.execute-api.us-east-1.localhost` yields bucket
`abc123.execute-api.us-east-1`, which is prepended to the path. `HostDispatch`
then prepends its own marker prefix on top of the already-corrupted path:

```
/_apigateway/execute-api/abc123/us-east-1/abc123.execute-api.us-east-1/test/hello
```

`dispatchHostRestAPI` reads the first segment as the stage name, finds no such
stage, and returns 403. The path is mangled before dispatch ever runs.

CI stayed green because every host-route test uses an `.amazonaws.com` base —
the only base with no S3 collision. See
[apigateway_test.go:3186](../../tests/integration/apigateway/apigateway_test.go),
[appsync_test.go:6437](../../tests/integration/appsync/appsync_test.go),
[lambda_test.go:5399](../../tests/integration/lambda/lambda_test.go),
[router/hostroute_test.go:30](../../tests/integration/router/hostroute_test.go).

## 3. How other emulators avoid this

| | bare `{bucket}.{base}` | host-routed services on the same base |
| --- | --- | --- |
| Real AWS | never — no such form exists | n/a, separate DNS zones per service |
| LocalStack | rejected; `s3.` label mandatory, else path-style | yes |
| floci | supported | yes |
| Overcast | supported | yes |

LocalStack sidesteps it by requiring the `s3.` label. floci supports the bare
form but splits at the **first** dot and requires the remainder to **exactly
match** a known base
([`S3VirtualHostFilter.extractBucket`](https://github.com/floci-io/floci/blob/main/src/main/java/io/github/hectorvent/floci/services/s3/S3VirtualHostFilter.java)):

```java
String firstLabel = hostname.substring(0, firstDot);
String remainder  = hostname.substring(firstDot + 1);
if (baseHostname != null && matchesEndpointHost(remainder, baseHostname)) {
    return firstLabel;
}
```

Exact-remainder vs. suffix is the whole difference. Under floci's rule
`abc123.execute-api.us-east-1.localhost` has remainder
`execute-api.us-east-1.localhost`, which matches no base, so S3 never claims it.

The price floci pays: its bucket is always exactly one label, so dotted bucket
names are not addressable virtual-hosted style at all. Overcast supports them
today ([s3_test.go:2770](../../tests/integration/s3/s3_test.go)) and — because
it can veto on reserved labels rather than restricting bucket shape — keeps
supporting them in **both** the labelled and the bare form. That is the one
place this design is deliberately ahead of both peers rather than level with
them; see §4.

## 4. Precedence rule

Precedence derives from the host grammar, evaluated once, **not** from
middleware registration order. Registration order is invisible action-at-a-
distance and is exactly what shipped this bug.

| Tier | Form | Claim |
| --- | --- | --- |
| A | `{bucket}.s3.{...}` / `{bucket}.s3-{region}.{...}` | S3. Bucket is everything before the **first** `.s3.` / `.s3-`. |
| B | `{bucket}.{base}` for a recognised base, **unless** the candidate in front of the base carries a reserved host label at segment index ≥ 1 | S3, bucket = everything before the base. The veto defers host-routed shapes to C. |
| C | `{id}.{label}[.{region}].{...}` for a registered host-route label at index ≥ 1 | The owning service. |
| — | anything else | unclaimed → S3 path-style, per AGENTS.md "Routing fallthrough is S3". |

Recognised bases for tier B: `localhost`, `localhost.overcast.sh`,
`localhost.localstack.cloud`, plus `OVERCAST_HOSTNAME` when set, matched
longest-first so a configured parent domain never shadows a longer default.

Tier B is a local-only convenience in the sense that real AWS has no bare
form — but it is the form AWS SDKs actually emit against an endpoint override
with virtual-hosted addressing, and the only form CDK's asset publisher uses
(it ignores `forcePathStyle`). It has to keep working, dotted bucket names
included.

**The disambiguator is the reserved-label veto, not a one-label restriction.**
An earlier draft copied floci's rule — bucket = first label only, remainder
must exactly equal a base — which does remove the ambiguity, but at the cost of
dotted bucket names in this form. floci has to be that strict because it has no
notion of reserved labels; Overcast does, so it can suffix-match and simply
decline candidates that look host-routed. That keeps the capability LocalStack
and floci both lack, and shrinks the residual limitation to only those bucket
names carrying a reserved label at segment index ≥ 1.

A must precede C so a dotted bucket addressed the explicit way wins:
`my.execute-api.s3.localhost` is bucket `my.execute-api`, not API Gateway.
B must precede C so an operator-configured base wins over a generic label
match: with `OVERCAST_HOSTNAME=execute-api.mycorp.com`,
`mybucket.execute-api.mycorp.com` is bucket `mybucket`.

### Verified classification matrix

Confirmed by probe against both the current and proposed rules:

| Host | Current | Proposed |
| --- | --- | --- |
| `mybucket.localhost:4566` | s3:mybucket | s3:mybucket |
| `cdk-…-ap-southeast-2.localhost.localstack.cloud:4566` | s3:cdk-… | s3:cdk-… |
| `mybucket.s3.us-east-1.localhost` | s3:mybucket | s3:mybucket |
| `legacy-dash-bucket.s3-us-west-2.localhost` | s3:legacy-dash-bucket | s3:legacy-dash-bucket |
| `my.dotted.bucket.s3.localhost` | s3:my.dotted.bucket | s3:my.dotted.bucket |
| `my.dotted.bucket.localhost` | s3:my.dotted.bucket | s3:my.dotted.bucket |
| `execute-api.thing.localhost` | s3:execute-api.thing | s3:execute-api.thing |
| `s3.localhost` | path-style | path-style |
| `weird.s3x.host` | path-style | path-style |
| `execute-api.localhost:4566` | s3:execute-api | s3:execute-api |
| `execute-api.s3.localhost` | s3:execute-api | s3:execute-api |
| **`my.execute-api.localhost`** | BOTH | hostroute id=`my` |
| `my.execute-api.s3.localhost` | BOTH | s3:my.execute-api |
| `abc123.execute-api.us-east-1.localhost.overcast.sh:4566` | BOTH | hostroute id=`abc123` |
| `abc123.execute-api.localhost:4566` | BOTH | hostroute id=`abc123` |
| `urlid.lambda-url.us-east-1.localhost.overcast.sh:4566` | BOTH | hostroute id=`urlid` |
| `myapi.appsync-api.us-east-1.localhost:4566` | BOTH | hostroute id=`myapi` |
| `mybucket.execute-api.mycorp.com` (ov=`execute-api.mycorp.com`) | BOTH | s3:mybucket |
| `abc.execute-api.us-east-1.execute-api.mycorp.com` (same ov) | BOTH | hostroute id=`abc` |

`BOTH` is the double-claim bug: S3 rewrites the path, then the host-route
middleware rewrites the already-corrupted result.

## 5. Design

One classifier, one middleware. `S3VirtualHostFor` and `HostDispatch` are
replaced by a single `HostAddressing` middleware, because S3-vs-service is one
decision, not two:

```go
type HostClaimKind int

const (
    HostClaimNone HostClaimKind = iota
    HostClaimS3
    HostClaimHostRoute
)

type HostClaim struct {
    Kind   HostClaimKind
    Bucket string         // set when Kind == HostClaimS3
    Route  HostRouteMatch // set when Kind == HostClaimHostRoute
}

func ClassifyHost(host, configuredHostname string) HostClaim
func HostAddressing(configuredHostname string, rows *[]HostRouteRow, logger *zap.Logger) func(http.Handler) http.Handler
```

`HostAddressing` classifies once, stamps the claim into the request context,
and applies the matching rewrite. Consequences:

- Ordering cannot cause a double claim; the two rewrites are mutually exclusive
  branches of one switch.
- `detectService` ([logger.go:103](../../internal/middleware/logger.go)) reads
  the stamped claim instead of re-deriving from `HostRouteService`, so the log
  label is guaranteed to match what actually happened — stronger than today's
  "same table" guarantee, which can still drift for a configured hostname that
  itself contains a registered label.
- `extractS3BucketFromHost` stays as a thin wrapper over `ClassifyHost`, so the
  existing unit tests keep their shape.

## 6. Reserved host labels

The collision only exists for hosts Overcast **dispatches** on. Precision
matters here, and the wording in §7 of the docs must follow it:

- **Reserved for dispatch** = keys of `hostRouteLabels`. Three today:
  `execute-api`, `lambda-url`, `appsync-api`. This is the set that collides,
  and the only set the bucket-name diagnostic uses.
- **Not** the full set of AWS endpoint prefixes. Reserving all ~400 would make
  `my.logs`, `my.events`, `my.states` collide for zero benefit, since Overcast
  does not host-route those.

### Convergence on the generated manifest

Per [aws-api-operation-coverage.md](./aws-api-operation-coverage.md) §11
("No AWS operation may be added outside the manifest"), `hostRouteLabels` must
not stay a free-form hand-maintained list. Target state:

1. `cmd/awsmodelgen` emits a per-service table carrying `EndpointPrefix` from
   the Smithy `aws.api#service` trait. The generator already parses this field
   (see `cmd/awsmodelgen/main_test.go` fixtures); it is simply not retained in
   [`Operation`](../../internal/awsapi/manifest.go). One row per service (~400)
   against 18.8k existing operation rows — negligible.
2. `awsapi.IsEndpointPrefix(label string) bool` exposes it.
3. A test asserts every `hostRouteLabels` key is either a real AWS endpoint
   prefix per the manifest, or an entry in a small documented data-plane
   override table. This is the same guard §4.1 of that plan already blesses for
   `X-Amz-Target` prefixes that Smithy does not encode.

The override table is required because Smithy models the **control plane**.
`execute-api` is API Gateway's real `endpointPrefix`, but `lambda-url` and
`appsync-api` are data-plane host conventions that no Smithy model carries —
and that plan explicitly excludes "service data/runtime endpoints with
intentionally arbitrary user paths (such as API Gateway execution)" from its
scope. Each override entry cites AWS documentation evidence.

Net effect, which is the point: **the label set can only grow when AWS adds a
service or a hostname**, arriving through the A5 model-refresh PR, not through
a contributor inventing a label.

> **Blocked in this branch.** Step 1 needs `make generate-aws-operations
> AWS_MODELS_DIR=…` against an `api-models-aws` checkout at revision
> `66e973cadf6b6e909b200217d0d6065e49445a9a` (see `models/aws/VERSION`). The
> snapshot is deliberately not vendored, and the generator validates the
> revision before writing. Steps 1–3 are therefore a follow-on PR; this branch
> keeps `hostRouteLabels` hand-maintained with the override-evidence comment in
> place, so the follow-on is a pure substitution.

### Scope of the limitation

A reserved label may not appear as the **second-or-later** dot-segment of a
bucket name — not merely the trailing one, since the label only needs to land
at hostname index ≥ 1:

| Bucket name | Bare host | Outcome |
| --- | --- | --- |
| `my.execute-api` | `my.execute-api.localhost` | → API Gateway |
| `my.execute-api.thing` | `my.execute-api.thing.localhost` | → API Gateway |
| `execute-api.thing` | `execute-api.thing.localhost` | → S3 ✅ (index 0) |

A bucket named *exactly* like a service does **not** collide, because
`ParseHostRoute` skips index 0 ([hostroute.go:108](../../internal/middleware/hostroute.go))
— `{id}` must be non-empty in every real AWS host-routed shape. That guard was
written for grammar fidelity and does collision defence for free.

The limitation is scoped to **bare virtual-hosted addressing only**. The bucket
remains fully usable:

| Addressing mode | `my.execute-api` bucket |
| --- | --- |
| Path-style `localhost:4566/my.execute-api/key` | works |
| Labelled vhost `my.execute-api.s3.localhost` | works |
| Bare vhost `my.execute-api.localhost` | claimed by API Gateway |

### The limitation is currently theoretical — CreateBucket rejects all dots

An earlier draft of this plan proposed a warn-once log at `CreateBucket` for
reserved-label names. **Dropped: it could never fire.**

`serviceutil.BucketName` ([validation.go:78](../../internal/serviceutil/validation.go))
restricts bucket names to lowercase letters, numbers and hyphens, so **no
dotted bucket name can be created through Overcast's own API at all** — via S3
directly or via CloudFormation, which dispatches through the same handler. A
name that could collide cannot exist, so there is nothing to warn about.
`TestCreateBucket_dottedNameIsRejected` pins this.

That also means the §4 tier-B veto is currently protecting against a shape no
Overcast-created bucket can take. It is still required: the veto is what makes
host-routed invokes work at all, and a *client* can send any Host it likes
regardless of what buckets exist.

This is itself a deliberate divergence from real AWS, which permits dots in
general-purpose bucket names (discouraged, because they break the
`*.s3.<region>.amazonaws.com` wildcard certificate over HTTPS — which is why
SDKs fall back to path-style for them). It predates this work and is already
called out on
`TestS3VirtualHostedStyle_DottedBucketNameHost_SplitsOnFirstDotS3`
([s3_test.go:2770](../../tests/integration/s3/s3_test.go)). See §13.

If that validator is ever relaxed to match AWS, the reserved-label warning
becomes worth adding, and `BucketNameReservedLabel` is already implemented and
unit-tested for it. Do not add it before then.

## 7. Performance

Host classification runs on **every request**, so this work must not add
per-request cost — and in fact removes a good deal of it.

### Measured baseline (current code)

**What:** ns/op, B/op, allocs/op for one pass of the host-classification work
the middleware chain performs per request.
**How:** `go test -run '^$' -bench BenchmarkBaseline -benchmem -count=3` against
a temporary bench calling `extractS3BucketFromHost(host, "localhost.overcast.sh")`
followed by `ParseHostRoute(host)` — the two calls `S3VirtualHostFor` and
`HostDispatch` make independently today.
**Environment:** `golang:1.24-bookworm` container (`scripts/docker-go.ps1`) on
Windows 11 / Docker Desktop, bind-mounted repo; `goos linux`, `goarch amd64`,
Go 1.24.13, AMD Ryzen 9 5900X 12-core, GOMAXPROCS=24. Median of 3.
**Included/excluded:** pure classification only — no HTTP server, no
`http.Handler` chain, no path rewriting.

| Host | ns/op | B/op | allocs/op |
| --- | --- | --- | --- |
| `localhost:4566` (path-style — the dominant case) | 230 | 176 | 4 |
| `127.0.0.1:4566` | 62 | 0 | 0 |
| `mybucket.localhost:4566` | 272 | 192 | 4 |
| `mybucket.s3.us-east-1.localhost:4566` | 188 | 160 | 3 |
| `abc123.execute-api.us-east-1.localhost.overcast.sh:4566` | 465 | 258 | 4 |

Split by half, the two avoidable costs are visible:

- `extractS3BucketFromHost` allocates **112 B / 2 allocs** even for
  `localhost:4566`, because it rebuilds the base list with
  `make([]string, 0, …)` and runs `slices.SortStableFunc` on **every call**
  ([s3virtualhost.go:179-186](../../internal/middleware/s3virtualhost.go)).
  The base list depends only on `OVERCAST_HOSTNAME`, which is fixed at startup.
- `ParseHostRoute` allocates **64–145 B / 2 allocs** because it
  `strings.Split`s the hostname ([hostroute.go:104](../../internal/middleware/hostroute.go)).

`net.ParseIP` is *not* a cost — its result does not escape, so it stays on the
stack (IP-literal row is 0 allocs).

### Targets for the new implementation

1. **One pass, not two.** A single `HostAddressing` middleware classifies once;
   today's two middlewares each parse the Host header independently.
2. **Precompute the base list once** at middleware construction. It is a
   function of `configuredHostname` only — building and sorting it per request
   is pure waste.
3. **No `strings.Split`.** Walk dot-separated segments by index
   (`strings.IndexByte` / `LastIndexByte`) so segment scanning allocates
   nothing.
4. **Cheap early-out.** The dominant case is a host with no dot before the port
   (`localhost:4566`) or an IP literal; both must exit in a few bytes of
   comparison.

**Acceptance gate:** `Classify` must be **0 allocs/op for every row above**,
and no row may regress on ns/op versus the baseline. Benchmarks live in
`internal/middleware/hostaddressing_bench_test.go` as permanent regression
cover, with the same five hosts plus a full-middleware benchmark measuring the
end-to-end per-request cost through a no-op handler.

### Measured result (after) — gate met

Same command and environment as the baseline, median of 3:

| Host | Before | After | Change |
| --- | --- | --- | --- |
| `localhost:4566` | 230 ns, 176 B, 4 allocs | **31.9 ns, 0 B, 0 allocs** | −86% time, no allocs |
| `127.0.0.1:4566` | 62 ns, 0 B, 0 allocs | **16.3 ns, 0 B, 0 allocs** | −74% time |
| `mybucket.localhost:4566` | 272 ns, 192 B, 4 allocs | **42.7 ns, 0 B, 0 allocs** | −84% time, no allocs |
| `mybucket.s3.us-east-1.localhost:4566` | 188 ns, 160 B, 3 allocs | **20.0 ns, 0 B, 0 allocs** | −89% time, no allocs |
| `abc123.execute-api.us-east-1.localhost.overcast.sh:4566` | 465 ns, 258 B, 4 allocs | **232.7 ns, 0 B, 0 allocs** | −50% time, no allocs |

Zero allocations on every path, and every row faster. The dominant case — a
plain path-style request, which is most S3 traffic — went from 230 ns and 4
heap allocations per request to 44 ns and none.

`NewHostClassifier` costs 1 alloc / 64 B, once per `router.New()`. That is
startup budget, not per-request, and sits far below the noise floor of the
startup measurements in [performance.md](../dev/performance.md).

**Remaining hotspot:** the host-routed row is still ~5× the others because
`awsRegionPattern.MatchString` (a `regexp`) runs once a label matches. It is
allocation-free and only on host-routed requests, which are rare relative to S3
traffic, so hand-rolling it is not justified yet — but it is the next thing to
cut if host-routed throughput ever matters. That row is also the noisiest
across runs (213–409 ns), consistent with regexp machine-pool contention.

## 8. Canonical URLs — minting must match routing

Fixing routing alone is incoherent: Overcast would route host-based URLs
correctly while still handing callers URLs that are empty, path-style, or
pointed at real AWS. Every command that surfaces an API URL must return the
canonical AWS shape on the configured base:

```
http://{id}.{label}.{region}.{OVERCAST_HOSTNAME}:{port}/{stage}/
```

### Current state

| Surface | Today | Target |
| --- | --- | --- |
| Lambda `FunctionUrl` | `{urlId}.lambda-url.{region}.{host}:{port}/` ✅ | unchanged — this is the reference implementation |
| API Gateway v2 `apiEndpoint` | **never populated** — field omitted by `v2APIToResponse` ([handler_http.go:604](../../internal/services/apigateway/handler_http.go)), so CFN `Fn::GetAtt ApiEndpoint` yields `""` | `http://{apiId}.execute-api.{region}.{host}:{port}` |
| API Gateway v1 | no invoke-URL field | unchanged — real AWS has none either; the console composes it client-side |
| AppSync `uris.GRAPHQL` | path-style `{base}/_appsync/{apiId}/graphql` | `http://{apiId}.appsync-api.{region}.{host}:{port}/graphql` |
| AppSync `dns.GRAPHQL` / `dns.REALTIME` | hardcoded `amazonaws.com` ([handler.go:232](../../internal/services/appsync/handler.go)) | the configured base |
| `AWS::URLSuffix` | `amazonaws.com` ([template.go:357](../../internal/services/cloudformation/template.go)) | see hazard below |

### One helper, shared with the router

Add a `serviceutil` helper that mints host-routed URLs from the **same label
table the router dispatches on**:

```go
func HostRoutedURL(cfg *config.Config, r *http.Request, label, id, region, path string) string
```

`buildFunctionURL` ([handler_url.go:124](../../internal/services/lambda/handler_url.go))
collapses into a call to it. This is the DRY requirement from
[CONTRIBUTING § Utilities](../../CONTRIBUTING.md), and more importantly it makes
drift structurally impossible: a URL Overcast hands out is, by construction,
one the grammar in §4 can route back.

### Hazard: `AWS::URLSuffix` is not only used for URLs

Flipping `AWS::URLSuffix` to the configured base is what makes CDK's
`api.url` output resolve locally, since CDK composes it from `AWS::Region` +
`AWS::URLSuffix`. But templates also use the same pseudo-parameter to build
**IAM service principals** (`Fn::Join ["", ["states.", {"Ref": "AWS::URLSuffix"}]]`
→ `states.amazonaws.com`). A blanket substitution would silently produce
`states.localhost.overcast.sh` as a principal.

This item therefore needs its own investigation and its own failing tests
before any change — audit real CDK-synthesised templates for every
`AWS::URLSuffix` use site, and only substitute where the result is a URL host.
It is sequenced last for that reason and must not be bundled with the
mechanical URL-minting changes above.

## 9. Behaviour changes

1. **Fixed** — every host-routed invoke over `localhost`,
   `localhost.overcast.sh`, `localhost.localstack.cloud`, or a configured
   `OVERCAST_HOSTNAME` now reaches its service: API Gateway v1 and v2, Lambda
   function URLs, AppSync GraphQL and realtime.
2. **No S3 regression.** Every previously-addressable bucket stays
   addressable, in every form, including dotted names in the bare
   virtual-hosted form. The reserved-label veto is what makes this possible:
   the first draft of this plan restricted tier B to a single label (floci's
   rule) and would have dropped dotted bare addressing, which was an
   unnecessary trade. No existing assertion, unit or integration, changed.

   The one name shape that becomes unreachable *in the bare form only* is a
   bucket carrying a reserved label at segment index ≥ 1 (`my.execute-api`) —
   see §6. It was already broken before this work, just with a corrupted path
   instead of a clean host-route claim.
3. **No new diagnostic.** A `CreateBucket` warning was planned and dropped —
   see §6: dotted bucket names cannot be created at all today, so it could
   never fire.

## 10. Phases

> **Progress:** H0-H2 complete. The planned CreateBucket reserved-label warning is dropped as unreachable -- see section 6. H3-H5 not started.

Each phase begins with failing tests and leaves `main` internally consistent,
per the shipping rule in [aws-api-operation-coverage.md](./aws-api-operation-coverage.md).

| Phase | Work | Gate |
| --- | --- | --- |
| H0 | Failing tests: classifier matrix, reserved-label predicate, host-routed integration tests across all bases for apigw v1/v2, Lambda URL, AppSync; S3 positive coverage retained. Permanent benchmarks per §7. | Every new test fails for the documented reason; no existing test fails except the two in §9.2. |
| H1 | `ClassifyHost` + `HostAddressing`; rewire `router.go`; `detectService` reads the stamped claim. | H0 green. §7 gate met: 0 allocs/op, no ns/op regression. `go vet ./...` clean. |
| H2 | `docs/networking.md` addressing-precedence section; `hostroute.go` recipe guardrail; reserved-label integration cover; `make docs-index`. | `make docs-check` green. |
| H3 | Canonical URL minting per §8: `serviceutil.HostRoutedURL`, API Gateway v2 `apiEndpoint`, AppSync `uris`/`dns`, `buildFunctionURL` collapsed onto the helper. | A minted URL, fed back as a `Host` header, reaches its own service — asserted end-to-end, not by string comparison. |
| H4 | `AWS::URLSuffix` audit and scoped substitution per §8's hazard. | Every `AWS::URLSuffix` use site in synthesised CDK templates classified as URL-host or not; IAM service principals provably unaffected. |
| H5 | *(follow-on PR)* Manifest-derived label validation per §6. | Requires an `api-models-aws` checkout at revision `66e973ca…`. |

H3's gate is deliberately behavioural rather than textual: asserting the minted
string equals an expected literal would pass even if the routing grammar and
the minting helper drifted apart. Round-tripping the URL through the router is
the only assertion that proves they agree.

## 11. Permanent guardrail

Until H3 lands, adding a host-routed service means adding a label by hand. The
reason the collision surface is only three labels wide is that all three are
hyphenated, AWS-specific, data-plane tokens that nobody ends a bucket name
with. That property is load-bearing, not incidental:

> **Never register a host-route label that is a plausible trailing segment of a
> bucket name.** Prefer the AWS data-plane hostname verbatim
> (`appsync-realtime-api`, not `realtime`).

This belongs in the "adding a new host-routed service" recipe in
[hostroute.go](../../internal/middleware/hostroute.go), where a contributor is
already reading when they are about to get it wrong. H3 makes it enforceable
rather than advisory.

## 12. Interaction with the AWS operation coverage work

For whoever picks up A3+ of
[aws-api-operation-coverage.md](./aws-api-operation-coverage.md).

**Verified 2026-07-28:** a trial merge of this branch with
`feat/aws-operation-rest-trie` (A3, `8e14e0cf`) applies with no conflicts, and
`internal/middleware`, `internal/awsapi`, and the router/s3/apigateway/appsync/
lambda integration suites all pass in the merged state. Nothing below is an
open action on A3 — it is the set of invariants that keep the two designs
compatible.

### 1. Path rewriting happens in middleware; REST matching must stay behind it

`HostAddressing` rewrites a host-routed request's path **before any route or
trie sees it**:

```
GET /prod/pets            Host: abc123.execute-api.us-east-1.localhost:4566
  -> GET /_apigateway/execute-api/abc123/us-east-1/prod/pets
```

This matters because an invoke path is arbitrary customer-defined data. A
perfectly legal API Gateway route is:

```
GET /prod/2015-03-31/functions/my-fn/invocations
```

Matched against a REST trie *before* the Host rewrite, that is Lambda `Invoke`
and would be 501'd — breaking a working customer API.

A3 already satisfies this by construction: `restFallback` is registered at
**route** level after every explicit service route, so it runs during chi
dispatch, long after middleware. The rewritten path is `/_apigateway/...`,
which matches an explicit route and never reaches the fallback. A3's additional
requirement that `middleware.ServiceFromCredential(r)` match `claim.SigningName`
gives a second layer of protection, since host-routed invokes are typically
unsigned.

The invariants to preserve: **no REST-claiming middleware ahead of
`HostAddressing`** (currently the 4th `r.Use`), the `/_*` exclusion stays, and
nothing recovers and re-matches the pre-rewrite path. The rewrite is where
"service data-plane traffic, not a modeled operation" gets decided.

### 2. No generator ask — `SigningName` already covers it

An earlier draft of this section asked A3 to retain `endpointPrefix` from the
`aws.api#service` trait so §6 could make `hostRouteLabels` evidence-based.
**Withdrawn:** A3 already generates `SigningName` from `aws.auth#sigv4`, and
`execute-api` is present in the generated corpus. That is sufficient evidence
for the one dispatch label AWS actually models.

`lambda-url` and `appsync-api` are host-only conventions carried by no Smithy
field at all — Lambda function URLs sign as `lambda`, AppSync as `appsync` — so
they belong in the documented override table regardless of which field is
retained. Retaining `endpointPrefix` too would not change that.

H5 therefore needs nothing from A3 beyond a small exported predicate over the
existing generated data (`SigningName` currently lives only on the private
`restOperation` table). That is this plan's work, not A3's.

### 3. Exported middleware API changed

Removed: `S3VirtualHost`, `S3VirtualHostFor`, `HostDispatch`,
`HostRouteService`. Replaced by `HostAddressing`, `HostClassifier`, and
`HostRouteServiceFor(HostRouteMatch)`. A3 does not reference any of them — the
trial merge confirms it — but another in-flight branch might.

### 4. `detectService` no longer re-derives the host-route label

It reads the `HostClaim` stamped by `HostAddressing`
([logger.go](../../internal/middleware/logger.go)). If a later phase adds
registry-based service labelling there, put it **after** the host-claim check:
a host-routed request's owning service is already known exactly, and the
manifest cannot improve on it.

Related, for A4's "no modeled non-S3 request reaches S3" corpus: which hosts do
and do not resolve to a bucket changed with this work. Corpus requests using
httptest's default `example.com` Host are unaffected (it matches no base); any
fixture that sets an explicit `Host` should be checked against the §4 matrix.

## 13. Follow-ups not in scope

- Folding `regionFromHost` ([region.go:49](../../internal/middleware/region.go))
  into the shared parse. It carries a **third** divergent label list
  (`execute-api`, `s3`, `sqs`, `sns`, `dynamodb`, `lambda`) that already
  disagrees with `hostRouteLabels` — it does not know `appsync-api` or
  `lambda-url`. Do it with H5, when the manifest makes the label set
  generated rather than hand-written.
- AppSync realtime on its own `appsync-realtime-api` host. Real AWS serves
  realtime from a separate hostname; Overcast colocates it under the same
  `/_appsync/{apiId}` prefix, so H3 advertises the colocated URL. Splitting it
  needs a fourth dispatch label, which per §6 should wait for H5's evidence
  gate rather than being hand-added now.
- Path-style AppSync URIs remain registered and supported after H3 switches the
  *advertised* `uris` to host-routed form. Removing them would be a breaking
  change for existing clients and is not proposed.
- **Bucket names cannot contain dots** ([validation.go:78](../../internal/serviceutil/validation.go)),
  while real AWS permits them in general-purpose buckets. Found while
  implementing H2 (see §6). Independent of host addressing and with its own
  blast radius across S3 validation and tests, so not folded in here. Relaxing
  it would make the §6 limitation reachable and would be the trigger to add the
  reserved-label warning.
