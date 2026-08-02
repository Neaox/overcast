# Release-bot messages

Everything `overcast-release[bot]` says lives here, as markdown rather than as
`printf` blocks inside the workflows that post it. These are prose a human reads
on a pull request, so they belong somewhere they can be reviewed and edited like
prose.

Placeholders are `$NAME` / `${NAME}`, filled from the workflow's environment by
`scripts/render-template.py`. An unknown placeholder is left in the output
rather than blanked, so a typo is visible instead of quietly deleting a
sentence.

| File | Posted when | By |
| --- | --- | --- |
| `pr-body.md` | the release PR is opened or regenerated | `release-prep.yml` |
| `updated.md` | `main` moved and new entries were folded in — heading only; the entries follow | `release-prep.yml` |
| `no-changes.md` | `main` moved but brought no changelog entries | `release-prep.yml` |
| `conflict.md` | `main` conflicts with the release branch, so nothing was changed | `release-prep.yml` |
| `already-open.md` | prep was dispatched while a release PR is already open | `release-prep.yml` |
| `hold.md` | a PR carries a breaking change while a minor or patch release PR is open | `release-hold.yml` |
| `hold-lifted.md` | that release left flight, so the hold came off on its own | `release-hold.yml` |
| `retarget-done.md` | `/retarget <branch>` repointed a PR | `retarget.yml` |
| `retarget-refused.md` | `/retarget` was asked for but not carried out | `retarget.yml` |

`retarget-refused.md`'s `$REASON` is the exception to prose living here: it is
one short sentence per failure, set where the failure is detected, because a
sentence naming the branch that was rejected does not survive being moved away
from the check that rejected it.

## Voice

Write as the bot reporting to a maintainer: **what it did**, then **what
happens next**, then anything that genuinely needs a human. Past tense for work
already done.

Avoid describing a state and leaving the reader to infer the action — "these
entries are not in the section yet" invites "so what do I do?", where "I added
them, nothing needed from you" does not. Name things concretely: `## [0.1.0]`
in `CHANGELOG.md`, not "the release section".

`hold.md` is the one message written for a contributor rather than the
maintainer, and it is a refusal — so it carries the extra weight of saying what
is *not* wrong. The change is fine; the timing is not; nothing in the PR needs
fixing. A refusal that omits that reads as a rejection of the work.

## What the App needs

The App is credentials, not a service — nothing runs between workflow runs.
Its installation needs:

| Permission | For |
| --- | --- |
| Contents: write | pushing release branches; creating a branch for `/retarget` |
| Pull requests: write | opening and editing the release PR; comments; changing a PR's base |
| Actions: write | re-running the hold check on a held PR once the release is out |

**Actions: write is the one to add** — the App predates it. Without it
`release-hold-lift.yml` fails and held PRs stay red until someone re-runs their
checks by hand. That is a delay, not a hole: the check re-evaluates the same way
whoever triggers it.
