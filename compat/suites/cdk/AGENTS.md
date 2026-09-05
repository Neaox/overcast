# AGENTS.md — cdk suite

> Conventions for AI agents and contributors working in `compat/suites/cdk/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules and the separation boundary that apply to every
> suite. This file covers only cdk-specific details.
>
> For quick-start, prerequisites, env vars and architecture see
> [README.md](README.md).

---

## What this suite tests

End-to-end CDK v2 deployment compatibility through the native `cdk` CLI. It is
the CDK column of the compatibility matrix.

Unlike the SDK suites, it does not test individual API operations. It tests
whether a real CDK app can bootstrap, synthesise, deploy a multi-resource
stack (including a nested one), have every resource verified through the AWS
SDK, be updated in place, and be destroyed — all pointed at Overcast. A
failure here is usually a CloudFormation provisioning gap rather than a
missing operation.

---

## Status

**Implemented.** One group, `cdk-lifecycle`, running in the compat CI matrix
(`.github/workflows/compat.yml`), where the job also runs `npm run typecheck`
before the suite.

---

## Runtime

| Item        | Value                                                                                                     |
| ----------- | ----------------------------------------------------------------------------------------------------------- |
| Language    | TypeScript, run directly by Node 22.18+/23.6+ (built-in type stripping; no build step, no loader)          |
| CDK app     | `aws-cdk-lib` v2 + `constructs`, pinned in `package.json`                                                  |
| CDK CLI     | `aws-cdk`, a **local devDependency** — resolved with `createRequire(...).resolve("aws-cdk/bin/cdk")` and spawned with `process.execPath` |
| Spot checks | `@aws-sdk/client-*` v3, one per verified service                                                           |
| CI image    | None of its own — GitHub Actions installs Node from `.node-version` and runs `npm ci` here; the compose path uses `.devcontainer/Dockerfile`, which already carries Node |

> SDK upgrade policy: [compat/AGENTS.md § SDK version pinning](../../AGENTS.md#sdk-version-pinning--upgrade-strategy).

**Never spawn the CDK CLI as `npx cdk` or `cdk`.** On Windows both are `.cmd`
shims that `spawn` cannot find under a bare name and, since the
CVE-2024-27980 fix, refuses to run without `shell: true`. Both failures land
on `Bootstrap`, before a single test does anything. `runCdk` in
`src/groups/lifecycle.ts` resolves the CLI's own entry point and runs it with
the Node already running the suite — see
[compat/AGENTS.md § Cross-platform rules](../../AGENTS.md#cross-platform-rules-for-suite-authors).

---

## File layout

```
compat/suites/cdk/
  AGENTS.md         ← you are here
  README.md         ← quick-start, prerequisites, env vars, architecture
  package.json      ← aws-cdk-lib, constructs, @aws-sdk/client-*; aws-cdk as a devDependency
  cdk.json          ← app entry point: `node src/app.ts`
  run.js            ← entry point: Node version check, then imports src/runner.ts
  tsconfig.json     ← NodeNext ESM, strict, type-stripping-compatible

  src/
    app.ts          ← the CDK app: CompatStage wrapping CdkCompatStack
    stack.ts        ← CdkCompatStack: every deployed resource and every CfnOutput
    runner.ts       ← applies the env filters, then runs once or serves the
                      interactive command loop
    lib/
      harness.ts    ← TestContext, TestGroup, runGroup(), runSuite(), makeRunId()
      clients.ts    ← makeClients(endpoint, region) for the verification calls
      exec.ts       ← execCmd(): spawn wrapper; throws ExecError with stdout+stderr
      commands.ts   ← stdin NDJSON command loop for interactive mode
    groups/
      lifecycle.ts  ← the cdk-lifecycle group and the CDK CLI wrapper
```

**One file for the stack.** Never split `CdkCompatStack` across several
`stack.*.ts` files.

---

## Group anatomy

The suite has **one group**, `cdk-lifecycle`, whose tests are ordered by
`depends` rather than split across groups. Every phase shares one deployed
stack, and a group is the unit of setup, teardown and context state — so a
second group would mean either a second deploy or state that has to survive
outside a context bag.

```typescript
export function makeLifecycleGroups(suite: string): TestGroup[] {
  return [
    {
      suite,
      service: "cdk",
      name: "cdk-lifecycle",
      tests: [
        {
          name: "Bootstrap",
          fn: async (ctx) =>
            runCdk(ctx, ["bootstrap", `aws://000000000000/${ctx.region}`]),
        },
        { name: "Synth", depends: ["Bootstrap"], fn: async (ctx) => runCdk(ctx, ["synth", cdkStackSelector(ctx)]) },
        // … Deploy, then one verification per resource, then Update, Destroy
        { name: "VerifyBucket", depends: ["VerifyStackStatus"], fn: verifyBucket },
      ],
      teardown: async (ctx) => {
        try {
          await destroyStack(ctx);
        } catch {
          // best-effort cleanup only
        }
      },
    },
  ];
}
```

A verification test reads the stack's outputs and checks the resource through
the AWS SDK — never by parsing CDK's own output:

```typescript
async function verifyBucket(ctx: TestContext): Promise<void> {
  const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
    (await fetchOutputs(ctx))) as Record<string, string>;
  const bucketName = outputs["BucketName"];
  assert.ok(bucketName, "missing BucketName output");

  const { s3 } = makeClients(ctx.endpoint, ctx.region);
  const resp = await s3.send(new ListBucketsCommand({}));
  assert.ok(
    resp.Buckets?.some((b) => b.Name === bucketName),
    `bucket ${bucketName} missing from ListBuckets`,
  );
}
```

`fetchOutputs` is the fallback path, so a single verification test can be run
on its own (`OVERCAST_COMPAT_TESTS=VerifyBucket`) against an already-deployed
stack without `VerifyStackStatus` having populated the cache first.

**A resource the tests find by name needs a `CfnOutput` in `stack.ts`.** Do
not reconstruct a CDK-generated physical name in a test; read it from an
output.

---

## Key types

```typescript
// lib/harness.ts
export interface TestContext {
  endpoint: string;
  region: string;
  runId: string;
  stackName: string;       // OcCompat-{runId}
  log(msg: string): void;  // stderr only — stdout is NDJSON
  signal?: AbortSignal;    // aborted by the interactive protocol's cancel
  [key: string]: unknown;  // inter-test state bag
}

export interface TestCase {
  name: string;
  fn: TestFn;
  skip?: boolean | string;
  depends?: string[];      // same-group tests that must pass first
}

export interface TestGroup {
  suite: string; service: string; name: string;
  tests: TestCase[];
  setup?: (ctx: TestContext) => Promise<void>;
  teardown?: (ctx: TestContext) => Promise<void>;
}
```

`execCmd(command, args, { cwd, env })` (`lib/exec.ts`) is the only way this
suite runs a subprocess. On a non-zero exit it throws `ExecError` carrying the
exit code, stdout and stderr, which is what makes a failed `cdk deploy`
readable in the result rather than "exit status 1".

---

## Naming conventions

| Element         | Convention                                                                          |
| --------------- | ------------------------------------------------------------------------------------- |
| Group name      | `cdk-lifecycle` — one group; do not add a second without a reason for a second deploy |
| Test name       | PascalCase phase or check, e.g. `Bootstrap`, `Deploy`, `VerifyBucket`                 |
| Stack name      | `OcCompat-{runId}`, selected as `CompatStage-{runId}/Stack`                          |
| Construct id    | `Compat<Thing>`, e.g. `CompatBucket`, `CompatQueue`, `CompatEsm`                     |
| Output key      | PascalCase noun the test reads, e.g. `BucketName`, `QueueArn`, `FunctionName`         |
| Context key     | `_`-prefixed camelCase for cached state, e.g. `_outputs`, `_queueUrl`                |

The `runId` goes in the stack name, which is what keeps concurrent runs and
the post-run orphan sweep from colliding. CDK derives every physical resource
name from the stack, so individual constructs do not need it themselves.

---

## Inter-test state

The context bag caches what a later phase needs:

```typescript
// in VerifyStackStatus:
(ctx as Record<string, unknown>)["_outputs"] = outputs;

// in a later verification:
const outputs = ((ctx as Record<string, unknown>)["_outputs"] ??
  (await fetchOutputs(ctx))) as Record<string, string>;
```

Always pair a cache read with a fallback that fetches the value, so a single
test stays runnable on its own. Never rely on inter-group state.

---

## Teardown rules (cdk-specific)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional CDK specifics:

- The group's teardown calls `cdk destroy --force` inside `try/catch`, and
  runs even when the tests failed partway. `Destroy` is also a test, so after
  a clean run the teardown's second attempt finds nothing left; whatever it
  reports is swallowed.
- Never deploy without `--require-approval never` — omitting it blocks on
  stdin.
- Never hard-code an AWS account ID in a test. The app reads
  `OVERCAST_ACCOUNT_ID`, defaulting to `000000000000`.
- `cdk bootstrap` and `cdk synth` create nothing that needs cleaning up.

---

## Error messages

Use `node:assert/strict` and give every assertion a message naming what was
expected and what was found:

```typescript
assert.ok(bucketName, "missing BucketName output");
assert.strictEqual(
  resp.Timeout,
  15,
  `expected updated timeout 15, got ${String(resp.Timeout)}`,
);
```

A bare `assert.ok(x)` reports "expected value to be truthy" and nothing about
which resource — which is the one thing the reader needs.

---

## Adding a lifecycle test

1. Add the test to the `cdk-lifecycle` group in
   [compat/suites/registry.json](../registry.json), with its `depends`. The
   group is scoped `"suites": ["cdk"]`, so no other suite is affected.
2. Add the matching entry to `makeLifecycleGroups()` in
   `src/groups/lifecycle.ts` — the same name, the same `depends`.
3. If it needs a new resource, add it to `src/stack.ts`, plus a `CfnOutput`
   for anything the test looks up by name.
4. Run `npm run typecheck`.
5. Run the suite against a live instance and check the NDJSON output.

---

## What agents must NOT do

- Never import from `internal/`, `router/`, or any Overcast server source tree
  — see [compat/AGENTS.md § Separation boundary](../../AGENTS.md#separation-boundary--non-negotiable).
- Never spawn the CDK CLI as `npx cdk`, `cdk`, or through `shell: true` —
  resolve its entry point and run it with `process.execPath`.
- Never use `execSync` — the runner needs the loop free while a deploy runs.
- Never hard-code the endpoint — the CDK CLI gets it through `cdkEnv`, and
  spot checks through `makeClients(ctx.endpoint, ctx.region)`.
- Never call `process.exit` inside a test or teardown.
- Never write to stdout inside a test — the runner parses stdout as NDJSON;
  use `ctx.log()` (stderr) for diagnostics.
- Never add a test to `lifecycle.ts` without adding it to `registry.json`.
  This suite builds its groups from the code rather than from the registry, so
  an unregistered test still runs and still reports — but the registry is what
  the compat server and the dashboard read for the expected matrix, and the
  test is missing from it.
- Never leave a deployed stack behind: any new group needs a teardown that
  destroys it.
