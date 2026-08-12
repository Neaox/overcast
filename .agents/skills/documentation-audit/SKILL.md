---
name: documentation-audit
description: "Verifies Overcast documentation against the code and reports what has drifted, as findings rather than prose. Checks environment-variable references against internal/config, counts and coverage claims against the registries that compute them, ports and internal routes against the router, links and heading anchors, behavioural claims a reader would act on, and plan-doc status headers against whether the work shipped. Groups findings into PR-sized issues using a severity vocabulary. Use before relying on a doc set — onboarding, a knowledge-transfer session, citing docs in new work — when a doc's claims look suspect, when a count or a configuration reference may be stale, or as a periodic sweep. Not for writing or improving prose, which is documentation-writing."
argument-hint: "Doc path, directory, or the area to audit"
---

# Documentation Audit — Overcast

A systematic accuracy pass over documentation, producing findings rather than prose. For writing or editing docs, use `documentation-writing`.

Run this when a doc set is about to be relied on — before a knowledge-transfer session, before onboarding, before citing a doc in new work, or periodically. Documentation drift is silent: nothing fails, and the cost lands on whoever trusts it.

---

## Where drift concentrates

One rule predicts most of it: **generated documentation stays true; hand-maintained enumerations do not.** Anything a human typed that the code also knows is a candidate.

The shapes below have all occurred in this repository. Treat them as a search pattern rather than a current defect list — each may since have been fixed, and the point is where to look, not what to expect.

- **The same count wrong in several documents at once.** A resource-type total appeared in five files and was low by a factor of 2.5 in all of them. When a number is worth stating twice, it is worth generating.
- **A document contradicting its own generated block.** A roadmap section claimed an operation count and a coverage gap that the generated table a few lines above already refuted.
- **A stale gaps table telling users a supported thing is unsupported.** The most expensive kind: it fails in the direction that loses adoption silently, with no error and no support question to correct it.
- **A wrong port or variable name in a high-traffic reference.** Cheap to check, high impact, since following it produces a connection refused.
- **Configuration read by the code but absent from the reference that claims completeness** — including security-relevant settings.
- **Plan docs claiming unstarted work that had shipped**, because the feature landed as a side effect of unrelated work and nobody revisited the proposal.
- **A generated doc silently excluding part of its subject**, because the generator's discovery rule missed a whole class. Generated does not mean complete — check what the generator looks for.

---

## Method

Mechanical checks first — they are cheap, exhaustive and produce indisputable findings. Judgement calls after.

### 1. Configuration reference against code

The highest-value single check. Extract every environment variable read by `internal/config`, and every one documented. Diff both directions:

- documented but not read (removed, or never existed)
- read but not documented (shipped without a reference entry)
- documented with the wrong default

`internal/config/config.go`'s `Load()` doc comment is usually accurate and complete — treat it as the source and the published reference as the copy.

### 2. Counts and coverage claims

Find every number in prose that describes the system, and compute the real one. Service counts, operation counts, resource-type counts, supported-feature tallies.

Beware approximations that were once right — "~50" is not protected by the tilde.

### 3. Ports, endpoints and routes

Every port and URL in the docs, against the actual defaults. Every documented `/_overcast/debug/*` and internal route against the registrations in `internal/router/`, both directions.

### 4. Links and anchors

Relative paths resolve; `#anchor` fragments match real headings under GitHub's slug rules. Cheap to script, and heading renames break them silently.

### 5. Behavioural claims

Now the judgement work. For each doc, list the statements a reader would *act* on — what persists, what needs Docker, what a flag does, what is enforced versus stored. Verify each against code.

Prioritise claims where being wrong is expensive: anything about durability, security posture, what is enforced, or whether a feature exists at all.

### 6. Cross-document contradictions

Where two docs describe the same thing, compare them. Where a doc contains both generated and hand-written content about the same subject, compare those too — that is a reliable source of a document disagreeing with itself.

### 7. Plan-doc status

For every file in `docs/plans/`, read the status header and verify against the code whether the work landed. The repo rule is that plan docs are accurate as of every commit, so a wrong header is a compliance gap, not a cosmetic one.

Expect the failure to be one-directional: docs claiming work is unstarted when it shipped, because the feature landed as a side effect of some other plan's work.

---

## Recording findings

Use a severity vocabulary, because the response differs:

| Severity | Meaning |
|---|---|
| **wrong** | Actively misleading. A reader would act on it and be wrong. |
| **stale** | Was true, no longer. |
| **incomplete** | Missing something material. |
| **cosmetic** | Broken link, wrong anchor, typo in a value. |

Report each as: `severity | file:line | current text | what the code does (path:line) | suggested correction`.

Be precise with line numbers so fixes can be applied directly. **Where you cannot verify a claim without running the system, say so** rather than guessing — an audit that quietly asserts unverified things is worse than one with gaps marked.

---

## Turning findings into issues

Group into PR-sized units, not one issue per line and not one issue for everything. Grouping by *area and cause* works well: the wrong facts in user-facing docs; a count and the tables that repeat it; a stale migration guide; the configuration reference; plan-doc statuses.

For each issue, state the finding, the evidence with `path:line`, and the suggested fix. Where several findings share a cause, say so — the fix is usually different from fixing them individually.

**Always ask whether the finding could be prevented rather than corrected.** If a fact is derivable from code, the fix is a generator or a CI check, not an edit. A correction made by hand today is one made again next year.

---

## Two things that make an audit worth more

**Look for the cause, not just the instance.** Several plan docs with wrong statuses is not several oversights — it is a process gap where nobody looks at a proposal document at the moment its proposal becomes true. Report the pattern, because the pattern is what gets fixed.

**Distinguish documentation defects from product defects.** An audit walks a lot of code and will find real bugs: silent no-ops, unreachable code, gaps with no issue. Those are usually more valuable than the doc findings. File them separately and do not bury them in a documentation report.

---

## Related skills

- `documentation-writing` — writing docs, and the rules an audit checks against.
- `github-issue-lifecycle` — filing what the audit finds.
- `aws-compatibility-review` — the equivalent pass for service behaviour against AWS.
