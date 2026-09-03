---
title: "Lambda execution environments"
description: "What a Lambda container carries, which updates retire a warm one, why small functions stall in bursts, and how the init binary reaches each container."
section: "Service Reference"
tags:
  - docs
  - lambda
  - runtime
  - services
---

# Lambda execution environments

What one [Lambda](../lambda.md) container holds, when it is thrown away, and what
it costs to start another.

Containers are reused for sequential invocations and scaled out one per
concurrent invocation, the same way Lambda reuses and scales execution
environments. After a burst, the surplus stays warm until the 15-minute idle
sweep.

## What retires a warm container

A container is created with the function's code, environment variables, memory
limit, timeout, handler, layers, logging configuration and VPC attachment fixed at
start, so a running container can never observe a change to any of them. An
`UpdateFunctionCode` or `UpdateFunctionConfiguration` that changes one retires the
environment immediately:

| Container | What happens |
| --- | --- |
| Idle | Destroyed as soon as the update is stored |
| Serving an invocation | Finishes it in the old environment, then is destroyed; in-flight invocations are never interrupted |
| On-demand, retired | No replacement is started — the next invocation cold starts one |
| Provisioned, retired | Rebuilt in the background against the new configuration |

Updates that change nothing the container can observe — the description or the
role — leave the warm container in place, so cosmetic edits cost no cold start.
Deleting a function destroys its containers immediately, and a container that
disappears without Overcast asking (`docker rm -f`, an OOM kill, a Docker restart)
is dropped from the warm set as soon as the Docker event stream reports it.

`GET /_overcast/lambda/instances` reports one entry per execution environment,
each carrying an `initOrigin` of `on-demand`, `proactive` or `provisioned`, fixed
for the environment's lifetime. The `lambda:InstanceEvicted` event additionally
carries an `evictedReason` — the eight values and what each means are in
[Troubleshooting § Working out why a warm environment went away](./troubleshooting.md#working-out-why-a-warm-environment-went-away).

## CPU is shared with the init, and small functions feel it in bursts

Every execution environment runs an Overcast init process as PID 1 — the parent of
the runtime, and what makes an invocation's log attribution exact. It is inside
the container, so its CPU comes out of the function's allocation, exactly as AWS's
own RAPID init does.

Lambda's allocation is proportional to memory, so a 128 MB function gets about 7%
of a vCPU, and the kernel enforces that as a quota per 100 ms period. Under a
sustained burst of back-to-back invocations, the small amount of extra CPU the
init uses is enough to exhaust that quota, and the container is throttled until
the period rolls over. Measured on a 128 MB `nodejs22.x` hello-world driven with
no think time: warm p50 is unchanged at ~5 ms, but roughly one invocation in eight
stalls for ~80 ms, taking p95 from ~15 ms to ~80 ms.

Real Lambda throttles a container that overruns its CPU allocation the same way.
The stalls disappear with more memory (at 1769 MB, a full vCPU, there are none)
or with any gap between invocations.

## Init delivery is shared across instances

That init binary reaches each container through a named Docker volume
(`overcast-lambda-init-<hash>-<arch>`) rather than a fresh copy per cold start.
The name is content-addressed, so any Overcast instance on the same daemon can
safely reuse a volume seeded by another instance's build — but only the instance
that seeded one prunes or removes it, so two Overcasts sharing a daemon never
delete a volume the other is still using.

A volume this instance reused but does not own, and then found empty and could
not remove, is not reused again: those cold starts fall back to copying the init
into the container, and an informational advisory appears on
`GET /_overcast/debug/metrics`.

## Related

- [Lambda](../lambda.md) — quick start and what works
- [Lambda limitations](./limitations.md) — the divergence table
- [Lambda concurrency](./concurrency.md) — how many containers run at once
- [Lambda troubleshooting](./troubleshooting.md) — throttles, layer errors, extension endpoints
- [Performance](../../performance.md) — startup and invocation targets
