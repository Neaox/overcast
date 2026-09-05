/**
 * Unit tests for runGroup()'s setup/test/teardown phasing.
 *
 * These are not compat tests — they need no emulator. They pin the rule the
 * IR states in compat/model/README.md § The scenario file, step 2: a failed
 * setup reports every test as `skip` and then still runs teardown. A group
 * whose setup created resources in an earlier step and then failed in a
 * later one has already left something behind, and teardown is the only
 * thing that will ever clean it up — skipping it leaks the resource.
 *
 * Run with: npm run test:unit
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { runGroup, type TestContext, type TestGroup } from "./harness.ts";

function makeCtx(): TestContext {
  return {
    endpoint: "",
    region: "us-east-1",
    runId: "test",
    log: () => {},
  } as TestContext;
}

/** Run one group, capturing the NDJSON events it writes to stdout. */
async function runGroupCapturingEvents(
  group: TestGroup,
): Promise<Record<string, unknown>[]> {
  const chunks: string[] = [];
  const realWrite = process.stdout.write.bind(process.stdout);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (process.stdout as any).write = (chunk: string) => {
    chunks.push(chunk);
    return true;
  };
  try {
    await runGroup(group, makeCtx());
  } finally {
    process.stdout.write = realWrite;
  }
  return chunks
    .join("")
    .split("\n")
    .filter((l) => l.trim().length > 0)
    .map((l) => JSON.parse(l));
}

describe("runGroup teardown after setup failure", () => {
  it("runs teardown even when setup throws", async () => {
    let teardownCalled = false;
    let testRan = false;

    const group: TestGroup = {
      suite: "node-js-sdk",
      service: "sqs",
      name: "sqs-gen-queue",
      setup: async () => {
        throw new Error("boom");
      },
      tests: [
        { name: "GetQueueAttributes", fn: async () => { testRan = true; } },
        { name: "SetQueueAttributes", fn: async () => { testRan = true; } },
      ],
      teardown: async () => {
        teardownCalled = true;
      },
    };

    const events = await runGroupCapturingEvents(group);

    assert.equal(teardownCalled, true, "teardown must run after a failed setup");
    assert.equal(testRan, false, "no test function should run after a failed setup");

    const results = events.filter((e) => e["event"] === "test_result");
    assert.equal(results.length, 2);
    for (const r of results) {
      assert.equal(r["status"], "skip");
      assert.match(String(r["error"]), /^setup failed: .*boom/);
    }
  });

  it("reports the counts skip totals from a failed setup", async () => {
    const group: TestGroup = {
      suite: "node-js-sdk",
      service: "sqs",
      name: "sqs-gen-queue",
      setup: async () => {
        throw new Error("boom");
      },
      tests: [
        { name: "GetQueueAttributes", fn: async () => {} },
        { name: "SetQueueAttributes", fn: async () => {} },
      ],
      teardown: async () => {},
    };

    const counts = await runGroup(group, makeCtx());
    assert.deepEqual(counts, {
      passed: 0,
      failed: 0,
      skipped: 2,
      unimplemented: 0,
      cancelled: 0,
    });
  });

  it("still propagates a teardown error to ctx.log rather than throwing", async () => {
    const logs: string[] = [];
    const ctx: TestContext = {
      endpoint: "",
      region: "us-east-1",
      runId: "test",
      log: (msg: string) => logs.push(msg),
    } as TestContext;

    const group: TestGroup = {
      suite: "node-js-sdk",
      service: "sqs",
      name: "sqs-gen-queue",
      setup: async () => {
        throw new Error("setup boom");
      },
      tests: [{ name: "GetQueueAttributes", fn: async () => {} }],
      teardown: async () => {
        throw new Error("teardown boom");
      },
    };

    // Must not throw — teardown errors are logged, not propagated.
    await runGroup(group, ctx);

    assert.ok(
      logs.some((l) => l.includes("teardown error") && l.includes("teardown boom")),
      `expected a teardown error log, got: ${JSON.stringify(logs)}`,
    );
  });
});

describe("runGroup teardown after a normal group", () => {
  it("runs teardown after tests pass", async () => {
    let teardownCalled = false;
    let setupCalled = false;

    const group: TestGroup = {
      suite: "node-js-sdk",
      service: "sqs",
      name: "sqs-gen-queue",
      setup: async () => {
        setupCalled = true;
      },
      tests: [{ name: "GetQueueAttributes", fn: async () => {} }],
      teardown: async () => {
        teardownCalled = true;
      },
    };

    const counts = await runGroup(group, makeCtx());

    assert.equal(setupCalled, true);
    assert.equal(teardownCalled, true, "teardown must run after a normal group");
    assert.deepEqual(counts, {
      passed: 1,
      failed: 0,
      skipped: 0,
      unimplemented: 0,
      cancelled: 0,
    });
  });

  it("runs teardown even when a test fails", async () => {
    let teardownCalled = false;

    const group: TestGroup = {
      suite: "node-js-sdk",
      service: "sqs",
      name: "sqs-gen-queue",
      tests: [
        {
          name: "GetQueueAttributes",
          fn: async () => {
            throw new Error("test boom");
          },
        },
      ],
      teardown: async () => {
        teardownCalled = true;
      },
    };

    const counts = await runGroup(group, makeCtx());

    assert.equal(teardownCalled, true, "teardown must run after a failed test");
    assert.equal(counts.failed, 1);
  });
});
