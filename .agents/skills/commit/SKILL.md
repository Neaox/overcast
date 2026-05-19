---
name: commit
description: "Create clean, human-readable Overcast commits with safe staging, concise messages, coherent commit boundaries, and minimal AWS compatibility references. Use when: committing changes, drafting commit messages, splitting staged work, or reviewing commit hygiene before a PR."
compatibility: opencode
metadata:
  audience: contributors
  workflow: commit
  languages: "go,typescript,markdown"
argument-hint: "Changed files, desired commit scope, or issue number"
license: MIT
---

# Commit — Overcast

Create commits that future maintainers can understand quickly. A commit should explain one coherent change and why it exists. The PR body can carry broader context, compatibility research, and detailed review notes.

All coding standards are in [CONTRIBUTING.md](../../../CONTRIBUTING.md). Agent guardrails are in [AGENTS.md](../../../AGENTS.md). PR preparation guidance is in the `pull-request` skill.

---

## When to Use

- The user asks to commit changes.
- Drafting or improving a commit message.
- Deciding what files belong in a commit.
- Splitting a messy working tree into coherent commits.
- Checking commit hygiene before creating a PR.

Do NOT create a commit unless the user explicitly asks. If the user asks for a PR, use the `pull-request` skill as well.

---

## Safety First

Before committing:

1. Run `git status --short` to see modified and untracked files.
2. Run `git diff` to inspect unstaged changes.
3. Run `git diff --staged` to inspect already staged changes.
4. Run recent `git log` to match the repository's commit style.
5. Do not stage unrelated user or agent changes.
6. Do not commit secrets, local config, credentials, debug dumps, or throwaway files.
7. Do not amend unless the user explicitly requested it and the repository safety rules allow it.

If the working tree contains unrelated changes, leave them alone and stage only files relevant to the requested commit.

---

## Commit Boundaries

Use one commit per coherent reason. A reviewer should be able to read the commit and understand a single intent.

Good boundaries:

- Handler behavior plus its tests.
- Store interface change plus both store implementations and tests.
- Documentation and generated capability updates for one service change.
- Web UI change plus its focused tests or type fixes.

Split commits when:

- A refactor is needed before a behavior change.
- Tests/docs are large enough to obscure the code change.
- Multiple services changed for different reasons.
- Generated files or capability tables are noisy.
- A bug fix and a new feature are mixed together.

Do not split so aggressively that commits stop building or become hard to follow.

---

## Message Format

Prefer concise conventional-style subjects:

```text
<type>(<scope>): <imperative summary>
```

Common types:

- `feat`: new user-facing capability, endpoint, service, resource type, or UI feature.
- `fix`: bug fix or regression fix.
- `compat`: AWS behavior alignment where compatibility is the primary story.
- `test`: tests only.
- `docs`: documentation only.
- `refactor`: internal restructuring with no behavior change.
- `chore`: maintenance, generated files, tooling, or build updates.

Common scopes:

- Service name: `s3`, `sqs`, `lambda`, `dynamodb`, `cloudformation`.
- Area name: `web`, `state`, `router`, `docs`, `compat`, `ci`.

Examples:

```text
feat(sqs): support receive-message long polling
fix(dynamodb): apply limit before filter expression
compat(lambda): align layer version route handling
test(s3): cover object version listing markers
docs(cloudformation): update resource support matrix
refactor(state): share sqlite store migration helpers
```

Keep subjects under roughly 72 characters when practical. Use the imperative mood: `add`, `fix`, `align`, `update`, `remove`.

---

## Commit Bodies

Most commits can be subject-only. Add a short body when the change needs context.

Good body content:

- Root cause for a bug.
- User-visible or AWS-visible behavior change.
- One important AWS docs link for compatibility-sensitive behavior.
- A short caveat or follow-up if it affects future work.

Avoid body content:

- Full test output.
- Long implementation walkthroughs.
- Multiple AWS links when one authoritative link is enough.
- PR-level research notes.
- Vague TODOs without a concrete reason.

Example:

```text
fix(sqs): return empty receive result for idle queues

Match AWS by returning a successful empty result instead of an error when
long polling times out without messages.

Refs: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ReceiveMessage.html
```

---

## AWS Compatibility Commits

For compatibility changes, the commit should make the intent obvious without becoming a mini design doc.

Include:

- The AWS-visible behavior being aligned.
- The affected operation or resource.
- One authoritative reference if the behavior is not obvious.

Usually leave deeper context for the PR body:

- Full AWS research trail.
- Other emulator comparisons.
- Long edge-case explanations.
- Risk assessment and follow-up plan.

Use `compat` when compatibility is the primary purpose. Use `fix` when the change is better understood as a bug fix, even if the expected behavior comes from AWS.

---

## Staging Discipline

Stage intentionally. Do not use `git add .` when the working tree contains unrelated changes.

Preferred approach:

1. Identify files that belong to the commit from `git status --short` and `git diff`.
2. Stage explicit paths with `git add <path> ...`.
3. Re-check `git diff --staged` before committing.
4. Confirm staged changes match the commit message.

If relevant changes are interleaved with unrelated edits in the same file, stop and ask before attempting partial staging unless the user explicitly asked for a split.

---

## CHANGELOG Relationship

Commits do not replace `CHANGELOG.md`.

- If the change is user-visible, compatibility-visible, config-visible, or release-note-worthy, ensure the branch includes an appropriate `[Unreleased]` entry.
- Put changelog entries under the correct Keep a Changelog category: `Added`, `Changed`, `Fixed`, `Removed`, `Deprecated`, or `Security` when present.
- Keep `[Unreleased]` tidy: augment an existing relevant service/area bullet when one exists.
- Do not add one changelog bullet per commit, endpoint, or small tweak.
- Describe the change with a clear verb: added, changed, removed, fixed, aligned, or updated.
- For fixes and compatibility changes, mention the old behavior and the new behavior so users know what changed.
- Prefer service-prefixed endpoint phrasing for endpoint-specific changes: `[sqs] `ReceiveMessage` now returns an empty result after long-poll timeout instead of returning an error`.
- Use `[service]` for service-wide changes and `[area]` for cross-cutting changes such as `[cloudformation]`, `[web]`, `[router]`, `[state]`, `[docs]`, or `[compat]`.
- Do not use `[service]` prefixes as a substitute for categorization; choose the category first, then phrase the entry.
- Keep changelog entries release-facing and concise; do not let bullets become novellas.
- If a bullet is getting too long, rewrite it into a tighter summary instead of appending indefinitely.
- Use the `pull-request` skill for detailed CHANGELOG management and a full reasonable `[Unreleased]` example before opening a PR.

---

## Verification Notes

Run verification before committing when changes include code, generated docs, or behavior.

Mention verification in the final response to the user, not usually in the commit body. Only include verification in the commit body when it is critical evidence for the change.

Examples:

- `go test -count=1 ./internal/services/sqs/... ./tests/integration/sqs/...`
- `gofmt -w ./internal/services/sqs ./tests/integration/sqs`
- `go vet ./internal/services/sqs/... ./tests/integration/sqs/...`
- `npx tsc --noEmit` from `web/` for TypeScript changes.
- `make docs` when capability tables or generated service docs changed.

---

## Final Response After Commit

After a successful commit, tell the user:

- Commit hash and subject.
- What was included.
- Verification run, or why it was not run.
- Any files intentionally left uncommitted.

Keep it brief.
