/**
 * Unit tests for the registry loader's impl-key resolution rules.
 *
 * These are not compat tests — they need no emulator. They pin the rules that
 * stop a run from reporting a result for a test that never executed:
 *
 * - a key that resolves to nothing aborts, instead of warning;
 * - a bare key for a name several groups declare is refused, instead of
 *   binding whichever group's implementation happened to be registered last;
 * - a key two group files both register is refused, instead of discarding one
 *   of the two implementations silently.
 *
 * Run with: npm run test:unit
 */
import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  ambiguousTestNames,
  buildGroupsFromRegistry,
  mergeImpls,
  testNameOwners,
  validateImpls,
  type ImplMap,
  type ImplSource,
  type Registry,
} from "./registry.ts";
import type { TestGroup } from "./harness.ts";

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

describe("duplicate impl registrations abort", () => {
  /**
   * This is the gap validateImpls cannot close. The merge that builds the
   * suite's impl map is last-writer-wins, so one of the two implementations is
   * discarded before validation ever sees the map — and the surviving key
   * resolves perfectly well, so nothing is reported. The discarded test then
   * runs the other file's implementation under its own name.
   */
  const merge = (sources: ImplSource[]) => mergeImpls(sources, "node-js-sdk");

  it("rejects a key two sources both register", () => {
    assert.throws(
      () =>
        merge([
          { name: "lambda/lambda-crud", impls: { "lambda-crud:CreateFunction": noop } },
          { name: "appsync/lambda-crud", impls: { "lambda-crud:CreateFunction": noop } },
        ]),
      (err: Error) => {
        assert.match(err.message, /duplicate impl registration/);
        assert.match(err.message, /lambda-crud:CreateFunction/);
        // Both registering files must be named: the key alone does not say
        // where to look, and one of the two files is in the wrong.
        assert.match(err.message, /lambda\/lambda-crud/);
        assert.match(err.message, /appsync\/lambda-crud/);
        return true;
      },
    );
  });

  it("reads a single source's duplicate as such", () => {
    // "both X and Y" would be nonsense when X and Y are the same file.
    assert.throws(
      () =>
        merge([
          { name: "iam/iam-users", impls: { "iam-users:CreateUser": noop } },
          { name: "iam/iam-users", impls: { "iam-users:CreateUser": noop } },
        ]),
      /registered twice by "iam\/iam-users"/,
    );
  });

  it("reports every duplicate, not just the first", () => {
    // Fixing one duplicate must not merely reveal the next.
    assert.throws(
      () =>
        merge([
          {
            name: "iam/iam-users",
            impls: { "iam-users:ListUsers": noop, "iam-users:CreateUser": noop },
          },
          {
            name: "cognito/iam-users",
            impls: { "iam-users:ListUsers": noop, "iam-users:CreateUser": noop },
          },
        ]),
      (err: Error) => {
        assert.match(err.message, /2 duplicate impl registration\(s\)/);
        // Sorted by key, so the message is stable run to run.
        assert.ok(
          err.message.indexOf("iam-users:CreateUser") <
            err.message.indexOf("iam-users:ListUsers"),
          `problems not sorted by key:\n${err.message}`,
        );
        return true;
      },
    );
  });

  it("merges disjoint sources, each key keeping its own impl", () => {
    // Negative control.
    const iamList = async () => {};
    const iamCreate = async () => {};
    const cognitoList = async () => {};
    const merged = merge([
      {
        name: "iam/iam-users",
        impls: { "iam-users:ListUsers": iamList, CreateUser: iamCreate },
      },
      {
        name: "cognito/cognito-userpools",
        impls: { "cognito-userpools:ListUsers": cognitoList },
      },
    ]);
    assert.deepEqual(merged, {
      "iam-users:ListUsers": iamList,
      CreateUser: iamCreate,
      "cognito-userpools:ListUsers": cognitoList,
    });
  });
});

describe("the suite's real registrations", () => {
  /**
   * They must resolve against the real registry.json, and no two group files
   * may claim the same key. These are the checks that catch a mis-binding
   * before a run reports one — in `npm run test:unit` rather than in results
   * that silently describe the wrong test.
   *
   * makeImplMap merges through mergeImpls, so building it at all is the
   * duplicate check.
   */
  it("merge without duplicate keys and resolve against registry.json", async () => {
    const { makeAllGroups, makeImplMap } = await import("../groups/index.ts");
    const { loadRegistry } = await import("./registry.ts");

    const impls = makeImplMap(makeAllGroups("node-js-sdk"), "node-js-sdk");
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
