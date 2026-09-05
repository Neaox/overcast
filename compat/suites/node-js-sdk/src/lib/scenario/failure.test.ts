/**
 * Unit tests for the one failure message every assertion uses
 * (compat/model/README.md § Failure messages).
 *
 * Run with: npm run test:unit
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { MAX_DESCRIBE } from "./expressions.ts";
import { failureMessage } from "./failure.ts";
import type { FailureDetail, FailureSite } from "./failure.ts";

const site: FailureSite = {
  group: "sqs-gen-queue",
  test: "SetQueueAttributes",
  scenarioFile: "compat/model/scenarios/sqs.json",
  step: "assert[2]",
};

describe("failureMessage", () => {
  it("assembles the six fields in order", () => {
    const detail: FailureDetail = {
      op: "GetQueueAttributes",
      params: { QueueUrl: "http://q/1" },
      kind: "readback",
      path: "$.Attributes.VisibilityTimeout",
      expected: '"60"',
      actual: '"30"',
    };
    assert.equal(
      failureMessage(site, detail),
      'sqs-gen-queue/SetQueueAttributes: GetQueueAttributes ' +
        'params={"QueueUrl":"http://q/1"} — ' +
        'readback at $.Attributes.VisibilityTimeout: ' +
        'expected "60", actual "30" ' +
        '(compat/model/scenarios/sqs.json assert[2])',
    );
  });

  it("clips params past MAX_DESCRIBE, the same as actual", () => {
    const big = { Entries: Array.from({ length: 500 }, (_, i) => `entry-${i}`) };
    const detail: FailureDetail = {
      op: "SomeBatchOp",
      params: big,
      kind: "responseField",
      expected: "a non-empty value",
      actual: "<missing>",
    };
    const message = failureMessage(site, detail);
    // The rendered params segment itself must be clipped — not merely short
    // relative to the whole message.
    const paramsSegment = message.slice(
      message.indexOf("params=") + "params=".length,
      message.indexOf(" — "),
    );
    assert.ok(paramsSegment.endsWith("… (elided)"));
    assert.equal(paramsSegment.length, MAX_DESCRIBE + "… (elided)".length);
  });
});
