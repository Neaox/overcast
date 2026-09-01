---
title: "API Gateway — Amazon API Gateway"
description: "API Gateway (REST v1 and HTTP v2) uses a REST API with path-based routing. REST API v1 is mounted at /restapis, HTTP API v2 at /v2/apis."
section: "Service Reference"
tags:
  - amazon
  - api
  - apigateway
  - docs
  - gateway
  - services
---

# API Gateway — Amazon API Gateway

API Gateway (REST v1 and HTTP v2) uses a REST API with path-based routing.
REST API v1 is mounted at `/restapis`, HTTP API v2 at `/v2/apis`.

---

## Known limitations

- **No VTL template mapping.** Integration request/response templates are not evaluated as VTL — values are passed through as-is.
- **Partial authorizer enforcement.** `COGNITO_USER_POOLS` (REST v1) and `JWT` (HTTP v2) authorizers are validated (RS256 signature + expiry + issuer/audience). Lambda (`TOKEN`, `REQUEST`) and IAM authorizers are stored but not enforced at request time.
- **No request validation.** Request validators are stored but not enforced at request time.
- **No WebSocket execution.** WEBSOCKET protocol type is accepted on creation but execution is not implemented.
- **Usage counters are not persisted.** Quota and throttle state lives in memory so the request path never hits the store; restarting Overcast resets it. Real API Gateway carries a quota across the whole period.
- **Usage plans only apply to REST v1.** As on AWS, HTTP APIs (v2) have no API-key or usage-plan concept, so nothing is measured or enforced there.

## Usage plan throttling and quotas

A method with `apiKeyRequired: true` resolves the caller's `x-api-key` to an
API key and to the usage plan covering `{restApiId, stage}` — a request with no
such plan is already refused with `403 Forbidden`. Once the plan is found, its
limits are **measured on every request**:

- **Throttle** — `throttle.rateLimit` is the tokens-per-second refill and
  `throttle.burstLimit` the bucket capacity, the token-bucket model AWS
  documents. The bucket is per (usage plan, API key), not per plan.
- **Quota** — `quota.limit` requests per `quota.period` (`DAY`, `WEEK`,
  `MONTH`) in calendar-aligned UTC windows; `quota.offset` is subtracted from
  the limit in the first period only. Windows roll over on the injected clock,
  so tests can fast-forward them.

Read the counters back with `GetUsage`, which returns AWS's daily
`[used, remaining]` log per API key (a date range wider than 400 days is
refused with `BadRequestException` — AWS documents no such cap, but the
response carries one entry per day per key). Reaching a limit also logs a warning and
publishes an `apigateway:Throttled` event — visible on the web UI's Events page
and on the Usage Plans page — coalesced to at most one per key per second.

**Rejection is opt-in and off by default.** Overcast is not a rate-limiting or
load-testing tool, and switching enforcement on can turn a local stack that
works today into one that gets `429`s it never used to. So by default an
over-limit request is counted, reported, and then **served normally**. Set
`OVERCAST_ENFORCE_APIGATEWAY_THROTTLE=true` to have over-limit requests
rejected the way AWS rejects them:

| Condition | Status | `x-amzn-ErrorType` | Body |
| --- | --- | --- | --- |
| Rate/burst exceeded (`THROTTLED`) | `429` | `TooManyRequestsException` | `{"message":"Too Many Requests"}` |
| Quota exhausted (`QUOTA_EXCEEDED`) | `429` | `LimitExceededException` | `{"message":"Limit Exceeded"}` |

A rejected request consumes neither quota nor a token, matching AWS — a `429`
does not count against the usage plan quota. A plan configuring neither a
throttle nor a quota never rejects anything, whatever the flag says.

## Invoke URLs

REST v1 and HTTP v2 APIs are reachable two ways, with identical behaviour:

- **Path-style** — `http://localhost:4566/restapis/{apiId}/{stage}/_user_request_/...`
- **Host-routed** — `http://{apiId}.execute-api.{region}.{base}/{stage}/...`,
  the shape real AWS uses. `{base}` is whatever hostname you reached Overcast
  on; set `OVERCAST_HOSTNAME=localhost.overcast.sh` so it resolves on every OS.

HTTP v2 APIs report the host-routed form in `apiEndpoint`, minted on the
hostname you called Overcast on rather than `amazonaws.com`, so
CloudFormation's `Fn::GetAtt ApiEndpoint` returns a URL you can dial. REST v1
has no such field, matching AWS — the console composes it client-side, and it
composes the host-routed form too, falling back to path-style only when the
endpoint it is connected to cannot carry a subdomain (a bare `localhost`, or an
IP). That is the URL its copy button yields.

Stack outputs that compose an invoke URL in the template (as CDK does) are also
re-hosted onto a reachable origin when returned by `DescribeStacks`. See
[networking.md](../networking.md).

<!-- BEGIN overcast:capabilities -->

## Operations

104 of 106 listed operations are implemented.
Per-operation status, notes and AWS API links: [API Gateway operations](apigateway/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/apigateway/latest/api/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
