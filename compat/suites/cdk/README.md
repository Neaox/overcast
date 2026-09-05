# cdk suite

Deploys a real **AWS CDK v2** app through the native `cdk` CLI, pointed at
Overcast, and verifies the resources it created (TypeScript, run directly by
Node — no build step).

> **Status: implemented.** See [AGENTS.md](AGENTS.md) for code conventions.

Unlike the SDK suites, this one does not test individual API operations. It
tests whether a CloudFormation-shaped deployment works end to end: bootstrap,
synth, deploy, spot-check every resource, update, destroy.

---

## What it covers

The app is a CDK `Stage` wrapping a single stack, so the whole
CloudFormation lifecycle runs against Overcast.

### Resources deployed

| Service         | Resources                                                    |
| --------------- | ------------------------------------------------------------ |
| S3              | Bucket                                                       |
| SQS             | Queue, DLQ, QueuePolicy, a queue inside a nested stack       |
| SNS             | Topic + SQS subscription                                     |
| DynamoDB        | Table with GSI + stream                                      |
| IAM             | Role (Lambda), Role (Step Functions), ManagedPolicy          |
| Lambda          | Function + 3 EventSourceMappings (SQS, DynamoDB stream, filtered DynamoDB stream) |
| CloudWatch Logs | LogGroup                                                     |
| KMS             | Key + alias                                                  |
| Secrets Manager | Secret                                                       |
| SSM             | StringParameter                                              |
| EC2/VPC         | VPC (1 AZ, public subnet), SecurityGroup                     |
| API Gateway     | REST API + mock method on the root resource                  |
| Step Functions  | StateMachine (single Pass state)                             |
| EventBridge     | EventBus + Rule targeting the SQS queue                      |
| CloudFormation  | A nested stack                                               |

[src/stack.ts](src/stack.ts) is the authoritative list.

### Lifecycle phases

Every test lives in one `cdk-lifecycle` group and declares what it depends on,
so the phases run in order and a failed deploy skips the checks that needed it
rather than reporting a cascade of unrelated failures.

| Phase             | What runs                                                                     |
| ----------------- | ------------------------------------------------------------------------------- |
| Bootstrap         | `cdk bootstrap`                                                               |
| Synth             | `cdk synth`                                                                   |
| Deploy            | `cdk deploy --require-approval never`                                         |
| Verify (create)   | Stack status, then one check per deployed resource                            |
| Verify (delivery) | Writes a DynamoDB item and waits for the stream to invoke the function, including through a filtered event-source mapping |
| Update            | Changes the Lambda timeout, redeploys, checks `UPDATE_COMPLETE` and the new value |
| Destroy           | `cdk destroy --force`, then checks the stack is gone                          |

The group's teardown destroys the stack again on a best-effort basis, so a run
that failed partway still cleans up.

---

## Prerequisites

- Node.js 22.18+ or 23.6+ — the suite has no build step; Node runs its
  TypeScript sources directly using built-in type stripping, which earlier
  releases do not have. `node run.js` says so plainly if yours is too old.
- `npm ci` in this directory. The CDK CLI is a **local devDependency**
  (`aws-cdk` in `package.json`), resolved and spawned from `node_modules` — a
  global `npm install -g aws-cdk` is neither needed nor used.
- An Overcast instance that can run Lambda containers — the delivery checks
  wait for a real invocation. The suite declares no `requires: [docker]` in
  the registry and has no skip switch for them, so against an instance with no
  Docker socket they fail rather than skip.
- Overcast running somewhere reachable — see
  [compat/AGENTS.md § Running a session](../../AGENTS.md#running-a-session--ports-are-chosen-never-assumed)
  for why `4566`/`4567` are off-limits for a test instance you start yourself.

---

## Running the suite

### Locally (Node.js required)

```bash
cd compat/suites/cdk
npm ci
npm run typecheck        # tsc --noEmit: "Node can run this"

# Start Overcast first (separate terminal), e.g.:
#   go run ./cmd/overcast serve

OVERCAST_ENDPOINT=http://localhost:4566 npm test
```

PowerShell:

```powershell
cd compat/suites/cdk
npm ci
npm run typecheck

$env:OVERCAST_ENDPOINT = "http://localhost:4566"
npm test
```

`npm test` is `node run.js`, which checks the Node version and then imports
`src/runner.ts`.

### Via Docker (no local toolchain required)

This suite ships no image of its own. It runs as a subprocess of the compat
runner container, which already carries Node.js. From the repo root:

```bash
OVERCAST_COMPAT_SUITE=cdk docker compose -f compat/docker-compose.yml run --rm compat
```

Arguments after the compose service name reach the container entrypoint rather
than the runner, which is why the suite selection is an environment variable —
see [compat/AGENTS.md § Running suites](../../AGENTS.md#running-suites-docker--ci).

### Via the Go CLI (recommended — runs all suites, or just this one)

```bash
# Starts its own Overcast instance on a free port and stops it afterwards:
go run ./cmd/compat
# or just this suite:
go run ./cmd/compat --suite cdk
# or against an instance you are already running:
go run ./cmd/compat --endpoint http://localhost:4566 --suite cdk
```

This is what CI runs. `cmd/compat` spawns `node run.js` in this directory —
see `defaultSuites` in [compat/runner.go](../../runner.go).

### On Windows, name a host whose subdomains resolve

`cdk deploy` publishes the stack's assets through the CDK's own S3 client,
which addresses the staging bucket **virtual-hosted** —
`cdk-hnb659fds-assets-000000000000-us-east-1.<host>`. Linux and macOS resolve
any `*.localhost` name to loopback, so the default host works there and in CI.
Windows resolves only `localhost` itself, and the run stops at `Deploy` with
`getaddrinfo ENOTFOUND cdk-hnb659fds-assets-000000000000-us-east-1.localhost`
while every other suite passes.

`*.localhost.overcast.sh` resolves to `127.0.0.1` on every OS, so name that
instead and the whole group passes on a Windows host:

```bash
go run ./cmd/compat --suite cdk --overcast-host localhost.overcast.sh
```

It needs a public DNS lookup, so it does not work offline; the user-facing
[CDK troubleshooting page](../../../docs/cdk/troubleshooting.md) covers the
same problem outside compat, and the alternatives.

---

## Environment variables

| Variable                      | Default                  | Description                                                                        |
| ----------------------------- | ------------------------ | ------------------------------------------------------------------------------------ |
| `OVERCAST_ENDPOINT`           | `http://localhost:4566`  | Overcast base URL; also passed to the CDK CLI as `AWS_ENDPOINT_URL`                |
| `OVERCAST_DEFAULT_REGION`     | `us-east-1`              | AWS region advertised to the CDK CLI and the spot-check clients                    |
| `OVERCAST_COMPAT_RUN_ID`      | `oc-<8 hex>`             | Names the stack (`OcCompat-{runId}`, selected as `CompatStage-{runId}/Stack`) and prefixes resource names |
| `OVERCAST_ACCOUNT_ID`         | `000000000000`           | Account the stage synthesises for                                                  |
| `OVERCAST_COMPAT_GROUPS`      | unset (all)              | Comma-separated group names to run                                                 |
| `OVERCAST_COMPAT_SERVICE`     | unset (all)              | Single service name to run                                                         |
| `OVERCAST_COMPAT_TESTS`       | unset (all)              | Comma-separated test names to run                                                  |
| `OVERCAST_COMPAT_TEST_PAIRS`  | unset                    | Comma-separated `group:test` pairs — overrides the three filters above             |
| `OVERCAST_COMPAT_INTERACTIVE` | unset                    | Set to `1` to serve the interactive command protocol instead of one batch run      |
| `AWS_ACCESS_KEY_ID`           | `test`                   | Placeholder credential passed to the CDK CLI; the emulator does not validate it    |
| `AWS_SECRET_ACCESS_KEY`       | `test`                   | As above                                                                           |
| `CDK_COMPAT_LAMBDA_TIMEOUT`   | `10`                     | Lambda timeout the app synthesises; the update phase re-synthesises with `15`      |

---

## Architecture

```
cdk/
  package.json      ← aws-cdk-lib, constructs, @aws-sdk/client-* ; aws-cdk as a devDependency
  cdk.json          ← app entry point: `node src/app.ts`
  run.js            ← entry point: Node version check, then imports src/runner.ts
  tsconfig.json     ← NodeNext ESM, strict, type-stripping-compatible
  README.md         ← you are here

  src/
    app.ts          ← the CDK app: a CompatStage wrapping CdkCompatStack
    stack.ts        ← CdkCompatStack — every deployed resource, and the outputs
                      the verification tests read
    runner.ts       ← entry point: applies the env filters, then runs the suite
                      once or serves the interactive command loop
    lib/
      harness.ts    ← TestContext, TestGroup, runGroup(), runSuite(), makeRunId(),
                      emitEvent()
      clients.ts    ← makeClients(endpoint, region) → the AWS SDK clients the
                      verification tests use
      exec.ts       ← execCmd(): spawn wrapper returning stdout/stderr, throwing
                      ExecError with both on a non-zero exit
      commands.ts   ← stdin NDJSON command loop for interactive mode
    groups/
      lifecycle.ts  ← the cdk-lifecycle group: every phase, and the CDK CLI wrapper
```

The test list appears twice: this suite builds its group from
`src/groups/lifecycle.ts` rather than from the registry, while the shared
[compat/suites/registry.json](../registry.json) declares the same
`cdk-lifecycle` group scoped `"suites": ["cdk"]` — which is what the compat
server and the dashboard read for the expected matrix. A new phase goes in
both.

### Key types (`lib/harness.ts`)

| Type / function | Purpose                                                                                                  |
| --------------- | ---------------------------------------------------------------------------------------------------------- |
| `TestContext`   | `endpoint`, `region`, `runId`, `stackName`, `log()`, an `AbortSignal`, plus a bag for inter-test state    |
| `TestGroup`     | `{ suite, service, name, tests[], setup?, teardown? }`                                                   |
| `TestCase`      | `{ name, fn, skip?, depends? }` — throw to fail, return to pass; `depends` names tests that must pass first |
| `runSuite()`    | Runs the groups and emits NDJSON to stdout                                                                |
| `makeRunId()`   | Returns `"oc-{8 hex}"`, unique per invocation                                                            |

---

## Adding a lifecycle test

1. Add the test to the `cdk-lifecycle` group in
   [compat/suites/registry.json](../registry.json), with its `depends`.
2. Add the matching entry to `makeLifecycleGroups()` in
   `src/groups/lifecycle.ts`, with the same name and the same `depends`.
3. If it needs a new resource, add it to `src/stack.ts` — and an output there
   if the test has to find it by name.
4. Run `npm run typecheck`, then the suite against a live instance.

See [AGENTS.md](AGENTS.md) for the conventions and the prohibitions.
