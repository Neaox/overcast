import type { Status } from "./wire";

export interface QueueEntry {
  batch_id: string;
  suite: string;
  group: string;
  test: string;
  state: "queued" | "running" | "pass" | "fail" | "cancelled";
  /** Wall-clock ms when this entry reached a terminal state. Used for auto-pruning after 60s. */
  completedAt?: number;
  /** Aggregate pass count — set on suite-level entries when batch_complete arrives. */
  passed?: number;
  /** Aggregate fail count — set on suite-level entries when batch_complete arrives. */
  failed?: number;
  /** Total tests in batch (passed + failed + skipped + unimplemented). */
  total?: number;
}

export interface TestCell {
  // Keyed by suiteName
  [suite: string]: { status: Status; error?: string; op?: string } | undefined;
}

export interface GroupRow {
  name: string;
  service: string;
  tests: Map<string, TestCell>; // testName → cells per suite
}

export interface ServiceSection {
  service: string;
  groups: Map<string, GroupRow>; // groupName → row
}

/** Pass/fail/skip/unimplemented/na counts for a single suite. */
export interface SuiteTotals {
  pass: number;
  fail: number;
  skip: number;
  unimplemented: number;
  na: number;
}

/** Progress info for a single suite (for progress cards and table headers). */
export interface SuiteInfo {
  done: boolean;
  queued: boolean;
  completed: number;
  total: number;
}

// RunState holds only raw facts delivered by events.
// All derived statistics (totals, completed counts, suite infos) are computed
// by memoized selectors so they are always consistent with the cell grid and
// never double-counted regardless of event replay order.
export interface RunState {
  status: "idle" | "running" | "done";
  endpoint: string;
  suites: string[];
  /** Suites whose data is from a previous run while a different suite re-runs. */
  staleSuites: string[];
  /** Suites that received suite_starting but not yet run_start. */
  queuedSuites: string[];
  /** Suites that have received run_end. */
  doneSuites: string[];
  /** Suites that failed to start or crashed, keyed by suite name → error message. */
  suiteErrors: Record<string, string>;
  /** Canonical test result grid: service → group → test → per-suite cell. */
  services: Map<string, ServiceSection>;
  /** Expected test count per suite, sourced from run_start total_tests. */
  suiteTotal: Record<string, number>;
  /** Wall-clock ms when each suite's run_start was received. */
  suiteStartedAt: Record<string, number>;
  /** Wall-clock ms of the first run_start in this run. */
  runStartedAt: number | null;
  /** Wall-clock ms when run_complete was received (null while running). */
  runFinishedAt: number | null;
  /** Duration of each suite's last completed run in ms (from run_end). */
  suiteDurationMs: Record<string, number>;
  /**
   * Pass count from each suite's most recent completed run, captured at
   * run_end time. Preserved through subsequent re-runs so SuiteCard can show
   * a Δ arrow comparing the new result to the last one.
   */
  suitePrevPassCount: Record<string, number>;
  /**
   * Snapshot of each cell's status from the *previous* completed run, keyed
   * by `service|group|test|suite`. Captured whenever cells are wiped for a
   * re-run, so the UI can render a flip badge (pass→fail, fail→pass, etc)
   * on cells whose status changed. Lets developers see at a glance what
   * their last edit broke or fixed without comparing runs by eye.
   */
  prevStatuses: Record<string, Status>;
  /** Suite process states in interactive mode */
  suiteStates: Record<string, "building" | "ready" | "busy" | "error">;
  /** Current queue entries across all suites */
  queue: QueueEntry[];
  /** Whether we're in interactive mode (detected from building/ready events) */
  interactive: boolean;
  /**
   * Tracks batch_ids that have already completed, keyed by `${batch_id}:${suite}`.
   * Used to immediately resolve queue entries added via POST /run after fast-completing
   * batches (where batch_complete arrives before the HTTP response).
   */
  completedBatches: Record<string, "pass" | "fail">;
  /**
   * Suites for which we have ever seen a complete run (all tests). Set on
   * run_end (batch mode) or when a batch_complete accounts for all tests in
   * the suite (interactive mode). Used to gate the progress bar in partial-run
   * cards — a 1/444 sliver is meaningful only when we know the total.
   */
  suiteHasFullRun: Record<string, boolean>;
}

export const initial: RunState = {
  status: "idle",
  endpoint: "",
  suites: [],
  staleSuites: [],
  queuedSuites: [],
  doneSuites: [],
  suiteErrors: {},
  services: new Map(),
  suiteTotal: {},
  suiteStartedAt: {},
  runStartedAt: null,
  runFinishedAt: null,
  suiteDurationMs: {},
  suitePrevPassCount: {},
  prevStatuses: {},
  suiteStates: {},
  queue: [],
  interactive: false,
  completedBatches: {},
  suiteHasFullRun: {},
};

/** Compose the key under which a cell's prior status is stored. */
export function prevStatusKey(
  service: string,
  group: string,
  test: string,
  suite: string,
): string {
  return `${service}|${group}|${test}|${suite}`;
}
