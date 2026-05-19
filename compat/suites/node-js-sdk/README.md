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

### Via Docker (no local Node.js required)

```bash
# Build the suite image
docker build -t overcast-compat-node-js-sdk compat/suites/node-js-sdk

# Run against a local Overcast instance
docker run --rm --network host \
  -e OVERCAST_ENDPOINT=http://localhost:4566 \
  overcast-compat-node-js-sdk
```

### Via the Go CLI (recommended — runs all suites)

```bash
go run ./cmd/compat --endpoint http://localhost:4566
# or just this suite:
go run ./cmd/compat --endpoint http://localhost:4566 --suite node-js-sdk
```

---

## Environment variables

| Variable                      | Default                 | Description                                |
| ----------------------------- | ----------------------- | ------------------------------------------ |
| `OVERCAST_ENDPOINT`           | `http://localhost:4566` | Overcast base URL                          |
| `OVERCAST_DEFAULT_REGION`     | `us-east-1`             | AWS region advertised to the SDK           |
| `OVERCAST_COMPAT_SKIP_DOCKER` | unset                   | Set to `1` to skip Lambda invocation tests |
| `OVERCAST_COMPAT_GROUPS`      | unset (all)             | Comma-separated group names to run         |

---

## Architecture

```
node-js-sdk/
  Dockerfile        ← self-contained CI image (node:20-alpine)
  package.json      ← dependencies: all @aws-sdk/client-* + tsx
  tsconfig.json     ← NodeNext ESM, strict
  README.md         ← you are here

  src/
    runner.ts       ← entry point; assembles groups, calls runSuite()
    lib/
      harness.ts    ← TestContext, TestGroup, runGroup(), runSuite(),
                       makeRunId(), emitEvent()
      clients.ts    ← makeClients(ctx) → { s3, sqs, dynamodb, … } (14 clients)
    groups/
      s3.ts             cloudwatch-logs.ts
      sqs.ts            iam.ts
      dynamodb.ts       sts.ts
      sns.ts            secretsmanager.ts
      lambda.ts         kms.ts
      ses.ts            ssm.ts
                        kinesis.ts
                        eventbridge.ts
```

### Key types (`lib/harness.ts`)

| Type / function | Purpose                                                                                                                   |
| --------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `TestContext`   | Passed to every test fn: `endpoint`, `region`, `runId`, `log()`, plus a `[key: string]: unknown` bag for inter-test state |
| `TestGroup`     | `{ suite, service, name, tests[], setup?, teardown? }`                                                                    |
| `TestCase`      | `{ name, fn, skip? }` — throw to fail, return to pass                                                                     |
| `runSuite()`    | Runs all groups; emits NDJSON to stdout                                                                                   |
| `makeRunId()`   | Returns `"oc-{8-hex}"` unique per invocation                                                                              |
| `emitEvent()`   | Writes a single NDJSON line to stdout                                                                                     |

### Client map (`lib/clients.ts`)

`makeClients(ctx)` returns an object with one pre-configured AWS SDK v3 client
per service. All clients point at `ctx.endpoint` with fixed credentials
(`overcast` / `overcast`) — the emulator accepts any non-empty values.

Services: `s3` (path-style), `sqs`, `sns`, `dynamodb`, `lambda`, `logs`,
`ses`, `iam`, `sts`, `secretsmanager`, `kms`, `ssm`, `kinesis`, `eventbridge`.

### Test groups

Each file under `src/groups/` exports a `make<Service>Groups(suite)` factory
that returns `TestGroup[]`. Groups are registered in `runner.ts`.

| File                 | Groups                                                                                      | Implemented? |
| -------------------- | ------------------------------------------------------------------------------------------- | :----------: |
| `s3.ts`              | s3-crud, s3-copy, s3-multipart, s3-versioning, s3-tagging, s3-website, s3-cors              | ✅ (mostly)  |
| `sqs.ts`             | sqs-queues, sqs-messages, sqs-dlq, sqs-fifo                                                 |      ✅      |
| `dynamodb.ts`        | dynamodb-tables, dynamodb-items, dynamodb-query, dynamodb-batch, dynamodb-txn, dynamodb-ttl |    ✅ P1     |
| `sns.ts`             | sns-topics, sns-publish, sns-subscriptions                                                  |    ✅ P1     |
| `lambda.ts`          | lambda-crud, lambda-invoke, lambda-aliases, lambda-layers                                   |      ✅      |
| `cloudwatch-logs.ts` | logs-groups, logs-events                                                                    |      ✅      |
| `ses.ts`             | ses-send, ses-identities, ses-templates                                                     | ✅ (partial) |
| `iam.ts`             | iam-users, iam-roles, iam-policies, iam-groups                                              |      ❌      |
| `sts.ts`             | sts-identity, sts-assume                                                                    |      ❌      |
| `secretsmanager.ts`  | secretsmanager-crud, secretsmanager-rotate                                                  |      ❌      |
| `kms.ts`             | kms-keys, kms-crypto, kms-asymmetric                                                        |      ❌      |
| `ssm.ts`             | ssm-parameters, ssm-secure, ssm-path                                                        |      ❌      |
| `kinesis.ts`         | kinesis-streams, kinesis-records, kinesis-shards                                            |      ❌      |
| `eventbridge.ts`     | eventbridge-buses, eventbridge-rules, eventbridge-events                                    |      ❌      |

---

## Adding a new test group

1. Open (or create) `src/groups/<service>.ts`.
2. Add a `TestGroup` object to the array returned by `make<Service>Groups()`.
3. Import and register the group in `src/runner.ts`.
4. Run `npm run typecheck` to verify no type errors.

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
      // skip: true  ← only when ext infra unavailable (e.g. Docker for Lambda)
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
