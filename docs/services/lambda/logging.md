---
title: "Lambda logging"
description: "What Overcast writes around each Lambda invocation: the Text lines, the JSON telemetry record vocabulary and its levels, what filtering applies to, and the extension telemetry gaps."
section: "Service Reference"
tags:
  - docs
  - lambda
  - logging
  - services
---

# Lambda logging

What [Lambda](../lambda.md) writes around each invocation, and which of it
reaches CloudWatch Logs.

`LogFormat` decides what is written; under `JSON` the two log levels decide how
much of it is kept. `LogGroup` is respected too: a function with a custom group
writes there, and the group is created on `CreateFunction` like the default
`/aws/lambda/<function-name>` one.

**Text** is the default and unchanged: the plain-text `START`, `END` and `REPORT`
lines real Lambda writes, byte for byte. `ApplicationLogLevel` and
`SystemLogLevel` do not apply to Text on AWS, so Overcast filters nothing in this
mode.

## The JSON record vocabulary

**JSON** replaces those three lines with the events AWS publishes through the
Telemetry API, one object per line, shaped `{"time", "type", "record"}`:

| Event `type` | Replaces | System log level | Record |
| --- | --- | --- | --- |
| `platform.initStart` | — | `DEBUG` | `initializationType`, `phase`, `functionName`, `functionVersion` |
| `platform.initRuntimeDone` | — | `DEBUG` on success, else `WARN` | `initializationType`, `phase`, `status` |
| `platform.initReport` | — | `DEBUG` on-demand, `INFO` provisioned, `WARN` when it failed | plus `metrics`: `durationMs` |
| `platform.start` | `START` | `INFO` | `requestId`, `version`, `tracing` |
| `platform.runtimeDone` | `END` | `DEBUG` on success, else `WARN` | `requestId`, `status`, `tracing`, `spans` (`responseLatency`), `metrics`: `durationMs`, `producedBytes` |
| `platform.report` | `REPORT` | `INFO` on success, else `WARN` | `requestId`, `status`, `tracing`, `metrics`: `durationMs`, `billedDurationMs`, `memorySizeMB`, `maxMemoryUsedMB` |

The levels are AWS's own system-log-level mapping, not an Overcast convention.
`status` is `success`, `failure` (the handler returned an error), `error` (the
environment ended the invocation) or `timeout`. `metrics.initDurationMs` appears
in `platform.report` only on the first report of an on-demand cold start.
`tracing` carries the `X-Amzn-Trace-Id` the runtime genuinely received; `spanId`
stays absent, because Overcast mints no spans.

`platform.runtimeDone`'s metrics are measured by the in-container init — the
invocation being handed to the runtime, to its answer arriving back at the init's
proxy — so they are a different, smaller span than `platform.report`'s
host-measured `durationMs`, as AWS's two records are two different measurements.
The host's own measurements are the fallback where the runtime never answered.

The three **init-phase** records come from that same init, which is PID 1 and the
proxy in front of the Runtime API, so it sees the phase begin and end without
inferring either, and they travel on the same sequence-ordered stream as the
container's own output. All three are `DEBUG` for a successful on-demand cold
start, so a default log stream never shows them; a provisioned environment's
`platform.initReport` is `INFO`, and an environment whose runtime died before
asking for work reports `status: error` on both closing records at `WARN`.
`platform.initReport`'s `durationMs` is measured inside the environment, a
different span from the host-measured `Init Duration`; the two differ by a few
milliseconds. In **Text** format none of the three is written to CloudWatch Logs.

## What is missing from the vocabulary

Overcast emits only the subset of AWS's schema it genuinely observes.

| Field or record | Overcast |
| --- | --- |
| `errorType` | Populated for the one outcome whose AWS name is documented — a runtime that exited is `Runtime.ExitError` |
| `platform.initStart`'s `runtimeVersion`, `runtimeVersionArn`, `instanceId`, `instanceMaxMemory` | Omitted |
| `platform.runtimeDone` spans other than `responseLatency` | Omitted — `responseDuration` ends only after the answer has finished streaming through the init's unbuffered proxy, and `runtimeOverhead` exists only at the runtime's next poll, both after the record is on its way |
| SnapStart restore records (`platform.restoreStart`, `platform.restoreRuntimeDone`, `platform.restoreReport`) | Not emitted; SnapStart is not emulated |
| Extension telemetry destinations (Logs API `PUT /2020-08-15/logs`, Telemetry API `PUT /2022-07-01/telemetry`) | HTTP only. Buffering configuration is honoured with AWS's defaults; out-of-range values are clamped to the documented limits rather than rejected |
| `UpdateFunctionConfiguration` with an explicitly empty `LoggingConfig: {}` | `501`. AWS's semantics for that shape are undocumented, and either guess — no-op or reset to defaults — mutates the function. `LoggingConfig` with explicit members applies normally |

## Filtering

Both levels default to `INFO` under `JSON`, as on AWS. The ordering is `TRACE` <
`DEBUG` < `INFO` < `WARN` < `ERROR` < `FATAL`, and a record is kept when its own
level is at or above the configured one.

- **`SystemLogLevel`** filters the platform records above, by the level in the
  table.
- **`ApplicationLogLevel`** filters the function's own stdout and stderr, by the
  `"level"` member of each record. A line that parses as a JSON object with a
  recognised level is filtered on it; everything else — unstructured text,
  malformed JSON, a missing or unknown level — is treated as `INFO`, which is what
  AWS documents. Level names are matched case-insensitively.

Filtering decides what reaches **CloudWatch Logs and the `X-Amz-Log-Result`
tail** only. Telemetry and Logs API subscribers receive the complete set either
way — AWS is explicit that the CloudWatch system log level does not affect
Telemetry API behaviour.

The configuration is handed to the runtime the way AWS hands it over, because the
managed runtimes and Powertools read it: `AWS_LAMBDA_LOG_FORMAT` is always set to
`Text` or `JSON`, and `AWS_LAMBDA_LOG_LEVEL` is set to the effective
`ApplicationLogLevel` only under `JSON`. Those values are baked into a container
at start, so changing `LogFormat` or either level retires the warm environments.

## Related

- [Lambda](../lambda.md) — quick start and what works
- [Lambda limitations](./limitations.md) — the divergence table
- [Lambda execution environments](./execution-environments.md) — why a log-config change costs a cold start
- [Lambda troubleshooting](./troubleshooting.md) — throttles, layer errors, extension endpoints
- [CloudWatch Logs](../cloudwatch-logs.md) — where function output lands
