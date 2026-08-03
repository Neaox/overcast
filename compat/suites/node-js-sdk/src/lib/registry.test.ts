/**
 * Unit tests for the registry loader's impl-key resolution rules.
 *
 * These are not compat tests — they need no emulator. They pin the two rules
 * that stop a run from reporting a result for a test that never executed:
 *
 * - a key that resolves to nothing aborts, instead of warning;
 * - a bare key for a name several groups declare is refused, instead of
 *   binding whichever group's implementation happened to be registered last.
 *
 * Run with: npm run test:unit
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  ambiguousTestNames,
  buildGroupsFromRegistry,
  testNameOwners,
  validateImpls,
  type ImplMap,
  type Registry,
} from "./registry.js";
import type { TestGroup } from "./harness.js";

const noop = async () => {};

/**
 * Two unrelated groups declaring a test of the same name, plus a name owned by
 * exactly one group — the shape that made a mis-binding possible.
 */
const twoGroupsOneName = (): Registry => ({
  version: 1,
  groups: [
    {
      service: "iam",
      name: "iam-users",
      tests: [{ name: "ListUsers" }, { name: "CreateUser" }],
    },
    {
      service: "cognito",
      name: "cognito-userpools",
      tests: [{ name: "ListUsers" }],
    },
  ],
});

function find(groups: TestGroup[], group: string, test: string) {
  const found = groups
    .find((g) => g.name === group)
    ?.tests.find((t) => t.name === test);
  assert.ok(found, `no test ${group}/${test} in built groups`);
  return found;
}

const build = (impls: ImplMap) =>
  buildGroupsFromRegistry(twoGroupsOneName(), impls, { suite: "node-js-sdk" });

describe("unresolvable impl keys abort", () => {
  it("rejects a key written with the old slash separator", () => {
    assert.throws(
      () =>
        validateImpls(
          twoGroupsOneName(),
          { "iam-users/CreateUser": noop },
          "node-js-sdk",
        ),
      (err: Error) =>
        err.message.includes("iam-users/CreateUser") &&
        err.message.includes("matches no registry entry") &&
        // The message must point at the colon form, since that is the fix.
        err.message.includes("iam-users:CreateUser"),
    );
  });

  it("rejects a key naming an unknown group", () => {
    assert.throws(
      () =>
        validateImpls(
          twoGroupsOneName(),
          { "iam-usres:CreateUser": noop },
          "node-js-sdk",
        ),
      /iam-usres:CreateUser/,
    );
  });

  it("rejects a key naming an unknown test", () => {
    assert.throws(
      () => validateImpls(twoGroupsOneName(), { CreateUsr: noop }, "node-js-sdk"),
      /CreateUsr/,
    );
  });
});

describe("ambiguous bare keys are refused", () => {
  it("rejects a bare key for a name several groups declare", () => {
    assert.throws(
      () => validateImpls(twoGroupsOneName(), { ListUsers: noop }, "node-js-sdk"),
      (err: Error) =>
        err.message.includes("ambiguous") &&
        err.message.includes("iam-users") &&
        err.message.includes("cognito-userpools"),
    );
  });

  it("accepts resolvable keys", () => {
    validateImpls(
      twoGroupsOneName(),
      {
        CreateUser: noop, // bare, single owner
        "iam-users:ListUsers": noop,
        "cognito-userpools:ListUsers": noop,
      },
      "node-js-sdk",
    );
  });
});

describe("buildGroupsFromRegistry resolution", () => {
  it("refuses the cross-group bare fallback", () => {
    const groups = build({ ListUsers: noop });
    assert.equal(
      find(groups, "cognito-userpools", "ListUsers").skip,
      "not yet implemented in node-js-sdk test suite",
    );
    assert.ok(
      find(groups, "iam-users", "ListUsers").skip,
      "iam-users/ListUsers bound to an ambiguous bare impl",
    );
  });

  it("binds a qualified key to its own group only", () => {
    const groups = build({ "iam-users:ListUsers": noop });
    assert.equal(find(groups, "iam-users", "ListUsers").skip, undefined);
    assert.ok(
      find(groups, "cognito-userpools", "ListUsers").skip,
      "cognito-userpools/ListUsers bound to iam-users' impl",
    );
  });

  it("still allows the bare fallback for an unambiguous name", () => {
    const groups = build({ CreateUser: noop });
    assert.equal(find(groups, "iam-users", "CreateUser").skip, undefined);
  });
});

describe("the suite's real registrations", () => {
  /**
   * They must resolve against the real registry.json. This is the check that
   * catches a mis-binding before a run reports one — in `npm run test:unit`
   * rather than in results that silently describe the wrong test.
   */
  it("resolve against registry.json", async () => {
    const { makeAllGroups, makeImplMap } = await import("../groups/index.js");
    const { loadRegistry } = await import("./registry.js");

    const impls = makeImplMap(makeAllGroups("node-js-sdk"));
    assert.ok(Object.keys(impls).length > 0, "no impls collected");
    validateImpls(loadRegistry(), impls, "node-js-sdk");
  });
});

describe("owner tracking", () => {
  it("reports which groups claim each name", () => {
    const registry = twoGroupsOneName();
    assert.ok(ambiguousTestNames(registry).has("ListUsers"));
    assert.ok(!ambiguousTestNames(registry).has("CreateUser"));
    assert.deepEqual(testNameOwners(registry).get("ListUsers"), [
      "cognito-userpools",
      "iam-users",
    ]);
  });
});
