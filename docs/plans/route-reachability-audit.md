# AWS Route Reachability Audit

> Status: audit complete against `main` at `41778729` (post `v0.0.1-alpha.33`). Seven issues filed. The reproducible check lives at `scripts/route-reachability.go`; re-run it rather than re-deriving this table.
>
> **Update, verified 2026-08-21: every finding is fixed.** All seven filed
> issues (#854–#860) are closed, as are #815 and #862 — every Category-A
> service now serves its modeled bindings, and the fallout ledger that tracked
> them (`internal/router/modelbinding_ledger_dev_test.go`) is empty. The
> model-vs-route gate proposed under "What would stop this recurring" has
> landed: see [manifest-enforcement.md](./manifest-enforcement.md) (PR #876,
> plus the path-namespace gate in #921), so this fault class now fails before
> merge. AGENTS.md's "router coverage" wording (supporting note below) was
> corrected in the same change. The results tables are kept as the record of
> the audit; the script remains the release-time re-check.
>
> **Update 2026-08-23 (#1199): the two remaining process gaps are closed.**
> Both residual items under "What would stop this recurring" (points 3 and 4)
> named a real gap: the script had no automated caller, and the service-addition
> checklist step it exists to back had no mechanical check behind it. Point 1's
> model-vs-route gate proves a *route is registered* at the modeled binding; it
> does not prove the wire actually reaches a handler when it answers — see
> `internal/routecheck`'s package doc for the AppConfig/AppRegistry sub-router
> case that shows the difference. Two things now close that gap:
>
> - The reusable half of this script — the model index, request builder, and
>   verdict logic — moved into `internal/routecheck` (`//go:build dev`), so it
>   is one implementation shared by two callers rather than something to
>   re-derive per caller.
> - `tests/integration/router/route_reachability_dev_test.go`'s
>   `TestAllDeclaredCapabilitiesAreReachable` runs the same sweep in-process,
>   against a full-service `httptest` server (`tests/helpers.NewTestServer`),
>   reading the live `capabilities.Default` registry rather than the generated
>   snapshot so a newly declared capability is swept immediately. It needs no
>   Docker instance and no network, so it runs on every PR under
>   `-tags slim,dev` — which is what gives AGENTS.md's "verify no custom
>   endpoints were introduced" checklist step the teeth point 4 asked for,
>   rather than only checking at release time.
>
> The script itself is unchanged in purpose and stays the release-time
> re-check against a real instance/image — see
> [RELEASE.md](../../RELEASE.md#creating-an-alpha-release) and the
> [`release` skill](../../.agents/skills/release/SKILL.md#1-smoke--is-it-alive-and-correctly-wired),
> both of which now name it as a required RC step. Failing-first evidence:
> temporarily flipping `elasticache/DescribeCacheEngineVersions` back to
> `StatusSupported` (the exact historical shape this audit already
> documented in Category B) reproduced a single red
> `TestAllDeclaredCapabilitiesAreReachable` naming that operation; reverting
> the flag turned it green again.

## Why this audit happened

Four instances of one fault had been found **by accident**, which is the problem — nobody knew how many more there were. The fault: a service is fully implemented, but mounted on an Overcast-invented path or an invented `X-Amz-Target` prefix, so every AWS SDK call reaches the generated 501 fallback while the working implementation sits behind a URL no client will ever send.

- #793 — EventBridge Scheduler on `/_scheduler/schedules/…` instead of `/schedules/{Name}`. Fixed in #807.
- #815 — AWS Backup dispatched off `X-Amz-Target: AWSBackup.*`, a prefix AWS Backup does not use.
- `appconfigdata` and `bedrock` — hand-probed during release testing; both 501 at their real AWS paths.

Contrast: `appsync` and `cognito` mount an `/_<service>` prefix **in addition to** serving their real AWS surface. The invented prefix alone is not the smell. The missing real route is.

## Method

1. Enumerated every registered service from `internal/router/tiers.go` (50 services, all tiers).
2. Took the authoritative wire surface from the pinned manifest, `internal/awsapi/manifest.gen.go` (source revision `66e973cadf6b6e909b200217d0d6065e49445a9a`). Not from `docs/services/*.md` and not from capability declarations — #794 already proved both overstate reality.
3. Took the *expectation set* from `internal/capabilities` (the `-tags dev` snapshot): 1,244 operations Overcast declares as Supported / Partial / Inert / WIP. An operation Overcast claims to implement is expected to be reachable at its modeled binding. `StatusUnsupported` and `DocOnly` rows are excluded — a 501 is the correct answer for those.
4. Probed a live instance: one request per modeled binding, addressed exactly as an SDK would, and classified the response.
5. Corroborated every finding by reading the routing code, and by a paired probe — the same operation at Overcast's invented binding *and* at the modeled one — so "implemented but unreachable" is demonstrated, not inferred.
6. Established provenance with `git log -S` and `git tag --contains` on the routing code.

### Running it

```sh
scripts/run-test-instance.sh --no-logs --name reach       # free port pair at/above 4570
go run -tags dev ./scripts/route-reachability.go -endpoint http://127.0.0.1:<port>
```

`-tags dev` is required; `internal/capabilities` is empty without it. Useful flags: `-service <key>` to narrow, `-show-body` to see who answered, `-json <path>` for the full report.

Verdicts:

| Verdict | Meaning |
|---|---|
| `reachable` | a handler answered — 2xx, or an AWS-shaped 4xx, which still proves the route |
| `shared-path` | answered only on a binding another Overcast service also models; read the body to see who answered |
| `unreachable` | 501 with `x-emulator-unsupported` on every modeled binding |
| `unmodeled` | the capability names no operation in the pinned manifest (a documented `capgen` exemption) |

The `shared-path` verdict exists because a naive pass scores AppConfig as fully reachable. AWS separates AppConfig and Service Catalog AppRegistry by endpoint hostname; both model `POST /applications`, and a single-endpoint emulator cannot. Whichever service registers the chi route first answers for both. The script flags any binding modeled by more than one *Overcast-declared* service, which is what turns that from an invisible pass into a "verify the owner" prompt.

## The `make aws-models-check` finding

**This is the most important result of the audit, and it is bigger than any individual service.**

AGENTS.md § "Reproduce the AWS operation coverage CI job" says `make aws-models-check` "validates the committed manifest, runtime ownership indexes, protocol identifiers, **router coverage**, and capability-to-model alignment". That reads as though it would catch an unreachable service. It cannot, and never could.

The target is:

```make
aws-models-check:
	$(GO) test -count=1 ./cmd/awsmodelgen ./internal/awsapi ./internal/protocol/codec ./tests/integration/router
	$(GO) run -tags dev ./cmd/capgen --check-model
```

Both halves were run against `main` while all the findings below were live. **Both pass.** No exemption is swallowing this; the gates simply measure something else.

### Why the router test cannot catch it

"Router coverage" is `TestGeneratedCorpus_noModeledOperationReachesS3` in `tests/integration/router/operation_ownership_test.go`. Its fixture registers **only S3**:

```go
srv := helpers.NewTestServer(t, helpers.WithServiceSubset("s3"))
```

and its pass condition is:

```go
if resp.StatusCode == http.StatusNotImplemented || resp.StatusCode == http.StatusServiceUnavailable {
	return true
}
```

A 501 *is the success case*. The test asks "does an unhandled modeled operation return a protocol-correct 501 instead of falling into S3's bucket/object wildcard?" — a real and valuable question, and the test is well built for it. But with only S3 registered it cannot ask whether any other service's handler is reachable, because no other service's handler is present. Scheduler returning 501 at `/schedules/{Name}` was indistinguishable from the intended behaviour.

`TestGeneratedCorpus_everyNonS3OperationHasRouteOwnership` in `internal/awsapi/registry_test.go` is the same question from the other side: it asserts the *generated registry* can classify every modeled operation, i.e. that the 501 table is complete. Also never asks whether a handler serves it.

### Why the capability check cannot catch it

`capgen --check-model` runs `checkCapabilitiesInManifest` (`cmd/capgen/main.go:250`), which is:

```go
if awsapi.HasOperation(cap.Service, operation) { continue }
fmt.Printf("UNKNOWN_MODEL_OPERATION %s/%s …")
```

That is a **name** check: does the operation Overcast declares exist in AWS's model? It never compares the declared operation's modeled `@http` binding to the route Overcast registers. `scheduler/CreateSchedule` is a real AWS operation name, so it passed for 33 releases while mounted on `/_scheduler/schedules/{Name}`.

### Conclusion

Nothing in the repo compares **a registered route to its modeled binding**. Every gate stops one step short:

| Gate | Asks | Does not ask |
|---|---|---|
| `TestGeneratedCorpus_noModeledOperationReachesS3` | does an unclaimed operation 501 instead of hitting S3? | is it claimed by the service that implements it? |
| `TestGeneratedCorpus_everyNonS3OperationHasRouteOwnership` | can the registry classify every modeled operation? | does a handler serve it? |
| `capgen --check-model` | is the declared operation *name* real in AWS? | is it registered at its modeled *binding*? |
| `capgen --check` | do declared capabilities match parsed handler ops? | do those handlers sit on modeled paths? |

Two supporting notes:

- AGENTS.md's "router coverage" wording should be corrected regardless of whether a new gate is added. It currently implies a guarantee that does not exist, which is how four instances of this fault reached release.
- `internal/services/appsync/rest_operations_dev.go` is the only per-service REST-operation inventory in the repo, and `rest_operations_inventory_test.go` asserts it matches the **registered routes** — not the model. It therefore currently certifies AppSync's two invented evaluation paths as correct (#860). That file is the natural place to hang a real model cross-check: declare each REST operation's method and path, and assert it equals the manifest's `@http` binding. Rolled out across services, that would turn this whole class of fault into a compile-time-ish failure, and `scripts/route-reachability.go` would become a belt-and-braces runtime confirmation rather than the only detector.

## Results

1,244 declared operations probed. **33 unreachable, 38 shared-path, 7 unmodeled, 1,166 reachable.**

### Category A — implemented but unreachable (issues filed)

| Service | Tier | Ops | Mechanism | Issue |
|---|---|---|---|---|
| `appconfig` | inert | 19 | whole surface on `/_appconfig`; `POST/GET/DELETE /applications` silently answered by AppRegistry; nested paths bare-404 | #854 |
| `appconfigdata` | partial | 2 | whole surface on `/_appconfigdata`; token is a path segment, not a modeled member | #855 |
| `opensearch` | inert | 8 | whole surface on `/_opensearch`, not `/2021-01-01/…` | #856 |
| `bedrock` | stub | 2 | whole surface on `/_bedrock`, not `/model/{modelId}/invoke` | #857 |
| `eks` | partial | 6 | six hand-invented paths, four with the wrong HTTP method | #858 |
| `msk` | partial | 2 (+1) | v2 API at `/v2/clusters`, modeled at `/api/v2/clusters`; `ListClustersV2` absent entirely | #859 |
| `appsync` | inert | 2 | evaluation ops under `/v1/apis/{apiId}/…`, modeled as API-independent `/v1/dataplane-*` | #860 |
| `backup` | inert | 9 | dispatched off `X-Amz-Target: AWSBackup.*`; no REST routes at all | #815 (already filed) |

Every one of these traces to `c731c6fb "Expand emulator services and web UI"`, which `git tag --contains` places in `v0.0.1-alpha.0` and all 33 releases since — the same commit and the same span as #793. None is a regression.

Nine services also declare an `X-Amz-Target` prefix for an API AWS models as `restJson1` with no target header at all: `appconfig` (`AppConfig.`), `appconfigdata` (`AppConfigData.`), `appregistry` (`AppRegistry.`), `appsync` (`AppSync.`), `backup` (`AWSBackup.`), `bedrock` (`Bedrock.`), `efs` (`EFS.`), `eks` (`EKS.`), `msk` (`Kafka.`), `opensearch` (`OpenSearch.`). For `appregistry`, `appsync`, `efs` and `eks` this is harmless dead weight because the real REST routes are also registered; for the rest it is the whole delivery mechanism. All of them should be removed as the services are wired.

### Category B — deliberately unimplemented; 501 is the intended answer

Recorded so the next auditor does not re-find them.

| Service | Ops | Why this is correct |
|---|---|---|
| `elasticache` | `DescribeCacheEngineVersions`, `RebootCacheCluster` | **Routing is correct.** `OwnsVersion` claims the Query API version, `DispatchQuery` has explicit `case` arms for both, and they reach `internal/services/elasticache/handler_stubs.go`, whose entire contents are `protocol.NotImplementedQueryXML(w, r)` under `// TODO(priority:P3)` comments. The request reaches the handler; the handler declines. Not a routing fault, so no route issue was filed. |
| `waf`, `shield`, `organizations` | all declared | `TierStub` services whose declared operations all probe reachable. Behaving as designed. |

One caveat on ElastiCache worth carrying forward: both stubs were declared `StatusSupported` in `capabilities_dev.go`, so `docs/services/elasticache.md` advertised ✅ Supported for two operations that always 501. That was a capability-declaration defect, not a routing one — a different fault class from this audit, deliberately not filed here to avoid padding. **Since fixed**: both rows now read `StatusUnsupported` ("stub; returns 501").

Similarly, three EKS capability rows (`DescribeAccessPolicy`, `UpdateIdentityProviderConfig`, `UpdateKubeconfig`) were declared `StatusSupported` while naming operations AWS does not model at all. Noted inside #858 and **since resolved with it**: the first two rows are gone, and `UpdateKubeconfig` survives honestly labelled as an emulator extension with its capgen exemption.

### Category C — reachable

The remaining 42 services answer on their modeled bindings: `acm`, `apigateway`, `appregistry`, `athena`, `autoscaling`, `cloudformation`, `cloudfront`, `cloudtrail`, `cloudwatch`, `cloudwatch-logs`, `cognito`, `dynamodb`, `dynamodbstreams`, `ec2`, `ecr`, `ecs`, `efs`, `elbv2`, `eventbridge`, `firehose`, `glue`, `iam`, `kinesis`, `kms`, `lambda`, `organizations`, `pipes`, `rds`, `route53`, `s3`, `scheduler`, `secretsmanager`, `ses`, `shield`, `sns`, `sqs`, `ssm`, `stepfunctions`, `sts`, `transfer`, `waf`, and `elasticache` (routing correct; see Category B).

42 here plus the 8 in Category A accounts for all 50 registered services.

`scheduler` is in this list as of #807 — it now serves `/schedules`, `/schedule-groups` and `/tags/{ResourceArn}` on their modeled bindings, and probes clean. #793 stands only as prior art.

### Shared bindings that are not faults

Three `shared-path` groups were checked by hand with real ARNs and are correct by design:

- `/v2/apis` — AppSync and API Gateway v2 both model it; `v2APIsDispatch` in `internal/router/router.go` separates them by SigV4 credential scope. Each service's probe got its own service's response.
- `/tags/{ResourceArn}` and `/v1/tags/{ResourceArn}` — `tagsDispatch` routes on the ARN's service segment. With a real ARN, AppSync, MSK, EKS and Scheduler each reach their own tag store. The probe's `probevalue` placeholder is not an ARN, which is why it lands on the fallback owner.
- The `/tags` fallback owner is API Gateway's ARN-keyed store, which is why `appconfig` tag operations answer 200 from the wrong service — captured in #854, not a separate finding.

## What would stop this recurring

In rough order of value:

1. A model-vs-route gate, per the `rest_operations_dev.go` note above. This is the only option that fails *before* merge. **Done** — [manifest-enforcement.md](./manifest-enforcement.md), PR #876/#921.
2. Correct the "router coverage" claim in AGENTS.md so the next contributor does not trust a guarantee that is not there. **Done**, same change.
3. Keep `scripts/route-reachability.go` runnable and run it at release-candidate time. It needs a live instance so it does not belong in unit CI as-is, but it is cheap against an RC container. **Done (#1199)** — named as a required step with the exact command in [RELEASE.md](../../RELEASE.md#creating-an-alpha-release) and the [`release` skill](../../.agents/skills/release/SKILL.md#1-smoke--is-it-alive-and-correctly-wired); the live-instance requirement is satisfied by the RC container the Smoke pass already starts.
4. When adding a service, the AGENTS.md checklist step "verify no custom endpoints were introduced" needs teeth. Every finding here is a custom endpoint that passed that step. **Done (#1199)** — `TestAllDeclaredCapabilitiesAreReachable` (`tests/integration/router/route_reachability_dev_test.go`) runs the same reachability sweep in-process on every PR under `-tags slim,dev`, via the shared `internal/routecheck` package, with no live instance or unit-CI cost the way the script itself would need. This is a stronger, per-PR version of point 3's release-time check, not a duplicate of it: the script proves the exact built RC binary/image is wired correctly (real build tags, real embedded assets, a real Docker daemon behind container-backed services); the in-process test proves the same wire-level claim continuously, at the moment a capability is declared, on plain `router.New` rather than the shipped artifact.
