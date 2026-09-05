/**
 * Unit tests for the closed assertion set's predicates.
 *
 * `isList` and `nonEmpty` get the most attention on purpose: `isList` is the
 * check every `List*` probe carries — getting it wrong fails 16 of the 25
 * `organizations` probes at once — and `nonEmpty` is the one every other
 * probe carries.
 *
 * Run with: npm run test:unit
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  acceptedCodes,
  errorCodes,
  errorMatches,
  evaluateChecks,
  evaluateListAbsent,
  evaluateListContains,
} from "./assertions.ts";
import type { EvalContext } from "./expressions.ts";

const ctx: EvalContext = {
  runId: "oc-12345678",
  group: "sqs-gen-queue",
  bag: new Map<string, unknown>([["queue.url", "http://q/1"]]),
};

describe("checks", () => {
  it("nonEmpty rejects null, empty string, list and object", () => {
    for (const value of [null, "", [], {}]) {
      assert.notEqual(
        evaluateChecks({ F: value }, { "$.F": { nonEmpty: true } }, ctx),
        null,
      );
    }
  });

  it("nonEmpty never fails on a number or a boolean", () => {
    assert.equal(evaluateChecks({ F: 0 }, { "$.F": { nonEmpty: true } }, ctx), null);
    assert.equal(
      evaluateChecks({ F: false }, { "$.F": { nonEmpty: true } }, ctx),
      null,
    );
  });

  it("nonEmpty on a missing path reports <missing>", () => {
    const mismatch = evaluateChecks({}, { "$.F": { nonEmpty: true } }, ctx);
    assert.equal(mismatch?.actual, "<missing>");
    assert.equal(mismatch?.path, "$.F");
  });

  it("isList accepts a list, an empty list and an omitted member", () => {
    assert.equal(evaluateChecks({ L: ["a"] }, { "$.L": { isList: true } }, ctx), null);
    assert.equal(evaluateChecks({ L: [] }, { "$.L": { isList: true } }, ctx), null);
    assert.equal(evaluateChecks({}, { "$.L": { isList: true } }, ctx), null);
  });

  it("isList rejects a present value that is not a list", () => {
    assert.notEqual(
      evaluateChecks({ L: "nope" }, { "$.L": { isList: true } }, ctx),
      null,
    );
    assert.notEqual(evaluateChecks({ L: {} }, { "$.L": { isList: true } }, ctx), null);
  });

  it("equals compares as JSON, including against a $ref", () => {
    assert.equal(
      evaluateChecks({ U: "http://q/1" }, { "$.U": { equals: { $ref: "queue.url" } } }, ctx),
      null,
    );
    assert.notEqual(
      evaluateChecks({ U: "http://q/2" }, { "$.U": { equals: { $ref: "queue.url" } } }, ctx),
      null,
    );
    assert.notEqual(evaluateChecks({ V: 30 }, { "$.V": { equals: "30" } }, ctx), null);
  });

  it("matches applies the regex unanchored unless the pattern anchors", () => {
    const pattern = "^p-[0-9a-zA-Z_]{8,128}$";
    assert.equal(
      evaluateChecks({ Id: "p-abcdefgh" }, { "$.Id": { matches: pattern } }, ctx),
      null,
    );
    assert.notEqual(
      evaluateChecks({ Id: "p-abc" }, { "$.Id": { matches: pattern } }, ctx),
      null,
    );
    assert.notEqual(
      evaluateChecks({ Id: 42 }, { "$.Id": { matches: pattern } }, ctx),
      null,
    );
  });

  it("missing holds when any segment is absent and fails when it resolves", () => {
    assert.equal(
      evaluateChecks({ Tags: {} }, { "$.Tags.compat": { missing: true } }, ctx),
      null,
    );
    assert.equal(evaluateChecks({}, { "$.Tags.compat": { missing: true } }, ctx), null);
    assert.notEqual(
      evaluateChecks(
        { Tags: { compat: "scenario" } },
        { "$.Tags.compat": { missing: true } },
        ctx,
      ),
      null,
    );
  });

  it("reports the first failing check, in declaration order", () => {
    const mismatch = evaluateChecks(
      { A: "a", B: "" },
      { "$.A": { equals: "a" }, "$.B": { nonEmpty: true } },
      ctx,
    );
    assert.equal(mismatch?.path, "$.B");
  });
});

describe("listContains / absent", () => {
  const response = {
    Policies: [{ Id: "p-1" }, { Id: "p-2" }],
    QueueUrls: ["http://q/1", "http://q/9"],
    Tags: [{ Key: "compat", Value: "scenario" }],
  };

  it("matches an item on every where entry", () => {
    assert.equal(
      evaluateListContains(response, "$.Policies", { "$.Id": "p-2" }, ctx),
      null,
    );
    assert.notEqual(
      evaluateListContains(response, "$.Policies", { "$.Id": "p-3" }, ctx),
      null,
    );
    assert.equal(
      evaluateListContains(
        response,
        "$.Tags",
        { "$.Key": "compat", "$.Value": "scenario" },
        ctx,
      ),
      null,
    );
    assert.notEqual(
      evaluateListContains(
        response,
        "$.Tags",
        { "$.Key": "compat", "$.Value": "other" },
        ctx,
      ),
      null,
    );
  });

  it("`$` is the item itself, for a list of strings", () => {
    assert.equal(
      evaluateListContains(response, "$.QueueUrls", { $: { $ref: "queue.url" } }, ctx),
      null,
    );
  });

  it("a missing list fails listContains and satisfies absent", () => {
    assert.notEqual(evaluateListContains({}, "$.QueueUrls", { $: "x" }, ctx), null);
    assert.equal(evaluateListAbsent({}, "$.QueueUrls", { $: "x" }, ctx), null);
  });

  it("absent fails when an item matches, and names the item", () => {
    assert.equal(evaluateListAbsent(response, "$.Tags", { "$.Key": "other" }, ctx), null);
    const mismatch = evaluateListAbsent(response, "$.Tags", { "$.Key": "compat" }, ctx);
    assert.ok(mismatch?.actual.includes("compat"));
  });
});

describe("error matching", () => {
  const spec = {
    shape: "QueueDoesNotExist",
    code: "AWS.SimpleQueueService.NonExistentQueue",
  };

  it("accepts the shape name or the wire code, from any surface", () => {
    assert.ok(errorMatches({ name: "QueueDoesNotExist" }, spec));
    assert.ok(errorMatches({ name: "AWS.SimpleQueueService.NonExistentQueue" }, spec));
    assert.ok(errorMatches({ __type: "com.amazonaws.sqs#QueueDoesNotExist" }, spec));
    assert.ok(errorMatches({ Code: "AWS.SimpleQueueService.NonExistentQueue" }, spec));
    assert.ok(
      errorMatches(
        {
          name: "ServiceException",
          $response: {
            headers: { "x-amzn-query-error": "AWS.SimpleQueueService.NonExistentQueue;Sender" },
          },
        },
        spec,
      ),
    );
  });

  it("rejects another error and a non-error", () => {
    assert.ok(!errorMatches({ name: "AccessDenied" }, spec));
    assert.ok(!errorMatches(undefined, spec));
    assert.deepEqual(errorCodes("not an object"), []);
  });

  it("names both spellings only when they differ", () => {
    assert.equal(
      acceptedCodes(spec),
      "QueueDoesNotExist or AWS.SimpleQueueService.NonExistentQueue",
    );
    assert.equal(
      acceptedCodes({ shape: "PolicyNotFoundException", code: "PolicyNotFoundException" }),
      "PolicyNotFoundException",
    );
  });
});
