---
name: pull-request
description: "Prepare Overcast pull requests with digestible commit hygiene, concise PR summaries, changelog fragment management, and AWS compatibility and visual evidence. Use when: creating a PR, preparing a branch for review, splitting commits, writing PR descriptions, screenshotting a visual change for reviewers, or deciding whether a change needs a changelog fragment under .changelog/."
compatibility: opencode
metadata:
  audience: contributors
  workflow: review
  languages: "go,typescript,markdown"
argument-hint: "PR goal, base branch, issue number, or changed service"
license: MIT
---

# Pull Request — Overcast

Prepare pull requests that are easy for humans to review. Commits should be readable and intentionally scoped; the PR body carries the deeper context, compatibility evidence, and review notes.

All coding standards are in [CONTRIBUTING.md](../../../CONTRIBUTING.md). Agent guardrails are in [AGENTS.md](../../../AGENTS.md). Changelog fragment format is in [.changelog/README.md](../../../.changelog/README.md); release rules are in [CHANGELOG.md](../../../CHANGELOG.md).

---

## When to Use

- Creating a pull request with `gh pr create`
- Preparing a branch before review
- Reviewing or improving commit structure
- Deciding whether to split, squash, or reorder commits
- Writing a PR title/body
- Adding a changelog fragment under `.changelog/`
- Documenting AWS compatibility evidence for a change
- Capturing screenshots for a change reviewers have to look at to judge

Do NOT use this as a substitute for implementation skills:

- New endpoints or services still use `new-feature`
- Bug fixes still use `bug-fix`
- Compatibility audits still use `aws-compatibility-review`
- Code reviews still use `code-review`

---

## Commit Hygiene

Commits are a navigation aid for future maintainers. Keep them digestible, human readable, and reviewable in isolation.

### Commit principles

- One coherent reason per commit.
- Prefer small commits that leave the tree buildable.
- Avoid noisy checkpoint commits such as `fix tests`, `wip`, or `cleanup`.
- Do not bury unrelated formatting, generated docs, or opportunistic refactors inside behavior changes.
- Keep commit subjects concise and concrete.
- Put detail in the PR body when it would make the commit message bulky.

### Good commit scopes

- `feat(sqs): support receive-message long polling`
- `fix(dynamodb): apply limit before filter expression`
- `compat(lambda): align layer version route handling`
- `test(s3): cover version listing markers`
- `docs(cloudformation): update resource support matrix`
- `chore(web): refresh generated route tree`

Use `compat` when the primary purpose is matching real AWS behavior, response shape, validation, status codes, identifiers, pagination, or state transitions. Use `fix` when correcting a bug even if AWS compatibility is the reason.

### Commit body guidance

Most commits should not need a long body. Add a short body only when it helps future readers understand why the change exists.

Include, when useful:

- The root cause for a bug fix.
- The behavior changed at the AWS/API boundary.
- A short note about intentional limitations or follow-up work.
- A reference to a tracked issue, PR, or authoritative AWS doc.

Avoid in commit bodies:

- Full test logs.
- Long AWS research notes.
- Multi-paragraph implementation walkthroughs.
- Every documentation link consulted.

Example:

```text
compat(s3): match create-bucket location validation

Reject malformed LocationConstraint values with the AWS error code so SDK
callers see the same failure path as real S3.

Refs: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateBucket.html
```

---

## PR Body

The PR body is where detail belongs. It should explain the review story without forcing reviewers to reverse-engineer the branch.

### Required sections

Use this shape unless the repository has a more specific PR template:

```markdown
## Summary
- <1-3 bullets describing the user-visible or AWS-visible result>

## Screenshots
- <only when the change is visual — see "Visual Evidence" below; omit the section otherwise>

## Verification
- <tests, builds, docs generation, manual checks>

## Notes
- <compat evidence, intentional limitations, follow-ups, or "None">
```

### Summary guidance

- Describe outcomes, not file lists.
- Name affected services or packages.
- Keep bullets short enough to scan.
- Mention if behavior changed at the AWS wire/API boundary.

### Verification guidance

- Include exact commands run.
- Include docs generation when capability tables or service docs changed.
- Say when a verification step was not run and why.
- Do not paste full passing output unless it contains useful context.
- For visual changes, say the screenshots were captured against a running emulator and dev server, not mocked up.

### Notes guidance

Use `Notes` for review-relevant context that should not be crammed into commits:

- AWS docs links for compatibility-sensitive behavior.
- Real AWS observations, including date/tool/version if used.
- Intentional emulator limitations.
- Follow-ups that are real, specific, and worth tracking.
- Risk areas reviewers should inspect.

Keep notes succinct. If there is extensive compatibility research, link to the issue, doc, or compatibility tracker instead of embedding everything in the PR.

---

## AWS Compatibility Evidence

For compatibility alignments, include enough evidence that reviewers can distinguish deliberate AWS fidelity from guesswork.

Preferred evidence order:

1. AWS API Reference or Developer Guide link.
2. Existing Overcast compatibility test or tracker reference.
3. Other emulator behavior when AWS docs are ambiguous.
4. Real AWS observation, only when the user explicitly approved using real AWS.

Put one or two high-value links in the PR body. Put only the most important link in a commit body, and only when the commit is hard to understand without it.

For surprising behavior, add a short code comment near the implementation using the verification comment style from `new-feature` and `bug-fix` skills. Do not use comments to restate obvious code.

---

## Visual Evidence

A prose description of a layout, a font, or a spacing change is not reviewable. When a change alters what the web UI looks like, consider capturing screenshots — the reviewer should not have to run the branch to judge whether it looks right.

### When screenshots are warranted

- Typography, colour, spacing, alignment, or density changes.
- New components, pages, panels, dialogs, or empty/loading/error states.
- Anything where "does this look correct?" is the actual review question.
- Theme or responsive behavior — see the light/dark and breakpoint guidance below.

### When to skip them

- Logic, data-fetching, or API changes with no visual result.
- Behavior a test asserts more precisely than an image can show.
- Pure refactors that render identically — say so instead.

### What to show

- **Before and after** when changing UI that already exists. A lone "after" gives the reviewer nothing to compare against.
- A single shot for genuinely new UI.
- Only the states that changed. Do not attach ten screenshots when two carry the argument.
- Keep the viewport identical between before and after, or the diff is unreadable.

#### Light and dark mode

Capture both themes when the change touches anything the theme controls — colour, contrast, borders, shadows, overlays, focus rings, or a token whose light and dark values differ. The console ships both (`web/src/styles/global.css` resolves them from `prefers-color-scheme` and the `data-theme` attribute on `<html>`), and a change that reads well on the dark ground can fail contrast on the light one.

One theme is enough when the change cannot differ between them: pure layout, spacing, font family or size, or copy.

#### Viewport breakpoints

Capture more than one width when the change crosses a breakpoint — responsive grids and column counts, sidebars and navigation that collapse, tables that scroll or restack, anything using `sm:`/`md:`/`lg:` variants.

Show the narrowest width where the layout still has to work and the widest where it changes again; the intermediate steps are usually noise. One width is enough for a change that renders identically at every size.

Be honest about the combinatorics: two themes times three widths is six images nobody reads. Pick the axis the change actually moves, and show the other axis once.

### Capturing

- Run the emulator and the dev server, and seed a real resource. Screenshot the running app, never a mockup.
- Capture the "after" first, while the branch is checked out.
- Drive theme and width from the browser tooling rather than by hand, so the pair really is identical apart from the axis under test — the preview browser's `resize_window` takes both a `colorScheme` and explicit `width`/`height`.
- To capture the "before", stash **only your own tracked changes** (`git stash push -- <paths>`), screenshot, then restore.

  Beware: `git stash` is shared across all worktrees of a repo. A concurrent session can push its own stash on top of yours between your push and your pop, so `git stash pop` may restore the wrong one. Prefer capturing "before" from a separate checkout of the base branch, and if you must stash, apply by commit hash (`git stash apply <sha>`) rather than by position.

### Hosting

`gh` cannot upload images to GitHub — the web uploader is browser-only. So:

- Push the images to a throwaway `assets/<branch-name>` branch and reference them with `raw.githubusercontent.com` URLs. This keeps binaries out of the PR diff and out of `main`'s history.
- Note in the PR body that the assets branch should be deleted after merge.
- Never commit screenshots into the repo tree to make them render.
- Never screenshot real credentials, real account identifiers, or customer data. Emulator fixtures only.

Example:

```markdown
## Screenshots

| Before | After |
| --- | --- |
| ![before](https://raw.githubusercontent.com/<owner>/<repo>/assets/<branch>/before-dark.png) | ![after](https://raw.githubusercontent.com/<owner>/<repo>/assets/<branch>/after-dark.png) |

Light mode, where the muted border had to change to hold contrast:

![after, light](https://raw.githubusercontent.com/<owner>/<repo>/assets/<branch>/after-light.png)

At 375px, where the three-column grid collapses to one:

![after, narrow](https://raw.githubusercontent.com/<owner>/<repo>/assets/<branch>/after-375.png)

Hosted on the `assets/<branch>` branch to keep them out of this diff; delete it after merge.
```

Name the files for the axis they vary — `after-light.png`, `after-375.png` — so a reviewer knows what they are looking at before the image loads.

---

## Changelog Management

The changelog is release-facing, not a commit log. Record a change when it is notable to users, SDK clients, service compatibility, configuration, deployment, or the web UI.

**Never edit the `[Unreleased]` section of `CHANGELOG.md`.** It stays empty between releases and CI (`python3 scripts/changelog.py check`) fails any PR that writes into it. Instead, add one fragment file per PR under `.changelog/` — new files at unique paths cannot merge-conflict, so PRs never fight over the changelog. At release time the fragments are curated into the new versioned section (see `.changelog/README.md` and RELEASE.md).

### Add a changelog fragment when

- Adding a new service, endpoint, resource type, or UI capability.
- Changing AWS-visible behavior, response shape, validation, pagination, state transitions, identifiers, or error codes.
- Fixing a bug users could observe.
- Adding or changing configuration, Docker/runtime behavior, or CloudFormation support.
- Improving compatibility in a way users may rely on.

### Usually skip the fragment for

- Tests only.
- Internal refactors with no behavior change.
- Comment-only changes.
- CI or build maintenance with no user-facing effect.
- Follow-up formatting or generated-file churn.
- Anything under `compat/`. The suites observe the emulator rather than change it, however large the diff.

### Skipping it is an explicit act

The `Changelog entry` check fails any PR that adds nothing under `.changelog/` — a forgotten fragment and a fragment nobody needed are the same empty diff, so the check asks rather than guesses. When the PR genuinely needs none, say so on the PR and the check clears itself:

```sh
gh pr comment <number> --body '/no-changelog CI-only: pins the release action to a digest, nothing shipped changes'
```

- The reason is required and is kept as the record of the decision — write the actual reason, not a placeholder. `n/a` is refused.
- Comment it as soon as the PR is opened. The check reads the PR's comments when it runs, so a waiver already there means it passes first time instead of going red and being cleared.
- `/needs-changelog` puts the question back. Do that yourself if you push work to the PR afterwards that users would want to read about: the waiver covers the PR, not the commit it was written on.
- No comment is needed when **every** file the PR touches is in an area that never produces a release note: `compat/`, `cmd/compat/`, `tests/`, test files anywhere (`*_test.go`, `*_test.py`, `*.test.tsx`, `*.spec.ts`), `docs/plans/`, `docs/dev/`, `.agents/`, `.claude/`, `.vscode/`, `.devcontainer/`, contributor docs (`AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`), and local tooling (`.golangci.yml`, `.air.toml`, `opencode.json`, `.gitignore`, `.gitattributes`). One file outside them and the check asks.
- Adding the fragment is always the better answer when the change is release-note-worthy. Do not waive to make a red check go away.

### During a release window

**Never edit `CHANGELOG.md` to answer the check.** That file belongs to the release PR — the bot merges `main` into the release branch on every push, and a second hand in the same section aborts that merge and stops the release PR refreshing itself. The check does not accept a `CHANGELOG.md` edit in place of a fragment.

Where the note goes depends on how far the release has got:

- **Release PR open.** Add a fragment exactly as usual. On the next push to `main` the bot folds it into the `## [x.y.z]` section and deletes the fragment; nothing else is needed. An open release PR is never a reason to skip the fragment. If the entry is **breaking** and the release in flight is not `0.x`, `Breaking-change hold` blocks the merge until the release goes out — the fragment stays correct and still gets folded in; only the merge waits.
- **Release PR merged, tag not yet published** (`bash scripts/release-candidate-check.sh` prints `true` on an ordinary PR). A fragment fails the release and there is nothing left to fold into. Wait for the tag if the change can wait — `main` in this state should be taking only what is needed to get the release out. If it cannot, waive with a reason saying the fragment is coming, and add it after the tag.
- **You are on the release PR.** Nothing to do: it is exempt by shape (`VERSION` + `CHANGELOG.md` + fragment deletions only, untagged version, non-empty section). Do not waive it and do not add a fragment. Note that `release-candidate-check.sh` prints `true` here too — that alone does *not* mean the release has merged, and reading it that way is what produced the wrong bot comment on [#563](https://github.com/Neaox/overcast/pull/563). If you push a code change onto the release branch, the check asks again and it is asking correctly: this is the **one** exception to "never edit `CHANGELOG.md`" — write the note straight into the `## [x.y.z]` section, because you are the hand that owns the file and a fragment left in `.changelog/` would fail the release. Better still, put the change on its own PR to `main` and let it fold itself in.

### How to write a fragment

- Write them with `python3 scripts/changelog.py new -e '<entry>'` rather than by hand — it names the file after the branch and appends, so neither the filename nor the syntax can be wrong. `--dry-run` shows the result without writing. Fragments have no frontmatter: every line stands alone.
- Entry grammar, one entry per line: `<+|-|~|*|section>[!|.] [area[/area...]] <prose>`. `+` Added, `-` Removed, `~` Changed, `*` Fixed; `deprecated` and `security` are spelled out. An indented line continues the entry above it.
- One file per PR, holding as many entries as the PR needs. Full format rules are in `.changelog/README.md`; `python3 scripts/changelog.py check` lints them locally.
- Choose the category for what the change *is*, not the service it touches; `[sqs] `ReceiveMessage`` still belongs under `Fixed` if it fixes behavior, or `Added` if it adds new support.
- Put the service/area slugs in `[...]`, primary first — it sorts related entries together at release time; omit for changes with no natural area. A change spanning several services stays one entry naming them all (`[efs/ecs]`), never one entry per service.
- Compatibility is an axis of its own, not a category. Unmarked means not breaking, **except `-` Removed, which means breaking unless written `-.`**. Mark `!` when existing code, config, or stored state stops working — including a newly *required* field or config key (an addition), stricter validation, a changed default, or a state format old data cannot survive. A `!` entry needs an indented `migration:` line. The linter also demands an explicit `!` or `.` when the prose reads like a break ("now requires", "now rejects", "no longer accepts", …).
- Write the fragment for this PR only. Do not edit other PRs' fragments and do not pre-aggregate per service — merging related entries into one bullet happens once, at release time.
- Do not write one bullet per commit, endpoint, or tiny behavior tweak — one fragment with one or two bullets covering the PR's user-visible change is the target.
- Describe the change with a clear verb: added, changed, removed, fixed, aligned, or updated.
- For fixes and compatibility changes, mention the old behavior and the new behavior so users know what changed; describe the full affected scope discovered during investigation, not only the original repro case.
- Prefer service-prefixed endpoint phrasing for endpoint-specific changes: `[sqs] `ReceiveMessage` now returns an empty result after long-poll timeout instead of returning an error`.
- Use service-level phrasing when the change affects many endpoints: `[dynamodb] pagination now preserves index and table keys across Query and Scan responses`.
- Use area-level phrasing for cross-cutting work: `[cloudformation] nested stacks now cascade deletion to child stacks instead of leaving child resources orphaned`.
- Keep entries concise and release-note friendly; bullets should not become novellas.
- Do not mention internal file names unless they are the user-facing change.

### Categorizing changelog items

- First choose the release category (`Added`, `Changed`, `Fixed`, `Removed`, `Deprecated`, `Security`). Then choose the item prefix.
- Endpoint-specific: start with `[service] `EndpointName`` and describe the before/after behavior.
- Service-wide: start with `[service]` and summarize the behavior across endpoints without listing every operation.
- Cross-cutting: start with `[area]`, such as `[cloudformation]`, `[web]`, `[router]`, `[state]`, `[docs]`, or `[compat]`.
- New services: use the existing bold service bullet style when adding a genuinely new service under `Added`.
- Unsupported or removed behavior: say what no longer happens and what users should expect instead.

Example bullet style (used in fragment bodies and in the released changelog):

```markdown
- **SQS** — ...; `ReceiveMessage` long polling (`WaitTimeSeconds` up to 20 s, returns early when a message arrives)
- [dynamodb] `Query`/`Scan` now apply `Limit` before filtering instead of after filtering, matching AWS `ScannedCount` semantics
- [sqs] `ReceiveMessage` now returns an empty result after long-poll timeout instead of returning an error
```

Reasonable fragment examples:

`.changelog/20260731-glue-data-catalog.md` — a new service uses the bold service summary style:

```markdown
---
section: Added
area: glue
---

- **Glue Data Catalog** — new service with database and table CRUD via JSON 1.1 (`CreateDatabase`, `GetDatabase`, `GetDatabases`, `DeleteDatabase`, `CreateTable`, `GetTable`, `GetTables`, `DeleteTable`)
```

`.changelog/20260731-dynamodb-limit-before-filter.md` — an endpoint-specific fix uses `[service] `EndpointName`` with before/after behavior:

```markdown
---
section: Fixed
area: dynamodb
---

- [dynamodb] `Query` and `Scan` now apply `Limit` before filtering instead of after filtering, matching AWS `ScannedCount` and pagination semantics
```

---

## Branch Preparation Checklist

Before creating the PR:

1. Check `git status` for untracked and unrelated files.
2. Review `git diff` and the branch diff against the base branch.
3. Confirm commits are coherent and readable.
4. Confirm a changelog fragment is added under `.changelog/` or intentionally skipped; never edit `CHANGELOG.md`'s `[Unreleased]` section.
5. Run scoped tests and required docs generation for changed areas.
6. Run final verification. Prefer `make check` (`fmt vet lint test`) over assembling a subset — "targeted equivalents" is how a required CI job gets skipped. At minimum: `go build ./...`, `go vet ./...`, `make lint-go`, and the scoped tests. Lint is not optional and is not implied by the others: CI runs it as its own job, and staticcheck findings pass build, vet and tests. For `web/` changes add `pnpm run typecheck` and `pnpm run lint` — never bare `tsc --noEmit`, which resolves the solution-style `web/tsconfig.json`, compiles zero files and always exits 0 (`tsc -b` is a correct alternative).
7. Ensure no secrets, local config, or throwaway debug output are included.
8. For visual changes, capture screenshots and confirm any capture harness (temporary pages under `web/public/`, seeded fixtures) is removed from the branch.

When creating a PR with `gh pr create`, use a heredoc for the body so markdown stays readable.

---

## After Opening — Waiting on CI

Enable auto-merge as a separate step right after opening, then **wait with one
command**:

```sh
gh pr merge <n> --squash --auto
scripts/pr-wait.sh <n>            # or scripts\pr-wait.ps1 <n>
```

`scripts/pr-wait.sh` wraps `gh pr checks --watch --fail-fast` and exits
0 / 1 / 2 / 8 (passed / failed / no checks will run / still pending). Run it in
the **background** — in Claude Code, the `Bash` tool with
`run_in_background: true` — so its single completion notification is the only
thing the conversation gets.

It does three things a bare `--watch` does not:

- **Returns at the first failure** rather than waiting out the rest of a doomed
  run, so investigation starts minutes earlier.
- **Fetches the failure detail.** For each failing job it prints the failure
  annotations and the tail of the failing step, capped
  (`PR_WAIT_MAX_JOBS`/`MAX_ANNOTATIONS`/`MAX_LOG_LINES`). The annotations are
  usually the whole answer — a compat gate, for instance, names the exact
  `suite/group/test` that regressed — so you diagnose from the notification
  instead of making three more calls to find out what broke.
- **Survives a re-run.** Push a fix and run it again: it waits on the new head's
  checks. If the head moves while it is watching it exits 8 and tells you the
  result is stale, rather than reporting a verdict for a commit you have already
  replaced.

It also waits for checks to *appear* before watching, since a freshly pushed
branch reports none for the first few seconds, and re-reads `mergeStateStatus`
at the end.

**Do not hand-roll a poll loop.** The recurring anti-pattern is a `while` loop
over `gh pr checks --json` on a `sleep` interval, wired to a per-iteration
notifier. It costs a request per interval, fires whether or not anything
changed, and turns "tell me when CI is done" into a running commentary — dozens
of "N passed, no failures" messages that change no decision. `gh pr checks`
already has `--watch`; there is nothing to reimplement. If you find yourself
writing `sleep` next to `gh pr checks`, stop and use the script.

Corollaries that have each cost a recovery:

- **Never pipe an exit-code-bearing gh command into another** (`gh ... --watch | tail`).
  The pipeline reports the *last* command's status, which is how
  [#410](https://github.com/Neaox/overcast/pull/410) merged over a failing compat
  check. `pr-wait.sh` redirects to a file for exactly this reason.
- **Green checks are not `CLEAN`.** `gh pr merge` proceeds on `UNSTABLE` (a
  failing *non-required* check) and only stops on `BLOCKED`. Read
  `mergeStateStatus`, which `pr-wait.sh` prints.
- **No checks at all means `CONFLICTING`, not queued** — GitHub dispatches no
  workflows on a conflicting PR. `pr-wait.sh` detects this and exits 2 rather
  than watching nothing until it times out.
- **A compat-gate failure is a stop signal even when the tests look unrelated.**
  Investigate, re-run the *full* workflow, and use the `flaky.json` quarantine
  process rather than merging past it.

While waiting, report only what changes a decision: a failure, or the final
result. Silence is the correct output for "still running".

---

## PR Title

Prefer a clear outcome-oriented title:

- `Add SQS long polling support`
- `Fix DynamoDB Query limit semantics`
- `Align Lambda layer route handling with AWS`
- `Update CloudFormation resource support docs`

Avoid titles that describe mechanics only:

- `Update handlers`
- `Fix tests`
- `Changes from review`
- `WIP`

---

## Example PR Body

```markdown
## Summary
- Aligns DynamoDB `Query`/`Scan` limit handling with AWS by applying `Limit` before filtering.
- Updates integration coverage for count and pagination behavior.
- Notes the behavior in the DynamoDB changelog entry.

## Verification
- `go test -count=1 ./internal/services/dynamodb/... ./tests/integration/dynamodb/...`
- `gofmt -w ./internal/services/dynamodb ./tests/integration/dynamodb`
- `go vet ./internal/services/dynamodb/... ./tests/integration/dynamodb/...`

## Notes
- AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Query.html
- Follow-up: none.
```
