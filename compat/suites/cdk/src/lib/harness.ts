export interface TestContext {
  endpoint: string;
  region: string;
  runId: string;
  stackName: string;
  log(msg: string): void;
  signal?: AbortSignal;
  [key: string]: unknown;
}

export interface SuiteContext {
  endpoint: string;
  region: string;
  runId: string;
  stackName: string;
}

export type TestFn = (ctx: TestContext) => Promise<void>;

export interface TestCase {
  name: string;
  fn: TestFn;
  skip?: boolean | string;
  depends?: string[];
}

export interface TestGroup {
  suite: string;
  service: string;
  name: string;
  tests: TestCase[];
  setup?: (ctx: TestContext) => Promise<void>;
  teardown?: (ctx: TestContext) => Promise<void>;
}

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
  status: "pass" | "fail" | "skip" | "unimplemented" | "cancelled";
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

interface PongEvent {
  event: "pong";
  suite: string;
  running_test: string;
}

export function emitEvent(
  ev:
    | RunStartEvent
    | TestStartEvent
    | TestResultEvent
    | RunEndEvent
    | BuildingEvent
    | ReadyEvent
    | BatchCompleteEvent
    | PongEvent,
): void {
  process.stdout.write(JSON.stringify(ev) + "\n");
}

const TEST_TIMEOUT_MS = 5 * 60_000;

function withTimeout<T>(promise: Promise<T>, label: string): Promise<T> {
  let timer!: ReturnType<typeof setTimeout>;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(
      () =>
        reject(
          new Error(`${label} timed out after ${TEST_TIMEOUT_MS / 1000}s`),
        ),
      TEST_TIMEOUT_MS,
    );
  });
  return Promise.race([promise, timeout]).finally(() =>
    clearTimeout(timer),
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
 * signal fires before the promise settles.
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

export function isUnimplemented(err: unknown): boolean {
  if (err == null || typeof err !== "object") return false;
  const e = err as Record<string, unknown>;
  const msg = String(e["message"] ?? "");

  const meta = e["$metadata"];
  if (meta != null && typeof meta === "object") {
    if ((meta as Record<string, unknown>)["httpStatusCode"] === 501)
      return true;
  }

  if (e["name"] === "UnknownOperationException") return true;
  if (e["name"] === "NotImplemented") return true;

  return (
    msg.includes("HTTP 501") ||
    msg.includes("NotImplemented") ||
    msg.includes("UnknownOperationException") ||
    msg.includes("x-emulator-unsupported")
  );
}

export interface RunGroupOptions {
  /** Map of "group:test" → AbortController for cancellation support. */
  abortControllers?: Map<string, AbortController>;
  /** Batch ID for event correlation in interactive mode. */
  batchId?: string;
}

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
  const failedOrSkipped = new Set<string>();

  if (group.setup) {
    try {
      await withTimeout(group.setup(ctx), `setup ${group.name}`);
    } catch (err) {
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

  try {
    for (const tc of group.tests) {
      if (tc.skip) {
        emitEvent({
          event: "test_result",
          suite: group.suite,
          service: group.service,
          group: group.name,
          test: tc.name,
          status: "skip",
          duration_ms: 0,
          error: typeof tc.skip === "string" ? tc.skip : undefined,
        });
        skipped++;
        failedOrSkipped.add(tc.name);
        continue;
      }

      if (tc.depends && tc.depends.length > 0) {
        const failedDeps = tc.depends.filter((dep) => failedOrSkipped.has(dep));
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

      emitEvent({
        event: "test_start",
        suite: group.suite,
        service: group.service,
        group: group.name,
        test: tc.name,
      });

      const started = Date.now();
      try {
        await withTimeout(raceAbort(tc.fn(ctx), ac.signal), tc.name);
        emitEvent({
          event: "test_result",
          suite: group.suite,
          service: group.service,
          group: group.name,
          test: tc.name,
          status: "pass",
          duration_ms: Date.now() - started,
        });
        passed++;
      } catch (err) {
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
            duration_ms: Date.now() - started,
            error: "cancelled",
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
            duration_ms: Date.now() - started,
            error,
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
            duration_ms: Date.now() - started,
            error,
          });
          failed++;
          failedOrSkipped.add(tc.name);
        }
      } finally {
        options?.abortControllers?.delete(acKey);
      }
    }
  } finally {
    if (group.teardown) {
      try {
        await withTimeout(group.teardown(ctx), `teardown ${group.name}`);
      } catch (err) {
        ctx.log(`[${group.name}] teardown error: ${String(err)}`);
      }
    }
  }

  return { passed, failed, skipped, unimplemented, cancelled };
}

export async function runSuite(
  suite: string,
  groups: TestGroup[],
  baseCtx: SuiteContext,
): Promise<void> {
  const suiteStart = Date.now();
  const total = groups.reduce((sum, g) => sum + g.tests.length, 0);

  emitEvent({
    event: "run_start",
    suite,
    started_at: new Date().toISOString(),
    endpoint: baseCtx.endpoint,
    version: "1",
    total_tests: total,
  });

  const log = (msg: string): void => {
    process.stderr.write(`[compat:${suite}] ${msg}\n`);
  };

  let passed = 0;
  let failed = 0;
  let skipped = 0;
  let unimplemented = 0;

  for (const group of groups) {
    const groupCtx: TestContext = { ...baseCtx, log };
    const r = await runGroup(group, groupCtx);
    passed += r.passed;
    failed += r.failed;
    skipped += r.skipped;
    unimplemented += r.unimplemented;
  }

  emitEvent({
    event: "run_end",
    suite,
    passed,
    failed,
    skipped,
    unimplemented,
    duration_ms: Date.now() - suiteStart,
  });
}

export function makeRunId(): string {
  return (
    "oc-" +
    Math.floor(Math.random() * 0xffffffff)
      .toString(16)
      .padStart(8, "0")
  );
}
