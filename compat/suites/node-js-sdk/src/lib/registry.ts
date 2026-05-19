/**
 * registry.ts — types and loader for the shared cross-suite test registry.
 *
 * The registry (../../registry.json) is the single source of truth for which
 * test groups and test cases exist across ALL compat suites (Node.js, Python,
 * Go, Java, .NET, Rust, CLI, etc.).  Every suite runner should:
 *
 *   1. Load the registry (loadRegistry()).
 *   2. Register its own implementations by name.
 *   3. Call buildGroupsFromRegistry() to get a TestGroup[] where every
 *      registered test runs and every un-registered test emits "skip"
 *      automatically — keeping the dashboard matrix consistent.
 *
 * When you add a new test to the registry you get:
 *   - All suites show it as "skip" immediately (not silently absent).
 *   - The dashboard comparison view lines up correctly across suites.
 *   - You only implement it once per language.
 */

import { createRequire } from "node:module";
import type { TestCase, TestFn, TestGroup } from "./harness.js";

// ─── Registry types ───────────────────────────────────────────────────────

export interface RegistryTestCase {
  /** PascalCase test name — must match the TestCase name used by runners. */
  name: string;
  /**
   * AWS API operation name when it differs from test name.
   * null means no documentation link (internal setup step).
   */
  op?: string | null;
  /**
   * Runtime capabilities required to run this test.
   * Suite runners skip the test if the capability is absent.
   */
  requires?: Array<"docker" | "smtp" | "network">;
  /**
   * If present, always emit as "skip" with this reason.
   * Only for tests that can never run in a standard compat environment.
   */
  skip?: string;
  /**
   * Names of other tests in the SAME group that must run (and pass) before
   * this test.  Runners topologically sort by dependency order and auto-skip
   * dependents when a dependency fails.
   */
  depends?: string[];
}

export interface RegistryGroup {
  service: string;
  /** Group name, e.g. "s3-crud". */
  name: string;
  /** Mark slow groups so they are scheduled first (longest-job-first). */
  slow?: boolean;
  tests: RegistryTestCase[];
}

export interface Registry {
  version: 1;
  groups: RegistryGroup[];
}

// ─── Loader ───────────────────────────────────────────────────────────────

/** Load registry.json from the canonical location (compat/suites/registry.json). */
export function loadRegistry(): Registry {
  const require = createRequire(import.meta.url);
  // Three levels up from node-js-sdk/src/lib/ → suites/
  return require("../../../registry.json") as Registry;
}

// ─── Builder ─────────────────────────────────────────────────────────────

export type ImplMap = Record<string, TestFn>;

export interface BuildOptions {
  suite: string;
  /** Which capability flags this runner supports. Default: []. */
  capabilities?: Array<"docker" | "smtp" | "network">;
  /** Optional setup functions keyed by group name. */
  setup?: Record<
    string,
    (ctx: import("./harness.js").TestContext) => Promise<void>
  >;
  /** Optional teardown functions keyed by group name. */
  teardown?: Record<
    string,
    (ctx: import("./harness.js").TestContext) => Promise<void>
  >;
}

/**
 * Build a TestGroup[] from the registry, filling missing impls with auto-skip.
 *
 * @param registry  Loaded from loadRegistry().
 * @param impls     Map of test name → async test function.
 * @param opts      Suite name, capabilities, per-group setup/teardown.
 *
 * @example
 * ```ts
 * const groups = buildGroupsFromRegistry(loadRegistry(), {
 *   CreateBucket: async (ctx) => { ... },
 *   PutObject:    async (ctx) => { ... },
 * }, { suite: "python-sdk" });
 * ```
 */
// eslint-disable-next-line @typescript-eslint/no-empty-function
const noop = async () => {};

/**
 * Topologically sort tests within a group using their `depends` edges.
 * Tests with no dependencies come first; tests whose deps are all
 * resolved come next.  Falls back to the registry declaration order
 * for tests at the same dependency depth.
 */
function topoSort(tests: RegistryTestCase[]): RegistryTestCase[] {
  const byName = new Map(tests.map((t) => [t.name, t]));
  const sorted: RegistryTestCase[] = [];
  const visited = new Set<string>();
  const visiting = new Set<string>(); // cycle detection

  function visit(t: RegistryTestCase): void {
    if (visited.has(t.name)) return;
    if (visiting.has(t.name)) return; // cycle — break it
    visiting.add(t.name);
    for (const dep of t.depends ?? []) {
      const depTest = byName.get(dep);
      if (depTest) visit(depTest);
    }
    visiting.delete(t.name);
    visited.add(t.name);
    sorted.push(t);
  }

  for (const t of tests) visit(t);
  return sorted;
}

export function buildGroupsFromRegistry(
  registry: Registry,
  impls: ImplMap,
  opts: BuildOptions,
): TestGroup[] {
  const caps = new Set(opts.capabilities ?? []);

  const groups = registry.groups
    .filter((rg) => rg.service !== "cdk") // CDK lifecycle tests belong to the cdk suite
    .map((rg) => {
    // Topologically sort tests by their declared dependencies so that
    // prerequisites always execute before the tests that need them.
    const sortedTests = topoSort(rg.tests);

    const tests: TestCase[] = sortedTests.map((rt): TestCase => {
      // Resolve op: registry null means suppress doc link (false in harness);
      // registry string overrides the test name; registry undefined means use test name.
      const op: string | false | undefined =
        rt.op === null ? false : (rt.op ?? undefined);

      const depends = rt.depends;

      // Static registry-level skip (annotated in the JSON).
      if (rt.skip) {
        return { name: rt.name, fn: noop, op, skip: rt.skip, depends };
      }

      // Capability gate — skip if the runner can't satisfy the requirement.
      if (rt.requires && rt.requires.some((c) => !caps.has(c))) {
        const missing = rt.requires.filter((c) => !caps.has(c));
        return {
          name: rt.name,
          fn: noop,
          op,
          skip: `requires ${missing.join(", ")} (not available in this environment)`,
          depends,
        };
      }

      // Look up by group-qualified key first ("groupName:testName"), then fall
      // back to the bare test name for impls that are not group-qualified.
      const qualifiedKey = `${rg.name}:${rt.name}`;
      const hasImpl = qualifiedKey in impls || rt.name in impls;
      if (!hasImpl) {
        // No implementation yet — surface as skip so the dashboard shows it.
        return {
          name: rt.name,
          fn: noop,
          op,
          skip: `not yet implemented in ${opts.suite} test suite`,
          depends,
        };
      }

      const fn = impls[qualifiedKey] ?? impls[rt.name];
      if (fn == null) {
        // Explicitly registered as null/undefined → SDK does not expose this.
        return {
          name: rt.name,
          fn: noop,
          op,
          na: `not yet supported by the AWS JavaScript SDK v3`,
          depends,
        };
      }

      return { name: rt.name, fn, op, depends };
    });

    const group: TestGroup = {
      suite: opts.suite,
      service: rg.service,
      name: rg.name,
      tests,
    };
    if (opts.setup?.[rg.name]) group.setup = opts.setup[rg.name];
    if (opts.teardown?.[rg.name]) group.teardown = opts.teardown[rg.name];
    return group;
  });

  // Longest-job-first: schedule slow groups before fast ones so they start
  // early and finish in parallel with the many quick groups instead of
  // becoming a long tail at the end of the run.
  const slowGroups = new Set(
    registry.groups.filter((g) => g.slow).map((g) => g.name),
  );
  groups.sort((a, b) => {
    const as = slowGroups.has(a.name) ? 1 : 0;
    const bs = slowGroups.has(b.name) ? 1 : 0;
    return bs - as; // slow first
  });

  return groups;
}

// ─── Validation ───────────────────────────────────────────────────────────

/**
 * Check that every key in `impls` matches at least one test in the registry.
 * Prints warnings for orphaned implementations (typos, renamed tests).
 * Safe to call in development; no-ops in CI unless OVERCAST_COMPAT_STRICT=1.
 */
export function validateImpls(
  registry: Registry,
  impls: ImplMap,
  suite: string,
): void {
  const registryNames = new Set(
    registry.groups.flatMap((g) => [
      ...g.tests.map((t) => t.name),
      ...g.tests.map((t) => `${g.name}:${t.name}`),
    ]),
  );
  const orphans = Object.keys(impls).filter((name) => !registryNames.has(name));
  if (orphans.length === 0) return;

  const strict = process.env.OVERCAST_COMPAT_STRICT === "1";
  const warn = (msg: string) => process.stderr.write(`[${suite}] ${msg}\n`);

  for (const name of orphans) {
    warn(
      `WARNING: impl "${name}" has no matching entry in registry.json. ` +
        `Check for a typo or add it to the registry.`,
    );
  }

  if (strict) {
    throw new Error(
      `[${suite}] ${orphans.length} orphaned impl(s) found. Fix or set OVERCAST_COMPAT_STRICT=0 to suppress.`,
    );
  }
}
