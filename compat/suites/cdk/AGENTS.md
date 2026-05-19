# AGENTS.md — cdk suite

> Conventions for AI agents and contributors planning or implementing
> `compat/suites/cdk/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules and separation boundary that apply to every suite.
> This file covers CDK-specific details for agents building this suite from
> scratch.
>
> For quick-start, prerequisites, and env vars see [README.md](README.md).

---

## What this suite tests

End-to-end CDK v2 deployment compatibility via the native `cdk` CLI. It
is the CDK column of the compatibility matrix.

Unlike SDK suites, this suite does not test individual API operations. It
verifies that a real CDK app can bootstrap, synthesize, deploy a
multi-resource stack, and destroy it cleanly — all pointed at Overcast.

---

## Status

**In progress.** A first runnable implementation exists in `src/`; continue
iterating from the current code rather than rebuilding the suite from scratch.

---

## Runtime

| Item        | Value                                        |
| ----------- | -------------------------------------------- |
| Language    | TypeScript (CDK app and runner) + Node.js 20 |
| AWS client  | `aws-cdk` CLI + CDK v2 libraries (pinned in `package.json`) |
| CDK version | CDK v2 (pinned in `package.json`)                           |

> SDK upgrade policy: [compat/AGENTS.md § SDK version pinning](../../AGENTS.md#sdk-version-pinning--upgrade-strategy).
| CI image    | `node:20-alpine` with `aws-cdk` installed    |

---

## File layout (planned)

```
compat/suites/cdk/
  AGENTS.md          ← you are here
  README.md          ← quick-start, prerequisites, env vars
  Dockerfile         ← node:20-alpine + aws-cdk + cdk bootstrap deps
  package.json       ← aws-cdk, aws-cdk-lib, constructs, tsx, @aws-sdk/client-*
  tsconfig.json      ← NodeNext, strict (mirror node-js-sdk)
  cdk.json           ← CDK app entrypoint: src/app.ts

  src/
    app.ts           ← CDK App: instantiates CdkCompatStack
    stack.ts         ← CdkCompatStack — all planned resources as CDK constructs
    runner.ts        ← entry point; runs lifecycle groups, emits NDJSON to stdout
    lib/
      harness.ts     ← TestContext, TestGroup, TestCase, runSuite(), emitEvent()
      clients.ts     ← makeClients(ctx) for spot-check API calls
    groups/
      lifecycle.ts   ← one TestGroup per CDK lifecycle phase
```

**One file for the CDK app stack.** Never split the stack across multiple
`stack.*.ts` files — a single `CdkCompatStack` class is sufficient.

---

## Group anatomy

Unlike SDK suites, test groups map to CDK lifecycle phases, not individual API
operations. Each phase is a `TestGroup` with `TestCase` entries.

```typescript
// src/groups/lifecycle.ts
import { execSync } from "child_process";
import type { TestGroup } from "../lib/harness.js";
import { makeClients } from "../lib/clients.js";

export function makeLifecycleGroups(suite: string): TestGroup[] {
  return [
    {
      suite,
      service: "cdk",
      name: "cdk-bootstrap",
      tests: [
        {
          name: "Bootstrap",
          fn: async (ctx) => {
            const out = execSync(
              "npx cdk bootstrap aws://000000000000/us-east-1",
              {
                encoding: "utf-8",
                env: { ...process.env, AWS_DEFAULT_REGION: ctx.region },
              },
            );
            if (!out.includes("Environment aws://")) {
              throw new Error(`bootstrap output unexpected: ${out}`);
            }
          },
        },
      ],
      // Bootstrap is idempotent — no teardown needed.
    },
    {
      suite,
      service: "cdk",
      name: "cdk-deploy",
      tests: [
        {
          name: "Synth",
          fn: async (ctx) => {
            execSync("npx cdk synth", {
              env: { ...process.env, AWS_DEFAULT_REGION: ctx.region },
            });
          },
        },
        {
          name: "Deploy",
          fn: async (ctx) => {
            execSync(`npx cdk deploy --require-approval never`, {
              env: { ...process.env, AWS_DEFAULT_REGION: ctx.region },
            });
            // Stash stack name so spot-check and destroy groups can use it.
            ctx["_stackName"] = `OcCompatStack-${ctx.runId}`;
          },
        },
      ],
      teardown: async (ctx) => {
        try {
          execSync("npx cdk destroy --force", {
            env: { ...process.env, AWS_DEFAULT_REGION: ctx.region },
          });
        } catch {}
      },
    },
    {
      suite,
      service: "cdk",
      name: "cdk-spot-check",
      tests: [
        {
          name: "BucketExists",
          fn: async (ctx) => {
            const { s3 } = makeClients(ctx);
            // Bucket name comes from the CDK stack output or a known naming pattern.
            const bucket = ctx["_s3BucketName"] as string;
            if (!bucket)
              throw new Error("_s3BucketName not set by deploy group");
            const { Buckets = [] } = await s3.send(new ListBucketsCommand({}));
            if (!Buckets.some((b) => b.Name === bucket)) {
              throw new Error(`expected bucket ${bucket} in ListBuckets`);
            }
          },
        },
      ],
      // Spot-check is read-only — no teardown needed.
    },
  ];
}
```

---

## Key types

Reuse the same `TestContext`, `TestGroup`, `TestCase` shapes as the
`node-js-sdk` suite. See
[suites/node-js-sdk/AGENTS.md](../node-js-sdk/AGENTS.md) for the full type
definitions.

```typescript
interface TestContext {
  endpoint: string; // e.g. "http://localhost:4566"
  region: string; // e.g. "us-east-1"
  runId: string; // unique per invocation; prefix all resource names
  log: (msg: string) => void;
  [key: string]: unknown; // inter-test state bag
}
```

---

## Naming conventions

| Element       | Convention                                                         |
| ------------- | ------------------------------------------------------------------ |
| Group name    | `cdk-<phase>` (kebab-case), e.g. `cdk-bootstrap`, `cdk-deploy`     |
| Test name     | Title-case phase name, e.g. `Bootstrap`, `Deploy`, `BucketExists`  |
| Stack name    | `OcCompatStack-{runId}` — always include `runId` to avoid clashes  |
| CDK resources | Use CDK logical IDs that include `runId` where CDK allows renaming |

---

## Inter-test state

Use the context bag to share data between lifecycle phases:

```typescript
// In the Deploy test:
ctx["_stackName"] = `OcCompatStack-${ctx.runId}`;
ctx["_s3BucketName"] = resolvedBucketName;

// In a spot-check test:
const bucket = ctx["_s3BucketName"] as string;
```

Keys must start with `_`. Never rely on inter-group state — `ctx` is fresh for
every group run.

---

## Teardown rules (cdk-specific)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional CDK specifics:

- The `cdk-deploy` group's teardown **must** call `cdk destroy --force`.
  This must run even if the deploy tests fail partway through. The runner
  always calls teardown after tests, even on failure.
- CDK may create a CDK toolkit bucket (typically `cdk-*`). If `destroy` leaves
  it behind, add explicit SDK cleanup in teardown via `makeClients(ctx)`.
- Never hard-code an AWS account ID. Use `000000000000` which Overcast treats
  as the default fake account.
- `cdk bootstrap` is idempotent — no teardown needed for the bootstrap
  group.
- `cdk synth` creates no durable resources — no teardown needed.

---

## Error messages

Format assertion errors to identify the failed check and the run context:

```typescript
throw new Error(
  `expected bucket ${bucket} in ListBuckets (runId=${ctx.runId})`,
);
throw new Error(
  `deploy failed: stack ${stackName} did not reach CREATE_COMPLETE`,
);
```

---

## What agents must NOT do

- Never import from `internal/`, `router/`, or any Overcast server source tree.
- Never hard-code the endpoint — CDK local uses `OVERCAST_ENDPOINT` env var;
  spot-check SDK calls use `ctx.endpoint`.
- Never call `process.exit` inside a test or teardown function.
- Never write to stdout inside a lifecycle step — the harness parses stdout as
  NDJSON.
- Never deploy without `--require-approval never` — omitting it will block on
  stdin in CI.
- Never leave orphaned stacks — always register a teardown that calls
  `cdk destroy --force`.
- Never reference CDK resources by hard-coded logical IDs shared with other
  test runs — always embed `runId` in stack/resource names.

---

## Implementation checklist

When building this suite from scratch:

1. Create `package.json` with `aws-cdk`, `aws-cdk-lib`, `constructs`,
   `@aws-sdk/client-*` packages, and `tsx` as a dev dependency.
2. Create `tsconfig.json` (mirror `node-js-sdk/tsconfig.json`).
3. Create `cdk.json` pointing at `src/app.ts`.
4. Create `src/stack.ts` with `CdkCompatStack` defining the planned resources
   (see README.md for the full resource list).
5. Create `src/app.ts` instantiating `CdkCompatStack`.
6. Copy `lib/harness.ts` and `lib/clients.ts` patterns from `node-js-sdk/src/`
   — adjust imports but keep the same `TestContext`/`TestGroup` interface.
7. Create `src/groups/lifecycle.ts` with the lifecycle phase groups.
8. Create `src/runner.ts` that calls `runSuite()` with all groups.
9. Create `Dockerfile` based on `node:20-alpine`; install `aws-cdk`
   and pin the version.
10. Register the suite in `compat/runner.go` and `compat/suites/registry.json`.
11. Run `tsc --noEmit` to confirm type correctness.
12. Run the suite locally against a live Overcast instance and verify that
    the NDJSON output is well-formed and every lifecycle phase emits a result.
