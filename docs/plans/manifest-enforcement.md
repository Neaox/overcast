# Making the pinned manifest the enforced source of truth

Status: **implemented and merged** — the route gates landed on `main` in PR #876,
with point 9's path-namespace gate completed by #921 (and the debug/data-plane
namespace moves in #929/#935). The **wire-fact gates** (points 6 and 10 below,
plus the exemption ratchet) landed second, closing the rest of
[#864](https://github.com/Neaox/overcast/issues/864).
Gates: [#863](https://github.com/Neaox/overcast/issues/863) (closed).
Audit that prompted it: [#861](https://github.com/Neaox/overcast/issues/861).
Remainder, all filed and none of it #864's: the shape artefact
[#883](https://github.com/Neaox/overcast/issues/883)/[#884](https://github.com/Neaox/overcast/issues/884),
route-to-owner attribution [#1227](https://github.com/Neaox/overcast/issues/1227),
the rpcv2Cbor probe [#1228](https://github.com/Neaox/overcast/issues/1228), and
the four unmodeled target prefixes
[#1226](https://github.com/Neaox/overcast/issues/1226).
As of 2026-08-22 the `unservedBindings` ledger is **empty** — all 43 opening
rows retired as #854–#860, #862 and #815 landed — and `protocolAsymmetries` is
empty too (#886 landed). The wire-fact gates add three more ledgers; as with the
first three, their contents live in the code the build reads and are not
restated here.

## The problem, in one sentence

`internal/awsapi/manifest.gen.go` holds 18,850 modeled operations with
`Protocol`, `Protocols`, `TargetPrefix`, `HTTPMethod` and `URI`, and until this
change `capgen` consulted it through exactly one call — `awsapi.HasOperation`,
a name-only check. Every other field was inert.

That gap produced #793, #815, #854–#860 and #862: ten services whose
implementation works and whose route disagrees with a file in the same
repository.

## What is enforced now

| # | Enforcement point | Where | Ratchet |
| --- | --- | --- | --- |
| 1 | A REST-bound operation is registered at the model's `HTTPMethod` + `URI` | `internal/router/modelbinding_dev_test.go` | `unservedBindings` |
| 2 | An operation is reachable over every protocol its service answers on | `internal/router/modelprotocol_dev_test.go` | `protocolAsymmetries` |
| 3 | `DocOnly` is an assertion: a DocOnly row may not have an implementation | `cmd/capgen` `checkDocOnlyRowsAreNotDispatched` | — |
| 4 | `StatusSupported` means reachable | `assertUnreachableRowsDoNotClaimSupport`, plus capgen's `WRONG_STATUS` | — |
| 5 | A capability `Note` may not state a binding the model contradicts | `cmd/capgen` `checkNotesBindingsMatchTheModel` | — |
| 6a | A service's `TargetPrefix()` is a prefix the model gives that service | `cmd/capgen` `checkTargetPrefixesAgainstTheModel` | `unmodeledTargetPrefixes` |
| 6b | A service's `PathPrefixes()` claim is a path space the model gives that service | `cmd/capgen` `checkPathPrefixesAgainstTheModel` | — |
| 6c | The Query API-version constants and their owners are modeled fact | `internal/awsapi/modelfacts_test.go` | — |
| 6d | `serviceAliases` is sorted, unique, and every target is a service key | `internal/awsapi/modelfacts_test.go` | — |
| 7 | A service's route skeleton is generated, not typed | `capgen --routes --service <name>` | — |
| 8 | A compat test's declared `op` is a real AWS operation | `cmd/capgen` `checkCompatRegistryServiceKeys` | — |
| 9 | Every **registered route** is a modeled binding or lives under `/_overcast/` | `internal/router/pathnamespace_dev_test.go` | `unmigratedRoutes` |
| 10 | A `DocOnly` row named like an operation **is** one, for its own service | `cmd/capgen` `checkDocOnlyRowsNameRealOperations` | `docOnlyRowsOutsideTheModel` |
| 11 | An exemption is deleted when its reason stops being true | `cmd/capgen` `checkModelExemptionsAreStillNeeded` | — |
| 12 | A query-discriminated binding is identified from method + path + query | `internal/router/modelquerybinding_dev_test.go` | `queryBranchNotProvable` |

Point 9 is the only one that walks **route → model**. Every other row starts
from something Overcast declares and asks whether it is served correctly; that
direction is structurally blind to a path the emulator *invented*, because an
invented path is declared nowhere. It answers requests, breaks no gate, and is
found only when somebody reads the routing table — which is what #793, #815 and
#854–#860 were.

On its first run it reported six invented paths a hand audit had missed, all of
them nested inside prefixes AWS really does bind (`/2015-03-31/functions/{name}/source`
and friends), plus a CloudFront route registered twice — once at AWS's plural
path and once at a singular one nothing models. The model → route direction
could not have found that second registration: CloudFront declares the
operation, the plural route serves it, and the gate is satisfied.

The migration it enforces is [One namespace for every non-canonical
URL](./non-canonical-url-namespace.md).

Points 6a–6d are the second half of point 6, and they are the answer to the
question the audit below leaves open for `detectService`: *which* hand-maintained
tables restate modeled fact, and which of them can be held to it. Four could,
and none of them is generated — a service's `TargetPrefix()` and `PathPrefixes()`
are decisions about what Overcast serves, `versions.go` has to name an owner per
API version that the models cannot supply, and `serviceAliases` has to name the
Overcast key a modeled identity answers to. But each has a half that *is* modeled
fact, and that half is now checked. See the section on wire facts below.

All of it runs in `make aws-models-check`, which runs
`go test -tags dev ./internal/router ./cmd/capgen` and `capgen --check-model`.

## The boundary — what the manifest cannot prove

**Decision: the manifest does not grow to carry member bindings. A second
generated artefact should cover shapes, and it is not in this change.**

**Reaffirmed when the wire-fact gates landed.** #883 is a *generator* change —
`cmd/awsmodelgen` emitting a second artefact from the pinned Smithy ASTs — and it
depends on #884 for a supported way to fetch those models, which are not
vendored. Nothing in this document's enforcement can be built from the manifest
alone, so folding #883 in would have made every gate here wait on a model fetch.
It stays a follow-on, and the boundary below stays stated rather than papered
over.

The manifest carries the `@http` trait and nothing else. URI templates do
include literal query bindings (`/apikeys?mode=import`,
`/backup-vaults/{Name}/mpaApprovalTeam?delete`), but a member-bound `httpQuery`
never appears in the template, because it is a property of the *input shape*,
not of the binding.

So these gates prove **method and path**. They say nothing about request
*shape*. Concretely, they would not have caught the second half of #793:
`GroupName` is an `httpQuery` member on Get/Delete and a body member on
Create/Update, and Overcast had it as a path segment. The path was wrong there
too, so the binding gate would have fired — but a service that reads an
`httpQuery` member out of the body serves the modeled path, on the modeled
method, and passes everything here.

Why not extend the manifest: it is 51,451 lines for the binding alone. Member
shapes are recursive, and a service's input shape can run to hundreds of
members across nested structures; carrying them would make the manifest a
second copy of the Smithy models rather than an index of them. The thing that
makes the manifest cheap to consult — one flat row per operation, no traversal
— is exactly what member shapes would destroy.

What a shape gate wants instead is a separate generated artefact — **filed as
#883**, with #884 for the fetch helper that produces it — most likely
per-service and loaded only by a test: for each operation, the members bound to
`httpLabel`, `httpQuery`, `httpHeader` and `httpPayload`, with their names.
That is enough to assert that a handler reads a query member from the query
string. It is a different generator, a different file, and a different issue.
**Do not let "the manifest is the source of truth" imply a guarantee it cannot
make.**

Three narrower limits, all visible in the code rather than assumed. The first
has since been half closed, and the entry says which half:

- **Query-discriminated bindings.** 139 modeled URIs pin a query parameter. chi
  routes on the path, so two operations differing only in the query reach one
  route and something downstream has to branch. The binding gate drops the query
  before comparing (`uriSegments`), so it proves the path is served and says
  nothing about the branch.

  Point 12 closes the half that is derivable. `Registry.ClaimRESTQuery` matches
  the literal query components Smithy declares, and its answer is what names the
  operation for IAM authorisation, for the request log and for a fallback's 501 —
  so `internal/router/modelquerybinding_dev_test.go` asserts every declared
  query-pinned binding resolves to its own operation, *and* that removing the
  query changes the answer. Without that second half the gate would certify
  bindings whose path was unique and whose query was decoration. Lambda is the
  case that makes it real: `GET …/provisioned-concurrency` is
  `GetProvisionedConcurrencyConfig` and `?List=ALL` is
  `ListProvisionedConcurrencyConfigs`, one route and two operations.

  What is still not proven is the **handler's** branch — that S3's
  `GET /{Bucket}?acl` runs `GetBucketAcl` rather than `ListObjectsV2`. That needs
  a request/response assertion per operation rather than a model, and S3 is also
  the one service the generated REST indexes deliberately exclude (it is the
  wildcard owner every unclaimed path falls to, so classifying it would have the
  registry claim the whole path space). Its query-pinned rows — most of the
  declared set — are therefore a recorded carve-out in `queryBranchNotProvable`,
  exercised by the s3 package's own request tests instead.
- **Routes broader than the model.** A subtree wildcard (`/v1/clusters/*`) or a
  fallback handler (API Gateway's ARN-keyed tag store) delivers the request to
  a service without the route table recording which operation it reaches. These
  are graded `coverageBroad` and recorded in `weaklyServedBindings` rather than
  passed silently, because a broader route is how a shared prefix comes to
  answer for a service nobody asked — #854's worst symptom.
- **rpcv2Cbor.** Modeled for several services and not probed by the protocol
  gate, which would need a CBOR encoder. An operation Overcast serves over
  neither JSON nor Query is already reported by the binding gate.

## Fallout, and why it is a ledger rather than exemptions

Enforcement failed on existing rows; that was the point. #864 is explicit that
"an exemption added to make a gate green is precisely how the current situation
arose", so the fallout is recorded as a **ratchet**: an unserved binding absent
from the ledger fails the build, *and so does a ledger entry whose fault has
been fixed*. An exemption says "stop asking"; the ledger says "this is
known-broken, here is who owns it, and the build fails the moment that stops
being true".

Every entry names a filed issue. The route moves are not in
this change because each carries the standing requirement stated on #854–#860
and #862 — review the implementation in full against `CONTRIBUTING.md` and
establish AWS fidelity from the pinned model rather than by assumption — and
#793 is the precedent for what a rushed one costs.

| Ledger | What it records |
| --- | --- |
| `unservedBindings` | Bindings no route serves, each naming the issue that owns the fix. Shrinks as they land. |
| `weaklyServedBindings` | Not faults — the gate's honest margin, where a wildcard or a fallback handler delivers the request without the route table proving which operation it reaches. |
| `protocolAsymmetries` | Operations reachable over one modeled protocol and not another. Empty since #886. |
| `unmodeledTargetPrefixes` | REST-only services that dispatch on an `X-Amz-Target` prefix no model gives them. A fault ledger: every row names the issue that retires it (#1226). |
| `docOnlyRowsOutsideTheModel` | Not faults — the honest margin of the DocOnly name check. Rows whose Operation reads like an AWS operation and documents something else, each saying what. |
| `queryBranchNotProvable` | Services the generated REST indexes deliberately exclude, so their query-discriminated bindings cannot be proven from the model. `s3` is the only one. |

**The counts are deliberately not repeated here.** They are derivable from
`internal/router/modelbinding_ledger_dev_test.go`, which the build reads and
this file does not; duplicating them produced a stale number and a merge
conflict on every PR that retired a row.

The ledger opened at 43 rows, and 43 capability rows moved from Supported/Inert
to WIP with it, because they were implemented, worked, and could not be called
by any SDK. Rows leave as their fixes land, and the ratchet's second direction
is what forces that — a ledger entry naming a binding that is now served fails
the build. #860 took AppSync's two out, #855 AppConfig Data's two, #858 EKS's
six, #859 MSK's v2 pair, #854 AppConfig's twelve — the last of which also
stopped AppRegistry answering `POST /applications` for an `appconfig` caller —
and #815 Backup's nine, a service that had registered no chi routes at all.

#862 is not in that table, and the reason is the ratchet earning its keep.
`ses/V2CreateEmailIdentity` was in `unservedBindings` while this branch was
being written; #871 moved the route to POST in the meantime, and on rebase the
gate's second direction failed with "a ledger entry names a binding that is now
served". An exemption list would have carried that row indefinitely, still
claiming a fault that no longer existed. #871 also left the eight SESv2 rows'
`DocOnly` flags in place with a comment saying they were untrue and that
"the flag comes off these eight rows with [#864]" — which is this change.

### Two findings from running the gates

**AppConfig's twelve rows did not answer a clean 501.** Every other unserved
binding falls through to `restFallback`, which asks the generated registry who
owns the path and answers `NotImplemented`. AppConfig's did not, because
AppRegistry registered `r.Route("/applications", …)` and a chi sub-router owns
its whole subtree: a path it does not match hits *its* NotFound, never the
parent's `/*`. A client calling AppConfig got a bare 404 with no AWS error body.
**The 501 fallback only protects paths no other service has claimed** — worth
knowing before adding a prefix route.

#854 fixed it by giving `/applications` to the main router, which dispatches on
the SigV4 credential scope exactly as `/v2/apis` already did, and by having both
sub-routers delegate `NotFound` and `MethodNotAllowed` to `restFallback`
(`router.delegateUnmatched`). That second half is the general remedy for the
sub-router problem, and is what any future dispatcher over a shared prefix
should copy.

**`restFallback`'s 501 is conditional on a correctly scoped request.** It
requires the SigV4 credential scope to match the modeled signing name before
claiming a request away from S3 (`claimAnswersCaller`). A request signed with
Overcast's service key rather than AWS's signing name — `opensearch` instead of
`es`, `msk` instead of `kafka` — falls into S3's wildcard and comes back
`NoSuchBucket: 2021-01-01`. That is a plausible answer to a question nobody
asked, the same shape as #854.

Real AWS does not have this problem, because it routes by endpoint and treats
the scope purely as a signing input; a mis-scoped request fails with a message
naming the expected value ("Credential should be scoped to correct service:
'es'"). Overcast cannot route by endpoint — ~1,950 modeled root segments are
also legal S3 bucket names (`internal/middleware/logger.go:37`) — but it could
answer the same way: when the path matches a modeled binding for service X and
the scope names a *different* real service, that is not ambiguity. `sigv4.go`
already has `invalidSignature()` and the manifest already has the signing name.
This is about the scope, not the signature, so it does not conflict with "not a
security boundary"; unsigned traffic must keep falling to S3. **Filed as #887**; it is a
runtime behaviour change, out of scope here.

### The one asymmetry, and why it is deliberate

`cloudwatch/GetMetricData` is reachable over Query and not over awsJson1_0,
which the model makes its *primary* protocol. It is deliberate and pre-existing
— CloudWatch's own `TestDispatchJSON_CoversEveryQueryOperation` has carried the
same exemption since #794, because the JSON encoding of `MetricDataQueries`,
epoch timestamps and `MetricDataResults` does not exist yet. Unlike the routing
faults, nothing recorded who will write that encoding or when. **Filed as
#886.**

## What became derivable

Three hand-maintained artefacts existed only because `capgen` had no way to
check a REST-routed service. capgen now reads the package's
`(http.ResponseWriter, *http.Request)` methods — which is where a REST-routed
operation is dispatched from — so all three are deleted:

- `internal/services/appsync/rest_operations_dev.go`, 85 rows of method, path
  and operation already in the manifest, plus the test keeping it in step with
  the routes;
- `internal/services/eks/capabilities_inventory_test.go`'s 50-case
  route-to-operation switch;
- `internal/services/ses/capabilities_inventory_test.go`, whose `restOnlyOps`
  allowlist named five of SESv2's eight REST-routed operations and had been
  missing the three tag ones since they were added — they passed only because
  their rows carried `DocOnly`, which is the flag doing duty as an exemption
  exactly as #863 describes. Widening the list was the wrong fix, and so was
  deriving it by reflection: a handler method's name is not an operation name
  (`GetSendStatistics` dispatches to `GetSendStatisticsStub`), so a
  reflected list demands a capability row for a method that is not an
  operation. Both directions the test really checked are capgen's, verified
  against this package before deleting it — a capability row with no dispatch
  reports `ORPHAN ses/…`, and an `initOps` key with no row reports
  `MISSING ses/…`. capgen runs over every service rather than the thirteen that
  happen to have this file;
- capgen's `parseRESTOperations` and its "REST-routed; not detectable" ORPHAN
  line, printed twenty times a run while asserting nothing.

**Twelve sibling `capabilities_inventory_test.go` files remain**, in the
action-dispatch services. They are now redundant with capgen's cross-check for
the same reason SES's was, but they are not wrong and nothing in this change
breaks them, so removing them is a follow-up rather than a sweep bolted onto
this one.

## The wire facts a service states about itself

The route gates hold the **route table** to the model. They say nothing about the
strings a service package writes down that restate the same modeled facts
somewhere else — and one of the ten faults began in exactly such a string: #815
dispatched AWS Backup off an `AWSBackup.` target prefix that appears in no model,
with no chi routes at all behind it.

Four such tables exist, and each has a half a build can check:

| Table | Hand-written because | Modeled half now checked |
| --- | --- | --- |
| `TargetPrefix()` | which services Overcast dispatches by target is Overcast's decision | the prefix string, against `Operation.TargetPrefix` |
| `PathPrefixes()` | which path space a service claims is Overcast's decision | that some modeled URI *for that service* lives under it |
| `internal/awsapi/versions.go` | a version needs an **owner**, which the models cannot give (DocumentDB, Neptune and RDS share `2014-10-31` and most of its actions; Overcast implements one of the three) | the version string, against `Operation.APIVersion` for that key |
| `serviceAliases` | the Overcast key a modeled identity answers to is Overcast's decision | strict sort, uniqueness, and a lower-case target — the left-hand side was already checked |

`PathPrefixes()` is the one worth reading twice, because it is the declaration
form of the whole fault class: `/_scheduler` (#793), `/_appconfig` (#854),
`/_appconfigdata` (#855), `/_opensearch` (#856), `/_bedrock` (#857) and
`/v2/clusters` where AWS models `/api/v2/clusters` (#859). None of those services
declared `PathPrefixes` at the time — the method arrived *with* each fix — so the
honest claim is not that this gate would have caught them, but that it is what
stops the next declaration of that shape. Every prefix in the tree passes today,
so it opens with no ledger; injecting `/_eks` into EKS's list is what proved it
fires.

`TargetPrefix()` opens with a ledger and an issue that owns it (#1226): four
REST-only services — AppRegistry, EFS, EKS and Scheduler — answer `POST /` for a
target prefix the models never give them. They do register their modeled REST
routes, so the prefix is redundant surface rather than the whole service, which
is why it is a ledger rather than a build failure today.

### The exemption sweep

`capabilityManifestExemptions` held fourteen entries. **Nine were false.** Every
one of them asserted that API Gateway v2 "has no `GetIntegration` operation" — and
`DeleteIntegration`, `GetAuthorizer`, `GetAuthorizers`, `DeleteAuthorizer`,
`GetDomainNames`, `DeleteDomainName`, `GetVpcLinks`, `DeleteVpcLink`.
`apigatewayv2` models all nine, and has for as long as the manifest has existed,
so nine live capability rows were exempt from the model check on the strength of a
statement about AWS that was never true.

Nothing was hiding behind them — the rows resolve cleanly once the exemptions are
deleted — but that is luck, not design, and it is exactly what #864 means by "an
exemption added to make a gate green is precisely how the current situation
arose". `checkModelExemptionsAreStillNeeded` now holds all three exemption tables
(`capabilityManifestExemptions`, `capabilityOperationAliases`,
`compatRegistryServiceExemptions`) to the rule that an exemption whose reason has
stopped being true is deleted, not reviewed.

The same rule reaches `DocOnly`. #863 made the flag an assertion about *dispatch*;
it stayed an exemption from the **name** check, which is the half #862 used —
six SESv2 rows carried it purely to silence a name mismatch, which removed them
from every other cross-check at once. A DocOnly row whose Operation is a single
PascalCase word is making a claim about AWS and is now held to it. Rows that
document something else keep saying so in their own spelling
(`Fn::ImportValue`, `{{resolve:ssm}}`, `SMS publish`,
`AWS::ServiceCatalogAppRegistry::Application`); the ones that read like operation
names and are not — AppSync's VTL resolver operations,
`dynamodb/GetShardIterator` (a Streams operation), `ses/V2Other` and
`sns/PublishToEndpoint` — are recorded with what they really are.

## Point 6 — `detectService` is a justified second source, `detectOperation` is not one

`detectService`'s step-2 path-prefix switch (`internal/middleware/logger.go:70`)
restates modeled URI prefixes: 20 of its 21 non-internal prefixes appear in the
manifest. It is **deliberately** hand-written and the file says why
(`logger.go:32-55`): it must run ahead of the credential scope, and the
manifest's ~1,950 single-owner root segments are almost all legal S3 bucket
names, so deriving it would route S3 traffic to other services. Three entries
are model-ambiguous (`/applications` is 8 services, `/v1/tags` is 12), and
answering "" would fall through to S3. Two of those — `/v2/apis` and, since
#854, `/applications` — are shared by services Overcast implements, so they
read the credential scope first and fall back to the prefix's historical owner.

This matters more than a routing table usually would: `detectService` picks the
IAM action a policy is evaluated against and the error wire format, so it is
behaviour. It is not enforced against the manifest here, and the reasoning
against doing so is sound.

`detectOperation` is the counter-example — same problem, already solved by
derivation, through `ClaimTarget`, `ClaimQuery` and `RESTOperation`
(`internal/middleware/restoperation.go:19-27`).

`isLambdaAPIVersionPrefix` (`logger.go:171`) is pure manifest fact and is
already asserted against the model in one direction
(`restoperation_test.go:490`). It is the cheapest remaining candidate for
generation, and is left alone here.

## Sequencing (historical)

The rule at the time was: not to be merged until `0.0.1-alpha.34` had shipped —
a sweep this size did not belong in an open release window. That held: the
change merged as #876 after alpha.34, with the gates and the fallout as
separate commits so they could be read apart.
