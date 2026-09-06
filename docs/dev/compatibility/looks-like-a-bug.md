# AWS behaviour that looks like a bug

Some of Overcast's behaviour reads as broken. A header a standard says to send,
missing. A malformed input accepted instead of rejected. An answer every other
emulator disagrees with. Each entry below is a case where that appearance is
correct behaviour: real AWS does the surprising thing, and we match it on purpose.

The page exists because these get "fixed" otherwise. Fidelity that looks like a
defect is removed by the next reader acting in good faith — a linter, a review
comment, a sibling implementation, or an issue written from the spec rather than
from AWS. Recording the evidence once is cheaper than re-deriving it each time,
and much cheaper than shipping the regression and finding it again.

Any service can have entries. The sections below are per-service only because
that is how a reader arrives: they are looking at one handler.

## What qualifies

An entry needs all four:

1. Overcast looks wrong — to a reader, a standard, a linter, or another emulator.
2. Real AWS was **measured**, not inferred from documentation.
3. The reproduction is written down, so anyone can re-run it.
4. The date it was last confirmed is recorded, because AWS changes.

A behaviour we merely believe AWS has does not belong here — that is a normal
comment. Neither does a known divergence: that is a `gaps` entry in
`services/<service>.yaml`. This page is only for *we look wrong and we are right*.

## Confirming a behaviour

**Checking real AWS requires the user's explicit approval in the current
conversation** ([README](./README.md)). Ask before you reach for it, and say what
you intend to run.

How expensive that is depends on the service, and it is worth saying so when you
ask:

- **Read-only against something already public** is the cheap case — no account,
  no credentials, no writes, nothing to tear down. S3 has
  [Open Data](https://registry.opendata.aws/) buckets, which are anonymously
  readable and reachable from anywhere, since bucket names are one global
  namespace.
- **Everything else** generally needs a real resource in a real account, which
  costs money and needs cleaning up. That is the tier-4 case in the
  [`bug-fix` skill](../../../.agents/skills/bug-fix/SKILL.md), and the permission
  is a bigger ask.

Record the exact command and its output, not a summary. A transcript someone can
re-run is the point; a paraphrase is what we already had.

## Recording it

Two halves, and the second is the one that gets forgotten:

1. Add an entry here, under the service, with the reproduction and the date.
2. **Put a comment at the code site pointing back to this page.** The entry only
   helps a reader who already suspects the answer is written down. The comment is
   what reaches the person about to delete the line.

---

## S3

### An unusable `Range` returns the whole object, not a 416

A `Range` header S3 cannot parse (`bytes=abc`), one whose end precedes its start
(`bytes=1-0`), or one naming a unit S3 does not know (`items=1-2`) is **ignored**:
the answer is `200` and the entire object. Only a *valid* range that overlaps none
of the object is a `416`. This is RFC 9110's own distinction — §14.1.1 calls the
first kind invalid and §15.5.17 reserves 416 for the second — but it reads as
permissiveness, and both floci and ministack return `416` for all three.

- **Confirmed:** 2026-09-06 · **Issue:** #1705 · **Code:** `parseByteRange` in `internal/services/s3/handler_object.go`

```bash
U=https://1000genomes.s3.amazonaws.com/README_phase3_alignments_sequence_20150526  # 136 bytes
for r in 'bytes=abc' 'bytes=1-0' 'items=1-2' 'bytes=500-600' 'bytes=-0'; do
  printf '%-14s ' "$r"; curl -s -o /dev/null -w '%{http_code}\n' -H "Range: $r" "$U"
done
# bytes=abc      200
# bytes=1-0      200
# items=1-2      200
# bytes=500-600  416
# bytes=-0       416
```

### No `Content-Range` on a 416

RFC 9110 §15.5.17 says a `416` response SHOULD carry
`Content-Range: bytes */<size>`. Real S3 sends no such header. Overcast omits it
too, and the object's size reaches the caller as the error document's
`ActualObjectSize` instead. floci and ministack both send it.

- **Confirmed:** 2026-09-06 · **Issue:** #1864 · **Code:** `applyObjectRange` and `errInvalidRange` in `internal/services/s3/`

```bash
curl -s -D - -o /dev/null -H 'Range: bytes=500-600' "$U" | grep -i 'HTTP/\|content-range'
# HTTP/1.1 416 Requested Range Not Satisfiable
# (no Content-Range line)
```

## Related

- [README.md](./README.md) — how compatibility review works, and the real-AWS approval rule
- [services/](./services/) — the per-operation records these entries came from
- [CONTRIBUTING.md § AWS is the tie-breaker](../../../CONTRIBUTING.md#aws-is-the-tie-breaker) — why AWS outranks the standard
