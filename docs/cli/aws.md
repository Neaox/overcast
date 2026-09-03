---
title: "Pointing AWS tools at Overcast"
description: "overcast env prints AWS_* exports for your shell to eval; overcast aws runs one AWS CLI call against the daemon with the ambient AWS environment scrubbed first."
section: "Reference"
tags:
  - aws
  - cli
  - docs
  - env
  - overcast
  - shell
---

# Pointing AWS tools at Overcast

`overcast env` redirects a whole shell; `overcast aws` redirects a single call.
Both clear the ambient `AWS_*` environment first, so a leftover `AWS_PROFILE`
or `AWS_ENDPOINT_URL` cannot quietly send the request to real AWS.

```bash
eval "$(overcast env)"        # this shell, from here on
overcast aws s3 mb s3://my-bucket
```

Part of the [CLI reference](../cli.md). SDK, Terraform and per-language setup
is in [Using AWS SDKs and CLI](../sdk-cli.md).

## `overcast env`

Prints `AWS_*` assignments for your shell to evaluate, preceded by an unset
line for every `AWS_*` variable already exported that these do not overwrite.

| Flag | Default | Description |
| --- | --- | --- |
| `--region` | _(auto)_ | Region to export: `$OVERCAST_REGION`, else `us-east-1`. |
| `--shell` | `auto` | `auto` (detected from the OS), `sh`, `powershell` or `fish`. |

```bash
eval "$(overcast env)"                 # sh / bash / zsh
overcast env --shell powershell | iex  # PowerShell
overcast env --shell fish | source
```

## `overcast aws [args...]`

Runs the host `aws` CLI against the daemon with every `AWS_*` variable unset
and dummy credentials, region and endpoint substituted. Everything after `aws`
passes through verbatim, `aws`-native flags such as `--debug` included.

`--endpoint` is read as Overcast's own only in the leading position — either
side of the `aws` token works. Anywhere else, and for `--endpoint-url`, it goes
to the AWS CLI untouched; with no flag at all, `OVERCAST_ENDPOINT` and
`OVERCAST_PORT` apply.

```bash
overcast aws s3 mb s3://my-bucket
overcast aws --endpoint http://localhost:4570 sqs list-queues
alias awslocal='overcast aws'   # LocalStack muscle memory
```

## Related

- [Using AWS SDKs and CLI](../sdk-cli.md) — the full walkthrough, with SDK examples
- [Migrating from LocalStack](../migration-from-localstack.md) — `awslocal` and the rest of the swap
- [Environment variable reference](../configuration/reference.md) — `OVERCAST_PORT` and every other environment variable
