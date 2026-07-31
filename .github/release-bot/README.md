# Release-bot messages

Everything `overcast-release[bot]` says lives here, as markdown rather than as
`printf` blocks inside `.github/workflows/release-prep.yml`. These are prose a
human reads on a pull request, so they belong somewhere they can be reviewed
and edited like prose.

Placeholders are `$NAME` / `${NAME}`, filled from the workflow's environment by
`scripts/render-template.py`. An unknown placeholder is left in the output
rather than blanked, so a typo is visible instead of quietly deleting a
sentence.

| File | Posted when |
| --- | --- |
| `pr-body.md` | the release PR is opened or regenerated |
| `updated.md` | `main` moved and new entries were folded in — heading only; the entries follow |
| `no-changes.md` | `main` moved but brought no changelog entries |
| `conflict.md` | `main` conflicts with the release branch, so nothing was changed |
| `already-open.md` | prep was dispatched while a release PR is already open |

## Voice

Write as the bot reporting to a maintainer: **what it did**, then **what
happens next**, then anything that genuinely needs a human. Past tense for work
already done.

Avoid describing a state and leaving the reader to infer the action — "these
entries are not in the section yet" invites "so what do I do?", where "I added
them, nothing needed from you" does not. Name things concretely: `## [0.1.0]`
in `CHANGELOG.md`, not "the release section".
