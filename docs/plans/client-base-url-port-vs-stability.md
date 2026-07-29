---
title: ClientBaseURL cannot serve both a stable issuer and a dialable resource URL
description: With OVERCAST_HOSTNAME set and the API port remapped, host-routed URLs carry the listen port and are undialable. The obvious fix breaks the Cognito OIDC issuer, which needs the opposite. Found during alpha.26 pre-release smoke testing.
---

# `ClientBaseURL` cannot serve both a stable issuer and a dialable resource URL

**Status:** open, deliberately not fixed for alpha.26 · **Found:** 2026-07-29
**Severity:** AppSync `uris`, API Gateway v2 `apiEndpoint` and Lambda function URLs are undialable
from the host when `OVERCAST_HOSTNAME` is set *and* the API port is remapped. Correct on the default
1:1 mapping, and correct whenever `OVERCAST_HOSTNAME` is unset.
**Pre-existing:** yes — the responsible line is in `v0.0.1-alpha.25`. **Not** an alpha.26 regression.
**Related:** #318 (per-caller queue URLs), #331 (published port), #351/#353 (output origins), #378
(host-routed grammar), and the Cognito OIDC issuer fix in this same release.

## Symptom

Measured against a real instance, `docker run -p 4652:4566` with
`OVERCAST_HOSTNAME=localhost.overcast.sh` and the Docker socket mounted (so the published port *is*
discoverable):

| Advertised URL | Port | Dialable from the host |
| --- | --- | --- |
| SQS `QueueUrl` | 4652 | ✅ |
| AppSync `uris.GRAPHQL` | **4566** | ❌ confirmed unreachable |
| API Gateway v2 `apiEndpoint` | **4566** | ❌ |

With `OVERCAST_HOSTNAME` unset, the same instance advertises the published port correctly — so the
defect is specific to the configured-hostname path.

This combination is not exotic: `scripts/run-test-instance.sh` picks a remapped port, and
`docs/dev/manual-testing.md` tells people to set `OVERCAST_HOSTNAME=localhost.overcast.sh` for CDK.

## Mechanism

`serviceutil.ClientBaseURL` takes the configured port unconditionally once a hostname is set:

```go
if cfg != nil && cfg.Hostname != "" {
    port := cfg.Port          // the LISTEN port
    if port <= 0 { port = requestPort(r) }
```

`cfg.PublishedPort` — the field that exists precisely to record the host-reachable port — is never
referenced anywhere in `internal/serviceutil`. The helper structurally cannot know it.

## Why the obvious fix is wrong

Taking the caller's port (matching `internal/services/cloudformation`'s own `clientBaseURL`, and
SQS's per-caller minting) fixes the table above and passes the whole suite **except** Cognito:

```
issuerURL = "http://overcast.local:39783/us-east-1/us-east-1_abc123"
     want   "http://overcast.local:4566/us-east-1/us-east-1_abc123"
```

That is not a stale test. The Cognito OIDC issuer was deliberately changed **in this release** to
come from the configured origin rather than the caller's Host, because an OIDC client validates
`iss` exactly and fetches signing keys from `{iss}/.well-known/jwks.json`. A per-caller `iss` means a
token minted for a host caller fails validation for a container caller, and vice versa — the failure
mode that fix removed.

So the two consumers want opposite things from one helper:

| Consumer | Needs | Why |
| --- | --- | --- |
| Cognito `iss`, OIDC discovery | **one stable origin** | the claim is compared literally; JWKS is fetched from it |
| AppSync `uris`, `apiEndpoint`, `FunctionUrl`, queue URLs | **per-caller reachability** | the holder has to dial it |

This was attempted, measured, and reverted rather than shipped: changing it is a design decision
about two distinct notions of "the external base", not a release patch.

## Suggested fix

Split the concept, rather than making one function guess:

1. `ClientBaseURL` (or a renamed `CanonicalBaseURL`) keeps today's behaviour — a single stable origin
   from configuration. Cognito's issuer and OIDC discovery keep using it.
2. Add `ReachableBaseURL(cfg, r)`: configured hostname, **caller's** port, falling back to
   `cfg.PublishedPort`, then `cfg.Port`. Point the resource-URL minters at it — `HostRoutedURL`,
   AppSync's `uris`, API Gateway v2's `apiEndpoint`, Lambda function URLs.
3. Note that `internal/services/cloudformation`'s private `clientBaseURL` already implements exactly
   (2). Two helpers with the same name and opposite precedence is itself a trap — fold that one into
   the shared helper when splitting.

The audit doc `harness-representativeness-audit.md` warns that changes here move assertions across
the whole repo, so run the full suite, not scoped packages. That warning is accurate: the Cognito
collision only appeared in the full run.

## Reproducing

```sh
docker run -d --name oc -e OVERCAST_HOSTNAME=localhost.overcast.sh -p 4652:4566 \
  -v /var/run/docker.sock:/var/run/docker.sock overcast:latest
aws --endpoint-url http://localhost:4652 appsync create-graphql-api \
  --name t --authentication-type API_KEY --query 'graphqlApi.uris.GRAPHQL' --output text
# => http://<id>.appsync-api.us-east-1.localhost.overcast.sh:4566/graphql   (4566, not 4652)
```

## Workaround

Leave `OVERCAST_HOSTNAME` unset, or publish the API on its own port (`-p 4566:4566`). Either makes
every advertised URL dialable.
