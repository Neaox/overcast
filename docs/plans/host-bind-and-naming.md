---
title: "Bind address: default, name, and the docs that contradict it"
description: Decision record for #761 (OVERCAST_HOST defaults to 0.0.0.0 while the docs say never expose Overcast) and #870 (OVERCAST_HOST means the opposite of LOCALSTACK_HOST). Frames the options; the security-posture call is deliberately left to the maintainer.
---

# Bind address: default, name, and the docs that contradict it

> Status: **complete, 2026-08-22.** Decided as A + C, sequenced per §4
> (maintainer approval: "pick both up"), revised same day: **C shipped as a
> straight rename with the old name *removed*, not kept as a permanent
> alias** — Overcast is alpha software and the standing policy is that a
> removed setting fails loudly naming its replacement rather than being
> silently accepted. #870 (rename, `OVERCAST_HOST` removed) shipped in
> [#1176](https://github.com/Neaox/overcast/pull/1176); #761
> (environment-dependent native default) shipped in
> [#1179](https://github.com/Neaox/overcast/pull/1179), sequenced after it as
> §4 planned. This doc is kept (rather than deleted) because both issues'
> comment threads reference it directly.
> Tracks: [#761](https://github.com/Neaox/overcast/issues/761) (default vs docs),
> [#870](https://github.com/Neaox/overcast/issues/870) (naming vs LocalStack convention).
> The two issues carry the full evidence; this doc exists so the *combined* decision
> is made once, in the right order, instead of each issue being fixed in isolation.

## 1. The problem, in two sentences

A native `overcast serve` listens on every interface by default
(`cfg.Host = envOr("OVERCAST_HOST", "0.0.0.0")`, `internal/config/config.go`),
while README.md, AGENTS.md and the architecture doc all say never to expose
Overcast on a public network — the default has already done a weaker version of
that for the user (#761). Separately, the variable that controls this is named
`OVERCAST_HOST`, which by the convention of every neighbouring emulator means
the *advertised* name, not the bind address — and a code comment falsely claims
equivalence with `LOCALSTACK_HOST`, which is actually the analogue of
`OVERCAST_HOSTNAME` (#870).

## 2. What is NOT in question

These are decision-free and should ship regardless of everything below:

| Item | Why it is free |
|---|---|
| **Startup log line naming the bind address** and that `OVERCAST_LISTEN` changes it | #761 asks for it under either resolution; precedent is the storage-mode log line |
| **Fix the false `LOCALSTACK_HOST` equivalence comment** (`internal/config/config.go`) | It is wrong today and misdirects LocalStack migrants; #870 asks for it independently of the rename |
| **Container default stays `0.0.0.0`** | Docker `-p` publishing requires it; the image is the primary distribution; #761 states this as a hard constraint |
| **An explicit setting always wins**, in both directions | Both issues assume it; nobody proposes otherwise |

## 3. The decision surface

### 3.1 #761 — what does a *native* `overcast serve` bind by default?

| Option | What ships | Cost | Benefit |
|---|---|---|---|
| **A. Environment-dependent default** (the issue's proposal; **shipped**) | Container → `0.0.0.0` (unchanged); native → `127.0.0.1`; detection reuses the `OVERCAST_DATA_DIR_SOURCE=image` marker storage-mode auto-detection already keys off (`isDockerImage` in `internal/config/state_auto.go`) — the mountpoint check that heuristic *also* uses is a separate signal for a different question ("is this data dir a mounted volume") and is not part of the containerised/native decision here | **Breaking for native users** reaching the instance from a VM / second machine / phone — and it breaks *quietly* (connection refused). Needs a `*!` changelog entry and a loud startup log | The default finally matches the documented advice; laptop-on-hotel-wifi exposure (full unauthenticated AWS surface; with `OVERCAST_DEBUG` also `/_debug/state` dump and `/_debug/reset` wipe) closes by default |
| **B. Docs-only** | Default unchanged; README/AGENTS/architecture doc say plainly that the default listens on all interfaces and to set `OVERCAST_HOST=127.0.0.1` on untrusted networks | The exposure stays; every reader must notice and act | Zero breakage; contradiction still removed (the issue accepts this as a valid resolution) |

The issue is explicit that **either** resolution is acceptable; only the
contradiction is not.

### 3.2 #870 — what is the variable called?

| Option | What ships | Cost | Benefit |
|---|---|---|---|
| **C. Rename, old name removed** (revised from the issue's alias proposal — see below; **shipped**) | `OVERCAST_LISTEN` is the only bind-address variable (LocalStack's `GATEWAY_LISTEN` idiom; value format already matches); `OVERCAST_HOST` is removed rather than kept as a permanent alias — a leftover `OVERCAST_HOST` fails at startup naming `OVERCAST_LISTEN`, so a straggler cannot be silently ignored; docs use the new name throughout | A breaking change (`*!` changelog entry with migration steps); every compose file, `.env`, test, and workflow setting `OVERCAST_HOST` must be updated in the same change | The `docs/dev/networking.md` disambiguation section becomes deletable; the `*_HOST`-means-advertised-name collision with LocalStack/Floci convention ends; no second name to keep supporting forever |
| **D. Keep the name** | Comment fix + docs clarity only | The silent wrong-variable failure modes #870 documents stay live (worst: someone tightening exposure gets no tightening and a broken URL surface, with no error) | No churn |

**Revision, 2026-08-22:** the issue's original proposal kept `OVERCAST_HOST`
working forever as a permanent alias (see the issue text and #1176's first
commit for that shape). The maintainer overrode this after #1176 opened:
Overcast is alpha, the API is fully changeable, and a permanent alias is the
wrong instinct for a project whose own fidelity principle is that silent
divergence — a stale setting doing nothing while looking accepted — is the
expensive failure mode. `OVERCAST_HOST` is removed outright; a value left
over from before the rename fails startup by name rather than being quietly
ignored.

Open sub-questions #870 flags for the same sitting: whether `OVERCAST_HOSTNAME`
keeps its double duty (advertised name + virtual-host base + `/etc/hosts` entry
+ TLS SAN — arguably one concept), and whether a `LOCALSTACK_HOST` →
`OVERCAST_HOSTNAME` migration shim is wanted.

## 4. Interaction — why decide these together

If both A and C are chosen, **C ships first**: introduce `OVERCAST_LISTEN`
(with `OVERCAST_HOST` removed), then change the *native default* in the same
release the new name is documented. The migration story becomes one coherent
sequence — rename `OVERCAST_HOST` to `OVERCAST_LISTEN` first, then, once native
default work lands, set `OVERCAST_LISTEN=0.0.0.0` to restore the old reach if
you were relying on it — instead of a renamed variable and a changed default
arriving as two separate surprises. If only one is chosen, nothing about it
constrains later adopting the other.

## 5. Recommendation (advisory only)

A + C, sequenced per §4, with the §2 items landing immediately and
independently. Rationale: the project's own fidelity principle is that silent
divergence is the expensive kind — a default that silently exposes, and a
variable that silently does the wrong thing, are both that kind. The breakage
in A is real but one changelog entry plus the startup log line makes it
diagnosable in seconds. This is written as advice, not a decision: the
security-posture trade-off in §3.1 is the maintainer's call.

## 6. Definition of done — complete, 2026-08-22

- [x] The §2 decision-free items are merged (both PRs).
- [x] The chosen option(s) from §3 are implemented with their issues' own
  acceptance criteria, as revised 2026-08-22 (#761: environment-dependent
  default + log line; #870: `OVERCAST_LISTEN` is the only bind-address
  variable, `OVERCAST_HOST` removed and fails loudly rather than kept as an
  alias, docs rename, disambiguation section deleted).
- [x] README.md, AGENTS.md, the architecture doc, `docs/README.md`'s
  configuration reference, `docs/migration-from-localstack.md`, and
  `docs/dev/networking.md` agree with the shipped behaviour.
- [x] This doc's status line records the decision and the shipping PRs. Kept
  rather than deleted: both #761 and #870's comment threads cite it directly.
