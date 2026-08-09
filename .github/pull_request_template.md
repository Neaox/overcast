<!--
  The shape below is the one required by .agents/skills/pull-request/SKILL.md.
  Read that skill for the full guidance — this template is its checklist, not a
  replacement for it. Delete the comments as you fill each section in.

  Detail belongs here, in the body. A reviewer should not have to
  reverse-engineer the branch to find the review story.
-->

## Summary

<!--
  1-3 bullets on the user-visible or AWS-visible result.
  Outcomes, not file lists. Name the services or packages affected.
  Say so explicitly if behaviour changed at the AWS wire/API boundary.
-->

-

## Screenshots

<!--
  Visual changes only — delete this whole section otherwise.

  Screenshots must come from a real browser driven against an emulator built
  from THIS branch. Never a mock-up, never a description of what it looks like.

  Name the theme and viewport variations you captured AND why those are the
  axes this change actually moves. A blanket cross-product of every theme by
  every width is as much a failure as capturing nothing: it buries the one
  frame that matters. If a change only moves layout, light-mode at two widths
  may be the honest answer; if it only moves colour, one width in both themes is.
-->

## Verification

<!--
  Exact commands run, not a summary of them. Include:
    - test runs (and `-race` where concurrency is in scope)
    - golangci-lint (CI uses v2.8.0 and catches what gofmt/vet miss)
    - `go vet` across the build-tag sets the change touches
    - docs generation (`make docs`) when capability tables or service docs changed
    - web: pnpm typecheck / lint / test
  Do not paste full passing output unless it carries useful context.

  State plainly any step you did NOT run, and why. "Could not run X because Y"
  is far more useful to a reviewer than silence or a guess.
-->

-

## Notes

<!--
  Review-relevant context that does not belong in a commit message. Write
  "None" if there genuinely is none.

  THE SURPRISE CHECK — answer this for the branch as a whole before opening:
  will these changes behave surprisingly, in a bad way, for someone whose code
  was written against real AWS?

  The worst version is directional: it works on Overcast and fails on AWS. That
  is the most damaging thing this project can ship — the user tested first, and
  we told them yes. It almost always comes from permissiveness: accepting an
  input AWS rejects, skipping a validation AWS enforces, defaulting a field AWS
  requires, ignoring a property AWS acts on. Where you cannot verify which side
  a validation falls on, prefer the behaviour that fails locally and loudly.

  If something here could surprise and the change is still right, KEEP IT AND
  NAME IT. A surprise you disclosed is a design decision; the same surprise
  found after merge is a bug with your name on it.

  Also use this section for:
    - AWS docs links for compatibility-sensitive behaviour; real-AWS
      observations, with date/tool/version
    - intentional emulator limitations
    - forks in the road where several options were defensible and you picked one
    - risk areas you want a reviewer to inspect
    - follow-ups that are real, specific and worth tracking
-->

-

<!--
  CHANGELOG — a fragment under .changelog/ is required for anything
  release-note-worthy. A PR without one has to say that is deliberate, and why
  (e.g. "/no-changelog — internal refactor, no user-visible change").
  See .changelog/README.md for the entry grammar.
-->
