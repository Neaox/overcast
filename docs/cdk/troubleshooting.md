---
title: "CDK troubleshooting"
description: "Symptom, cause and fix for a CDK deploy against Overcast: a bootstrap that fails, a stack that never leaves CREATE_IN_PROGRESS, and the Windows S3 asset upload."
section: "CDK"
tags:
  - cdk
  - cloudformation
  - docs
  - troubleshooting
  - windows
---

# CDK troubleshooting

When `cdk bootstrap` or `cdk deploy` misbehaves against Overcast, start here;
[Using AWS CDK](../cdk.md) has the working setup every entry assumes.

| Symptom | Cause | Fix |
| --- | --- | --- |
| `cdk bootstrap` fails | Overcast is not running, or `AWS_ENDPOINT_URL` is unset | Start Overcast and export the endpoint. Bootstrap needs S3, SSM, IAM and STS, all supported |
| A stack sits in `CREATE_IN_PROGRESS` | Provisioning runs on a background goroutine, so this is expected briefly | Wait. If it never clears, a resource handler is hung or failing — check the server logs |
| A stack ends in `ROLLBACK_COMPLETE` | A resource handler failed | The server logs name it |
| `Fn::GetAtt` returns an unexpected value | The attribute is not one of the supported ones | Unsupported attributes fall back to the resource's physical ID — see the [CloudFormation reference](../services/cloudformation.md) |
| A `--hotswap` deploy behaves differently from a full one | Hotswap bypasses CloudFormation and calls the service API directly, such as `UpdateFunctionCode` | It works wherever that operation is implemented — check the service's page |
| Some resources have no backing state | Their types are stubbed | [Resource type coverage](./resource-types.md), then [Partial resource coverage](./limitations.md#partial-resource-coverage) |
| `cdk deploy` fails on Windows with an S3 connection or DNS error | `*.localhost` subdomains do not resolve on Windows | [Below](#s3-asset-upload-fails-on-windows) |

## S3 asset upload fails on Windows

**Symptom:** `cdk deploy` fails on Windows with an S3 connection or DNS
resolution error after a successful bootstrap. The error originates in the CDK
asset publisher (Node.js), not in the CloudFormation create/update step.

**Root cause:** CDK's asset publisher sends S3 requests using virtual-hosted
style, constructing a bucket hostname from your endpoint URL:

```
cdk-hnb659fds-assets-<account>-<region>.localhost
```

On Windows, `*.localhost` subdomains do **not** resolve by default — only
`localhost` itself is in the hosts file. On Linux and macOS the system resolver
handles `*.localhost` automatically, so this issue does not affect those
platforms.

**Fix:** Use a wildcard-DNS hostname instead of `localhost`. Overcast treats
the `OVERCAST_HOSTNAME` environment variable as an additional virtual-host base,
so any `<bucket>.<hostname>` request is correctly rewritten to path-style.

Every `*.localhost.overcast.sh` subdomain resolves to `127.0.0.1` on every OS,
with no hosts-file edits — see
[Hostnames that resolve for every caller](../networking/hostnames.md):

```bash
# Start Overcast with the wildcard-DNS hostname
docker run --rm -p 4566:4566 \
  -e OVERCAST_HOSTNAME=localhost.overcast.sh \
  ghcr.io/overcast-sh/overcast:latest

# Point CDK at that hostname
export AWS_ENDPOINT_URL=http://localhost.overcast.sh:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

npx cdk bootstrap aws://000000000000/us-east-1
npx cdk deploy --require-approval never
```

CDK then constructs a bucket hostname like
`cdk-hnb659fds-assets-000000000000-us-east-1.localhost.overcast.sh:4566`, which
Overcast's S3 virtual-host middleware rewrites to the path-style route.

> [!NOTE]
> The same hostname works on Linux and macOS, so it is safe in a shared CI/CD
> environment where developers are on different host operating systems. It needs
> a public DNS lookup, so it does not work offline or behind DNS rebinding
> protection — [Hostnames that resolve for every
> caller](../networking/hostnames.md) has the fallbacks, and the other two
> wildcard domains Overcast recognises.

## Related

- [CDK limitations](./limitations.md) — the behaviours that are working as intended
- [CDK resource type coverage](./resource-types.md) — whether a type provisions for real
- [Using AWS CDK](../cdk.md) — bootstrap and deploy against Overcast
- [Hostnames that resolve for every caller](../networking/hostnames.md) — the wildcard-DNS domain and its fallbacks
- [Troubleshooting](../troubleshooting.md) — the whole-emulator symptom index
- [All documentation](../README.md) — every guide and service page
