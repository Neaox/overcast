# CloudFormation synchronous fast-path — plan

> Status: not started.
> Goal: make small stacks reach their terminal status **before** `CreateStack` /
> `ExecuteChangeSet` / `UpdateStack` / `DeleteStack` returns, so an SDK waiter's
> immediate first `DescribeStacks` check sees the final state and never enters
> its 5-second poll delay. Expected effect: `cdk deploy` of N fast stacks drops
> from ~5s × N to ~0.3s × N.

## Why (measured evidence)

Investigated 2026-07-19 against a running `overcast:dev` container
(Docker Desktop on Windows 11 / WSL2, hybrid state backend, 15 services
enabled, verbose logging; timings from `docker logs --timestamps` and a
probe stack driven with `curl`):

- A CDK bootstrap + deploy of 4 app stacks appeared to hang for ~45s.
  Server-side, **every request completed in <200ms**; a 3-resource probe
  stack (2 × SQS queue, 1 × SSM parameter) went `CreateStack` →
  `CREATE_COMPLETE` in **184ms** end to end.
- The per-stack wall cost was ~5.1s: the CDK's AWS SDK waiter does an
  immediate first `DescribeStacks` check — which sees `CREATE_IN_PROGRESS`
  because provisioning starts in a goroutine microseconds earlier — then
  sleeps 5s (the waiter's `minDelay`) before checking again. The stack is
  actually complete milliseconds later; nobody notices for 5 seconds.
- 5 stacks (toolkit + 4 app stacks) × 5s ≈ 25s of pure client-side
  polling latency that the emulator can eliminate.

Real AWS returns `CREATE_IN_PROGRESS` because provisioning genuinely takes
minutes. Nothing in the API contract requires `IN_PROGRESS` to be
*observable*: a stack that is `CREATE_COMPLETE` by the first describe is
indistinguishable from a very fast deployment. `CreateChangeSet` already
returns `CREATE_COMPLETE` synchronously
([handler.go:440](../../internal/services/cloudformation/handler.go)).

## Design

**Bounded wait, no template heuristics.** Keep provisioning in its
goroutine exactly as today, but have the provisioner entry points block
the calling handler until provisioning reaches a terminal status **or** a
budget elapses, whichever is first:

```go
// provisioner.go — createStack (same shape for updateStack / deleteStack)
func (p *provisioner) createStack(stack *Stack, tmpl *Template) {
    done := make(chan struct{})
    p.wg.Add(1)
    go func() {
        defer p.wg.Done()
        defer close(done)
        p.provisionStackResources(stack, tmpl)
    }()
    p.awaitBriefly(done) // select { <-done | <-p.clk.After(budget) }
}
```

Why this shape and not "provision synchronously for small stacks":

- **No size/shape heuristic to get wrong.** Nested stacks recurse
  arbitrarily deep, custom resources call out to Lambda, and a template
  with 3 resources can still be slow if a handler stalls. A time budget
  handles all of these uniformly: fast stacks return complete, slow
  stacks degrade to today's async behaviour automatically.
- **One choke point.** Both protocol front-ends — Query
  ([handler.go:200](../../internal/services/cloudformation/handler.go),
  [handler.go:558](../../internal/services/cloudformation/handler.go)) and
  typed/JSON
  ([typed_logic.go:605](../../internal/services/cloudformation/typed_logic.go))
  — call the same `prov.createStack` / `prov.updateStack`, so waiting
  inside the provisioner methods covers every path with no handler
  changes. `nestedStackHandler` calls `provisionStackResources` directly
  and is unaffected (children are already synchronous within the parent).
- **Failure semantics unchanged.** A fast-failing stack returns with
  `ROLLBACK_COMPLETE`/`CREATE_FAILED` already visible — the API response
  itself is identical (`StackId` / empty result); only what the *next*
  describe sees changes. Rollback, event recording (`recordEvent`), and
  `DescribeStackEvents` history are untouched.
- **Shutdown unchanged.** The goroutine + `p.wg` lifecycle is identical;
  `provisioner.stop` still drains as before.

**Budget configuration.** New `*config.Config` field, e.g.
`CFNSyncWaitMS` / env `OVERCAST_CFN_SYNC_WAIT_MS`, default **1000**;
`0` disables the wait (restores current behaviour — the escape hatch if
this ever misbehaves). Follows the existing typed-env-var pattern in
[internal/config/config.go](../../internal/config/config.go); no
`os.Getenv` in service code. The wait must use the injected
`clock.Clock` (`clk.NewTimer` — remember to stop it), never `time.After`,
so tests can skip it instantly.

**Caveat — handler goroutine occupancy:** each in-flight create/update
holds one HTTP handler goroutine for up to the budget. That is the same
cost as any 1s-slow request and is bounded by the client's own
concurrency; no pooling needed. Do not raise the default budget above
~2s: SDK default HTTP timeouts and CDK progress UX assume the call
returns promptly.

## Workstreams

### P0 — failing tests first (per AGENTS.md)

1. Integration test (`tests/integration/cloudformation/`): create a small
   stack via the SDK/wire client, then **immediately** `DescribeStacks`
   → expect `CREATE_COMPLETE`. Must fail against current code
   (`CREATE_IN_PROGRESS`) before P1 lands. Repeat for
   `ExecuteChangeSet` (CREATE-type changeset) and `DeleteStack` →
   immediate describe shows `DELETE_COMPLETE` (or stack gone), and
   `UpdateStack` → `UPDATE_COMPLETE`.
2. Unit test for the wait primitive with the mock clock:
   - done closes before budget → returns as soon as done closes;
   - budget elapses first → returns at budget, provisioning continues
     in background and completes afterwards;
   - budget = 0 → returns immediately (feature off).
3. Regression guard: `DescribeStackEvents` after a fast create still
   contains the full ordered history
   (`CREATE_IN_PROGRESS` → per-resource events → `CREATE_COMPLETE`).

### P1 — the wait

1. Add `awaitBriefly` (name TBD) to `provisioner`; wire into
   `createStack`, `updateStack`, `deleteStack`.
2. Add the config field + env parsing + default.
3. Plumb the budget into `newProvisioner` (it already receives
   `*config.Config`).

### P2 — changeset execution status (latent fidelity bug, same area)

`ExecutionStatus` is set to `EXECUTE_IN_PROGRESS` on execute
([handler.go:551](../../internal/services/cloudformation/handler.go),
[typed_logic.go:598](../../internal/services/cloudformation/typed_logic.go))
but **nothing ever sets `EXECUTE_COMPLETE`**
([types.go:169](../../internal/services/cloudformation/types.go) is
defined and unused). Tools that poll `DescribeChangeSet` after execution
spin forever. Fix: when provisioning triggered by a changeset reaches a
terminal stack status, flip the changeset to `EXECUTE_COMPLETE` (or
`EXECUTE_FAILED` on rollback/failure). Cleanest: pass an optional
completion callback into `createStack`/`updateStack` from
`ExecuteChangeSet`, invoked from the provisioning goroutine after the
terminal status is persisted. Test: execute a CREATE changeset, describe
it after stack completion → `EXECUTE_COMPLETE`.

### P3 — document where the observed time goes (docs/performance.md)

`docs/performance.md` currently covers startup and per-request overhead
but says nothing about **client-perceived workflow latency**, which is
where the "overcast feels slow" reports actually come from. Add a section
(e.g. "Client-perceived latency: SDK waiters and CDK deploys") that
documents, with measurement conditions per the existing
[Documenting performance claims](../performance.md#documenting-performance-claims)
policy:

- Stack provisioning is asynchronous but typically completes in
  milliseconds (probe: 3-resource stack `CREATE_COMPLETE` in 184ms;
  conditions: 2026-07-19, `overcast:dev` Alpine image under Docker
  Desktop/WSL2 on Windows 11, hybrid backend, timings via
  `docker logs --timestamps` and `curl` polling at 100ms).
- Without the sync fast-path, each CDK stack deploy costs one SDK waiter
  `minDelay` (~5s) regardless of server speed; with it (budget ≥ stack
  provisioning time), the waiter's immediate first check succeeds.
- What the emulator can and cannot fix: CDK CLI startup and `cdk synth`
  (measured ~8s of zero-request wall time in the same session) are
  client-side and unaffected.
- The related startup-timeline mislabeling ("Go runtime + package init"
  absorbing container-creation / OS-loader time, and
  `startup_duration_ms` sharing the polluted anchor) is covered by its
  own plan: [startup-metrics-honesty.md](./startup-metrics-honesty.md).

### Docs / hygiene checklist (before finishing)

- [ ] `docs/services/cloudformation.md` — describe the sync fast-path,
      the env var, and the changeset `EXECUTE_COMPLETE` fix (update both
      tables if any endpoint semantics rows change).
- [ ] `CHANGELOG.md` entry.
- [ ] `make docs` if capability tables changed.
- [ ] `gofmt -w` → `go vet` on
      `./internal/services/cloudformation/...` and `./internal/config/...`;
      scoped tests
      (`go test -count=1 ./internal/services/cloudformation/... ./tests/integration/cloudformation/...`);
      widen to `go build ./... && go vet ./...` at the end.
- [ ] `make bench-startup` unchanged (the wait is request-path only; it
      must not touch `router.New()`).

## Verification (end to end)

Rebuild the image, rerun the motenova `cdk deploy` workflow that
originally took ~45s, and compare `docker logs --timestamps`: per-stack
gap between consecutive template fetches should drop from ~5.1s to
roughly the stack's real provisioning time (<0.5s). Document the
before/after numbers with conditions in the PR description.

## Risks / open questions

- **Waiter behaviour assumption**: the win depends on the SDK waiter's
  first check being immediate. Verified for the CDK v2 toolkit's
  deploy waiter empirically in the 2026-07-19 session (first
  `DescribeStacks` lands within ~10ms of `ExecuteChangeSet`); if some
  SDK's waiter sleeps first, that client simply keeps today's timing —
  no regression.
- **Clients that assert on `IN_PROGRESS`**: a test suite that *requires*
  observing `CREATE_IN_PROGRESS` via describe (not events) would break.
  Considered acceptable — real AWS makes no such guarantee, and
  `DescribeStackEvents` history still shows the full transition. The
  `OVERCAST_CFN_SYNC_WAIT_MS=0` escape hatch exists.
- **Compat suites**: run `compat/suites/*` CloudFormation/CDK suites
  before merging; they are the contract for this behaviour.
