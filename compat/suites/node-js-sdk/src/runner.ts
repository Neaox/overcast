/**
 * runner.ts — Entry point for the node-js-sdk compatibility suite.
 *
 * Reads configuration from environment variables, assembles all test groups,
 * and runs the suite via runSuite() which emits NDJSON to stdout.
 *
 * Environment variables:
 *   OVERCAST_ENDPOINT              Overcast base URL (default: http://localhost:4566)
 *   OVERCAST_DEFAULT_REGION         AWS region (default: us-east-1)
 *   OVERCAST_COMPAT_SKIP_DOCKER    Skip tests requiring Docker (default: false)
 *   OVERCAST_COMPAT_GROUPS         Comma-separated group names to run (default: all)
 *   OVERCAST_COMPAT_SERVICE        AWS service name to run (default: all)
 *   OVERCAST_COMPAT_TESTS          Comma-separated test names to run (default: all)
 *   OVERCAST_COMPAT_TEST_PAIRS     Comma-separated "group:test" pairs to run (overrides above filters)
 *   OVERCAST_COMPAT_RUN_ID         Run ID injected by the root runner (default: auto-generated)
 *   OVERCAST_COMPAT_NO_CLEANUP     Set to "1" to skip post-run resource sweep
 */

import {
  runSuite,
  makeRunId,
  emitEvent,
  Semaphore,
  runGroup,
} from "./lib/harness.js";
import type { TestContext, TestGroup, RunGroupOptions } from "./lib/harness.js";
import { startCommandLoop } from "./lib/commands.js";
import { makeClients } from "./lib/clients.js";
import { sweepAll } from "./lib/cleanup.js";
import {
  loadRegistry,
  buildGroupsFromRegistry,
  validateImpls,
} from "./lib/registry.js";
import type { ImplMap } from "./lib/registry.js";
import { makeS3Groups } from "./groups/s3.js";
import { makeSQSGroups } from "./groups/sqs.js";
import { makeDynamoDBGroups } from "./groups/dynamodb.js";
import { makeSNSGroups } from "./groups/sns.js";
import { makeLambdaGroups } from "./groups/lambda.js";
import { makeCloudWatchLogsGroups } from "./groups/cloudwatch-logs.js";
import { makeSESGroups } from "./groups/ses.js";
import { makeIAMGroups } from "./groups/iam.js";
import { makeSTSGroups } from "./groups/sts.js";
import { makeSecretsManagerGroups } from "./groups/secretsmanager.js";
import { makeKMSGroups } from "./groups/kms.js";
import { makeSSMGroups } from "./groups/ssm.js";
import { makeKinesisGroups } from "./groups/kinesis.js";
import { makeEventBridgeGroups } from "./groups/eventbridge.js";
import { makeCloudFormationGroups } from "./groups/cloudformation.js";
import { makeEC2Groups } from "./groups/ec2.js";
import { makeECSGroups } from "./groups/ecs.js";
import { makeCognitoGroups } from "./groups/cognito.js";
import { makeAppSyncGroups } from "./groups/appsync.js";
import { makeAPIGatewayGroups } from "./groups/apigateway.js";
import { makeCloudFrontGroups } from "./groups/cloudfront.js";
import { makeRDSGroups } from "./groups/rds.js";
import { makeElastiCacheGroups } from "./groups/elasticache.js";
import { makeStepFunctionsGroups } from "./groups/stepfunctions.js";
import { makeWAFGroups } from "./groups/waf.js";
import { makeShieldGroups } from "./groups/shield.js";

const SUITE = "node-js-sdk";

const endpoint = process.env.OVERCAST_ENDPOINT ?? "http://localhost:4566";
const region = process.env.OVERCAST_DEFAULT_REGION ?? "us-east-1";
const runId = process.env.OVERCAST_COMPAT_RUN_ID ?? makeRunId();

// Optional filters — all passed as comma-separated env vars.
const filterGroups = process.env.OVERCAST_COMPAT_GROUPS?.split(",").map((s) =>
  s.trim(),
);
// Filter by AWS service name (e.g. "s3"). Groups whose service does not match
// are excluded entirely.
const filterService = process.env.OVERCAST_COMPAT_SERVICE?.trim() || undefined;
// Filter individual test names within groups (e.g. "PutObject,GetObject").
const filterTests = process.env.OVERCAST_COMPAT_TESTS?.split(",").map((s) =>
  s.trim(),
);
// Explicit "group:test" pairs — overrides all other filters when set.
// Used by the dashboard "re-run non-passing" button.
const filterTestPairs: Set<string> | undefined = process.env
  .OVERCAST_COMPAT_TEST_PAIRS
  ? new Set(
      process.env.OVERCAST_COMPAT_TEST_PAIRS.split(",").map((s) => s.trim()),
    )
  : undefined;

// ── Build registry-driven group list ─────────────────────────────────────
// Extract implementations from the existing group files into a flat map so
// that buildGroupsFromRegistry can:
//   (a) run every test that has an impl, and
//   (b) auto-emit "skip" for any test that is in the registry but not yet
//       implemented here — keeping the dashboard matrix consistent as new
//       tests are added to the registry.

const skipDocker = process.env.OVERCAST_COMPAT_SKIP_DOCKER === "1";

const existingGroups: TestGroup[] = [
  ...makeS3Groups(SUITE),
  ...makeSQSGroups(SUITE),
  ...makeDynamoDBGroups(SUITE),
  ...makeSNSGroups(SUITE),
  ...makeLambdaGroups(SUITE),
  ...makeCloudWatchLogsGroups(SUITE),
  ...makeSESGroups(SUITE),
  ...makeIAMGroups(SUITE),
  ...makeSTSGroups(SUITE),
  ...makeSecretsManagerGroups(SUITE),
  ...makeKMSGroups(SUITE),
  ...makeSSMGroups(SUITE),
  ...makeKinesisGroups(SUITE),
  ...makeEventBridgeGroups(SUITE),
  ...makeCloudFormationGroups(SUITE),
  ...makeEC2Groups(SUITE),
  ...makeECSGroups(SUITE),
  ...makeCognitoGroups(SUITE),
  ...makeAppSyncGroups(SUITE),
  ...makeAPIGatewayGroups(SUITE),
  ...makeCloudFrontGroups(SUITE),
  ...makeRDSGroups(SUITE),
  ...makeElastiCacheGroups(SUITE),
  ...makeStepFunctionsGroups(SUITE),
  ...makeWAFGroups(SUITE),
  ...makeShieldGroups(SUITE),
];

// Extract impls map (name → fn) from existing groups, skipping already-skipped tests.
const impls: ImplMap = {};
const setup: Record<string, (ctx: TestContext) => Promise<void>> = {};
const teardown: Record<string, (ctx: TestContext) => Promise<void>> = {};
for (const g of existingGroups) {
  if (g.setup) setup[g.name] = g.setup;
  if (g.teardown) teardown[g.name] = g.teardown;
  for (const t of g.tests) {
    // Register under the group-qualified key "groupName:testName" to avoid
    // collisions between groups that share test names (e.g. lambda-crud and
    // appsync-functions both have CreateFunction/GetFunction/etc.).
    if (!t.skip) impls[`${g.name}:${t.name}`] = t.fn;
  }
}

const registry = loadRegistry();
validateImpls(registry, impls, SUITE);

const allGroups = buildGroupsFromRegistry(registry, impls, {
  suite: SUITE,
  capabilities: skipDocker ? [] : ["docker"],
  setup,
  teardown,
});

const isInteractive = process.env.OVERCAST_COMPAT_INTERACTIVE === "1";

if (isInteractive) {
  // ── Interactive mode ─────────────────────────────────────────────────────
  // Long-lived process: emit ready, then accept commands via stdin (NDJSON).

  emitEvent({
    event: "building",
    suite: SUITE,
    message: "Loading registry and building test groups...",
  });

  const totalTests = allGroups.reduce((sum, g) => sum + g.tests.length, 0);
  emitEvent({ event: "ready", suite: SUITE, total_tests: totalTests });

  const cancellationRegistry = new Map<string, AbortController>();

  // Handle SIGINT/SIGTERM gracefully so Ctrl+C cleans up resources.
  let shuttingDown = false;
  const gracefulExit = () => {
    if (shuttingDown) return;
    shuttingDown = true;
    process.stderr.write(`[compat:${SUITE}] received signal — shutting down\n`);
    for (const ac of cancellationRegistry.values()) {
      ac.abort();
    }
    process.exit(0);
  };
  process.on("SIGINT", gracefulExit);
  process.on("SIGTERM", gracefulExit);

  const log = (msg: string): void => {
    process.stderr.write(`[compat:${SUITE}] ${msg}\n`);
  };

  const slots = Math.max(
    1,
    parseInt(process.env["OVERCAST_COMPAT_PARALLEL_SLOTS"] ?? "8", 10) || 8,
  );
  const sem = new Semaphore(slots);

  const closeCommandLoop = startCommandLoop({
    onRun: async (cmd) => {
      const batchStart = Date.now();
      const groupsToRun: TestGroup[] = [];

      // Empty or absent tests means "run all groups".
      if (!cmd.tests || cmd.tests.length === 0) {
        groupsToRun.push(...allGroups);
      }

      for (const req of cmd.tests ?? []) {
        const group = allGroups.find((g) => g.name === req.group);
        if (!group) {
          log(`unknown group in run command: ${req.group}`);
          continue;
        }
        if (req.tests && req.tests.length > 0) {
          const requested = new Set(req.tests);
          groupsToRun.push({
            ...group,
            tests: group.tests.filter((t) => requested.has(t.name)),
          });
        } else {
          groupsToRun.push(group);
        }
      }

      const baseCtx: TestContext = {
        endpoint,
        region,
        runId,
        log,
      };

      const opts: RunGroupOptions = {
        abortControllers: cancellationRegistry,
        batchId: cmd.batch_id,
      };

      const results = await Promise.all(
        groupsToRun.map((group) =>
          sem.run(() => runGroup(group, { ...baseCtx }, opts)),
        ),
      );

      let totalPassed = 0;
      let totalFailed = 0;
      let totalSkipped = 0;
      let totalUnimplemented = 0;
      let totalCancelled = 0;
      for (const r of results) {
        totalPassed += r.passed;
        totalFailed += r.failed;
        totalSkipped += r.skipped;
        totalUnimplemented += r.unimplemented;
        totalCancelled += r.cancelled;
      }

      emitEvent({
        event: "batch_complete",
        suite: SUITE,
        batch_id: cmd.batch_id,
        passed: totalPassed,
        failed: totalFailed,
        skipped: totalSkipped,
        unimplemented: totalUnimplemented,
        cancelled: totalCancelled,
        duration_ms: Date.now() - batchStart,
      });
    },

    onCancel: (cmd) => {
      if (cmd.group && cmd.test) {
        // Cancel a specific test
        const key = `${cmd.group}:${cmd.test}`;
        const ac = cancellationRegistry.get(key);
        if (ac) ac.abort();
      } else {
        // Cancel all in-flight tests
        for (const ac of cancellationRegistry.values()) {
          ac.abort();
        }
      }
    },

    onShutdown: async () => {
      // Cancel all in-flight work
      for (const ac of cancellationRegistry.values()) {
        ac.abort();
      }
      closeCommandLoop();
      process.exit(0);
    },

    onPing: () => {
      // Report the currently executing test (if any) so the orchestrator
      // knows what is running when diagnosing a stall.
      let runningTest = "";
      for (const [key, ac] of cancellationRegistry) {
        if (!ac.signal.aborted) {
          runningTest = key;
          break;
        }
      }
      emitEvent({
        event: "pong",
        suite: SUITE,
        running_test: runningTest,
      });
    },
  });
} else {
  // ── Batch mode (legacy) ────────────────────────────────────────────────

  let groups = filterGroups
    ? allGroups.filter((g) => filterGroups.includes(g.name))
    : allGroups;

  if (filterService) {
    groups = groups.filter((g) => g.service === filterService);
  }

  if (filterTests && filterTests.length > 0) {
    groups = groups
      .map((g) => ({
        ...g,
        tests: g.tests.filter((t) => filterTests.includes(t.name)),
      }))
      .filter((g) => g.tests.length > 0);
  }

  // OVERCAST_COMPAT_TEST_PAIRS overrides all other filters.
  // Only groups/tests listed as "group:test" are included.
  if (filterTestPairs && filterTestPairs.size > 0) {
    groups = allGroups
      .map((g) => ({
        ...g,
        tests: g.tests.filter((t) =>
          filterTestPairs.has(`${g.name}:${t.name}`),
        ),
      }))
      .filter((g) => g.tests.length > 0);
  }

  await runSuite(SUITE, groups, { endpoint, region, runId });

  // Post-suite sweep: delete resources created by *this* run.  We scope to the
  // current runId so that parallel suites don't accidentally delete each other's
  // resources.  Per-group teardown handles the happy path; this catches crashes
  // and interrupted runs.  Suppress with OVERCAST_COMPAT_NO_CLEANUP=1.
  const clients = makeClients({ endpoint, region });
  const sweepLog = (msg: string): void => {
    process.stderr.write(`[compat:${SUITE}] ${msg}\n`);
  };
  await sweepAll(clients, sweepLog, runId);
}
