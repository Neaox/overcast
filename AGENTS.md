# AGENTS.md

> **For AI agents** (Claude, Copilot, Cursor, etc.) and agent-assisted workflows.
>
> This file is the canonical agent instructions file. Other filenames are symlinks to it
> so that different agents pick it up automatically (e.g. `CLAUDE.md` → `AGENTS.md`,
> `.github/copilot-instructions.md` → `../AGENTS.md`).
> If your agent expects a different filename, create a local symlink rather than copying:
> `ln -s AGENTS.md GITHUB.md` / `ln -s AGENTS.md COPILOT.md` / etc.
>
> **Read [CONTRIBUTING.md](./CONTRIBUTING.md) first** — it has coding standards,
> core principles, error handling, logging, performance, design patterns, service package
> structure, the endpoint/service checklists, and web UI standards.
> Everything there applies to you too — this file adds agent-specific guardrails only.
>
> For test conventions, see [tests/AGENTS.md](./tests/AGENTS.md).
> For current implementation status and what to build next, see [STATUS.md](./STATUS.md).

---

## What belongs here vs elsewhere

- **AGENTS.md (this file):** AI-agent-specific workflow rules and guardrails — things that only matter because an agent is executing work autonomously (e.g. "never leave the workspace broken", "run `go vet` before finishing"). If a human contributor wouldn't need to read it, it belongs here.
- **[CONTRIBUTING.md](./CONTRIBUTING.md):** Everything relevant to all contributors, human or AI — coding standards, architecture decisions, checklists, design patterns, web UI conventions. If a human opening a PR would benefit from reading it, it belongs there.
- **[README.md](./README.md):** Everything relevant to users of Overcast — installation, configuration, supported services, quickstart. Not for contributors.

---

## Non-goals — decision guide for agents

Before implementing anything, check these constraints. If a request conflicts, push back.

- **Not a staging environment.** No 100% API parity. Do not base production go/no-go decisions on Overcast tests.
- **Not a security boundary.** Credentials accepted but not validated. Never expose on a public network.
- **Not a performance testing tool.** No latency emulation, no throttling, no quotas.
- **CloudFormation/IAM are partial.** Both services are implemented at a partial level. CloudFormation supports `CreateStack`/`DeleteStack`/`DescribeStacks`/`ListStacks` and provisions ~50 resource types (EC2/VPC, API Gateway, ECS, IAM, EventBridge, KMS, Lambda, S3, SQS, SNS, DynamoDB, Logs, SSM, SecretsManager, Step Functions) via internal dispatch to the emulated services. IAM supports roles, policies, users, groups, and instance profiles. `cdk deploy` works for stacks using supported resource types. Coverage is not exhaustive — continue ensuring all service implementations remain compatible with CloudFormation (standard ARN formats, required response fields, etc.).
- **Not a production dependency, ever.** Local dev and CI only. No durability guarantees, no security model.
- **Not a perfect replica.** We emulate the most-used 20% with high fidelity. Edge cases may differ.

---

## Repository layout

```
cmd/overcast/main.go         <- unified CLI: `overcast serve` (daemon) + bridge/status/trust subcommands
internal/
  bff/                       <- Go BFF: serves embedded SPA + /api/* proxy routes
  config/                    <- typed env-var config
  hostbridge/                <- mDNS publisher + port-80 reverse proxy (`overcast bridge`)
  router/                    <- chi router, middleware, health + debug endpoints
  middleware/                <- RequestID, Logger, Recovery, SigV4 stub
  protocol/                  <- AWS wire format (XML/JSON errors, ARNs, request IDs)
  state/                     <- Store interface + MemoryStore + SQLiteStore
  serviceutil/               <- shared helpers (request, pagination, validation, logging, lazy init)
  services/
    s3/                      <- 27 service packages; see STATUS.md for coverage
    sqs/
    dynamodb/
    ...
tests/
  AGENTS.md                  <- test conventions (GWT, mocks, helpers)
  helpers/                   <- TestServer, assertions, MockStore
  integration/               <- HTTP-level tests per service
docs/services/               <- per-service endpoint support matrices
```

---

## Code quality — quick reference

All coding standards are in [CONTRIBUTING.md](./CONTRIBUTING.md). This section is a lookup table for common agent decisions.

### Error format by service

| Service                    | Format                     | Helper                                  |
| -------------------------- | -------------------------- | --------------------------------------- |
| S3                         | XML                        | `protocol.WriteXMLError(w, r, aerr)`    |
| SQS, SNS, DynamoDB, Lambda | JSON                       | `protocol.WriteJSONError(w, r, aerr)`   |
| Unimplemented              | Same format as the service | `protocol.NotImplementedXML/JSON(w, r)` |

501 responses get `x-emulator-unsupported: true` and all responses get a request ID — both automatically. Never set either manually.

### Other rules not to forget

- **State:** all through `state.Store`; JSON serialisation in `store.go` only. Update both implementations when changing the interface.
- **Malformed persisted state must be isolated.** A single corrupt or stale record in `state.Store` must not make list/scan operations, unrelated resources, or the whole service return HTTP 500. When reading many records, skip malformed records and log/track the gap where practical; when reading one named resource, prefer a modeled AWS-style not-found/invalid-resource error if the record cannot be safely decoded. Only return `InternalError` for actual infrastructure failures (store unavailable, query failed, marshal failed), not for one bad persisted payload that can be isolated without breaking AWS-facing semantics.
- **Clock:** `clock.Clock` only — never `time.Now()`. See [CONTRIBUTING § Clock](./CONTRIBUTING.md#time--clock-injection).
- **Shared helpers:** use `serviceutil` — see [CONTRIBUTING § Utilities](./CONTRIBUTING.md#shared-utilities--use-serviceutil-never-duplicate).
- **Routing fallthrough is S3.** Both the chi router and the logger's `detectService` treat S3 as the catch-all: any request that doesn't match a registered route or a known path prefix is dispatched to the S3 handler and labelled `service=s3` in logs. This is deliberate — S3 has no distinguishing header or path prefix. Consequences:
  - When you add a service that uses **versioned REST paths** (e.g. `/2018-10-31/...`, `/v3/foo`) or any non-S3 root path, you must (a) register the routes in `RegisterRoutes`, and (b) add the path prefix to `detectService` in [internal/middleware/logger.go](./internal/middleware/logger.go). Otherwise every request to that service will appear in logs as `service=s3` and bypass IAM/region/SigV4 middleware that branches on service name.
  - If you see `service=s3` in logs for a request that clearly isn't S3 (e.g. `POST /2018-10-31/layers/.../versions`), that's the symptom — fix `detectService`, don't ignore it.
  - **Bugs cause fallthrough too.** A typo in a route path, a missing `RegisterRoutes` entry, a misnamed `chi.URLParam`, or a middleware that mutates the URL can all cause an otherwise-correct request to miss its service handler and land in S3 with a 404/501. When debugging an unexpected `service=s3` log line, don't just patch `detectService` — confirm the request was actually routed to the right handler. The `detectService` label and the chi route match are independent: a request can be labelled correctly but still fall through to S3 due to a routing bug, or vice versa.
  - 501s under `service=s3` for paths like `/<bucket>/?encryption=` or `/<bucket>/?policy=` are real S3 sub-resource calls and belong to S3.

---

## Checklists

The full checklists are in CONTRIBUTING.md:

- [How to add an endpoint](./CONTRIBUTING.md#how-to-add-an-endpoint)
- [How to add a service](./CONTRIBUTING.md#how-to-add-a-service)
- [Service package structure](./CONTRIBUTING.md#service-package-structure)
- [Web UI standards](./CONTRIBUTING.md#web-ui-standards)

---

## Agent workflow — before and after every task

### Before editing

1. Identify the **service** and **AWS protocol** (Query, JSON 1.1, REST JSON, REST XML)
2. Locate an **existing implementation** to mirror — copy the pattern, not the temptation to invent
3. Check **config impact** — does it need a new field in `*config.Config`?
4. Check **storage impact** — does it need `state.Store` changes? Both implementations?
5. Check **docs impact** — capability tables, service docs, STATUS.md, CHANGELOG.md
6. Define a **minimal useful test plan** — failing test first

### Before finishing

1. Run **scoped tests** (`go test -count=1 ./internal/services/x/... ./tests/integration/x/...`)
2. Run **`gofmt -w`** then **`go vet`** over changed packages
3. Run **`make docs`** if you changed capabilities or service behavior
4. Verify **no custom endpoints** were introduced — everything must match real AWS wire format
5. Verify **CloudFormation handlers** are registered for any new resource types (or stubbed)
6. Widen to `go build ./...` and `go vet ./...` for final check

---

## Common mistakes

Agents most often trip on these — check before finishing:

- **Creating non-AWS endpoints or custom response fields** — the AWS SDK must work unmodified
- **Changing wire formats without tests** — request/response shapes are the compatibility contract
- **Forgetting `make docs`** after capability changes — generated tables will drift
- **Updating only one store implementation** — `MemoryStore` and `SQLiteStore` must stay in sync
- **Forgetting CloudFormation resource handlers** — every resource-creating endpoint needs an entry in `provisioner.go`
- **Using `time.Now()` instead of `clock.Clock`** — makes tests untestable
- **Bypassing `serviceutil` / duplicating helper logic** — DRY across services
- **Returning bare `404`** — unimplemented operations must return `501`
- **Using subfolders as sub-packages inside a service** — all service files live in one flat package
- **Testing only with raw HTTP** — prefer AWS SDK clients for management-plane validation where possible

---

## Human handoff — when behaviour is unclear

1. Prefer **real AWS behaviour** — spin up a resource and test the edge case
2. Then **existing Overcast behaviour** — consistency within the codebase matters
3. Then **compatibility test expectations** — what do tests in `tests/` and `compat/` expect?

If a task would require broad architectural changes, **stop and surface the tradeoffs** rather than refactoring across services silently. A `501` with an honest explanation is better than a divergent `200`.

---

## Release awareness

Changes merged into `main` do **not** imply a stable release. Docker images are only published when a release tag is pushed. Do not assume code on `main` is available to end users — treat it as nightly/integration until tagged.

---

## Working efficiently

`go build ./...` and `go vet ./...` over the whole repo is slow. Go's build cache means **only changed packages recompile**, so scope verification to what actually changed:

```sh
# After touching internal/services/foo/ — build and vet only that subtree
go build ./internal/services/foo/...
go vet  ./internal/services/foo/...

# Or run its tests (test compilation implies vet, -count=1 avoids cached results)
go test -count=1 ./internal/services/foo/... ./tests/integration/foo/...
```

Widen to `./...` only once before marking a task done. Avoid `go build ./cmd/overcast` during iteration — the `cmd/overcast` main package embeds the web UI, adding unnecessary overhead for backend-only changes. Use `./cmd/overcast -tags slim` or stay within `./internal/...` until the final check.

For TypeScript changes, `npx tsc --noEmit` in `web/` is always scoped to that directory and is already incremental.

### Iterating on a specific test

When fixing a single handler or function, run only the relevant test rather than the full package suite:

```sh
go test -count=1 -run TestMyFunction ./internal/services/foo/...
```

To compile and vet without executing tests (fast syntax/type check):

```sh
go test -run=^$ -count=0 ./internal/services/foo/...
```

### Run `gofmt` before `go vet`

`go vet` can emit misleading output on unformatted code. Always format first:

```sh
gofmt -w ./internal/services/foo/
```

Or to check without writing: `gofmt -l ./internal/services/foo/`

### Check the editor error panel (workspace problems) before running a build

The language server surfaces compile and vet errors without a terminal round-trip. Use the `get_errors` tool to read the current problem list — if it's empty for the files you changed, a scoped `go vet` is usually sufficient confirmation.

### Use the `Explore` sub-agent for read-only investigation

When you need to understand an unfamiliar part of the codebase (e.g. "how does the SQS handler parse queue URLs?"), delegate to the `Explore` sub-agent rather than chaining many sequential searches in the main conversation. It returns a focused summary and keeps your context clean for implementation work.

---

## What agents must NOT do

- **Never push directly to `main`.** Agents must not run `git push origin main`, push the current branch when it is `main`, create or move tags on `main`, or otherwise update protected release branches directly. Always work on a feature/release branch and use a pull request or explicit human-managed merge path. If a task appears to require a direct `main` push to trigger automation, stop and ask for human confirmation instead.
- **Never commit directly on `main`.** All changes must go through a non-`main` branch and pull request. Before committing, run `git branch --show-current`; if it returns `main`, stop and ask before doing anything else. This applies to every change, including release prep, docs-only edits, generated files, and emergency fixes.
- **Start editing workflows on a branch.** At the start of any skill or workflow that may edit files or create commits, check `git branch --show-current`. If it returns `main`, create or switch to a task branch before editing; if unrelated worktree changes make that unsafe, stop and ask. Use clear branch names such as `fix/sqs-visibility-timeout`, `compat/elasticache-serverless-cache`, or `release/0.0.1-alpha.6`.
- **Amend related mistakes instead of narrating them.** If you forgot a directly related file such as a changelog entry, generated doc, or focused test, amend or squash your own commit so the branch stays coherent. This is fine even after pushing when you own the branch, it is not shared, and you use `git push --force-with-lease` to avoid overwriting other people's work. Use a separate follow-up commit instead when the change is already merged, the branch is known to be shared, or unrelated commits now sit on top. Do not create noisy correction commits or give the user a running play-by-play of fixups unless the branch history is shared or the user asks for that detail.
- **Never leave the workspace in a broken state.** After every change, check the workspace problem list (compiler errors, type errors, lint errors) - via the `get_errors` tool, and fix any problems you introduced before considering the task done. You are not finished while problems you caused remain open.
  - **`go build ./...` is necessary but not sufficient.** It only catches compile errors. Also run `go vet ./...` to catch lint/static-analysis warnings (unused params, unused funcs, unnecessary nil checks, etc.) that appear in the VS Code Problems panel but don't fail compilation. Fix every warning you introduced.
  - **Sub-agents must do this too.** A sub-agent invoked by a parent agent is held to the same standard. Before returning a result, run `go build ./...` (for Go changes) and/or `npx tsc --noEmit` (for TypeScript changes) and fix every error you caused. If a linter or vet warning is introduced (e.g. `go vet ./...` reports a new issue), fix it. Do not offload verification to the parent — own it.
- Never implement a handler or fix a bug without a failing/reproducing test first
- Never return bare `404` for unimplemented operations — always `501`
- Never call `os.Getenv` in service code — use `*config.Config`
- Never update only the summary table in a service doc — update both tables
- Never publish a performance claim without measurement conditions — every number (startup time, memory, image size, latency) must document what was measured, how, and under what conditions. See [docs/performance.md § Documenting performance claims](docs/performance.md#documenting-performance-claims)
- Never do blocking work (store reads, network I/O, DDL, file reads) inside `<svc>.New()` or an `Init*` method called from `router.New()`. Use a `sync.Once`-guarded lazy-init method called from the handlers that need it. See [docs/performance.md § Startup budget — rules for service authors](docs/performance.md#startup-budget--rules-for-service-authors)
- Never edit `web/src/routeTree.gen.ts` — it is auto-generated by TanStack Router when the dev server runs (`npm run dev` in `web/`). After adding or changing route files, check whether the dev server is already running (the user usually has it running); if so, the file will update automatically. Only regenerate manually if the server is not running.
- Never assume that you are the only AGENT working - be careful with git operations that may break what others are working on (e.g `git stash` or `git checkout`)

---

## Tool use discipline — preventing hallucination in tool chains

Tools are the agent's primary source of truth about the runtime environment. To ensure clean, reliable tool-chaining:

### Ground truth rule

- **Tool outputs are authoritative.** If a tool returns data (e.g., `runtime_list_instances` returns a list of endpoints), that IS the current state. Never override it with cached knowledge, prior context, or assumptions.
- When a current tool result conflicts with prior context, surface the discrepancy explicitly for user review rather than silently choosing one.

### Chaining discipline

- **Use tool N's output as input to tool N+1.** For example: `runtime_list_instances` → use exactly those endpoints for `runtime_probe_instance`. Never probe endpoints not returned by the prior tool unless explicitly asked.
- **Document the dependency.** Annotate tool calls with the reason: "Using endpoint from runtime_list_instances: http://localhost:4566"
- **Never diverge into cached assumptions** once a tool chain is active. If prior knowledge suggests a different path, ask the user: "I know from earlier context that X, but the tool just returned Y—which should I use?"

### Validation before deviation

- Before using an endpoint, config value, or other runtime fact NOT returned by a tool, explicitly ask or state the assumption
- Default action when uncertain: use only what the current tools returned
- If no tool has provided it, that's a signal to call an appropriate tool

### Immutable snapshots

- Treat a tool's result as immutable within the task context
- Reference it by the snapshot, not by reconstructed logic
- Example: "Using the instances from step 1 (localhost:4566, 127.0.0.1:4566)"—then don't invent a third instance mid-chain

### This prevents

- Hallucinating service endpoints or config from prior logs
- Ignoring explicit tool output because prior context "feels" more reliable
- Skipping obvious follow-up tools because of assumptions about what they'd return
- Silent divergence from the stated task into cached knowledge paths
