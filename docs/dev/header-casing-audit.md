# HTTP header name casing: fidelity audit

Contributor-depth notes on how Overcast handles HTTP header **names** — on the wire, in
authorisation checks, and in the event payloads it hands to resolvers and Lambda functions.
It exists because "the emulator title-cases my header" is a recurring bug report, and the
answer is different for each of those three layers. Read this before "fixing" a casing
complaint: two of the three layers are working as intended, and the one that wasn't had a
narrow, specific cause.

Written up from a report that `POST /_overcast/appsync/apis/<api-id>/graphql` with
`--header 'x-api-key: da2-...'` showed the header as `X-Api-Key`.

---

## The three layers, and which one was actually broken

There are three distinct mechanisms that people conflate:

1. **The wire.** What bytes the server writes for a response field name. Governed by the
   HTTP version. **Correct in Overcast — verified empirically, see below.**
2. **Go's in-process representation.** `net/http` canonicalises every incoming field name
   through `textproto.CanonicalMIMEHeaderKey`, so a client's `x-api-key` is stored in
   `r.Header` as `X-Api-Key`. `r.Header.Get("x-api-key")` still matches because `Get`
   canonicalises its argument too. **Not a bug, and not observable by users** — unless it
   escapes into layer 3.
3. **Emulated AWS event payloads.** The map of header names AppSync puts in
   `$context.request.headers`, or API Gateway puts in a proxy event's `headers`. Here AWS
   has its own documented casing, which owes nothing to Go. **This is where the reported
   bug was**: Go-canonical names were leaking straight through into resolver context.

SigV4's canonical-request lowercasing is a fourth, unrelated mechanism (it lowercases
names only to compute a signature) and is not in scope here.

---

## Layer 1 — the wire (no defect)

RFC 9113 §8.2.1 requires HTTP/2 field names to be lowercase and says a name containing
uppercase MUST be treated as malformed; HTTP/3 inherits this. HTTP/1.1 (RFC 9110 §5.1)
makes field names case-**insensitive**, so no wire rule is engaged by the reported repro,
which is plain `curl` to `http://localhost:4566`.

Overcast does serve h2c (`cmd/overcast/cmd_serve.go:187` wraps the handler with
`h2c.NewHandler`), so HTTP/2 is genuinely reachable. Go's `x/net/http2` server lowercases
field names when encoding HEADERS frames, independently of the canonical casing held in
the `w.Header()` map.

**Verified empirically.** A throwaway h2c server running the same handler shape as
`internal/services/appsync/handler.go:123-124` (`w.Header().Set("Content-Type", …)`,
`w.Header().Set("x-amzn-requestid", …)`) was driven by a hand-rolled HTTP/2 client that
HPACK-decoded the raw response HEADERS frame. Every field name came back lowercase:

```
":status": "200"
"content-type": "application/json"
"x-amzn-requestid": "req-1"
"content-length": "2"
"date": "..."
```

The same probe sent `x-api-key` as a lowercase HTTP/2 request field and confirmed the
handler's `r.Header.Get("x-api-key")` still read it.

**No manual header emission exists to break this.** A sweep for hand-built header blocks
found none in any HTTP path: every response name goes through `w.Header()`. The `\r\n`
string-building hits are `internal/smtp/mailer.go` (RFC 5322 email headers — a different
protocol) and `internal/mcp/lsp_client.go` (`Content-Length` framing over stdio, not
HTTP). The only `Hijack()` implementations are pass-through wrappers in
`internal/middleware/drainbody.go:110` and `internal/middleware/logger.go:314`, used so
`github.com/coder/websocket` can perform its own upgrade.

**Verdict: the RFC 9113 rule is real and Overcast already complies.** Nothing to fix.

---

## Layer 3 — event payloads, surface by surface

### AppSync `$context.request.headers` / `ctx.request.headers` — was broken, now fixed

**AWS:** the resolver context reference addresses headers by their lowercase name
throughout — a `custom:nadia` request header is read as `$context.request.headers.custom`
(VTL) / `ctx.request.headers.custom` (JS), and `x-api-key` likewise stays lowercase.
It also states: "AWS AppSync doesn't expose the cookie header in
`$context.request.headers`."
Sources:
[resolver-context-reference.html](https://docs.aws.amazon.com/appsync/latest/devguide/resolver-context-reference.html)
§ Access request headers, and its JS twin
[resolver-context-reference-js.html](https://docs.aws.amazon.com/appsync/latest/devguide/resolver-context-reference-js.html).
Whichever rule AWS applies internally (lowercase, or preserve-as-sent), it never
title-cases a lowercase header the way Go does — so Go-canonical output is wrong either
way for the reported input.

**Overcast, before:** `flattenHeaders` (`internal/services/appsync/handler_execute.go`)
copied `http.Header` keys verbatim, i.e. Go-canonical. That map is the single choke point
for four consumer-visible surfaces:

| Consumer | Call site |
| --- | --- |
| JS (`APPSYNC_JS`) `ctx.request.headers` | `handler_execute.go:1179` |
| VTL `$context.request.headers` | `handler_execute.go:1411` |
| Direct Lambda resolver event `request.headers` | `handler_execute.go:2756` |
| Lambda authorizer event `requestHeaders` | `handler_execute.go:291` |

**User-visible: yes, and it silently returns null.** VTL map access is an exact Go map
lookup (`vtl_evaluator.go:1740`, `case "get"`), so a resolver written from the AWS docs as
`$ctx.request.headers.get("x-api-key")` — or `ctx.request.headers['x-api-key']` in JS —
found nothing, while `X-Api-Key` worked. Authentication itself was never affected:
`authenticateAPIKey` uses `r.Header.Get("x-api-key")` (`handler_execute.go:168`), which
canonicalises its argument.

**Fix (applied, small):** `flattenHeaders` now lowercases on the way out.

`resolveContext.Headers` is assigned raw `r.Header` at `handler_execute.go:673` and `:809`,
which looks like a second leak but is not: those are internal carriers, and every read of
`.Headers` in the package goes through `flattenHeaders` (the four rows above are the
complete list). No resolver can observe the raw value, so both sites are correct as-is —
the single choke point is what makes one fix cover every execution path.

The subscription path is likewise clean. `realtimeAuthRequest`
(`handler_subscriptions.go:151`) clones `r.Header` only to feed `authenticateRequest`,
which reads via `Header.Get` (case-insensitive); `handleSubscriptionStart` registers the
subscription without ever building a `resolveContext`, so subscription resolvers never see
a header map at all.

### API Gateway REST (v1) proxy event — acceptable divergence, one value bug fixed

**AWS:** the documented input format shows
`curl … -H 'header1: value1' -H 'header2: value1' -H 'header2: value2'` producing
`"headers": {"header1": "value1", "header2": "value2"}` alongside
`"multiValueHeaders": {"header2": ["value1","value2"]}` — i.e. **the last value** in
`headers`, all values in `multiValueHeaders`. Names appear as the client sent them.
Source:
[set-up-lambda-proxy-integrations.html](https://docs.aws.amazon.com/apigateway/latest/developerguide/set-up-lambda-proxy-integrations.html)
§ Input format of a Lambda function for proxy integration.

**Overcast:** `handler_execution.go` builds both maps from `r.Header`, so names are
Go-canonical.

**Casing: not fixed, and deliberately so.** Go discards the client's original casing during
parsing — it is simply not recoverable from `r.Header` without a custom HTTP parser. Of the
two available approximations, Go-canonical is the closer one for REST: real API Gateway
injects its own title-cased names into this event (`Accept`, `X-Forwarded-For`,
`X-Amzn-Trace-Id`, `CloudFront-Forwarded-Proto`), so a blanket lowercase would be wrong for
the majority of entries. Recorded here as a known, low-impact divergence rather than a
defect. Revisiting it would mean a custom header parser — large, and not worth it.

**Value selection: fixed (one line).** The code took `vals[0]` for both `headers` and
`queryStringParameters`; AWS documents the last value. Now `vals[len(vals)-1]`.

### API Gateway HTTP API (v2) proxy event — 2.0 correct, 1.0 fixed

**AWS:** "The following examples show the structure of each payload format version. **All
headernames are lowercased.**" — this sentence governs **both** the 1.0 and 2.0 examples on
that page, so an HTTP API lowercases header names even when the integration is configured
for payload format 1.0. Format 2.0 additionally has no `multiValueHeaders`: "Duplicate
headers are combined with commas and included in the `headers` field. Duplicate query
strings are combined with commas and included in the `queryStringParameters` field," and
cookies move to a `cookies` array. Format 1.0 keeps the REST shape (single value in
`headers`, list in `multiValueHeaders`).
Source:
[http-api-develop-integrations-lambda.html](https://docs.aws.amazon.com/apigateway/latest/developerguide/http-api-develop-integrations-lambda.html)
§ Payload format differences / § Payload format structure.

**Overcast, payload 2.0:** already correct — lowercased names, comma-joined values,
`cookies` array (`handler_execution.go`, `executeV2LambdaProxy`).

**Overcast, payload 1.0 — was wrong two ways, now fixed.** The 1.0 branch reused the
2.0-shaped `headers` map (comma-joined values) while building `multiValueHeaders` from raw
`r.Header`, so a single event carried `headers: {"header2": "value1,value2"}` next to
`multiValueHeaders: {"Header2": [...]}` — lowercase in one field, Go-canonical in the
other, and a comma-joined value where AWS documents a single one. The branch now lowercases
both maps and uses last-value semantics for `headers` and `queryStringParameters`.

### Lambda function URLs — not implemented

**AWS:** "The request and response event formats follow the same schema as the Amazon API
Gateway payload format version 2.0", so header names are lowercased and duplicates
comma-joined, exactly as for HTTP APIs.
Source:
[urls-invocation.html](https://docs.aws.amazon.com/lambda/latest/dg/urls-invocation.html)
§ Request and response payloads.

**Overcast:** no function-URL support exists (no `FunctionUrlConfig` / `lambda-url` handling
in `internal/services/lambda`). Nothing to diverge yet — but whoever adds it should build
the event from the v2 path in `internal/services/apigateway/handler_execution.go`, not the
v1 one.

---

## Sibling issues found and not fixed

Each of these is the same class of problem — the emulator normalising something AWS
preserves, or preserving something AWS normalises — but each is a judgement call rather
than a clear-cut fix.

**AppSync drops repeated header values (medium).** `flattenHeaders` keeps only
`vals[0]`, and its comment asserted this was AWS behaviour. The docs say otherwise: "You
can also pass multiple headers in a single request… You could then access these as an
array, such as `ctx.request.headers.custom[1]`"
([resolver-context-reference-js.html](https://docs.aws.amazon.com/appsync/latest/devguide/resolver-context-reference-js.html)).
Fixing it means changing the return type from `map[string]string` to `map[string]any` so a
repeated name yields a list, which ripples into VTL, JS, the direct Lambda event and the
authorizer event simultaneously. Deferred as too broad for a casing fix; the misleading
comment has been corrected to flag it. **Size: medium**, mostly test churn.

**AppSync HTTP data source canonicalises outbound header names (low).**
`handler_execute.go:1703` sends the request mapping template's `params.headers` via
`req.Header.Set(k, v)`, which canonicalises whatever the template author wrote. Over
HTTP/1.1 to a local origin this is invisible in practice. Fixing it means writing directly
into the `http.Header` map to bypass canonicalisation. **Size: small**, but low value.

**Response header names are Go-canonical over HTTP/1.1 (cosmetic, won't fix).**
`writeLambdaProxyResponse` (`handler_execution.go`) uses `w.Header().Set`, so a Lambda
returning `content-type` gets `Content-Type` on an HTTP/1.1 response, whereas real API
Gateway HTTP APIs serve HTTP/2 and emit lowercase. Layer 1 above shows Overcast already
emits lowercase over h2c, so this only shows up on HTTP/1.1 — where names are
case-insensitive by spec. Not worth a custom writer.

**Query-string and form-field names (checked, no issue).** Query parameter names are
case-**sensitive** in both AWS and Overcast, and `r.URL.Query()` preserves them exactly.
`internal/middleware/iam_enforce.go` lowercases only header *values* it inspects
(`Content-Type`), not names.

**Header names compared case-sensitively (checked, no issue found).** Sweeps for
`strings.HasPrefix(k, "X-…")`-style comparisons over raw header keys turned up only
`internal/bff/bff.go:587`, which lowercases before comparing
(`strings.HasPrefix(strings.ToLower(k), "x-amz-meta-")`). Everything else reads through
`Header.Get`/`Values`, which canonicalise.

---

## Note for triagers

A `HEADERS::::::` style log line in a bug report screenshot is **not** from this codebase —
no such string exists in the tree or at HEAD. That is local debug scaffolding on the
reporter's side. The emulator only logs full request headers on 5xx responses, via
`zap.Any("request_headers", …)` at `internal/middleware/logger.go:383`, and that field
prints the Go-canonical `http.Header` map by design (it is a Go-internals debug aid, not an
emulated AWS surface).

---

## Regression tests

| Test | Package |
| --- | --- |
| `TestFlattenHeaders_lowercasesHeaderNames` | `internal/services/appsync` |
| `TestBuildDirectLambdaResolverEvent_defaultDomainAndHeaders` | `internal/services/appsync` |
| `TestExecuteRestLambdaProxy_duplicateHeaderUsesLastValue` | `internal/services/apigateway` |
| `TestExecuteV2LambdaProxy_payloadFormatOneLowercasesHeaderNames` | `internal/services/apigateway` |
