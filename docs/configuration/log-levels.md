---
title: "Log levels"
description: "What each OVERCAST_LOG_LEVEL prints — info, debug, trace, warn and error — and which one to attach to a bug report."
section: "Reference"
tags:
  - configuration
  - docs
  - levels
  - log
  - logging
---

# Log levels

`OVERCAST_LOG_LEVEL` controls how much Overcast logs. `info` is the default;
`debug` is what to attach to a bug report:

```bash
OVERCAST_LOG_LEVEL=debug overcast serve
```

| Level   | What you'll see                                                                                             |
| ------- | ------------------------------------------------------------------------------------------------------------- |
| `info`  | **Default.** Lifecycle events (start, shutdown, migrations) and one line per AWS API call your app makes.     |
| `debug` | Everything in `info`, plus the reasoning behind each response — what to attach to a bug report.               |
| `trace` | Everything in `debug`, plus emulator machinery: health-check probes, console polling, background flush ticks.  |
| `warn`  | One-liners for handled-but-unexpected conditions (a malformed record was skipped, a slow filesystem).         |
| `error` | One-liners for failures that need attention (storage degraded, a migration failed).                           |

`trace` is very high volume — use it for a short capture window rather than
always-on. LocalStack's `DEBUG=1` and `LS_LOG` are read as aliases for this
variable.

For contributors: the full call-site policy (what belongs at `debug` vs `trace`)
is in [CONTRIBUTING.md § Log
levels](https://github.com/overcast-sh/overcast/blob/main/CONTRIBUTING.md#log-levels).

## Related

- [Debug endpoints](../debug-endpoints.md) — request traces, which are a
  separate switch (`OVERCAST_DEBUG`)
- [Environment variable reference](./reference.md)
- [Configuration](../configuration.md)
