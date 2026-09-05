/**
 * The shared error-matching conformance fixtures,
 * compat/model/testdata/errors.
 *
 * Three interpreters read the same documents and must agree about which
 * clauses they satisfy. Each suite writes this test once, against its own
 * matcher, so a rule only one backend implements fails somewhere rather than
 * being discovered when a generated group disagrees with itself across suites
 * (compat/model/README.md § Errors).
 *
 * A fixture whose surfaces this suite cannot see is skipped by name and with a
 * reason: a silently ignored fixture would look exactly like a passing one.
 *
 * Run with: npm run test:unit
 */
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { describe, it } from "node:test";
import { fileURLToPath } from "node:url";

import { errorCodes, errorMatches } from "./assertions.ts";
import type { ErrorSpec } from "./ir.ts";

/**
 * The fixture directory, resolved from this module's own location the way
 * loader.ts resolves a scenario path — never from the working directory,
 * which differs between `npm test` and `cmd/compat`. This file sits at
 * compat/suites/node-js-sdk/src/lib/scenario/, five directories below
 * compat/.
 */
const FIXTURE_DIR_URL = new URL(
  "../../../../../model/testdata/errors/",
  import.meta.url,
);
const FIXTURE_DIR = fileURLToPath(FIXTURE_DIR_URL);

/** The whole carrier vocabulary. A fixture naming anything else is a typo. */
const KNOWN_CARRIERS = new Set([
  "exceptionName",
  "bodyType",
  "bodyCode",
  "queryErrorHeader",
  "errorTypeHeader",
  "cliBanner",
]);

/**
 * What the AWS SDK for JavaScript puts in front of this suite: the error class
 * it minted, the deserialized body members, and `$response.headers`.
 * `x-amzn-errortype` is on the wire but the SDK has already folded it into the
 * error by then, so it is not a surface of its own here; the AWS CLI's stderr
 * banner belongs to another suite.
 */
const OBSERVED_CARRIERS = new Set([
  "exceptionName",
  "bodyType",
  "bodyCode",
  "queryErrorHeader",
]);

const WHAT_THIS_SUITE_SEES =
  "the SDK hands the interpreter an error object and $response.headers, " +
  "never a process's stderr";

interface FixtureWire {
  status?: number;
  exceptionName?: string;
  headers?: Record<string, string>;
  body?: Record<string, unknown>;
  stderr?: string;
}

interface FixtureCase {
  name: string;
  error: ErrorSpec;
  matches: boolean;
  via?: string;
}

interface Fixture {
  id: string;
  title: string;
  why: string;
  carriers: string[];
  wire: FixtureWire;
  expect: FixtureCase[];
}

function loadFixtures(): Fixture[] {
  return readdirSync(FIXTURE_DIR)
    .filter((name) => name.endsWith(".json"))
    .sort()
    .map(
      (name) =>
        JSON.parse(
          readFileSync(new URL(name, FIXTURE_DIR_URL), "utf8"),
        ) as Fixture,
    );
}

/**
 * The fixture as this suite would have observed it: an Error carrying the
 * class name the SDK minted, the deserialized body members it lifts onto the
 * error, and the raw response the SDK attaches as `$response`.
 */
function asSdkError(wire: FixtureWire): Error {
  const body = wire.body ?? {};
  const err = new Error(String(body["message"] ?? "")) as Error &
    Record<string, unknown>;
  if (wire.exceptionName !== undefined) err.name = wire.exceptionName;
  for (const key of ["__type", "Code", "code"]) {
    if (body[key] !== undefined) err[key] = body[key];
  }
  err["$response"] = {
    statusCode: wire.status ?? 400,
    headers: { ...(wire.headers ?? {}) },
  };
  return err;
}

const fixtures = loadFixtures();

describe("the shared error fixtures", () => {
  it("has fixtures to run", () => {
    assert.ok(
      fixtures.length > 0,
      `no fixtures in ${FIXTURE_DIR}: the shared conformance set may not be ` +
        "skipped by deleting it",
    );
  });

  it("names only carriers the vocabulary declares", () => {
    for (const fixture of fixtures) {
      for (const carrier of fixture.carriers) {
        assert.ok(
          KNOWN_CARRIERS.has(carrier),
          `${fixture.id}: unknown carrier ${JSON.stringify(carrier)}; the ` +
            "vocabulary is fixed by compat/model/README.md § Errors",
        );
      }
    }
  });

  for (const fixture of fixtures) {
    const observesFixture = fixture.carriers.some((c) =>
      OBSERVED_CARRIERS.has(c),
    );
    describe(fixture.id, () => {
      for (const testCase of fixture.expect) {
        const skip = !observesFixture
          ? `this suite reads none of the fixture's surfaces (${fixture.carriers.join(", ")}): ${WHAT_THIS_SUITE_SEES}`
          : testCase.matches &&
              (testCase.via === undefined ||
                !OBSERVED_CARRIERS.has(testCase.via))
            ? `this expectation matches through ${JSON.stringify(testCase.via)}, which this suite does not observe: ${WHAT_THIS_SUITE_SEES}`
            : undefined;
        it(testCase.name, { skip }, () => {
          const observed = asSdkError(fixture.wire);
          assert.equal(
            errorMatches(observed, testCase.error),
            testCase.matches,
            `${fixture.id}: the error reports ${JSON.stringify(errorCodes(observed))}, ` +
              `the clause names ${JSON.stringify(testCase.error)}`,
          );
        });
      }
    });
  }
});
