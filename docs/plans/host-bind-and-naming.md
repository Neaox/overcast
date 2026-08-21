---
title: "Bind address: default, name, and the docs that contradict it"
description: Decision record for #761 (OVERCAST_HOST defaults to 0.0.0.0 while the docs say never expose Overcast) and #870 (OVERCAST_HOST means the opposite of LOCALSTACK_HOST). Frames the options; the security-posture call is deliberately left to the maintainer.
---

# Bind address: default, name, and the docs that contradict it

> Status: **decided 2026-08-22 — A + C, sequenced per §4** (maintainer approval:
> "pick both up"). Implementation in progress: #870 (rename with alias) ships
> first, then #761 (environment-dependent native default). Owner: in flight.
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
| **Startup log line naming the bind address** and that `OVERCAST_HOST` changes it | #761 asks for it under either resolution; precedent is the storage-mode log line |
| **Fix the false `LOCALSTACK_HOST` equivalence comment** (`internal/config/config.go`) | It is wrong today and misdirects LocalStack migrants; #870 asks for it independently of the rename |
| **Container default stays `0.0.0.0`** | Docker `-p` publishing requires it; the image is the primary distribution; #761 states this as a hard constraint |
| **An explicit setting always wins**, in both directions | Both issues assume it; nobody proposes otherwise |

## 3. The decision surface

### 3.1 #761 — what does a *native* `overcast serve` bind by default?

| Option | What ships | Cost | Benefit |
|---|---|---|---|
| **A. Environment-dependent default** (the issue's proposal) | Container → `0.0.0.0` (unchanged); native → `127.0.0.1`; detection via the same signal storage-mode resolution uses (`OVERCAST_DATA_DIR_SOURCE=image` + mountpoint detection, `internal/config/state_auto.go`) | **Breaking for native users** reaching the instance from a VM / second machine / phone — and it breaks *quietly* (connection refused). Needs a `*!` changelog entry and a loud startup log | The default finally matches the documented advice; laptop-on-hotel-wifi exposure (full unauthenticated AWS surface; with `OVERCAST_DEBUG` also `/_debug/state` dump and `/_debug/reset` wipe) closes by default |
| **B. Docs-only** | Default unchanged; README/AGENTS/architecture doc say plainly that the default listens on all interfaces and to set `OVERCAST_HOST=127.0.0.1` on untrusted networks | The exposure stays; every reader must notice and act | Zero breakage; contradiction still removed (the issue accepts this as a valid resolution) |

The issue is explicit that **either** resolution is acceptable; only the
contradiction is not.

### 3.2 #870 — what is the variable called?

| Option | What ships | Cost | Benefit |
|---|---|---|---|
| **C. Rename with permanent alias** (the issue's proposal) | `OVERCAST_LISTEN` accepted (LocalStack's `GATEWAY_LISTEN` idiom; value format already matches); `OVERCAST_HOST` works forever as an alias; both-set-with-different-values fails at startup; docs use the new name | A second name to keep accepting (cheap); docs churn | The `docs/dev/networking.md` disambiguation section becomes deletable; the `*_HOST`-means-advertised-name collision with LocalStack/Floci convention ends |
| **D. Keep the name** | Comment fix + docs clarity only | The silent wrong-variable failure modes #870 documents stay live (worst: someone tightening exposure gets no tightening and a broken URL surface, with no error) | No churn |

Open sub-questions #870 flags for the same sitting: whether `OVERCAST_HOSTNAME`
keeps its double duty (advertised name + virtual-host base + `/etc/hosts` entry
+ TLS SAN — arguably one concept), and whether a `LOCALSTACK_HOST` →
`OVERCAST_HOSTNAME` migration shim is wanted.

## 4. Interaction — why decide these together

If both A and C are chosen, **C ships first**: introduce `OVERCAST_LISTEN`,
then change the *native default* in the same release the new name is
documented. The migration story becomes one coherent line — "native builds now
listen on loopback; set `OVERCAST_LISTEN=0.0.0.0` (né `OVERCAST_HOST`) to
restore the old reach" — instead of a renamed variable and a changed default
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

## 6. Definition of done (once decided)

- The §2 decision-free items are merged.
- The chosen option(s) from §3 are implemented with their issues' own
  acceptance criteria (#761: default or docs + log line; #870: alias behaviour,
  conflict-fail, docs rename, disambiguation section deleted).
- README.md, AGENTS.md, the architecture doc, `docs/README.md`'s configuration
  reference, and `docs/dev/networking.md` agree with the shipped behaviour.
- This doc's status line records the decision and the shipping PRs, or the doc
  is deleted once fully implemented and nothing references it.
