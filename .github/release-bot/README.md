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
| `changelog-missing.md` | a PR adds no fragment under `.changelog/` and has not said that is deliberate | `changelog-required.yml` |
| `changelog-missing-release-window.md` | the same, while `VERSION` is prepared but untagged — where a fragment would fail the release, so the ask is "wait for the tag or waive" | `changelog-required.yml` |
| `changelog-waived.md` | `/no-changelog <reason>` recorded that a PR needs no fragment | `changelog-required.yml` |
| `changelog-waiver-refused.md` | `/no-changelog` was asked for but not carried out | `changelog-waiver.yml` |

`retarget-refused.md`'s `$REASON` and `changelog-waiver-refused.md`'s `$PROBLEM`
are the exception to prose living here: they are one short sentence per failure,
set where the failure is detected, because a sentence naming the branch that was
rejected does not survive being moved away from the check that rejected it.

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
| Actions: write | re-running the hold check on a held PR once the release is out; re-running the changelog gate when a waiver is commented |

**Actions: write is the one to add** — the App predates it. Without it
`release-hold-lift.yml` and `changelog-waiver.yml` fail, and the PRs they would
have cleared stay red until someone re-runs their checks by hand. That is a
delay, not a hole: both checks re-evaluate the same way whoever triggers them,
and the changelog gate reads the waiver comment itself rather than being told
about it.
