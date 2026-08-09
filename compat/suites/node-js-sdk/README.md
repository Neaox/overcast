# node-js-sdk suite

Runs the full Overcast AWS compatibility matrix using **AWS SDK v3 for
JavaScript** (TypeScript, Node.js 20).

Tests cover all services — including ones not yet implemented in Overcast.
Failures on unimplemented services are expected and are the coverage metric,
not a problem to fix.

---

## Quick start

### Locally (Node.js 20+ required)

```bash
cd compat/suites/node-js-sdk
npm install

# Start Overcast first (separate terminal):
#   go run ./cmd/overcast -- serve
#   — or —
#   docker run -p 4566:4566 ghcr.io/your-org/overcast

npm test
```

### Via the Go CLI (recommended — runs all suites)

```bash
# Starts its own Overcast instance on a free port and stops it afterwards:
go run ./cmd/compat
# or just this suite:
go run ./cmd/compat --suite node-js-sdk
# or against an instance you are already running:
go run ./cmd/compat --endpoint http://localhost:4566 --suite node-js-sdk
```

### Via Docker (no local toolchain required)

This suite has no image of its own — it runs as a subprocess of the compat
runner container, which already carries Node.js. Start Overcast and the runner
together with compose, from the repo root:

```bash
OVERCAST_COMPAT_SUITE=node-js-sdk docker compose -f compat/docker-compose.yml run --rm compat
```

---

## Environment variables

| Variable                        | Default                 | Description                                             |
| ------------------------------- | ----------------------- | ------------------------------------------------------- |
| `OVERCAST_ENDPOINT`             | `http://localhost:4566` | Overcast base URL                                       |
| `OVERCAST_DEFAULT_REGION`       | `us-east-1`             | AWS region advertised to the SDK                        |
| `OVERCAST_COMPAT_SKIP_DOCKER`   | unset                   | Set to `1` to drop the `docker` capability, skipping every test the registry marks `requires: [docker]` |
| `OVERCAST_COMPAT_GROUPS`        | unset (all)             | Comma-separated group names to run                      |
| `OVERCAST_COMPAT_SERVICE`       | unset (all)             | Single AWS service name to run, e.g. `s3`               |
| `OVERCAST_COMPAT_TESTS`         | unset (all)             | Comma-separated test names to run within those groups   |
| `OVERCAST_COMPAT_TEST_PAIRS`    | unset                   | Comma-separated `group:test` pairs — overrides all three filters above |
| `OVERCAST_COMPAT_RUN_ID`        | auto-generated          | Run ID injected by the root runner; scopes resource names and the post-run sweep |
| `OVERCAST_COMPAT_NO_CLEANUP`    | unset                   | Set to `1` to skip the post-run resource sweep          |
| `OVERCAST_COMPAT_INTERACTIVE`   | unset                   | Set to `1` for the long-lived command-loop mode the runner and dashboard use |
| `OVERCAST_COMPAT_PARALLEL_SLOTS`| `8`                     | Concurrent group executions in interactive mode         |

---

## Architecture

```
node-js-sdk/
  package.json      ← dependencies: all @aws-sdk/client-* + tsx
  tsconfig.json     ← NodeNext ESM, strict
  README.md         ← you are here

  src/
    runner.ts       ← entry point; builds groups from the registry, then either
                       runs the suite once or serves the interactive command loop
    lib/
      harness.ts    ← TestContext, TestGroup, runGroup(), runSuite(),
                       Semaphore, makeRunId(), emitEvent()
      clients.ts    ← makeClients(ctx) → { s3, sqs, dynamodb, … } (30 clients)
      registry.ts   ← loads ../../registry.json, builds groups from it,
                       validates impl keys (mergeImpls, validateImpls)
      commands.ts   ← stdin NDJSON command loop for interactive mode
      cleanup.ts    ← sweepAll(): post-run resource sweep, scoped to the runId
    groups/
      index.ts      ← makeAllGroups() + makeImplMap() — the registration point
      apigateway.ts       elasticache.ts     ses.ts
      appsync.ts          eventbridge.ts     shield.ts
      cloudformation.ts   iam.ts             sns.ts
      cloudfront.ts       kinesis.ts         sqs.ts
      cloudwatch-logs.ts  kms.ts             ssm.ts
      cognito.ts          lambda.ts          stepfunctions.ts
      dynamodb.ts         pipes.ts           sts.ts
      ec2.ts              rds.ts             waf.ts
      ecs.ts              s3.ts
      efs.ts              secretsmanager.ts
```

### Key types (`lib/harness.ts`)

| Type / function | Purpose                                                                                                                   |
| --------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `TestContext`   | Passed to every test fn: `endpoint`, `region`, `runId`, `log()`, plus a `[key: string]: unknown` bag for inter-test state |
| `TestGroup`     | `{ suite, service, name, tests[], setup?, teardown? }`                                                                    |
| `TestCase`      | `{ name, fn, skip?, op?, na?, depends? }` — throw to fail, return to pass. `skip` takes a reason string, `na` marks an operation the SDK does not expose (excluded from pass rates), `depends` names same-group tests that must pass first, `op` overrides (or with `false` suppresses) the AWS doc link |
| `runSuite()`    | Runs all groups; emits NDJSON to stdout                                                                                   |
| `makeRunId()`   | Returns `"oc-{8-hex}"` unique per invocation                                                                              |
| `emitEvent()`   | Writes a single NDJSON line to stdout                                                                                     |

### Client map (`lib/clients.ts`)

`makeClients(ctx)` returns an object with one pre-configured AWS SDK v3 client
per service. All clients point at `ctx.endpoint` with fixed credentials
(`overcast` / `overcast`) — the emulator accepts any non-empty values.

Services: `s3` (path-style), `sqs`, `sns`, `dynamodb`, `lambda`, `logs`, `ses`,
`iam`, `sts`, `secretsmanager`, `kms`, `ssm`, `kinesis`, `eventbridge`, `pipes`,
`cloudformation`, `ec2`, `ecs`, `cognito`, `appsync`, `apigateway`,
`apigatewayv2`, `rds`, `elasticache`, `efs`, `sfn`, `wafv2`, `shield`,
`cloudfront`, `ecr`.

### Test groups

The group list is **not** defined here — it is built from the shared
cross-suite registry at [`compat/suites/registry.json`](../registry.json),
which is the single source of truth for which groups and tests exist across
every suite. `runner.ts` loads it, collects this suite's implementations, and
calls `buildGroupsFromRegistry()`.

The consequence worth knowing: **a registry test with no implementation here is
not absent, it is reported as a skip** ("not yet implemented in node-js-sdk test
suite"). So there is no implemented/not-implemented table to keep in this file —
run the suite, or open the dashboard's comparison view, and the skips are the
coverage gap. Tests the JavaScript SDK cannot express at all are marked `na`
instead and excluded from pass rates.

Each file under `src/groups/` exports a `make<Service>Groups(suite)` factory
returning `TestGroup[]`; `groups/index.ts` composes them all via
`makeAllGroups()`, and `makeImplMap()` flattens them into the group-qualified
`group:test` keys the registry resolves against.

Two registration mistakes are hard errors that exit the suite rather than
warnings, because either one would otherwise report a result for a test that
never ran:

- **A duplicate key** — two group files producing the same `group:test`. One
  implementation would be silently discarded (`mergeImpls`).
- **An unusable key** — one matching no registry entry (a typo or stale name),
  or a bare name that several groups declare, which cannot say which group it
  implements (`validateImpls`). `ListUsers` belongs to both `iam-users` and
  `cognito-userpools`, so it must be qualified.

---

## Adding a new test group

1. Add the group and its tests to [`compat/suites/registry.json`](../registry.json).
   Nothing runs until it is declared there, and every other suite immediately
   shows the new tests as skips — which is the point.
2. Open (or create) `src/groups/<service>.ts`.
3. Add a `TestGroup` object to the array returned by `make<Service>Groups()`.
   The `name` must match the registry group, and each `TestCase.name` a test
   the registry declares in it.
4. For a new file, import the factory and spread it into `makeAllGroups()` in
   `src/groups/index.ts` (not `runner.ts`).
5. Run `npm run typecheck` for type errors and `npm run test:unit` for the
   registry unit tests, which resolve this suite's real impl keys against the
   real `registry.json` and so catch a mis-keyed registration without a run.

Group anatomy:

```typescript
{
  suite,                  // passed in from runner.ts
  service: "s3",          // lowercase AWS service name
  name: "s3-new-feature", // kebab-case, unique across all groups
  setup: async (ctx) => {
    // create prerequisite resources — throw to skip all tests
  },
  tests: [
    {
      name: "OperationName",  // PascalCase, matches AWS API operation
      fn: async (ctx) => {
        const { s3 } = makeClients(ctx)
        const resp = await s3.send(new SomeCommand({ ... }))
        if (!resp.Field) throw new Error("expected Field")
      },
      // Infrastructure requirements belong in the registry, not here:
      // `"requires": ["docker"]` auto-skips the test wherever the capability
      // is absent, in every suite at once.
    },
  ],
  teardown: async (ctx) => {
    // always wrapped in try/catch; runs even if tests failed
    try { await s3.send(new DeleteBucketCommand({ ... })) } catch {}
  },
}
```

### Rules

- **Never mock the SDK.** Every call hits a real Overcast instance.
- **Never skip to hide a gap.** Let the test run and fail — that's the signal.
- **Use `ctx.runId` for all resource names** (`${ctx.runId}-<short-suffix>`).
- **Assert meaningful state**, not just "no error".
- **Teardown must be fault-tolerant** — wrap each delete in `try/catch`.
- **Use `ctx.log()` for debug output** — never write directly to stdout.

---

## Output format

The runner emits NDJSON to stdout. See the [wire format spec](../../README.md#wire-format-ndjson) in the root README.
