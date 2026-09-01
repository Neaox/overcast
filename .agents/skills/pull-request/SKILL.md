---
name: pull-request
description: "Prepare and own Overcast pull requests through green required checks and merge readiness, with digestible commit hygiene, concise PR summaries, changelog fragment management, and AWS compatibility and visual evidence. Use when: creating or monitoring a PR, preparing a branch for review, fixing checks on an owned PR, splitting commits, writing PR descriptions, screenshotting a visual change for reviewers, or deciding whether a change needs a changelog fragment under .changelog/."
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
- <one line naming the theme/width variations you captured and why those are the axes this
  change moves>

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
- For visual changes, say the screenshots were captured from a real browser driven against an emulator built from the branch, not mocked up.

### Notes guidance

Use `Notes` for review-relevant context that should not be crammed into commits:

- AWS docs links for compatibility-sensitive behavior.
- Real AWS observations, including date/tool/version if used.
- Intentional emulator limitations.
- Follow-ups that are real, specific, and worth tracking.
- Risk areas reviewers should inspect.
- Anything that could surprise code written against real AWS, and any fork in the road where
  several options were defensible and you picked one — see [The surprise check](#the-surprise-check--run-this-before-opening-the-pr).

Keep notes succinct. If there is extensive compatibility research, link to the issue, doc, or compatibility tracker instead of embedding everything in the PR.

---

## The surprise check — run this before opening the PR

Before writing the description, ask one question about the branch as a whole:

> **Will these changes cause surprising or unexpected behavior — in a bad way — for someone whose code was written against real AWS?**

Surprise is the failure mode this project actually ships. A response that looks right and acts
wrong costs a user a debugging session they will blame on their own code, because the emulator
answered confidently.

**The worst version is directional: it works on Overcast and fails on AWS.** A stack that
deploys clean locally and then breaks in the account is the single most damaging thing we can
produce — the user did the responsible thing, tested first, and we told them yes. The divergence
that causes it is almost always *permissiveness*: we accept an input AWS rejects, skip a
validation AWS enforces, default a field AWS requires, ignore a resource property AWS acts on,
or allow a combination AWS refuses. Being stricter than AWS is a bug too, but it fails loudly
and locally, where the user can see it. Being laxer fails in production. **When you cannot
verify which side a validation falls on, prefer the behavior that fails locally.**

Work through where surprise comes from:

- **A fork in the road settled on taste.** The branch picked one of several defensible options —
  a name for a derived resource, a default for an omitted field, error-vs-no-op on a missing
  resource, empty list vs omitted field, ordering of side effects or validation — without
  checking what that service does. See [CONTRIBUTING § AWS is the tie-breaker](../../../CONTRIBUTING.md#aws-is-the-tie-breaker).
- **A missing constraint reads as a working feature.** Required-vs-optional fields, value ranges,
  name/charset rules, quota-shaped limits, and cross-field combinations that AWS rejects. Nothing
  in the diff looks wrong; the emulator simply never says no. CloudFormation amplifies this — a
  property we accept and ignore turns into a stack that provisions locally and rolls back on AWS.
- **An existing behavior changed shape.** Callers already depend on the old response, status,
  ordering, or timing. Changing it toward AWS is correct and still worth calling out; changing
  it away from AWS is a defect.
- **A fidelity fix moved a divergence somewhere else** — a sibling handler, a CloudFormation
  path, the web UI, a compat suite — rather than removing it.
- **A `200` now hides a gap** that previously answered `501`.

**Additive is fine when it is clearly additive.** A new endpoint, a new optional field AWS also
returns, a new internal `/_` surface, a new emulator-only tool — none of these can surprise code
that does not call them, and they need no special defense. The bar applies to anything that
changes what an *existing* call does. If you cannot tell which kind a change is, it is not
clearly additive; treat it as a behavior change and say so under `Notes`.

If the answer is "yes, something here could surprise" and the change is still right, keep it and
name it explicitly under `Notes` — a reviewer who has been told where to look is the point. A
surprise you noticed and disclosed is a design decision; the same surprise found after merge is
a bug with your name on it.

---

## AWS Compatibility Evidence

For compatibility alignments, include enough evidence that reviewers can distinguish deliberate AWS fidelity from guesswork.

**Every claim the PR description makes about how real AWS behaves must link to its source.** If
the description says AWS returns a particular error code, omits a field, orders states a certain
way, or accepts an input Overcast used to reject, the sentence carries a link to the evidence
for that specific claim. This is not satisfied by a general docs link at the bottom, and it is
not satisfied by the claim being true — a reviewer cannot tell a verified fact from a confident
guess without the source, and unsourced assertions are exactly how a wrong one gets waved
through. If you cannot produce a source, say so in the same sentence ("inferred from the SDK
model; not documented") so the uncertainty is reviewable rather than invisible.

Preferred evidence order:

1. AWS API Reference or Developer Guide link.
2. Existing Overcast compatibility test or tracker reference.
3. Other emulator behavior when AWS docs are ambiguous.
4. Real AWS observation, only when the user explicitly approved using real AWS.

Cite the tier you actually used — a compat-test reference is a legitimate source, a docs link you
did not read is not. For a real-AWS observation, include the date and tool/version, since that is
what makes it re-checkable later.

Put the link next to the claim it supports. Put only the most important link in a commit body, and only when the commit is hard to understand without it.

For surprising behavior, add a short code comment near the implementation using the verification comment style from `new-feature` and `bug-fix` skills. Do not use comments to restate obvious code.

---

## Visual Evidence

A prose description of a layout, a font, or a spacing change is not reviewable. When a change alters what the web UI looks like, consider capturing screenshots — the reviewer should not have to run the branch to judge whether it looks right.

### When screenshots are warranted

- Typography, colour, spacing, alignment, or density changes.
- New components, pages, panels, dialogs, or empty/loading/error states.
- Anything where "does this look correct?" is the actual review question.
- Theme or responsive behavior — see the light/dark and breakpoint guidance below.
- Anything whose value depends on it having really run — a Lambda invocation's output, its log records, an ECS task's state. Prose can claim the emulator produced that; a screenshot of it is the proof, and it is the hardest kind to fake.

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

#### Choosing the variations — and saying which you chose

Deciding which images to take is part of the change, not packaging applied afterwards. Work through both axes explicitly, against this diff:

- **Does it touch anything the theme controls?** Colour, contrast, borders, shadows, overlays, focus rings, opacity against a background, or a token whose light and dark values differ. If yes, capture both themes.
- **Does it cross a breakpoint?** Responsive grids and column counts, sidebars and navigation that collapse, tables that scroll or restack, anything using `sm:`/`md:`/`lg:` variants. If yes, capture more than one width.
- **If neither**, one image is the right answer, and the PR should say why one was enough.

Be honest about the combinatorics: two themes times three widths is six images nobody reads, and the one carrying the argument is buried among the five that do not. Pick the axis the change actually moves, and show the other axis once.

**State the decision in the PR body, in one line.** Not the mechanics — the reasoning: which axes you captured, and why those. A reviewer should be able to tell the selection was reasoned about rather than swept, and should be told when an axis was deliberately left out. Blanket-capturing every combination is as much a failure as capturing nothing.

A worked example, from a branch that genuinely needed both axes for different shots and different reasons:

> Widths 375/1280, one theme: the grid orphaned a field onto its own row and a hint wrapped
> inside a narrow column — both pure layout, so the theme cannot change the verdict. Both
> themes at 1280 for the level-select controls: they render `disabled` at reduced opacity,
> and opacity against a background is exactly what reads fine on the dark ground and fails
> contrast on the light one.

Three images, each answering a question a reviewer would otherwise have to run the branch to settle. The six-image sweep would have contained the same three and buried them.

### Capturing

Screenshots come from a real browser, driven against a real emulator, headlessly — so an agent with no display can produce them and a reviewer gets the same image every time.

The repo declares the **`chrome-devtools` MCP server** for both agent clients: [`.mcp.json`](../../../.mcp.json) for Claude Code, [`opencode.json`](../../../opencode.json) for opencode. Claude Code's runs `--headless --isolated` — no window, throwaway profile — because a background agent has no display; opencode's is headful because a human is watching. If the tools are not there, the client has not picked the file up yet — restart the session (and approve the server if prompted) rather than reaching for another mechanism. Nothing is added to `web/package.json` and no browser is installed by the repo.

- **Build the image from the branch and run it on a free loopback port, with the Docker socket mounted** — never 4566/4567, which belong to the user's own instance. Go through the make target and the launcher script rather than calling `docker` yourself: those two are what an agent can be granted (see [§ Permissions](#permissions-for-the-capture) below), and they are the reason the grant is narrower than "run any container with any flags". Start and stop the container in the same step, so a failed capture cannot leave one running:

  ```sh
  make docker-console                          # tags overcast:<sanitised branch>
  scripts/run-test-instance.sh --name overcast-shot --no-logs \
      --image "overcast:$(sh scripts/image-tag.sh)" --mount-docker-socket
  # ... seed, capture ...
  docker stop overcast-shot
  make docker-clean                            # do not leave one image per branch behind
  ```

  **The ports are chosen, not fixed — read them from the script's output.** It prints `API endpoint:` and `Web UI:` lines with the pair it found free at or above 4570, publishes both to `127.0.0.1` only, and refuses 4566 and 4567 in either role. Hardcoding 4590 the way this section used to is how two concurrent captures collide.

  The image tag comes from the branch for the same reason: `overcast:dev` was one tag shared by every worktree on the machine, so a parallel agent's build could land between yours and your `docker run` and you would screenshot their code without any sign that it happened. `scripts/image-tag.sh` derives it and `make docker-clean` removes the pair when you are done.

  Chrome runs on the host, so a loopback-published port is reachable directly — no second container and no Docker networking to arrange. A dev server works too; the image is preferred because it is what a reviewer runs, and it puts the branch's SPA and the emulator in one place.

  **The socket mount is not optional dressing, and skipping it costs you twice.** A container cannot see its own port mapping from the inside, so on remapped ports Overcast cannot tell the SPA where the API is: `deriveAPIBaseURL` returns `endpointKnown: false` and the console shows the *Connect to Overcast* screen instead of your page. With the socket, `resolvePublishedPort` asks Docker for the container's own bindings, recovers the published port, and the console connects to it unprompted. The socket is also what lets the instance run **Lambda and ECS** at all — and a screenshot of a real invocation, with genuine output and real platform log records, is the least fakeable visual evidence there is.

  `--mount-docker-socket` appends exactly `-v /var/run/docker.sock:/var/run/docker.sock` and nothing else. Ask for it when you need it and leave it off when you do not: anything that can reach the Docker socket can start a privileged container, and is therefore root on the host.

- **Seed a real resource** through `scripts/awslocal.sh` against the published API port before capturing. An empty table is a different screenshot from a populated one, and usually not the one under review.

- **If you cannot mount the socket** — a sandbox or a locked-down CI runner may refuse it — the console will show the connect screen, and a screenshot of *that* is the classic wasted capture. Seed the endpoint yourself in `navigate_page`'s `initScript`, which runs before the SPA does:

  ```js
  // <API port> is the one scripts/run-test-instance.sh printed, not a fixed 4590.
  localStorage.setItem('overcast:endpoint', JSON.stringify({
    baseUrl: 'http://127.0.0.1:<API port>', label: 'shot', explicit: true }))
  ```

  This is a fallback, not the normal path: it fixes the console's endpoint but does nothing for Lambda or ECS, which stay unavailable without the socket.

- **Drive the browser to the state worth showing.** This is the point of using the MCP rather than a URL-to-PNG tool: dialogs, tab switches, hover and focus states, validation errors, an empty state, a mid-flow step — none of them has a URL, and they are usually the states the change is actually about. `take_snapshot` gives the accessibility tree with a `uid` per element; `click`, `hover`, `fill`, `fill_form`, `press_key` and `handle_dialog` take those uids. Reach the state, then shoot it.

- **Wait for the page, do not guess.** `wait_for` blocks until given text appears — use the text that only exists once the state is real (a seeded resource's name, the dialog's own heading). A screenshot of a spinner is worse than none.

- **Set the axes with `emulate`, never by hand**, so a pair really is the same state photographed twice, differing only in the axis under test. It takes `colorScheme: "light" | "dark"` and `viewport: "<width>x<height>x<dpr>"`. Shoot each variant with `take_screenshot` and its `filePath`; `uid` scopes the shot to a single element when the surrounding page is noise.

  **`emulate` first, then navigate — not the other way round.** Changing the viewport under a page that has already rendered leaves layout the app measured at mount at the old width, which is exactly the responsive behaviour a width pair is meant to show. One variant is one full pass:

  ```text
  emulate colorScheme=light viewport=375x900x1
  navigate_page /sqs (initScript) -> wait_for "<seeded name>" -> take_screenshot after-375.png
  emulate colorScheme=light viewport=1280x900x1
  navigate_page /sqs (initScript) -> wait_for "<seeded name>" -> take_screenshot after-1280.png
  ```

  For a state you reached by clicking, emulate first and then drive to it again; a reload throws the state away and a bare resize leaves it stale.

  Write the files into the gitignored `screenshots/` directory. If `filePath` is refused as "not within any of the configured workspace roots", the client did not hand the server a root — write to the OS temp directory and move the files.

- **Capture the "after" first**, while the branch is checked out.

- **Capture the "before" from a second worktree at the base commit** — with headless capture this is now the easy path as well as the safe one. Add a worktree at the base commit, build and run a second image from it under a different container name and port pair, and repeat the same navigation, the same interaction and the same `emulate` values. Nothing in your working tree moves, both containers can be up at once, and repeating the steps exactly is what makes the pair comparable.

  If you nevertheless stash, stash **only your own tracked changes** (`git stash push -- <paths>`), screenshot, then restore.

  Beware: `git stash` is shared across all worktrees of a repo. A concurrent session can push its own stash on top of yours between your push and your pop, so `git stash pop` may restore the wrong one. Prefer capturing "before" from a separate checkout of the base branch, and if you must stash, apply by commit hash (`git stash apply <sha>`) rather than by position.

#### Permissions for the capture

Capturing needs things an agent is not granted by default, and they are granted as **scripts**, not as `docker`:

```
Bash(make docker-console:*)
Bash(make docker-clean:*)
Bash(scripts/run-test-instance.sh:*)
```

`Bash(docker run:*)` is the entry to avoid asking for. It permits any image with any flags — `--privileged`, `--pid=host`, `-v /:/host` — which is a grant of host root, and none of it is needed to take a screenshot. `scripts/run-test-instance.sh` is narrower on purpose: it takes named options only, has no `docker run` passthrough of any kind, publishes to `127.0.0.1`, refuses the user's ports in either role, and adds the socket mount only when asked and only in exactly one form. That is what makes granting the script a smaller thing than granting `docker run` — and it is worth reading the script's header before granting it rather than taking that on trust.

`docker stop <name>` is still a bare `docker` call. Ask for `Bash(docker stop:*)` if you want the teardown unattended; it is a much smaller thing to grant than `run`.

If the socket mount is refused anyway, say so in the PR body and use the `initScript` fallback above — do not go looking for another way to reach the daemon.

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
- No comment is needed when **every** file the PR touches is in an area that never produces a release note: `compat/`, `cmd/compat/`, `tests/`, test files anywhere (`*_test.go`, `*_test.py`, `*.test.tsx`, `*.spec.ts`), `docs/plans/`, `docs/dev/`, `.agents/`, `.claude/`, `.vscode/`, `.devcontainer/`, contributor docs (`AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`), and local tooling (`.golangci.yml`, `.air.toml`, `opencode.json`, `.mcp.json`, `.gitignore`, `.gitattributes`). One file outside them and the check asks.
- Adding the fragment is always the better answer when the change is release-note-worthy. Do not waive to make a red check go away.

### During a release window

**Never edit `CHANGELOG.md` to answer the check.** That file belongs to the release PR — the bot merges `main` into the release branch on every push, and a second hand in the same section aborts that merge and stops the release PR refreshing itself. The check does not accept a `CHANGELOG.md` edit in place of a fragment.

Where the note goes depends on how far the release has got:

- **Release PR open.** Add a fragment exactly as usual. On the next push to `main` the bot folds it into the `## [x.y.z]` section and deletes the fragment; nothing else is needed. An open release PR is never a reason to skip the fragment. If the entry is **breaking** and the release in flight is not `0.x`, `Breaking-change hold` blocks the merge until the release goes out — the fragment stays correct and still gets folded in; only the merge waits.
- **Release PR merged, tag not yet published** (`bash scripts/release-candidate-check.sh` prints `true` on an ordinary PR). A fragment fails the release and there is nothing left to fold into. Wait for the tag if the change can wait — `main` in this state should be taking only what is needed to get the release out. If it cannot, waive with a reason saying the fragment is coming, and add it after the tag.
- **You are on the release PR.** Nothing to do: it is exempt by shape (`VERSION` + `CHANGELOG.md` + fragment deletions only, untagged version, non-empty section). Do not waive it and do not add a fragment. Note that `release-candidate-check.sh` prints `true` here too — that alone does *not* mean the release has merged, and reading it that way is what produced the wrong bot comment on [#563](https://github.com/overcast-sh/overcast/pull/563). If you push a code change onto the release branch, the check asks again and it is asking correctly: this is the **one** exception to "never edit `CHANGELOG.md`" — write the note straight into the `## [x.y.z]` section, because you are the hand that owns the file and a fragment left in `.changelog/` would fail the release. Better still, put the change on its own PR to `main` and let it fold itself in.

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
8. For visual changes, capture screenshots with the `chrome-devtools` MCP against a container built from the branch, and confirm nothing the capture needed is left behind: the container and image stopped and removed, any temporary page under `web/public/` or seeded fixture removed from the branch, and the images themselves outside the repo tree (`screenshots/` is gitignored).
9. Run [the surprise check](#the-surprise-check--run-this-before-opening-the-pr) over the branch, and disclose anything it turns up under `Notes`.
10. Confirm every statement the description makes about real AWS behavior links to its source — see [AWS Compatibility Evidence](#aws-compatibility-evidence).

When creating a PR with `gh pr create`, use a heredoc for the body so markdown stays readable.

---

## Completion Contract

Opening the PR is not completion. Pushing a branch is not completion. An agent that creates or
updates a PR owns it until it is green and ready to merge.

- Start the repository PR waiter immediately after opening the PR and after every subsequent push.
- On the first failure, inspect the annotations and failing-step logs, identify whether it is caused
  by the branch, and establish who owns the PR's implementation before changing it.
- If the agent authored the majority of the PR's code, proactively fix every actionable branch
  failure in the same task worktree. The user has already delegated ownership of that work; do not
  stop to ask again for ordinary CI repairs within its scope.
- If the agent authored little or none of the PR's code, report the failing check and root cause,
  then ask the user whether to fix it before modifying another author's work. Continue monitoring
  while waiting when useful, but do not infer write authority from a request to watch CI.
- Run the relevant local verification, self-review the resulting diff, commit or amend coherently,
  push, and restart the waiter against the new head. Repeat until the required checks pass.
- Do not stop at "CI is running", "most checks passed", or "the PR was created". Completion means
  required checks are green, the PR is not draft, it has no conflicts, and GitHub reports it ready
  to merge. A required human review or external service outage is a blocker to report, not a green
  result to imply.
- Do not merge or enable auto-merge unless the user explicitly authorized merging. The default
  handoff is a green, merge-ready PR for the user or reviewer to merge.
- **An agent that nothing can wake after its turn ends discharges this contract differently**,
  because it cannot own a PR past that point — a subagent being the example. Where merging was
  authorized and auto-merge is armed, "green and ready to merge" is something auto-merge reaches
  unattended; such an agent finishes at *auto-merge armed with no FAILED required check*, verified
  by a foreground call, and reports that. Where it was not authorized, it waits in the FOREGROUND —
  never by backgrounding the waiter and ending its turn, which strands it (see
  [After Opening](#after-opening--waiting-on-ci)). Either way it hands the outcome back to its
  caller rather than stopping to be woken. This bullet does not apply to a session a human is
  watching: there, background the waiter as usual and keep owning the PR until it is green.

While waiting, report only a failure that requires action or the final green, merge-ready result.

## After Opening — Waiting on CI

If the user explicitly authorized merging, enable auto-merge as a separate step after opening.
Whether or not auto-merge is authorized, **wait with one command**:

```sh
scripts/pr-wait.sh <n>            # or scripts\pr-wait.ps1 <n>
```

Only when merging was explicitly authorized, run this separately before the waiter:

```sh
gh pr merge <n> --squash --auto
```

`scripts/pr-wait.sh` wraps `gh pr checks --watch --fail-fast` and exits
0 / 1 / 2 / 8 (passed / failed / no checks will run / still pending).

**How to run it is a fork on one question.** The two answers below are
**complete rules, not a progression**. Follow the one whose condition you meet
and ignore the other; neither refines the other, and whichever you happen to
read second does not win. The condition is not *who you are* — it is:

> Can anything wake you after this turn ends?

**Yes — a human is watching the conversation.** Run it in the **background**:
in Claude Code, the `Bash` tool with `run_in_background: true`. You stay
interruptible while it runs, and its single completion notification is the only
thing the conversation gets, landing in front of the person who is actually
waiting for it. This is the common case and the default.

**No — your turn ending is final.** Do not background it and stop. A stopped
**subagent** is the example: it is re-invoked only while a live tracked
background child is still running, and `pr-wait` routinely exits within seconds
— a bad argument, a `gh` auth hiccup, a PR whose checks had already settled, its
own exit 2 for "not worth acting on". The child is dead before the turn ends,
the wake never comes, and the agent is stranded mid-task owing a final report it
will never send; eight parallel subagents hung exactly that way on 2026-08-22
and had to be resumed by hand. Two endings, both fine:

- **Run it in the foreground** and block. `gh pr checks --watch` has its own
  timeout, so the wait is bounded, and you read the exit code yourself rather
  than depending on a notification that may never arrive.
- **Or skip waiting entirely.** Once merging was authorized and
  `gh pr merge <n> --squash --auto` is armed, there is nothing to wait *for*:
  auto-merge completes the PR unattended, long after the turn ends. The correct
  terminal state is **auto-merge armed, and one foreground
  `gh pr checks <n>` shows no FAILED required check → report and exit.** Pending
  checks at that point are not a reason to keep the turn open.

Subagent is the *example* of the second case, not the test for it. The test is
the wake question above — and neither answer licenses a hand-rolled poll loop,
which is still forbidden to everyone.

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
  [#410](https://github.com/overcast-sh/overcast/pull/410) merged over a failing compat
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
- Aligns DynamoDB `Query`/`Scan` limit handling with AWS: `Limit` caps items *read*, and the
  filter expression is applied afterwards, so a filtered page can return fewer than `Limit`
  items and still carry `LastEvaluatedKey`
  ([API_Query](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Query.html)).
- Updates integration coverage for count and pagination behavior.
- Notes the behavior in the DynamoDB changelog entry.

## Verification
- `go test -count=1 ./internal/services/dynamodb/... ./tests/integration/dynamodb/...`
- `gofmt -w ./internal/services/dynamodb ./tests/integration/dynamodb`
- `go vet ./internal/services/dynamodb/... ./tests/integration/dynamodb/...`

## Notes
- Behavior change, not additive: callers that previously got a full page under a filter now get
  a short page plus `LastEvaluatedKey`. That is what real DynamoDB does — code written against
  AWS already handles it, code written against the old Overcast behavior may not.
- `ScannedCount` vs `Count` reporting was already correct and is unchanged.
- Follow-up: none.
```
