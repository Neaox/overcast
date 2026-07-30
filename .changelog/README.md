# Changelog fragments

`CHANGELOG.md`'s `[Unreleased]` section stays **empty** between releases. Every
release-note-worthy change is recorded here instead, as one fragment file per
PR. New files at unique paths cannot merge-conflict, so PRs never fight over
the changelog. At release time the fragments are curated into the new
versioned section of `CHANGELOG.md` and deleted (see [RELEASE.md](../RELEASE.md)).

CI runs `python3 scripts/changelog.py check` on every push and PR: it lints
every fragment in this directory and fails if `[Unreleased]` gained content.

## File format

One file per PR: `.changelog/YYYYMMDD-<slug>.md`, where the date is the UTC day
the fragment is written and the slug describes the change (lowercase letters,
digits, hyphens — the branch topic usually works).

```markdown
---
section: Fixed        # Added | Changed | Fixed | Removed | Deprecated | Security
area: cloudformation  # optional: service/area slug used for grouping
---

- [cloudformation] update replacements no longer destroy same-ID resources; pinned-name resources now update in place
```

- `section` (required): the Keep a Changelog category the entry belongs under.
  Choose the category first, then phrase the entry.
- `area` (optional): a service or area slug — `sqs`, `cloudformation`, `web`,
  `router`, `state`, `ci`, `docs`, `compat`, … Used only to sort related
  fragments next to each other at assembly time so they are easy to merge.
- Body (required): one or more markdown list items in the house bullet style —
  see "Changelog Management" in
  [.agents/skills/pull-request/SKILL.md](../.agents/skills/pull-request/SKILL.md).
  No headings.

## What merits a fragment

Only changes that affect shipped artifacts or release notes: runtime/service
behaviour, AWS compatibility, config/env vars, Docker or binary packaging,
release process, measured performance changes, or user-facing docs guidance.
Skip fragments for CI-only changes, test-only changes, local tooling, internal
refactors, or cleanup that does not affect shipped artifacts.

Write the fragment for your own change only. Do not edit or merge other PRs'
fragments, and do not try to keep entries aggregated per service — that
curation happens once, at release time, with the whole release visible.

## Release time

Release prep runs:

```sh
python3 scripts/changelog.py assemble <version>
```

which prints a draft `## [<version>]` section (fragments grouped by category,
then area). Curate that draft into `CHANGELOG.md` per the house style — merge
same-area fragments into single bullets, tighten prose — then `git rm` every
fragment file (everything here except this README).
`scripts/check-release-changelog.py` fails the release while any fragment
remains.
