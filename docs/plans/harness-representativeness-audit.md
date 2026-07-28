# Integration-harness audit — unrepresentative defaults

> Status: **complete — all findings fixed**, 2026-07-28. Branch
> `claude/inspiring-jennings-42768f`.
> Follows [host-routing-precedence.md](./host-routing-precedence.md), which documents
> the original instance: `defaultTestConfig()` left `OVERCAST_HOSTNAME` unset, so
> Lambda minted `{urlId}.lambda-url.us-east-1.127.0.0.1:PORT` and the Docker-gated
> round-trip test passed against an IP base that matches no virtual-host rule.
>
> Scope: `tests/helpers/server.go` defaults, and the client-facing URL and ARN
> minting they fail to exercise. Four findings, three real; six tests, all green.
>
> Each finding below records the evidence as it stood *before* the fix — the
> failing output is what justifies the change, so it is kept rather than rewritten
> in the past tense. The **Fix** paragraph at the end of each section states what
> landed. Line numbers in the finding sections are therefore pre-fix positions;
> the links resolve to the file, not the line.

## Summary

| # | Finding | Verdict | Test | Fix |
| --- | --- | --- | --- | --- |
| 1 | `Host: "127.0.0.1"` | **No gap** — nothing client-facing derives from it | — | none needed |
| 2 | `Port: 0` makes `Config.ExternalBaseURL()` return `http://localhost:0` | **Real gap** — harness | `TestQueueURL_awsHostFallbackIsDialable` | bind listener first, write real port into `cfg.Port` |
| 3 | Cognito mints `iss` from `r.Host` and a hardcoded `http://` | **Real gap** — product, 2 axes | 3 tests (below) | both sites via `serviceutil.ClientBaseURL` |
| 4 | Invoke-event / ARN account IDs hardcoded, not read from config | **Real gap** — product, 7 sites | 2 tests (below) | `accountID()` helper per service |

TLS (the brief's candidate 2) is not a finding on its own — it is the mechanism
that hides half of finding 3, and is written up there.

## 1. `Host: "127.0.0.1"` — no gap

`cfg.Host` reaches exactly three places, none of which mint anything a client
consumes:

- `Config.Addr()` ([config.go:485](../../internal/config/config.go)) — the listen
  address, which httptest supplies itself.
- `internal/router/debug.go:158` and `internal/mcp/runtime_provider.go:3552,3786`
  — echoed back as diagnostic metadata.

No test asserts the echoed value; the one place `"0.0.0.0"` appears in a test
(`internal/mcp/repo_provider_test.go:1345`) is a hand-written mock response
string, not an assertion against a harness server. The harness value does diverge
from the real default (`0.0.0.0`, [config.go:691](../../internal/config/config.go)),
but nothing observable depends on it being an IP or on it being either value.
**Recorded and closed** — changing it would be churn.

## 2. `Port: 0` makes `Config.ExternalBaseURL()` degenerate

`Config.ExternalBaseURL()` formats `c.Port` verbatim
([config.go:499-505](../../internal/config/config.go)). The harness leaves `Port`
at 0 because httptest assigns the real port after the config is built, so on
**every** test server:

```
cfg.ExternalBaseURL()  =  "http://localhost:0"        <- no client can dial this
ts.ExternalBase()      =  "http://localhost:39783"    <- the real bound port
srv.URL                =  "http://127.0.0.1:39783"    <- the dial address
```

`serviceutil.ClientBaseURL` is immune — it explicitly falls back to the request
port when `cfg.Port <= 0` ([request.go:123-126](../../internal/serviceutil/request.go),
covered by `TestClientBaseURL_configuredHostnameWithDynamicPort`). But the
services that call `cfg.ExternalBaseURL()` **directly** get `:0`: SQS
([handler.go:108,116](../../internal/services/sqs/handler.go)), the
CloudFormation provisioner ([provisioner.go:1970,1977,2028](../../internal/services/cloudformation/provisioner.go)),
SNS ([handler_publish.go:233](../../internal/services/sns/handler_publish.go)),
ECR, and AppSync's context fallback.

Two divergent notions of "the external base" is the underlying defect; `:0` is
how it becomes visible.

### Why no test catches it

Both sides of every affected assertion are the same call, so they agree on any
value:

- `TestQueueURL_awsHostFallsBackToConfiguredOrigin`
  ([endpoint_addressing_test.go:84](../../tests/integration/sqs/endpoint_addressing_test.go))
  builds `want` from `srv.Config.ExternalBaseURL()` — the same expression the
  handler evaluates. Verified: it passes today asserting
  `http://localhost:0/000000000000/aws-host-queue`.
- The CloudFormation checks at
  [cloudformation_test.go:1303,1370,1428](../../tests/integration/cloudformation/cloudformation_test.go)
  build a queue URL the same way and then feed it back to `ReceiveMessage`. That
  round-trips through Overcast's own parser, which takes the queue name with
  `path.Base` and never looks at the port — so a `:0` origin is accepted and
  never dialed.

This matters because a queue URL is the one resource URL an SDK is *guaranteed*
to dial: `@aws-sdk/middleware-sdk-sqs` replaces the resolved endpoint with the
QueueUrl's origin, and .NET and Java v1 use it as the request URI outright (see
[clientendpoint.go](../../internal/middleware/clientendpoint.go)).

**Reproducing test:** `TestQueueURL_awsHostFallbackIsDialable`
([tests/integration/sqs/endpoint_fallback_test.go](../../tests/integration/sqs/endpoint_fallback_test.go)).

```
QueueUrl = "http://localhost:0/000000000000/aws-host-dialable",
     want "http://localhost:40023/000000000000/aws-host-dialable"
```

**Fix.** `NewTestServer` now binds the listener with `httptest.NewUnstartedServer`
*before* calling `router.New`, reads the OS-assigned port off
`srv.Listener.Addr()`, writes it into `so.cfg.Port`, then attaches the handler and
starts serving ([tests/helpers/server.go](../../tests/helpers/server.go)).

Ordering is the load-bearing part. Setting the port after `httptest.NewServer`
would be a data race: `router.New` starts background init goroutines that may read
the config, so the value has to be final before that call, not after it. Binding
first also means any service reading `cfg.Port` at construction gets the real
value.

`ExternalBase()` was left as-is. It derives the port from `ts.URL` and now agrees
with `cfg.ExternalBaseURL()` by construction, so it stays a correct accessor
rather than a second source of truth.

## 3. Cognito issuer URLs ignore both the configured hostname and TLS

This is the same defect as the original Lambda bug, in a different service.

```go
// internal/services/cognito/handler_auth.go:307
func (s *Service) issuerURL(r *http.Request, poolID string) string {
	return "http://" + r.Host + "/" + s.region(r.Context()) + "/" + poolID
}
```

14 call sites route through it, plus a duplicated copy of the same expression for
the OIDC discovery document
([handler_managed_login.go:1334-1335](../../internal/services/cognito/handler_managed_login.go)).
Two independent defects:

**a) Host.** The issuer is the address the *authenticating client* dialed, not the
address Overcast is configured to advertise. `OVERCAST_HOSTNAME` is ignored
entirely. Every other client-facing URL in the codebase goes through
`serviceutil.ClientBaseURL`; this is the outlier.

**b) Scheme.** `http://` is hardcoded. With `OVERCAST_TLS_CERT`/`OVERCAST_TLS_KEY`
set the server serves https ([cmd_serve.go:306](../../cmd/overcast/cmd_serve.go)),
but every minted token claims an http issuer and `jwks_uri` points at http.

`iss` is not decoration: an OIDC client validates it against its configured
issuer and fetches `{iss}/.well-known/jwks.json` for keys. A token whose issuer
the validating party cannot dial — a sibling container handed `127.0.0.1`, or a
host CLI handed a compose service name — fails verification rather than
degrading. Under TLS the client either rejects the issuer or requests keys over a
scheme the server does not answer.

### Why no test catches it

Defect (a) was **undetectable before the harness fix**: with `Hostname` unset,
`r.Host` and the configured hostname were the same string. It is detectable now
only because the harness defaults `Hostname` to `localhost` while httptest still
dials `127.0.0.1`.

Defect (b) is undetectable **today**: no test anywhere sets
`TLSCertFile`/`TLSKeyFile`, there is no `WithTLS` harness option, and so
`cfg.TLSEnabled()` is false in every server the suite builds. The https branches
of `serviceutil.ClientBaseURL` ([request.go:120](../../internal/serviceutil/request.go))
and `config.ExternalBaseURL` ([config.go:501](../../internal/config/config.go)) run
only in `internal/config`'s own unit tests; the `buildFunctionURL` https path
([handler_url.go:124](../../internal/services/lambda/handler_url.go)) has no cover
at all. Nothing pins the scheme of any minted URL.

Two related sites were checked and are **correct**: AppSync's `websocketBaseURL`
handles `https` → `wss` before `http` → `ws`
([handler.go:389-397](../../internal/services/appsync/handler.go)), and every
`region()`/`accountID()` helper of the form "config value, else literal default"
is a sound nil-safe fallback.

**Reproducing tests:**

- `TestIssuerURL_honoursConfiguredHostnameAndTLS`
  ([internal/services/cognito/handler_auth_issuer_test.go](../../internal/services/cognito/handler_auth_issuer_test.go))
  — covers both axes at unit level, including the TLS case the integration suite
  cannot express.
- `TestIssuer_usesConfiguredHostnameNotDialAddress` and
  `TestOIDCDiscovery_usesConfiguredHostname`
  ([tests/integration/cognito/cognito_issuer_test.go](../../tests/integration/cognito/cognito_issuer_test.go))
  — end-to-end through real authentication and the discovery endpoint.

```
IdToken iss = "http://127.0.0.1:37251/us-east-1/us-east-1_413B7529",
       want "http://localhost:37251/us-east-1/us-east-1_413B7529"
issuerURL   = "http://127.0.0.1:39783/us-east-1/us-east-1_abc123",
       want "https://overcast.local:4566/us-east-1/us-east-1_abc123"
```

**Fix.** Both expressions now go through `serviceutil.ClientBaseURL`, which
resolves hostname, port and scheme together from config and falls back to the
request's own Host when no external hostname is set — so both axes are fixed at
once and the duplicated copy is gone
([handler_auth.go](../../internal/services/cognito/handler_auth.go),
[handler_managed_login.go](../../internal/services/cognito/handler_managed_login.go)).

Token validation is unaffected: `ValidateCognitoToken` derives the pool ID from
the issuer with `poolIDFromIssuer`, which takes the trailing path segment
([jwt.go:142](../../internal/services/cognito/jwt.go)). It never compares the
issuer against a recomputed value, so changing the origin cannot invalidate a
token.

Out of scope but noted: `issuerURLTyped`
([typed_logic.go:714](../../internal/services/cognito/typed_logic.go)) returns a
bare `region + "/" + poolID` — no scheme, no host — so a device-auth token
carries a structurally different `iss` from a password-auth one. A third
spelling of the same value, worth folding into the same fix.

## 4. Hardcoded account IDs in invoke events and ARNs

The harness account-ID default (`000000000000`) is also the literal five handlers
hardcode, so an assertion built from either side agrees with the other whether or
not the value was ever read from config. Only a server configured with a
different account ID can tell them apart — and no test uses `WithAccountID` on
any of these paths.

| Site | Field | Consumer |
| --- | --- | --- |
| [lambda/handler_url_invoke.go:190](../../internal/services/lambda/handler_url_invoke.go) | `requestContext.accountId` | customer handler code |
| [apigateway/handler_execution.go:221](../../internal/services/apigateway/handler_execution.go) | `requestContext.accountId` (REST v1 proxy) | customer handler code |
| [apigateway/handler_execution.go:442](../../internal/services/apigateway/handler_execution.go) | `requestContext.accountId` (HTTP API v2) | customer handler code |
| [apigateway/handler_execution.go:498](../../internal/services/apigateway/handler_execution.go) | `requestContext.accountId` (v2 route, v1 payload) | customer handler code |
| [cloudfront/handler_functions.go:45](../../internal/services/cloudfront/handler_functions.go) | `FunctionMetadata.FunctionARN` | SDK / CDK |

Every one of these handlers already holds a `*config.Config`. The CloudFront case
is the clearest evidence this is oversight rather than convention: the *same
service* reads config correctly for distribution ARNs
([handler.go:93,643](../../internal/services/cloudfront/handler.go), via
`protocol.DistributionARN(h.cfg.AccountID, id)`), so a function and a
distribution created on one server report different accounts.

**Reproducing tests:**

- `TestCreateFunction_arnUsesConfiguredAccountID`
  ([tests/integration/cloudfront/cloudfront_account_test.go](../../tests/integration/cloudfront/cloudfront_account_test.go)).
  Note the existing `parsedFunctionSummary` in `cloudfront_test.go` does not
  parse `FunctionMetadata` at all, so the ARN was unasserted on both counts.
- `TestBuildFunctionURLEvent_accountIDFromConfig`
  ([internal/services/lambda/handler_url_invoke_account_test.go](../../internal/services/lambda/handler_url_invoke_account_test.go)).
  Written as a unit test because the only integration cover for function-URL
  events is Docker-gated (`TestInvokeFunctionURL_hostRouted_success`), so a run
  without Docker would skip the finding entirely.

```
FunctionARN = "arn:aws:cloudfront::000000000000:function/account-func",
       want "arn:aws:cloudfront::111122223333:function/account-func"
requestContext.accountId = "000000000000", want "111122223333"
```

The three API Gateway sites are **not** covered by a test here: the request
context is only built while invoking a Lambda, which needs Docker, and the
`NodeRuntime` stub does not execute code so it cannot echo the event back. They
are the same one-line defect as the Lambda site, which the unit test above pins.

**Fix.** Each service gained an `accountID()` helper of the form already used by
elasticache, rds and cloudtrail — config value, else the standard emulator
account for handlers constructed without config — and all sites now call it.

Two sites beyond the five above turned up while fixing and are included, since
both are the identical defect in code being touched anyway:

- [cloudfront/handler_monitoring.go:145](../../internal/services/cloudfront/handler_monitoring.go)
  — the realtime-log-config ARN, hardcoded the same way as the function ARN.
- [lambda/handler_functions.go](../../internal/services/lambda/handler_functions.go)
  `isForeignAccountLayerARN` already did the config lookup correctly, but inline;
  it now calls the helper, so the idiom appears once per service.

That last one is worth noting because it is the counter-example that shows the
others are oversight rather than convention: the same file, in the same service,
already knew to read config.

### Region defaults — no gap found

`us-east-1` was checked separately and is clean. Every occurrence in service code
is either a nil-safe fallback behind a `cfg.Region` check (elasticache, rds,
cloudtrail, backup, transfer, appsync), derived from a source ARN (pipes), or
explicitly unused (`_ = region` in
[cloudfront/handler_functions.go:62](../../internal/services/cloudfront/handler_functions.go)).
No region literal reaches a client-facing value unconditionally.

## Verification

All six tests green. `go vet -tags slim ./...` clean, `gofmt -l` clean, full
`go test ./...` green.

The full-suite run matters more than usual here: finding 2's fix changes
`cfg.Port` on **every** test server in the repo, so any assertion anywhere that
was anchored to the degenerate `http://<hostname>:0` base would move. Scoped runs
cannot establish that, which is why the whole suite was run rather than the five
affected packages.

No test assertion was rewritten to accommodate a fix. The existing
`TestQueueURL_awsHostFallsBackToConfiguredOrigin` still builds its expectation
from `Config.ExternalBaseURL()` and still passes — it is simply no longer vacuous,
because that call now returns a dialable base. Retargeting it at `ExternalBase()`
would be equivalent and is not worth the churn.
