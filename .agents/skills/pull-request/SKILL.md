---
name: pull-request
description: "Prepare Overcast pull requests with digestible commit hygiene, concise PR summaries, CHANGELOG management, and AWS compatibility and visual evidence. Use when: creating a PR, preparing a branch for review, splitting commits, writing PR descriptions, screenshotting a visual change for reviewers, or deciding what belongs in CHANGELOG.md."
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

All coding standards are in [CONTRIBUTING.md](../../../CONTRIBUTING.md). Agent guardrails are in [AGENTS.md](../../../AGENTS.md). Changelog format and release rules are in [CHANGELOG.md](../../../CHANGELOG.md).

---

## When to Use

- Creating a pull request with `gh pr create`
- Preparing a branch before review
- Reviewing or improving commit structure
- Deciding whether to split, squash, or reorder commits
- Writing a PR title/body
- Updating `CHANGELOG.md`
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

## CHANGELOG Management

`CHANGELOG.md` is release-facing, not a commit log. Update it when the change is notable to users, SDK clients, service compatibility, configuration, deployment, or the web UI.

### Update CHANGELOG when

- Adding a new service, endpoint, resource type, or UI capability.
- Changing AWS-visible behavior, response shape, validation, pagination, state transitions, identifiers, or error codes.
- Fixing a bug users could observe.
- Adding or changing configuration, Docker/runtime behavior, or CloudFormation support.
- Improving compatibility in a way users may rely on.

### Usually skip CHANGELOG for

- Tests only.
- Internal refactors with no behavior change.
- Comment-only changes.
- CI or build maintenance with no user-facing effect.
- Follow-up formatting or generated-file churn.

### How to edit `[Unreleased]`

- Follow the inline instructions already present in `CHANGELOG.md`.
- Put entries under the correct Keep a Changelog category: `Added`, `Changed`, `Fixed`, `Removed`, `Deprecated`, or `Security` when present.
- Do not use `[service]` prefixes as a substitute for categorization; `[sqs] `ReceiveMessage`` still belongs under `Fixed` if it fixes behavior, or `Added` if it adds new support.
- Keep `[Unreleased]` tidy: first look for an existing service/area bullet and augment it instead of adding a new list item.
- Do not add one bullet per commit, endpoint, or tiny behavior tweak.
- Describe the change with a clear verb: added, changed, removed, fixed, aligned, or updated.
- For fixes and compatibility changes, mention the old behavior and the new behavior so users know what changed.
- Prefer service-prefixed endpoint phrasing for endpoint-specific changes: `[sqs] `ReceiveMessage` now returns an empty result after long-poll timeout instead of returning an error`.
- Use service-level phrasing when the change affects many endpoints: `[dynamodb] pagination now preserves index and table keys across Query and Scan responses`.
- Use area-level phrasing for cross-cutting work: `[cloudformation] nested stacks now cascade deletion to child stacks instead of leaving child resources orphaned`.
- Keep entries concise and release-note friendly; bullets should not become novellas.
- Use semicolons to append related capabilities to an existing service bullet.
- If an existing bullet is becoming too long to scan, rewrite it into a tighter summary rather than blindly appending more clauses.
- Add a dedicated bullet only for a genuinely new service, cross-cutting feature, distinct user-facing area, or targeted shipped-version fix that needs a commit reference.
- Do not mention internal file names unless they are the user-facing change.

### Categorizing changelog items

- First choose the release category (`Added`, `Changed`, `Fixed`, `Removed`, `Deprecated`, `Security`). Then choose the item prefix.
- Endpoint-specific: start with `[service] `EndpointName`` and describe the before/after behavior.
- Service-wide: start with `[service]` and summarize the behavior across endpoints without listing every operation.
- Cross-cutting: start with `[area]`, such as `[cloudformation]`, `[web]`, `[router]`, `[state]`, `[docs]`, or `[compat]`.
- New services: use the existing bold service bullet style when adding a genuinely new service under `Added`.
- Unsupported or removed behavior: say what no longer happens and what users should expect instead.

Example CHANGELOG style:

```markdown
- **SQS** — ...; `ReceiveMessage` long polling (`WaitTimeSeconds` up to 20 s, returns early when a message arrives)
- [dynamodb] `Query`/`Scan` now apply `Limit` before filtering instead of after filtering, matching AWS `ScannedCount` semantics
- [sqs] `ReceiveMessage` now returns an empty result after long-poll timeout instead of returning an error
```

Reasonable `[Unreleased]` example:

```markdown
## [Unreleased]

### Added

- **Glue Data Catalog** — new service with database and table CRUD via JSON 1.1 (`CreateDatabase`, `GetDatabase`, `GetDatabases`, `DeleteDatabase`, `CreateTable`, `GetTable`, `GetTables`, `DeleteTable`)
- [sqs] `ReceiveMessage` now supports long polling with `WaitTimeSeconds` up to 20 seconds instead of returning immediately when no messages are available
- [cloudformation] nested stacks now fetch `TemplateURL` templates and cascade deletion to child stacks instead of leaving child resources unmanaged

### Changed

- [lambda] container invocations now connect functions with `VpcConfig` to synthetic VPC Docker networks instead of running all functions on the default bridge network
- [web] service detail pages now group unsupported operations separately instead of mixing them into the primary action list

### Fixed

- [dynamodb] `Query` and `Scan` now apply `Limit` before filtering instead of after filtering, matching AWS `ScannedCount` and pagination semantics
- [s3] `CreateBucket` now rejects malformed `LocationConstraint` values with AWS-compatible errors instead of silently creating the bucket
- [apigateway] proxy executions now preserve multi-value query parameters instead of collapsing repeated keys to the last value

### Removed

- [sns] `Subscribe` no longer accepts unsupported `application` and `firehose` protocols as pending subscriptions; it now rejects them with `InvalidParameter`
```

This example is intentionally mixed: new services use the existing bold service summary style, endpoint-specific changes use `[service] `EndpointName``, and broader service or area changes use `[service]` or `[area]`.

---

## Branch Preparation Checklist

Before creating the PR:

1. Check `git status` for untracked and unrelated files.
2. Review `git diff` and the branch diff against the base branch.
3. Confirm commits are coherent and readable.
4. Confirm `CHANGELOG.md` is updated or intentionally skipped.
5. Run scoped tests and required docs generation for changed areas.
6. Run final verification. Prefer `make check` (`fmt vet lint test`) over assembling a subset — "targeted equivalents" is how a required CI job gets skipped. At minimum: `go build ./...`, `go vet ./...`, `make lint-go`, and the scoped tests. Lint is not optional and is not implied by the others: CI runs it as its own job, and staticcheck findings pass build, vet and tests. For `web/` changes add `pnpm run typecheck` and `pnpm run lint` — never bare `tsc --noEmit`, which resolves the solution-style `web/tsconfig.json`, compiles zero files and always exits 0 (`tsc -b` is a correct alternative).
7. Ensure no secrets, local config, or throwaway debug output are included.
8. For visual changes, capture screenshots and confirm any capture harness (temporary pages under `web/public/`, seeded fixtures) is removed from the branch.

When creating a PR with `gh pr create`, use a heredoc for the body so markdown stays readable.

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
