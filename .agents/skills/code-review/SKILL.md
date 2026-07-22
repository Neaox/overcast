---
name: code-review
description: "Review code for AWS parity, correctness, quality, performance, best practices, and maintainability in the Overcast codebase. Use when: reviewing a PR, auditing a file, checking for code smells, finding performance issues, detecting goroutine leaks, memory leaks, identifying AWS wire-format or observable-behavior drift, identifying spaghetti code (mixed abstraction levels, action-at-a-distance, boolean traps, implicit ordering, tangled control flow), identifying overly complex functions or oversized modules, ensuring DRY idiomatic best-practice Go and TypeScript."
argument-hint: "File path, directory, or description of what to review"
---

# Code Review — Overcast

Perform a thorough code review against the project's standards. Flag violations, suggest fixes, and explain why each issue matters.

**AWS parity is mandatory for observable behaviour.** Overcast is useful only when SDKs, CLIs, IaC tools, and user code can rely on it behaving like real AWS. Treat every observable API detail as the compatibility contract: request parsing, routing, auth/signature expectations, validation order, error code, HTTP status, headers, response shape, field casing, default values, pagination tokens, timestamps, ordering, state transitions, side effects, idempotency, eventual/async behavior, and CloudFormation-visible attributes. If real AWS behaviour is knowable, require parity. If it is uncertain, require evidence from AWS docs, SDK models, or real AWS compatibility tests instead of guesses. If faithful implementation is not feasible, require an honest documented `501`/unsupported response rather than a divergent successful response.

**General principle:** beyond the specific checks below, always follow accepted best practice — the widely understood, proven ways to write correct, secure, performant, and maintainable software. The checklists codify the most important rules for this codebase, but they are not exhaustive. If something violates a well-established best practice that isn't listed here, flag it anyway.

**Developer experience is paramount.** Our audience is developers — every API response, error message, CLI output, and UI interaction should be intuitive, predictable, and helpful. Consider the user at all times: clear error messages that explain what went wrong and how to fix it, consistent behaviour that matches what developers expect from real AWS, sensible defaults that work out of the box, and a web UI that is discoverable without reading documentation. If something is confusing or requires guesswork, it's a bug.

**Don't nitpick.** Focus on substance — correctness, performance, safety, maintainability, and convention adherence. Don't flag trivial style preferences, cosmetic formatting choices, or minor naming alternatives that don't improve clarity. If there truly is a better way, guide towards it — but don't get lost in minutiae. A review full of nitpicks is demoralising and buries the findings that actually matter.

**Clean code is non-negotiable.** Code must be idiomatic, DRY, SOLID where those principles improve clarity, easy to read, well-factored around single responsibilities, race-safe, resource-safe, and performant by default. Prefer the smallest correct design, but flag cleverness, hidden coupling, accidental duplication, leaky abstractions, uncontrolled allocation growth, goroutine/resource leaks, and work done in the wrong layer.

**Bug-fix reviews must check blast radius.** When reviewing a bug fix, identify the underlying class of bug and search for sibling handlers, services, stores, CloudFormation paths, tests, and frontend callers that may share the same failure mode. Do not approve a fix that only patches the reported case while leaving equivalent observable regressions elsewhere, unless the remaining scope is explicitly documented as follow-up work.

**Be careful.** Use git commands wisely, do not lose any uncommitted changes. Always double check the command before executing it.

**Be efficient.** Make good use of sub-agents to parallelize read-only review work and speed up the review process. For example, use one agent to inspect tests and verification, another to understand related service patterns, and another to compare AWS compatibility evidence. Do not delegate unsafe git operations or edits during review unless the user explicitly asked for fixes.

Keep in what is laid out in [CONTRIBUTING.md](../../../CONTRIBUTING.md) and the general principles above.

## When to Use

- Reviewing a pull request or changeset
- Auditing a file or module for quality
- Checking for performance regressions before merging
- Investigating code smells or technical debt
- Validating that new code follows project conventions

## Review Procedure

1. **Identify scope** — determine which files/directories to review. If a diff or PR is provided, focus on changed lines plus surrounding context.
2. **Understand the wider context** — never review a file or change in isolation. Look at how the code fits into the surrounding module, service, and project. Check how similar problems are already solved elsewhere. Ask:
   - Does this follow the conventions established by existing code in this area?
   - If it breaks an established convention, is there a good reason? Is the new approach demonstrably better?
   - If it introduces a new pattern, should existing code be updated for consistency, or does the old pattern still serve its purpose? If updating existing code is warranted but out of scope for this change, recommend a TODO — don't let the inconsistency become invisible.
   - Could this change confuse the next person who works in this area?
3. **Verify AWS contract evidence** — for AWS-facing changes, compare against real AWS behaviour wherever possible. Use AWS docs, Smithy/API models, SDK behavior, existing compatibility tests, or real AWS probes. Flag unsupported assumptions, missing evidence, and any custom endpoint/field/status/header on the AWS API surface.
4. **Check regression risk** — identify nearby code paths and sibling services that could be affected by the same change or bug pattern. For bug fixes, search for analogous code and require coverage for the broader failure mode, not only the reported reproduction.
5. **Read the code** — load each file. For large files, read in sections.
6. **Apply each checklist category** below against every file in scope.
7. **Report findings** — group by severity (Critical / Warning / Suggestion), include file path and line numbers, quote the offending code, and provide a concrete fix.

## Review Checklist

Apply all **Common** checks to every file. Then apply the **Go** or **Frontend / TypeScript** section depending on the language.

---

### Common (all languages)

#### Complexity & Size

- [ ] Functions fit on one screen (~40 lines). Longer functions should be split
- [ ] Cyclomatic complexity is low — no deeply nested `if/else/switch` chains (max 3 levels)
- [ ] Each function has a single clear responsibility
- [ ] Guard clauses and early returns used — happy path is unindented
- [ ] No "god functions" that orchestrate too many steps — break into named sub-steps
- [ ] Files stay under ~500 lines. Larger files likely do too much — split by concern

#### Spaghetti Code

Spaghetti code lacks clear structure: control flow jumps around unpredictably, concerns are tangled, and the reader cannot follow a straight line from input to output. Flag any of the patterns below and suggest concrete restructuring.

**Detection patterns — flag when present:**

- [ ] **Mixed abstraction levels** — a single function does HTTP parsing, business logic, and persistence in one body. Separate into: parse → validate → handle → respond
- [ ] **Action at a distance** — a variable is set in one branch and silently relied upon 30+ lines later; or a flag (`bool`, mutated pointer) is set here but consumed somewhere else. Prefer explicit data flow: pass values, return values, use named types
- [ ] **Boolean trap** — a function accepts bare `bool` parameters with no context (`doThing(ctx, true, false, true)`). The call site is illegible. Use option structs, named constants, or split into specialised functions
- [ ] **Flags-as-control-flow** — `done`, `found`, `ok` booleans set in one loop/branch to drive a later branch. Invert the logic with early returns or extract a helper that returns the value directly
- [ ] **Long parameter lists** (>4 parameters with no grouping). Group related parameters into a named struct
- [ ] **Implicit ordering dependencies** — steps that must execute in a specific order but the code gives no indication why. Name the steps, document the invariant, or encode the order in a sequence/pipeline type
- [ ] **Deeply nested callbacks / closures** — more than 2 levels of nested anonymous functions. Extract to named functions or flatten with sequential code
- [ ] **Copy-paste branching** — two branches do nearly the same thing with a small variation. Extract the shared path; pass the variation as a parameter or a small function
- [ ] **God variable** — a single struct or map accumulates fields across many unrelated code paths and is passed everywhere. Split into focused types; assemble at the call site that needs the combined view
- [ ] **Returned-but-ignored intermediate state** — a function returns multiple values but callers consistently ignore some of them. The signature is probably wrong — remove unused return values or split into focused functions
- [ ] **Interleaved setup / teardown / logic** — resource acquisition, business logic, and cleanup are interspersed rather than clearly sequenced. Use `defer` for cleanup and group setup at the top

**Remediation suggestions to include in the review:**

- Extract function with a name that explains its purpose — the name is the most important part
- Replace nested `if/else` with guard clauses (early returns) so the happy path is flat
- Replace flag/boolean parameters with a small options struct or two separate functions
- Split a pipeline into named stages — each stage takes a typed input and returns a typed output
- Move shared setup to a constructor or factory rather than sprinkling it across branches
- For complex orchestration, consider a thin coordinator function that names and sequences the steps, delegating each to a well-named helper

#### Modularity

- [ ] Prefer small, generic, reusable building blocks over monolithic one-off implementations
- [ ] Functions and components should be composable — combine simple pieces to build complex behaviour
- [ ] Extract reusable logic early — if something could serve a second caller, make it a standalone unit now
- [ ] Modules have clear, narrow interfaces — inputs and outputs are obvious, internals are hidden
- [ ] Avoid tight coupling — a change in one module should not force changes across unrelated modules
- [ ] Shared utilities belong in common packages (`serviceutil`, `protocol`, `@/lib/`, `@/components/ui/`) — not inlined in each consumer

#### Memory Footprint & Streaming

- [ ] Never load unbounded data into memory — stream it. Use `io.Reader`/`io.Writer` pipelines (Go) or readable/writable streams (TypeScript) for request/response bodies, file uploads, batch results, log tails
- [ ] `io.ReadAll`, `bytes.Buffer`, `JSON.parse(await res.text())` on full bodies are only acceptable when the data is provably small and bounded
- [ ] Prefer `io.Copy`, `json.NewDecoder(r.Body)`, chunked writes over accumulate-then-send patterns
- [ ] Pre-size collections (`make([]T, 0, n)`, `new Array(n)`) when the size is known or estimable — avoid repeated reallocation
- [ ] Scope large temporary allocations tightly — don't hold buffers across request boundaries
- [ ] No unbounded growth in maps, slices, or channels acting as caches — set capacity limits or eviction
- [ ] Target: <15 MiB idle memory. Every allocation choice contributes

#### DRY — Don't Repeat Yourself

- [ ] No copy-pasted logic — extract shared helpers when a pattern appears in 2+ places
- [ ] No duplicate error-handling boilerplate — use shared helpers
- [ ] Constants used instead of magic numbers/strings scattered through code
- [ ] But: avoid over-engineering — DRY extractions must be easier to understand than the duplication

#### Naming & Readability

- [ ] Names reveal intent — a reader should understand purpose without reading the body
- [ ] Functions named as verbs (`createBucket`, `parseRequest`), types as nouns (`BucketStore`, `QueueMessage`)
- [ ] No abbreviations unless universally understood (`ctx`, `cfg`, `err`, `req`, `resp` are fine; `bkt`, `msg`, `hdlr` are not)
- [ ] Consistent naming across similar constructs — if S3 calls it `handlePutObject`, SQS shouldn't call it `processSendMessage`
- [ ] Code is self-documenting — comments explain _why_, not _what_. If a comment restates the code, delete it
- [ ] No misleading names — a function called `validate` must actually reject invalid input, not just log a warning

#### Correctness & Error Handling

- [ ] No silent failures — every error path either returns, logs, or writes a response
- [ ] Errors are never discarded — always wrapped, returned, or handled
- [ ] No dead code — unused functions, variables, commented-out blocks must be removed
- [ ] TODOs include priority: `// TODO(priority:P1): description`
- [ ] No credentials, secrets, or PII in logs or comments

#### Testing Quality

- [ ] Every new feature / endpoint has a corresponding test — untested code is unfinished code
- [ ] Every bug fix has a reproducing test that fails before the fix
- [ ] Tests follow Given/When/Then structure — intent is unambiguous
- [ ] Test names use `Test<Subject>_<scenario>` — scenario describes the condition, not the outcome
- [ ] Tests are deterministic — no time dependencies, no random ordering, no shared mutable state
- [ ] Table-driven tests for variations — no copy-paste-tweak test functions
- [ ] Tests assert behaviour, not implementation — don't test private internals when the public API suffices
- [ ] No test helpers that silently swallow errors — use `t.Fatal` / `require` for setup failures

#### Security Basics

- [ ] User-supplied input validated at system boundaries — query params, headers, body fields
- [ ] No path traversal risk — file/key names sanitised, `..` sequences rejected
- [ ] No unbounded reads — request bodies have size limits (`http.MaxBytesReader` or equivalent)
- [ ] No SQL/NoSQL injection — parameterised queries, not string interpolation
- [ ] No sensitive data in error messages returned to clients — internal details stay server-side
- [ ] CORS headers managed by middleware only — handlers don't set `Access-Control-*` directly

#### AWS Fidelity

- [ ] Every observable behaviour matches real AWS wherever possible — not just the specific happy path under review
- [ ] Request routing, protocol parsing, action names, content types, target headers, query parameters, URI labels, and payload decoding match the AWS protocol for that service
- [ ] Error codes, messages, HTTP status codes, response formats, and validation order match what real AWS returns — not what seems reasonable
- [ ] Response field names, casing, nesting, empty/default values, omitted fields, timestamps, ARNs, IDs, and pagination tokens match the real AWS API wire format
- [ ] Headers, metadata, request IDs, checksums, ETags, and modeled response traits are present or absent according to AWS behaviour
- [ ] Validation rejects the same inputs AWS rejects, accepts the same inputs AWS accepts, and does not introduce stricter emulator-only constraints on AWS APIs
- [ ] State transitions, idempotency, side effects, ordering guarantees, eventual consistency, retries, and async behavior follow AWS sequencing — intermediate states exist and are observable when AWS exposes them
- [ ] CloudFormation-visible `Ref`, `GetAtt`, physical IDs, replacement semantics, delete behavior, and resource attributes match AWS for supported resource types
- [ ] AWS SDKs and CLI can use the endpoint unmodified — no non-AWS request requirements or custom response fields on AWS API surfaces
- [ ] When unsure how AWS behaves, require evidence from AWS docs, SDK/service models, existing compatibility tests, or real AWS probes — don't guess
- [ ] Known unavoidable divergences are documented in service docs and code comments where helpful
- [ ] A `501`/unsupported response is always preferable to a `200` that silently diverges from real AWS

#### Regression & Bug-Fix Blast Radius

- [ ] The change cannot regress existing supported AWS behaviour, SDK compatibility, CLI flows, CloudFormation provisioning, or web UI assumptions
- [ ] Bug fixes include a reproducing test that fails before the fix and protects the observable AWS/user behaviour after the fix
- [ ] The reviewer identified the bug class, not just the failing case, and searched for equivalent patterns in sibling handlers/services/stores/tests
- [ ] Shared helpers or common validators were updated when multiple call sites need the same fix; copy-pasted one-off patches are flagged
- [ ] New tests cover representative sibling cases, edge cases, and negative cases where regression risk is plausible
- [ ] Existing tests were not weakened, skipped, or rewritten to match incorrect behaviour
- [ ] Documentation and generated capability tables were updated when behaviour or support status changed

#### Edge Cases & Defensive Coding

- [ ] Nil/zero-value inputs handled — don't panic on nil pointers, empty slices, or zero-length strings
- [ ] Boundary conditions tested — empty collections, max-length inputs, Unicode, special characters
- [ ] Integer overflow considered for counters, sizes, and timestamps
- [ ] Map lookups check for missing keys before using the value
- [ ] Slice operations guard against out-of-bounds access

---

### Go (`internal/`, `cmd/`, `tests/`)

#### Error Handling

- [ ] Errors wrapped with context (`fmt.Errorf("scope: %w", err)`), never discarded
- [ ] AWS errors use `protocol.Wrap()` / `protocol.WriteXMLError` / `protocol.WriteJSONError` — never raw `http.Error`
- [ ] Unimplemented operations return `501` via `protocol.NotImplementedXML/JSON` — never bare `404`
- [ ] `errors.Is` / `errors.As` used for inspection — not type assertions on wrapped errors
- [ ] HTTP response bodies closed (`defer resp.Body.Close()`) after error checks

#### Idiomatic Go

- [ ] Follows [Effective Go](https://go.dev/doc/effective_go) conventions
- [ ] Small interfaces — accept the narrowest interface (`io.Reader` not `*os.File`)
- [ ] Value receivers for read-only methods; pointer receivers only when mutating or for large structs
- [ ] Named return values only when they improve readability (not as a shortcut)
- [ ] No `interface{}` / `any` in tight loops or when a concrete type is known
- [ ] Table-driven tests with `t.Run` per case — no copy-pasted test functions
- [ ] Exported symbols have doc comments
- [ ] Error sentinels named `ErrXxx`, constructors named `NewXxx`

#### Module & File Layout

- [ ] Service packages follow the file layout: `service.go`, `handler.go`, `handler_stubs.go`, `store.go`, `types.go`
- [ ] `handler.go` contains only implemented handlers — no stubs
- [ ] Handler groups split into `handler_<group>.go` only when exceeding ~200 lines
- [ ] No subfolders inside service packages — multiple files in one package is the Go way
- [ ] No reimplemented utilities that already exist in `serviceutil`, `protocol`, or the standard library — use `serviceutil` when a pattern appears in 2+ services

#### Performance

- [ ] Pre-sized slices: `make([]string, 0, n)` when length is known or estimable
- [ ] Streaming for large/unbounded data: `io.Reader`/`io.Writer` pipelines, not `io.ReadAll` into memory
- [ ] `strings.Builder` for concatenation, `strconv` over `fmt.Sprintf` for simple conversions
- [ ] `sync.Pool` for hot-path buffers where allocation profiling shows benefit
- [ ] No unnecessary copies of large structs — pass by pointer
- [ ] JSON decoding via `json.NewDecoder(r.Body)` — not `io.ReadAll` + `json.Unmarshal` for request bodies
- [ ] Target: every handler ≤1 ms overhead above store access

#### Concurrency & Safety

- [ ] Every goroutine respects context cancellation: `select { case <-ctx.Done(): return }`
- [ ] Tickers and timers cleaned up: `defer ticker.Stop()`
- [ ] No goroutine leaks — every spawned goroutine has a clear exit path
- [ ] Channels are closed or drained — no abandoned senders/receivers
- [ ] `r.Context()` passed to all blocking or downstream calls
- [ ] State accessed only through `state.Store` — no unprotected shared maps or globals
- [ ] Mutex scope is minimal — lock, do work, unlock. No I/O under lock

#### Memory Leaks

- [ ] HTTP response bodies always closed (`defer resp.Body.Close()`)
- [ ] Resources with cleanup (`os.File`, DB connections, tickers) always deferred
- [ ] No unbounded growth in maps, slices, or channels acting as caches
- [ ] Contexts are cancelled or have timeouts — no leaked goroutines waiting forever
- [ ] Large temporary allocations scoped tightly — not held across request boundaries

#### Project-Specific Rules

- [ ] `clock.Clock` used instead of `time.Now()` in all service/handler/store code
- [ ] `*config.Config` used — never `os.Getenv` in service code
- [ ] All mutable state through `state.Store` — JSON serialisation only in `store.go`
- [ ] Both `MemoryStore` and `SQLiteStore` updated when `Store` interface changes
- [ ] Structured logging with `zap` — no `fmt.Sprintf` in log messages
- [ ] Log levels correct: DEBUG for per-request detail, INFO for lifecycle, WARN for handled anomalies, ERROR for 5xx/panics
- [ ] Cross-platform: `filepath.Join` not string concatenation, `os.TempDir()` not `/tmp`
- [ ] `make test` passes with `-race`, `make lint` passes

---

### Frontend / TypeScript (`web/`)

#### Type Safety & Linting

- [ ] `strict: true` honoured — no `// @ts-ignore`, no `as any` to silence errors
- [ ] `import type` used for type-only imports (`consistent-type-imports` rule)
- [ ] No unused variables or parameters (allowed: `_` prefix, rest siblings)
- [ ] No floating Promises — every `Promise` is `await`ed, returned, or explicitly `void`ed
- [ ] No misused Promises — async functions not passed to void callbacks (async event handlers OK)
- [ ] No unnecessary type assertions (`as X` where the type is already correct)
- [ ] No unnecessary conditions (always-true / always-false checks)
- [ ] `no-explicit-any` — prefer `unknown` / proper types; `any` infects callers
- [ ] Build produces zero errors: `npm run build` must succeed with no type or bundle errors
- [ ] ESLint produces zero errors: `npm run lint` must pass (warnings tracked, errors block)
- [ ] Fix type errors, don't silence them.
- [ ] Prefer type guards and control flow analysis over type assertions. If you find yourself writing `as any` or `as unknown as X`, consider if a type guard function or discriminated union could achieve the same result with better safety.

#### Component & Data Patterns

- [ ] API calls go through `web/src/services/api/` — never direct `fetch` in components
- [ ] AWS SDK v3 clients used for standard AWS endpoints — `emulatorFetch` only for `/_overcast/*` endpoints
- [ ] TanStack Query for all server state — key factories and query/mutation options factories in `data.ts`
- [ ] Query invalidation after mutations uses `qc.invalidateQueries({ queryKey: keys.xxx() })`
- [ ] TanStack Router links are fully typed — no `as any` casts on route paths
- [ ] Route params accessed via `Route.useParams()` / `Route.useSearch()` — never string construction
- [ ] `routeTree.gen.ts` never manually edited

#### React Hooks & Effects

Effects are an escape hatch for synchronizing with _external_ systems (DOM APIs, third-party widgets, network). Everything else should stay inside the render cycle or event handlers. Flag any of these misuses:

- [ ] **Derived state in an Effect** — if a value can be computed from existing props/state, compute it during render; never store it in a separate state variable and sync it with `useEffect`
  ```tsx
  // 🔴 Avoid
  const [fullName, setFullName] = useState("");
  useEffect(() => {
    setFullName(first + " " + last);
  }, [first, last]);
  // ✅ Good
  const fullName = first + " " + last;
  ```
- [ ] **Expensive calculation in an Effect** — use `useMemo` to cache; never `useEffect` + extra state
  ```tsx
  // ✅ Good
  const visibleTodos = useMemo(
    () => getFiltered(todos, filter),
    [todos, filter],
  );
  ```
- [ ] **Resetting all state on prop change via Effect** — pass a different `key` to the component instead; React will reset all state automatically
- [ ] **Adjusting partial state on prop change via Effect** — set it directly during rendering (store the derived ID, not the object) or use `key`; `useEffect` causes an extra render pass with stale values
- [ ] **Event-specific logic in an Effect** — if code runs because the user did something (click, submit), put it in the event handler. Effects run because the component _appeared_, not because of an interaction
  ```tsx
  // 🔴 Avoid: fires again on every remount / page reload
  useEffect(() => { if (product.isInCart) showNotification(...); }, [product]);
  // ✅ Good: fire only when the button is clicked
  function handleBuyClick() { addToCart(product); showNotification(...); }
  ```
- [ ] **Chains of Effects** — multiple Effects that update state solely to trigger the next Effect cause cascading re-renders and become brittle. Consolidate all state updates into the event handler that originated the action
- [ ] **App-initialization logic in an Effect** — it fires twice in development (Strict Mode). Use a module-level `let didInit = false` guard or run it at the module top level outside any component
- [ ] **Notifying parents via Effect** — call the parent callback directly inside the same event handler that updates local state; don't relay through `useEffect`
- [ ] **Pushing data up to a parent via Effect** — lift the data fetch to the parent and pass it down as props; child→parent data flow via Effects makes the data flow hard to trace
- [ ] **Manual external-store subscription with Effect + state** — use `useSyncExternalStore` instead; it handles server rendering and avoids tearing
- [ ] **Data fetching without cleanup** — Effects that call `setState` after `fetch` without an `ignore`/`AbortController` cleanup will produce race conditions. Always return a cleanup function that sets `ignore = true` (or aborts the request). Prefer TanStack Query which handles this automatically

**The test:** before writing an Effect, ask "Is this synchronizing with something _outside_ React?" If the answer is "no — it just updates state based on other state or handles a user event", remove the Effect.

#### Styling & className Construction

- [ ] Tailwind CSS v4 canonical classes — no arbitrary-value syntax (`[…]`) when a standard class exists
- [ ] Design tokens (`text-fg`, `bg-bg`, `border-border`, `text-accent`) — no hardcoded hex values
- [ ] `cn()` used for any conditional/dynamic className — no template literals, no string concatenation, no bare ternaries in `className`
- [ ] `cn()` not used for single static strings — just use a plain string literal
- [ ] Shared classes hoisted out of ternary branches (no dup-ternary)
- [ ] Consider `cva()` when `cn()` has ≥3 conditional args (variant signal)
- [ ] Tailwind v4 syntax: prefer `*:` over `[&>…]` for child selectors

#### Component Organisation

- [ ] Shared components in `web/src/components/ui/` — one file per component
- [ ] Service-specific components in `web/src/features/<service>/components/`
- [ ] Components under ~200 LOC — extract when growing or reused elsewhere

## Severity Levels

| Level          | Meaning                                                                                                                                    | Action                                                                          |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------- |
| **Critical**   | Bug, data loss risk, goroutine/memory leak, security issue, race condition                                                                 | Must fix before merge                                                           |
| **Warning**    | Performance issue, DRY violation, oversized function/module, missing error context                                                         | Should fix before merge                                                         |
| **Suggestion** | Style nit, naming improvement, minor refactor opportunity                                                                                  | Nice to have                                                                    |
| **TODO**       | Improvement opportunity that doesn't block the current change — technical debt, future optimisation, missing edge case, potential refactor | Add a `// TODO(priority:Pn):` comment in code so it's tracked and not forgotten |

Not everything has to be fixed right now. If the review surfaces something worth doing but out of scope for the current change, recommend adding a TODO with an appropriate priority. The goal is to never lose sight of improvements — capture them in code so they're visible and actionable later.

## Output Format

````
## Code Review: <scope>

### Critical
- **[file.go:42]** Goroutine leak — channel `ch` is never closed when context is cancelled
  ```go
  // current
  go func() { ch <- result }()
  // fix: add select with ctx.Done()
  go func() { select { case ch <- result: case <-ctx.Done(): } }()
````

### Warning

- **[handler.go:180-220]** Function `handleCreateThing` is 85 lines with 4 levels of nesting — split validation into a helper

### Suggestions

- **[store.go:15]** Consider `ErrThingNotFound` instead of `errNotFound` for clarity

### Summary

X critical, Y warnings, Z suggestions across N files reviewed.

```

```
