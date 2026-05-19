# AGENTS.md — pulumi suite

> Conventions for AI agents and contributors planning or implementing
> `compat/suites/pulumi/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules and separation boundary that apply to every suite.
> This file covers Pulumi-specific details for agents building this suite
> from scratch.
>
> For quick-start, prerequisites, and env vars see [README.md](README.md).

---

## What this suite tests

End-to-end Pulumi deployment compatibility using the **Pulumi AWS Classic
provider** pointed at Overcast. It is the Pulumi column of the compatibility
matrix.

Like the `terraform` and `tofu` suites, this suite does not test individual
API operations. It verifies that `pulumi up` can provision a multi-resource
stack and `pulumi destroy` removes it cleanly.

---

## Status

**Planned.** No implementation exists yet. Follow the implementation checklist
at the end of this file to build the suite from scratch.

---

## Runtime

| Item          | Value                                                    |
| ------------- | -------------------------------------------------------- |
| Language      | TypeScript (Pulumi program + runner) + Node.js 20        |
| IaC tool      | Pulumi CLI (`pulumi`) + `@pulumi/aws` provider           |
| CI image      | `node:20-alpine` with Pulumi CLI installed               |
| State backend | Local file backend (`PULUMI_BACKEND_URL=file://./state`) |

---

## File layout (planned)

```
compat/suites/pulumi/
  AGENTS.md          ← you are here
  README.md          ← quick-start, prerequisites, env vars
  Dockerfile         ← node:20-alpine + pulumi CLI; runs runner.ts
  package.json       ← @pulumi/aws, @pulumi/pulumi, @aws-sdk/client-*, tsx
  tsconfig.json      ← NodeNext, strict (mirror node-js-sdk)
  Pulumi.yaml        ← Pulumi project definition
  Pulumi.dev.yaml    ← stack config for the local dev stack
  state/             ← local file backend state (created at runtime; gitignored)

  src/
    index.ts         ← Pulumi program: all planned resources as @pulumi/aws constructs
    runner.ts        ← entry point; runs lifecycle groups, emits NDJSON to stdout
    lib/
      harness.ts     ← TestContext, TestGroup, TestCase, runSuite(), emitEvent()
      clients.ts     ← makeClients(ctx) for spot-check SDK calls
    groups/
      lifecycle.ts   ← one TestGroup per Pulumi lifecycle phase
```

---

## Group anatomy

Test groups map to Pulumi lifecycle phases (`up`, spot-check assertions,
`destroy`). Each phase is a `TestGroup` with `TestCase` entries.

```typescript
// src/groups/lifecycle.ts
import { execSync } from "child_process";
import type { TestGroup } from "../lib/harness.js";
import { makeClients } from "../lib/clients.js";
import { ListBucketsCommand } from "@aws-sdk/client-s3";

export function makeLifecycleGroups(suite: string): TestGroup[] {
  return [
    {
      suite,
      service: "pulumi",
      name: "pulumi-up",
      tests: [
        {
          name: "Up",
          fn: async (ctx) => {
            execSync("pulumi up --yes --non-interactive", {
              encoding: "utf-8",
              env: {
                ...process.env,
                AWS_DEFAULT_REGION: ctx.region,
                PULUMI_BACKEND_URL: "file://./state",
                PULUMI_CONFIG_PASSPHRASE: "",
              },
            });
          },
        },
      ],
      teardown: async (ctx) => {
        try {
          execSync("pulumi destroy --yes --non-interactive", {
            env: {
              ...process.env,
              AWS_DEFAULT_REGION: ctx.region,
              PULUMI_BACKEND_URL: "file://./state",
              PULUMI_CONFIG_PASSPHRASE: "",
            },
          });
        } catch {}
      },
    },
    {
      suite,
      service: "pulumi",
      name: "pulumi-spot-check",
      tests: [
        {
          name: "BucketExists",
          fn: async (ctx) => {
            const { s3 } = makeClients(ctx);
            const bucket = ctx["_s3BucketName"] as string;
            if (!bucket) throw new Error("_s3BucketName not set by up group");
            const { Buckets = [] } = await s3.send(new ListBucketsCommand({}));
            if (!Buckets.some((b) => b.Name === bucket)) {
              throw new Error(
                `expected bucket ${bucket} in ListBuckets (runId=${ctx.runId})`,
              );
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

---

## Naming conventions

| Element          | Convention                                                           |
| ---------------- | -------------------------------------------------------------------- |
| Group name       | `pulumi-<phase>` (kebab-case), e.g. `pulumi-up`, `pulumi-spot-check` |
| Test name        | Title-case action, e.g. `Up`, `Destroy`, `BucketExists`              |
| Stack name       | `oc-compat-{runId}` — always include `runId` to avoid clashes        |
| Pulumi resources | Named with `runId` prefix wherever the provider supports naming      |
| State dir        | `./state` — created at runtime, never committed, add to `.gitignore` |

---

## Inter-test state

Stash resource names retrieved from Pulumi stack outputs into the context bag
so spot-check tests can use them without hard-coding:

```typescript
// After `pulumi up`, read outputs:
const outputs = JSON.parse(
  execSync("pulumi stack output --json", {...}).toString()
);
ctx["_s3BucketName"] = outputs.bucketName;
```

Keys must start with `_`. Never rely on inter-group state.

---

## Teardown rules (Pulumi-specific)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional Pulumi specifics:

- The `pulumi-up` group's teardown **must** call `pulumi destroy --yes
--non-interactive` even if the `up` tests fail.
- Always pass `--non-interactive` and `--yes` to prevent blocking on stdin.
- Set `PULUMI_CONFIG_PASSPHRASE=""` (empty string, not unset) to avoid the
  passphrase prompt when using the local file backend.
- If `pulumi destroy` leaves orphaned resources (e.g. non-empty S3 buckets),
  add explicit SDK cleanup in teardown via `makeClients(ctx)` before calling
  `pulumi destroy`.
- Never commit the `state/` directory — add it to `.gitignore`.

---

## What agents must NOT do

- Never import from `internal/`, `router/`, or any Overcast server source tree.
- Never hard-code the endpoint — use `OVERCAST_ENDPOINT` env var for the
  Pulumi AWS provider; use `ctx.endpoint` for spot-check SDK calls.
- Never call `process.exit` inside a test or teardown function.
- Never write to stdout inside a lifecycle step — the harness parses stdout as
  NDJSON.
- Never run `pulumi up` without `--yes --non-interactive` — it will block on
  stdin in CI.
- Never leave orphaned Pulumi stacks — `pulumi destroy` must always run in
  teardown.
- Never commit the local file backend state (`state/` directory).

---

## Implementation checklist

When building this suite from scratch:

1. Create `package.json` with `@pulumi/aws`, `@pulumi/pulumi`,
   `@aws-sdk/client-*`, and `tsx` as a dev dependency.
2. Create `tsconfig.json` (mirror `node-js-sdk/tsconfig.json`).
3. Create `Pulumi.yaml` declaring the project name and runtime.
4. Create `src/index.ts` with all planned resources as `@pulumi/aws`
   constructs (see README.md for the full resource list).
5. Copy `lib/harness.ts` and `lib/clients.ts` from `node-js-sdk/src/` and
   adjust as needed.
6. Create `src/groups/lifecycle.ts` with lifecycle phase groups.
7. Create `src/runner.ts` that calls `runSuite()` with all groups, setting
   `PULUMI_BACKEND_URL` before invoking Pulumi CLI.
8. Add `state/` to `.gitignore`.
9. Create `Dockerfile` based on `node:20-alpine`; install the Pulumi CLI
   binary and `npm install`; set `CMD` to run the runner.
10. Register the suite in `compat/runner.go` and `compat/suites/registry.json`.
11. Run `tsc --noEmit` to confirm type correctness.
12. Run the suite locally against a live Overcast instance and verify NDJSON.
