import { makeLifecycleGroups } from "./groups/lifecycle.js";
import { makeRunId, runSuite, emitEvent, runGroup } from "./lib/harness.js";
import type { TestGroup, TestContext, RunGroupOptions } from "./lib/harness.js";
import { startCommandLoop } from "./lib/commands.js";

const SUITE = "cdk";

const endpoint = process.env["OVERCAST_ENDPOINT"] ?? "http://localhost:4566";
const region = process.env["OVERCAST_DEFAULT_REGION"] ?? "us-east-1";
const runId = process.env["OVERCAST_COMPAT_RUN_ID"] ?? makeRunId();
const stackName = `OcCompat-${runId}`;

const filterGroups = process.env["OVERCAST_COMPAT_GROUPS"]
  ?.split(",")
  .map((s) => s.trim())
  .filter(Boolean);
const filterService =
  process.env["OVERCAST_COMPAT_SERVICE"]?.trim() || undefined;
const filterTests = process.env["OVERCAST_COMPAT_TESTS"]
  ?.split(",")
  .map((s) => s.trim())
  .filter(Boolean);
const filterTestPairs = process.env["OVERCAST_COMPAT_TEST_PAIRS"]
  ? new Set(
      process.env["OVERCAST_COMPAT_TEST_PAIRS"]
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean),
    )
  : undefined;

const allGroups: TestGroup[] = [...makeLifecycleGroups(SUITE)];

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

if (filterTestPairs && filterTestPairs.size > 0) {
  groups = allGroups
    .map((g) => ({
      ...g,
      tests: g.tests.filter((t) => filterTestPairs.has(`${g.name}:${t.name}`)),
    }))
    .filter((g) => g.tests.length > 0);
}

const isInteractive = process.env.OVERCAST_COMPAT_INTERACTIVE === "1";

if (isInteractive) {
  // ── Interactive mode ─────────────────────────────────────────────────────
  // Long-lived process: emit ready, then accept commands via stdin (NDJSON).

  emitEvent({
    event: "building",
    suite: SUITE,
    message: "Loading CDK test groups...",
  });

  const totalTests = allGroups.reduce((sum, g) => sum + g.tests.length, 0);
  emitEvent({ event: "ready", suite: SUITE, total_tests: totalTests });

  const cancellationRegistry = new Map<string, AbortController>();

  const log = (msg: string): void => {
    process.stderr.write(`[compat:${SUITE}] ${msg}\n`);
  };

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
        stackName,
        log,
      };

      const opts: RunGroupOptions = {
        abortControllers: cancellationRegistry,
        batchId: cmd.batch_id,
      };

      // CDK runs groups sequentially (CDK deploy is inherently serial).
      let totalPassed = 0;
      let totalFailed = 0;
      let totalSkipped = 0;
      let totalUnimplemented = 0;
      let totalCancelled = 0;
      for (const group of groupsToRun) {
        const r = await runGroup(group, { ...baseCtx }, opts);
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
        const key = `${cmd.group}:${cmd.test}`;
        const ac = cancellationRegistry.get(key);
        if (ac) ac.abort();
      } else {
        for (const ac of cancellationRegistry.values()) {
          ac.abort();
        }
      }
    },

    onShutdown: async () => {
      for (const ac of cancellationRegistry.values()) {
        ac.abort();
      }
      closeCommandLoop();
      process.exit(0);
    },

    onPing: () => {
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
  // ── Batch mode ───────────────────────────────────────────────────────────

  await runSuite(SUITE, groups, {
    endpoint,
    region,
    runId,
    stackName,
  });
}
