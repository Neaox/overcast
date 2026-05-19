/**
 * harness.ts — Core test framework for the Overcast compat Node.js suite.
 *
 * Defines the TestGroup / TestCase shapes and the runGroup() executor that
 * emits NDJSON events to stdout as tests complete.
 *
 * Rules (see compat/AGENTS.md):
 * - Never write non-NDJSON to stdout — use ctx.log() for debug output.
 * - Tests throw to signal failure; returning void means pass.
 * - Teardown always runs, even if tests failed.
 */

export interface TestContext {
  /** Overcast endpoint base URL, e.g. "http://localhost:4566" */
  endpoint: string;
  /** AWS region, e.g. "us-east-1" */
  region: string;
  /**
   * Short unique prefix for resource names in this run.
   * Format: "oc-<8-char-hex>". Use to avoid collisions between runs.
   * Example: "oc-a3f9b12c"
   */
  runId: string;
  /**
   * Write a debug message to stderr (never stdout).
   * The Go runner surfaces these as WARN log lines.
   */
  log(msg: string): void;
  /** AbortSignal for cancellation support (interactive mode). */
  signal?: AbortSignal;
  /**
   * Per-group state bag. Use to pass values between sequential tests within
   * the same group (e.g. resource IDs created in an earlier test).
   * Keys are arbitrary strings; values are unknown.
   */
  [key: string]: unknown;
}

export type TestFn = (ctx: TestContext) => Promise<void>;

export interface TestCase {
  /** PascalCase name matching the AWS API operation where applicable. */
  name: string;
  fn: TestFn;
  /**
   * If set, the test is emitted as "skip" without running.
   * Pass a string reason (e.g. "requires Docker") so the dashboard can
   * explain why. Only use when the test requires external infrastructure
   * that isn't guaranteed to be present. Do NOT skip to hide a gap.
   */
  skip?: boolean | string;
  /**
   * AWS API operation name for documentation links.
   * - Omit: the test `name` is used as the operation name.
   * - String: use this name instead (e.g. when test name is a variant like
   *   "QueryWithLimit" but the real operation is "Query").
   * - `false`: suppress the doc link entirely for this test.
   */
  op?: string | false;
  /**
   * If set, the test is emitted as "na" (not applicable) without running.
   * Use this when the AWS SDK client does not yet expose this operation.
   * NA results are excluded from pass-rate calculations.
   */
  na?: string;
  /**
   * Names of other tests in the same group that must pass before this test
   * can run.  If any dependency failed or was skipped, this test is
   * automatically skipped with a reason referencing the failed dep.
   */
  depends?: string[];
}

export interface TestGroup {
  /** Suite name — passed in from runner.ts. */
  suite: string;
  /** AWS service name, e.g. "s3", "iam". */
  service: string;
  /**
   * Group identifier, e.g. "s3-crud". Used in NDJSON output.
   * Convention: "<service>-<feature>", all lowercase kebab.
   */
  name: string;
  tests: TestCase[];
  /**
   * Optional setup that runs before any tests in the group.
   * Failures here abort the group (all tests emitted as skip).
   */
  setup?: (ctx: TestContext) => Promise<void>;
  /**
   * Optional teardown that runs after all tests, even if they failed.
   * Must be fault-tolerant — wrap every delete in try/catch.
   */
  teardown?: (ctx: TestContext) => Promise<void>;
}

// ─── NDJSON event shapes ──────────────────────────────────────────────────

interface RunStartEvent {
  event: "run_start";
  suite: string;
  started_at: string;
  endpoint: string;
  version: "1";
  total_tests?: number;
}

interface TestStartEvent {
  event: "test_start";
  suite: string;
  service: string;
  group: string;
  test: string;
}

interface TestResultEvent {
  event: "test_result";
  suite: string;
  service: string;
  group: string;
  test: string;
  status: "pass" | "fail" | "skip" | "unimplemented" | "na" | "cancelled";
  duration_ms: number;
  error?: string;
}

interface RunEndEvent {
  event: "run_end";
  suite: string;
  passed: number;
  failed: number;
  skipped: number;
  unimplemented: number;
  duration_ms: number;
}

// ─── Interactive-mode event shapes ────────────────────────────────────────

interface BuildingEvent {
  event: "building";
  suite: string;
  message: string;
}

interface ReadyEvent {
  event: "ready";
  suite: string;
  total_tests: number;
}

interface BatchCompleteEvent {
  event: "batch_complete";
  suite: string;
  batch_id: string;
  passed: number;
  failed: number;
  skipped: number;
  unimplemented: number;
  cancelled: number;
  duration_ms: number;
}

/** Emit a NDJSON event to stdout. */
export function emitEvent(
  event:
    | RunStartEvent
    | TestStartEvent
    | TestResultEvent
    | RunEndEvent
    | BuildingEvent
    | ReadyEvent
    | BatchCompleteEvent,
): void {
  process.stdout.write(JSON.stringify(event) + "\n");
}

/**
 * Returns true if the error represents an unimplemented operation in the
 * emulator. These are known feature gaps, not broken implementations.
 *
 * Three paths to detect "not implemented":
 *  1. HTTP 501: JSON-protocol services expose the status via
 *     `err.$metadata.httpStatusCode`.
 *  2. HTTP 501 on XML-protocol services (IAM, SES, STS, …): Overcast returns
 *     a JSON 501 body; the SDK's XML parser fails before populating
 *     `$metadata`. The raw HTTP response is still on `err.$response.statusCode`.
 *  3. UnknownOperationException (HTTP 400): returned by JSON-protocol services
 *     (DynamoDB, CloudWatch Logs, DynamoDB Streams) and the router's JSON
 *     fallback when the target action is not registered.
 *  4. NotImplemented (HTTP 400): returned by the router's XML fallback for
 *     query-protocol services (IAM, STS, SES, etc.) whose action is not
 *     registered. The SDK parses this into `err.name === "NotImplemented"`.
 */
function isUnimplemented(err: unknown): boolean {
  if (err == null || typeof err !== "object") return false;
  const e = err as Record<string, unknown>;

  // Path 1: standard SDK error with parsed metadata.
  const meta = e["$metadata"];
  if (meta != null && typeof meta === "object") {
    if ((meta as Record<string, unknown>)["httpStatusCode"] === 501)
      return true;
  }

  // Path 2: deserialization error wrapping a raw HTTP response.
  const resp = e["$response"];
  if (resp != null && typeof resp === "object") {
    if ((resp as Record<string, unknown>)["statusCode"] === 501) return true;
  }

  // Path 3: UnknownOperationException — JSON-protocol "not registered" error.
  if (e["name"] === "UnknownOperationException") return true;
  if (e["__type"] === "UnknownOperationException") return true;

  // Path 4: NotImplemented — XML query-protocol "not registered" error (IAM, STS, etc.).
  if (e["name"] === "NotImplemented") return true;
  if (e["Code"] === "NotImplemented") return true;

  return false;
}

// ─── Concurrency semaphore ───────────────────────────────────────────────────

/**
 * Limits how many async tasks run concurrently.
 * Each call to `run()` acquires a slot, awaits fn(), then releases.
 */
export class Semaphore {
  private slots: number;
  private readonly queue: Array<() => void> = [];

  constructor(slots: number) {
    this.slots = slots;
  }

  async run<T>(fn: () => Promise<T>): Promise<T> {
    await new Promise<void>((resolve) => {
      if (this.slots > 0) {
        this.slots--;
        resolve();
      } else {
        this.queue.push(resolve);
      }
    });
    try {
      return await fn();
    } finally {
      const next = this.queue.shift();
      if (next) {
        next();
      } else {
        this.slots++;
      }
    }
  }
}

// ─── Timeout helper ───────────────────────────────────────────────────────────

const TEST_TIMEOUT_MS = 120_000; // 120 s per individual test / setup call — Docker cold starts can take ~30-60s under load

/**
 * Race a promise against a deadline. Rejects with a clear timeout message
 * if `ms` elapses before the promise settles.
 *
 * NOTE: The original promise is NOT cancelled (JS has no cancellation without
 * AbortController). It will eventually settle on its own, but since we
 * immediately move on to the next test / group, the dangling promise cannot
 * produce a second test_result event — it is simply ignored.
 */
function withTimeout<T>(
  promise: Promise<T>,
  ms: number,
  label: string,
): Promise<T> {
  let handle!: ReturnType<typeof setTimeout>;
  const timeout = new Promise<never>((_, reject) => {
    handle = setTimeout(
      () =>
        reject(new Error(`${label} timed out after ${Math.round(ms / 1000)}s`)),
      ms,
    );
  });
  return Promise.race([promise, timeout]).finally(() =>
    clearTimeout(handle),
  ) as Promise<T>;
}

// ─── Abort helpers ────────────────────────────────────────────────────────

/** Return true if the error represents an AbortController cancellation. */
function isAbortError(err: unknown): boolean {
  if (err instanceof DOMException && err.name === "AbortError") return true;
  if (
    err != null &&
    typeof err === "object" &&
    (err as Record<string, unknown>).name === "AbortError"
  )
    return true;
  return false;
}

/**
 * Race a promise against an AbortSignal. Rejects with AbortError if the
 * signal fires before the promise settles. Properly cleans up the listener.
 */
function raceAbort<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  if (signal.aborted) {
    return Promise.reject(new DOMException("Cancelled", "AbortError"));
  }
  return new Promise<T>((resolve, reject) => {
    const onAbort = () => reject(new DOMException("Cancelled", "AbortError"));
    signal.addEventListener("abort", onAbort, { once: true });
    promise.then(
      (val) => {
        signal.removeEventListener("abort", onAbort);
        resolve(val);
      },
      (err) => {
        signal.removeEventListener("abort", onAbort);
        reject(err);
      },
    );
  });
}

// ─── Runner ───────────────────────────────────────────────────────────────

export interface RunGroupOptions {
  /** Map of "group:test" → AbortController for cancellation support. */
  abortControllers?: Map<string, AbortController>;
  /** Batch ID for event correlation in interactive mode. */
  batchId?: string;
}

/** Run a single test group, emitting one test_result per test. */
export async function runGroup(
  group: TestGroup,
  ctx: TestContext,
  options?: RunGroupOptions,
): Promise<{
  passed: number;
  failed: number;
  skipped: number;
  unimplemented: number;
  cancelled: number;
}> {
  let passed = 0;
  let failed = 0;
  let skipped = 0;
  let unimplemented = 0;
  let cancelled = 0;

  // Track which tests passed so we can cascade-skip dependents.
  const passedTests = new Set<string>();
  const failedOrSkipped = new Set<string>();

  // Setup phase — abort group on failure
  if (group.setup) {
    try {
      await withTimeout(
        group.setup(ctx),
        TEST_TIMEOUT_MS,
        `setup ${group.name}`,
      );
    } catch (err) {
      // Emit all tests as skipped if setup fails
      const reason = `setup failed: ${String(err)}`;
      for (const tc of group.tests) {
        emitEvent({
          event: "test_result",
          suite: group.suite,
          service: group.service,
          group: group.name,
          test: tc.name,
          status: "skip",
          duration_ms: 0,
          error: reason,
        });
        skipped++;
      }
      ctx.log(`[${group.name}] ${reason}`);
      return { passed, failed, skipped, unimplemented, cancelled };
    }
  }

  // Test execution phase
  try {
    for (const tc of group.tests) {
      if (tc.na) {
        emitEvent({
          event: "test_result",
          suite: group.suite,
          service: group.service,
          group: group.name,
          test: tc.name,
          status: "na",
          duration_ms: 0,
          error: tc.na,
          ...(tc.op !== undefined ? { op: tc.op === false ? "" : tc.op } : {}),
        });
        continue;
      }

      if (tc.skip) {
        const reason = typeof tc.skip === "string" ? tc.skip : undefined;
        emitEvent({
          event: "test_result",
          suite: group.suite,
          service: group.service,
          group: group.name,
          test: tc.name,
          status: "skip",
          duration_ms: 0,
          ...(reason ? { error: reason } : {}),
          ...(tc.op !== undefined ? { op: tc.op === false ? "" : tc.op } : {}),
        });
        skipped++;
        failedOrSkipped.add(tc.name);
        continue;
      }

      // Dependency gate — skip if any declared dependency failed or was skipped.
      if (tc.depends && tc.depends.length > 0) {
        const failedDeps = tc.depends.filter((d) => failedOrSkipped.has(d));
        if (failedDeps.length > 0) {
          emitEvent({
            event: "test_result",
            suite: group.suite,
            service: group.service,
            group: group.name,
            test: tc.name,
            status: "skip",
            duration_ms: 0,
            error: `dependency failed: ${failedDeps.join(", ")}`,
            ...(tc.op !== undefined
              ? { op: tc.op === false ? "" : tc.op }
              : {}),
          });
          skipped++;
          failedOrSkipped.add(tc.name);
          continue;
        }
      }

      // Create per-test AbortController for cancellation support.
      const ac = new AbortController();
      const acKey = `${group.name}:${tc.name}`;
      options?.abortControllers?.set(acKey, ac);

      ctx.signal = ac.signal;
      const start = Date.now();
      try {
        emitEvent({
          event: "test_start",
          suite: group.suite,
          service: group.service,
          group: group.name,
          test: tc.name,
        });
        await withTimeout(
          raceAbort(tc.fn(ctx), ac.signal),
          TEST_TIMEOUT_MS,
          tc.name,
        );
        const duration_ms = Date.now() - start;
        emitEvent({
          event: "test_result",
          suite: group.suite,
          service: group.service,
          group: group.name,
          test: tc.name,
          status: "pass",
          duration_ms,
          ...(tc.op !== undefined ? { op: tc.op === false ? "" : tc.op } : {}),
        });
        passed++;
        passedTests.add(tc.name);
      } catch (err) {
        const duration_ms = Date.now() - start;
        const error =
          err instanceof Error ? `${err.name}: ${err.message}` : String(err);
        if (isAbortError(err)) {
          emitEvent({
            event: "test_result",
            suite: group.suite,
            service: group.service,
            group: group.name,
            test: tc.name,
            status: "cancelled",
            duration_ms,
            error: "cancelled",
            ...(tc.op !== undefined
              ? { op: tc.op === false ? "" : tc.op }
              : {}),
          });
          cancelled++;
          failedOrSkipped.add(tc.name);
        } else if (isUnimplemented(err)) {
          emitEvent({
            event: "test_result",
            suite: group.suite,
            service: group.service,
            group: group.name,
            test: tc.name,
            status: "unimplemented",
            duration_ms,
            error,
            ...(tc.op !== undefined
              ? { op: tc.op === false ? "" : tc.op }
              : {}),
          });
          unimplemented++;
          failedOrSkipped.add(tc.name);
        } else {
          emitEvent({
            event: "test_result",
            suite: group.suite,
            service: group.service,
            group: group.name,
            test: tc.name,
            status: "fail",
            duration_ms,
            error,
            ...(tc.op !== undefined
              ? { op: tc.op === false ? "" : tc.op }
              : {}),
          });
          failed++;
          failedOrSkipped.add(tc.name);
        }
      } finally {
        options?.abortControllers?.delete(acKey);
      }
    }
  } finally {
    // Teardown always runs
    if (group.teardown) {
      try {
        await group.teardown(ctx);
      } catch (err) {
        ctx.log(`[${group.name}] teardown error: ${String(err)}`);
      }
    }
  }

  return { passed, failed, skipped, unimplemented, cancelled };
}

/** Run all groups in parallel, emitting run_start and run_end events.
 *
 * Each group receives its own shallow copy of the context so that
 * per-group state stored via `ctx[key] = value` does not leak between
 * concurrent groups. Node.js is single-threaded so stdout writes from
 * `emitEvent` never interleave, but the state isolation is still required
 * for correctness.
 */
export async function runSuite(
  suite: string,
  groups: TestGroup[],
  ctx: Omit<TestContext, "log">,
): Promise<void> {
  const log = (msg: string): void => {
    process.stderr.write(`[compat:${suite}] ${msg}\n`);
  };
  const baseCtx = { ...ctx, log } as unknown as TestContext;

  const suiteStart = Date.now();

  // Compute total test count upfront for progress tracking.
  const totalTests = groups.reduce((sum, g) => sum + g.tests.length, 0);

  emitEvent({
    event: "run_start",
    suite,
    started_at: new Date().toISOString(),
    endpoint: ctx.endpoint as string,
    version: "1",
    total_tests: totalTests,
  });

  // Limit concurrent group execution to avoid overwhelming the emulator.
  // OVERCAST_COMPAT_PARALLEL_SLOTS is injected by the Go runner based on
  // CPU count and the number of active suites (default: 8).
  const slots = Math.max(
    1,
    parseInt(process.env["OVERCAST_COMPAT_PARALLEL_SLOTS"] ?? "8", 10) || 8,
  );
  const sem = new Semaphore(slots);

  // Run all groups through the semaphore; each gets its own context copy.
  const groupResults = await Promise.all(
    groups.map((group) => sem.run(() => runGroup(group, { ...baseCtx }))),
  );

  let totalPassed = 0;
  let totalFailed = 0;
  let totalSkipped = 0;
  let totalUnimplemented = 0;
  for (const { passed, failed, skipped, unimplemented } of groupResults) {
    totalPassed += passed;
    totalFailed += failed;
    totalSkipped += skipped;
    totalUnimplemented += unimplemented;
    // cancelled is tracked in interactive mode only; ignored here
  }

  emitEvent({
    event: "run_end",
    suite,
    passed: totalPassed,
    failed: totalFailed,
    skipped: totalSkipped,
    unimplemented: totalUnimplemented,
    duration_ms: Date.now() - suiteStart,
  });
}

/** Generate a short random ID for resource name prefixes. */
export function makeRunId(): string {
  return (
    "oc-" +
    Math.floor(Math.random() * 0xffffffff)
      .toString(16)
      .padStart(8, "0")
  );
}
