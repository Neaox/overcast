/**
 * failure.ts — the one failure message every assertion uses.
 *
 * Debuggability is the interpreter's whole cost: a generated test has no
 * source to read, so its failure message has to say everything a hand-written
 * test's source would. compat/model/README.md § Failure messages fixes the
 * six fields and their order, and `failureMessage()` is the only place they
 * are assembled.
 *
 *   1. group/test
 *   2. the operation
 *   3. the exact params JSON sent, after evaluating every expression
 *   4. the assertion kind and, for checks/where, the path
 *   5. expected vs actual
 *   6. the scenario file and the step index
 */

import { describeClipped } from "./expressions.ts";

/** Fields 1 and 6: where in the scenario the failure happened. */
export interface FailureSite {
  group: string;
  test: string;
  /** Repo-relative, e.g. "compat/model/scenarios/sqs.json". */
  scenarioFile: string;
  /** "call", "assert[2]", "assert[1].assert", "setup[0]", "teardown[1]". */
  step: string;
}

/** Fields 2 to 5: what was attempted and what came back. */
export interface FailureDetail {
  /** The operation of the call this clause made — not always the test's. */
  op: string;
  /** The evaluated params, exactly as sent. */
  params: unknown;
  /** The assertion kind, or "call" / "export" for a step-level failure. */
  kind: string;
  /** The checks/where path, when the clause has one. */
  path?: string;
  expected: string;
  actual: string;
}

/** Assemble the six fields, in order. */
export function failureMessage(
  site: FailureSite,
  detail: FailureDetail,
): string {
  const where = detail.path === undefined ? "" : ` at ${detail.path}`;
  return (
    `${site.group}/${site.test}: ${detail.op} ` +
    `params=${describeClipped(detail.params)} — ` +
    `${detail.kind}${where}: ` +
    `expected ${detail.expected}, actual ${detail.actual} ` +
    `(${site.scenarioFile} ${site.step})`
  );
}

/**
 * Build the Error a failing step or clause throws.
 *
 * `cause` is the SDK error that provoked the failure, when there was one. It
 * is kept for two reasons: `Error.cause` makes the original stack reachable
 * when debugging, and the four "unimplemented" surfaces the harness sniffs
 * (`$metadata.httpStatusCode`, `$response.statusCode`, `__type`, `Code`, and
 * the `UnknownOperationException` / `NotImplemented` names) are copied onto
 * the thrown error so `isUnimplemented()` still fires. Without that copy a
 * probe of a Tier 0 operation would record `fail` instead of `unimplemented`
 * and the whole point of the `organizations` half of the pilot would be lost.
 */
export function scenarioFailure(message: string, cause?: unknown): Error {
  const err = new Error(message, cause === undefined ? undefined : { cause });
  err.name = "ScenarioFailure";
  if (cause !== undefined) copyUnimplementedMarkers(err, cause);
  return err;
}

const UNIMPLEMENTED_NAMES = new Set([
  "UnknownOperationException",
  "NotImplemented",
]);

/**
 * Copy the fields harness.ts `isUnimplemented()` reads from `cause` onto
 * `target`, so a wrapped 501 (or an unregistered action) is still recognised.
 */
function copyUnimplementedMarkers(target: Error, cause: unknown): void {
  if (cause === null || typeof cause !== "object") return;
  const src = cause as Record<string, unknown>;
  const dst = target as unknown as Record<string, unknown>;
  for (const key of ["$metadata", "$response", "__type", "Code"]) {
    if (src[key] !== undefined) dst[key] = src[key];
  }
  const name = src["name"];
  if (typeof name === "string" && UNIMPLEMENTED_NAMES.has(name)) {
    target.name = name;
  }
}

/** `<name>: <message>` for an SDK error, or the value itself if it is not one. */
export function describeError(err: unknown): string {
  if (err instanceof Error) return `${err.name}: ${err.message}`;
  return String(err);
}
