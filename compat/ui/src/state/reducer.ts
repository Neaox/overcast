import { create } from "mutative";
import type {
  RunState,
  ServiceSection,
  WireEvent,
  Status,
  QueueEntry,
} from "../types/index";
import { initial } from "../types/index";
import { prevStatusKey } from "../types/state";

export { initial };

export type Action =
  | { type: "reset" }
  | { type: "clear_results" }
  | { type: "event"; payload: WireEvent }
  | { type: "queued"; entries: QueueEntry[] }
  | { type: "prune_queue" }
  | {
      type: "seed_registry";
      groups: Array<{
        service: string;
        name: string;
        tests: Array<{ name: string }>;
      }>;
    };

/** Remove queue entries that completed more than 60 seconds ago. */
const DONE_LINGER_MS = 60_000;

export function reducer(state: RunState, action: Action): RunState {
  if (action.type === "reset") return { ...initial, services: new Map() };
  if (action.type === "clear_results") {
    // Wipe per-suite cell statuses but keep the services/groups/tests structure
    // so the grid stays visible as empty placeholders.
    return create(state, (draft) => {
      for (const svc of draft.services.values()) {
        for (const grp of svc.groups.values()) {
          for (const testName of [...grp.tests.keys()]) {
            grp.tests.set(testName, {});
          }
        }
      }
      draft.status = "idle";
      draft.suites = [];
      draft.doneSuites = [];
      draft.staleSuites = [];
      draft.queuedSuites = [];
      draft.queue = [];
      draft.runStartedAt = null;
      draft.runFinishedAt = null;
      draft.suiteTotal = {};
      draft.suiteStartedAt = {};
      draft.suiteDurationMs = {};
      draft.suitePrevPassCount = {};
      draft.suiteErrors = {};
      draft.suiteHasFullRun = {};
      draft.completedBatches = {};
      draft.prevStatuses = {};
      // Keep: interactive, endpoint, suiteStates, services structure
    });
  }
  if (action.type === "queued") return applyQueued(state, action.entries);
  if (action.type === "prune_queue") {
    const cutoff = Date.now() - DONE_LINGER_MS;
    const hasStale = state.queue.some(
      (q) => q.completedAt !== undefined && q.completedAt <= cutoff,
    );
    if (!hasStale) return state;
    return create(state, (draft) => {
      draft.queue = draft.queue.filter(
        (q) => q.completedAt === undefined || q.completedAt > cutoff,
      );
    });
  }
  if (action.type === "seed_registry") {
    return create(state, (draft) => {
      for (const g of action.groups) {
        if (!draft.services.has(g.service)) {
          draft.services.set(g.service, {
            service: g.service,
            groups: new Map(),
          });
        }
        const svc = draft.services.get(g.service)!;
        if (!svc.groups.has(g.name)) {
          svc.groups.set(g.name, {
            name: g.name,
            service: g.service,
            tests: new Map(),
          });
        }
        const grp = svc.groups.get(g.name)!;
        for (const t of g.tests) {
          if (!grp.tests.has(t.name)) {
            grp.tests.set(t.name, {});
          }
        }
      }
    });
  }
  return applyEvent(state, action.payload);
}

/** Capture each cell's current final status into `prevStatuses` before it
 * gets wiped, so the UI can render pass→fail / fail→pass flip indicators
 * after the next run completes. Ignores `running` cells since they have no
 * completed result yet. */
function snapshotStatuses(
  services: Map<string, ServiceSection>,
  clearSuites: Set<string>,
  prevStatuses: Record<string, Status>,
): void {
  for (const svc of services.values()) {
    for (const grp of svc.groups.values()) {
      for (const [testName, cells] of grp.tests) {
        for (const s of clearSuites) {
          const cell = cells[s];
          if (cell && cell.status !== "running") {
            prevStatuses[prevStatusKey(svc.service, grp.name, testName, s)] =
              cell.status;
          }
        }
      }
    }
  }
}

/**
 * Removes all cells belonging to `clearSuites` from the services map and
 * prunes groups/services that become empty. Designed to operate on a mutative
 * draft (mutable) but also safe on any plain mutable copy.
 */
function wipeSuiteCells(
  services: Map<string, ServiceSection>,
  clearSuites: Set<string>,
): void {
  for (const svc of services.values()) {
    const emptyGrps: string[] = [];
    for (const [grpName, grp] of svc.groups) {
      const emptyTests: string[] = [];
      for (const [testName, cells] of grp.tests) {
        for (const s of clearSuites)
          delete (cells as Record<string, unknown>)[s];
        if (Object.keys(cells).length === 0) emptyTests.push(testName);
      }
      for (const t of emptyTests) grp.tests.delete(t);
      if (grp.tests.size === 0) emptyGrps.push(grpName);
    }
    for (const g of emptyGrps) svc.groups.delete(g);
  }
  // Collect then delete to avoid mutating while iterating.
  const emptySvcs = [...services.entries()]
    .filter(([, svc]) => svc.groups.size === 0)
    .map(([name]) => name);
  for (const name of emptySvcs) services.delete(name);
}

function findServiceForGroup(
  services: Map<string, ServiceSection>,
  group: string,
): ServiceSection | undefined {
  for (const svc of services.values()) {
    if (svc.groups.has(group)) return svc;
  }
  return undefined;
}

/**
 * Hydrates state.queue and marks individual cells as "queued" based on entries
 * returned by POST /run. Only marks cells that already exist in state.services
 * (we can't create new rows because queue entries don't carry the service name).
 * Does not downgrade a "running" cell.
 */
function applyQueued(state: RunState, entries: QueueEntry[]): RunState {
  if (entries.length === 0) return state;
  return create(state, (draft) => {
    // Guard against HMR: state may have been created before completedBatches existed.
    draft.completedBatches ??= {};
    // Receiving queued entries from POST /run proves the server is in
    // interactive mode, even if building/ready events haven't arrived yet.
    draft.interactive = true;
    for (const entry of entries) {
      // Hydrate state.queue (skip duplicates)
      const already = draft.queue.some(
        (q) =>
          q.suite === entry.suite &&
          q.group === entry.group &&
          q.test === entry.test,
      );
      if (!already) {
        const completedState =
          draft.completedBatches[`${entry.batch_id}:${entry.suite}`];
        if (completedState !== undefined) {
          // This batch already completed before the POST /run response arrived
          // (race: fast-completing tests emit batch_complete via SSE first).
          // Add the entry as already-terminal so it lingers for 60s then prunes.
          draft.queue.push({
            ...entry,
            state: completedState,
            completedAt: Date.now(),
          });
        } else {
          draft.queue.push({ ...entry });
          // A new (non-terminal) queued entry means this suite is no longer
          // done — clear it from doneSuites so the card reflects reality.
          draft.doneSuites = draft.doneSuites.filter((s) => s !== entry.suite);
        }
      }

      // Mark individual cells as queued when the group already exists in
      // state.services so the result grid gives immediate visual feedback.
      if (entry.test) {
        // Specific test entry — mark just that cell.
        const svc = findServiceForGroup(draft.services, entry.group);
        if (svc) {
          const grp = svc.groups.get(entry.group);
          if (grp && grp.tests.has(entry.test)) {
            const prev = grp.tests.get(entry.test)!;
            const cellStatus = prev[entry.suite]?.status;
            // Don't downgrade an actively-running cell.
            if (cellStatus !== "running") {
              grp.tests.set(entry.test, {
                ...prev,
                [entry.suite]: { status: "queued" },
              });
            }
          }
        }
      } else if (!entry.group) {
        // Suite-level entry ("run all") — mark every existing cell for this
        // suite as queued so the result grid shows visual feedback immediately.
        for (const svc of draft.services.values()) {
          for (const grp of svc.groups.values()) {
            for (const [testName, cells] of grp.tests) {
              const cellStatus = cells[entry.suite]?.status;
              if (cellStatus !== "running") {
                grp.tests.set(testName, {
                  ...cells,
                  [entry.suite]: { status: "queued" },
                });
              }
            }
          }
        }
      }
    }
  });
}

function applyEvent(state: RunState, ev: WireEvent): RunState {
  // Full-reset fast paths: return a brand-new object directly without
  // going through create() at all. mutative has zero work to do and
  // reselect will see new object references on every slice, which is
  // correct — the entire state has changed.
  if (ev.event === "run_reset" && (ev.suites ?? []).length === 0) {
    // Full reset — snapshot every suite's statuses into prevStatuses so the
    // diff survives into the next run.
    const prevStatuses: Record<string, Status> = { ...state.prevStatuses };
    snapshotStatuses(state.services, new Set(state.suites), prevStatuses);
    return {
      ...initial,
      status: "running",
      runStartedAt: Date.now(),
      suiteDurationMs: state.suiteDurationMs,
      suitePrevPassCount: state.suitePrevPassCount,
      prevStatuses,
    };
  }
  if (
    ev.event === "run_start" &&
    state.status !== "running" &&
    state.services.size === 0
  ) {
    // Clean start — suiteDurationMs, suitePrevPassCount, and prevStatuses
    // survive so the delta arrow and per-cell flip badges are visible on
    // the first re-run.
    return {
      ...initial,
      status: "running",
      endpoint: ev.endpoint,
      suites: [ev.suite],
      suiteTotal: { [ev.suite]: ev.total_tests ?? 0 },
      suiteStartedAt: { [ev.suite]: Date.now() },
      runStartedAt: Date.now(),
      suiteDurationMs: state.suiteDurationMs,
      suitePrevPassCount: state.suitePrevPassCount,
      prevStatuses: state.prevStatuses,
    };
  }

  // All other events: use create() for structural sharing — unmodified
  // slices keep their reference so reselect returns cached results.
  return create(state, (draft) => {
    switch (ev.event) {
      case "suite_starting": {
        if (!draft.queuedSuites.includes(ev.suite))
          draft.queuedSuites.push(ev.suite);
        break;
      }

      case "suite_error": {
        // Move suite out of queued and record the error.
        draft.queuedSuites = draft.queuedSuites.filter((s) => s !== ev.suite);
        draft.suiteErrors[ev.suite] = ev.error;
        break;
      }

      case "run_reset": {
        const resetSet = new Set(ev.suites ?? []);
        if (resetSet.size === 0) {
          // Empty suites = full server-side clear — wipe results but keep structure.
          return reducer(state, { type: "clear_results" });
        }
        // Partial reset — preserve other suites' data as stale.
        draft.status = "running";
        draft.staleSuites = draft.suites.filter((s) => !resetSet.has(s));
        draft.suites = draft.suites.filter((s) => !resetSet.has(s));
        draft.queuedSuites = draft.queuedSuites.filter((s) => !resetSet.has(s));
        draft.doneSuites = draft.doneSuites.filter((s) => !resetSet.has(s));
        draft.runStartedAt = null;
        draft.runFinishedAt = null;
        for (const s of resetSet) {
          delete draft.suiteTotal[s];
          delete draft.suiteStartedAt[s];
          delete draft.suiteDurationMs[s];
          delete draft.suiteErrors[s];
          // Keep suitePrevPassCount — it's set by run_end and outlives resets.
        }
        snapshotStatuses(draft.services, resetSet, draft.prevStatuses);
        wipeSuiteCells(draft.services, resetSet);
        break;
      }

      case "run_start": {
        if (state.status === "running") {
          // Another suite joining the current run — add without resetting.
          draft.endpoint = ev.endpoint;
          if (!draft.suites.includes(ev.suite)) draft.suites.push(ev.suite);
          draft.queuedSuites = draft.queuedSuites.filter((s) => s !== ev.suite);
          draft.staleSuites = draft.staleSuites.filter((s) => s !== ev.suite);
          draft.suiteTotal[ev.suite] = ev.total_tests ?? 0;
          if (!draft.suiteStartedAt[ev.suite])
            draft.suiteStartedAt[ev.suite] = Date.now();
          if (!draft.runStartedAt) draft.runStartedAt = Date.now();
          draft.runFinishedAt = null;
        } else if (state.services.size > 0) {
          // Transitioning into a new run with existing data — preserve other
          // suites as stale, clear only the suite being re-run.
          const clearSet = new Set([ev.suite]);
          draft.status = "running";
          draft.staleSuites = draft.suites.filter((s) => s !== ev.suite);
          draft.suites = draft.suites.filter((s) => s !== ev.suite);
          draft.queuedSuites = draft.queuedSuites.filter((s) => s !== ev.suite);
          draft.doneSuites = draft.doneSuites.filter((s) => s !== ev.suite);
          draft.runStartedAt = Date.now();
          draft.runFinishedAt = null;
          delete draft.suiteTotal[ev.suite];
          delete draft.suiteStartedAt[ev.suite];
          delete draft.suiteDurationMs[ev.suite];
          snapshotStatuses(draft.services, clearSet, draft.prevStatuses);
          wipeSuiteCells(draft.services, clearSet);
          if (!draft.suites.includes(ev.suite)) draft.suites.push(ev.suite);
          draft.endpoint = ev.endpoint;
          draft.suiteTotal[ev.suite] = ev.total_tests ?? 0;
          draft.suiteStartedAt[ev.suite] = Date.now();
        } else {
          // Clean start handled by early return above — this branch is
          // unreachable, but satisfies the type checker.
        }
        break;
      }

      case "test_start":
        {
          const { suite, service, group, test } = ev;
          // Ensure the suite is tracked so selectors count its cells.
          if (!draft.suites.includes(suite)) draft.suites.push(suite);
          if (draft.status === "idle") draft.status = "running";
          if (!draft.services.has(service))
            draft.services.set(service, { service, groups: new Map() });
          const svc = draft.services.get(service)!;
          if (!svc.groups.has(group))
            svc.groups.set(group, { name: group, service, tests: new Map() });
          const grp = svc.groups.get(group)!;
          const existing = grp.tests.get(test)?.[suite];
          // Transition to running if: no cell yet, or cell is queued.
          // Leaves existing pass/fail/skip cells in place until test_result
          // arrives — avoids flicker on re-runs for tests not yet queued.
          if (!existing || existing.status === "queued") {
            const prev = grp.tests.get(test) ?? {};
            grp.tests.set(test, { ...prev, [suite]: { status: "running" } });
          }
          // Advance matching test-level queue entry to "running".
          for (const q of draft.queue) {
            if (
              q.suite === suite &&
              q.group === group &&
              q.test === test &&
              q.state === "queued"
            ) {
              q.state = "running";
              break;
            }
          }
          break;
        }

      case "test_result": {
        const { suite, service, group, test, status, error, op } = ev;
        // Ensure the suite is tracked so selectors count its cells.
        if (!draft.suites.includes(suite)) draft.suites.push(suite);
        if (draft.status === "idle") draft.status = "running";
        if (!draft.services.has(service))
          draft.services.set(service, { service, groups: new Map() });
        const svc = draft.services.get(service)!;
        if (!svc.groups.has(group))
          svc.groups.set(group, { name: group, service, tests: new Map() });
        const grp = svc.groups.get(group)!;
        // Last-write-wins: overwriting is idempotent. Duplicate events (e.g.
        // SSE replay after /results load) land in the same cell and are
        // counted exactly once by the selector layer — no double-counting.
        const prev = grp.tests.get(test) ?? {};
        grp.tests.set(test, { ...prev, [suite]: { status, error, op } });
        // Advance matching test-level queue entry to terminal state.
        // skip/unimplemented are non-failure outcomes → mapped to "pass".
        const queueState: QueueEntry["state"] =
          status === "fail"
            ? "fail"
            : status === "cancelled"
              ? "cancelled"
              : "pass";
        const now = Date.now();
        for (const q of draft.queue) {
          if (
            q.suite === suite &&
            q.group === group &&
            q.test === test &&
            (q.state === "queued" || q.state === "running")
          ) {
            q.state = queueState;
            q.completedAt = now;
            break;
          }
        }
        break;
      }

      case "run_end": {
        if (!draft.doneSuites.includes(ev.suite))
          draft.doneSuites.push(ev.suite);
        // Record the authoritative duration and pass count from the runner.
        // These survive the next run_start so the delta arrow can compare.
        draft.suiteDurationMs[ev.suite] = ev.duration_ms;
        draft.suitePrevPassCount[ev.suite] = ev.passed;
        // batch-mode run_end always represents a full run.
        draft.suiteHasFullRun ??= {};
        draft.suiteHasFullRun[ev.suite] = true;
        break;
      }

      case "run_complete": {
        draft.status = "done";
        draft.staleSuites = [];
        draft.queuedSuites = [];
        draft.runFinishedAt = Date.now();
        break;
      }

      case "building": {
        draft.interactive = true;
        draft.suiteStates[ev.suite] = "building";
        if (!draft.suites.includes(ev.suite)) draft.suites.push(ev.suite);
        break;
      }

      case "ready": {
        draft.interactive = true;
        draft.suiteStates[ev.suite] = "ready";
        if (!draft.suites.includes(ev.suite)) draft.suites.push(ev.suite);
        draft.suiteTotal[ev.suite] = ev.total_tests;
        break;
      }

      case "batch_complete": {
        // Transition all active entries for this batch to a terminal state.
        // They linger for 60s so the user can see the outcome, then prune_queue removes them.
        const batchState: QueueEntry["state"] = ev.failed > 0 ? "fail" : "pass";
        const now = Date.now();
        // Record the result so applyQueued can immediately resolve entries that
        // are added after this event fires (race: fast batches complete before
        // the POST /run HTTP response arrives at the browser).
        draft.completedBatches ??= {};
        draft.completedBatches[`${ev.batch_id}:${ev.suite}`] = batchState;
        for (const q of draft.queue) {
          if (
            q.batch_id === ev.batch_id &&
            q.suite === ev.suite &&
            (q.state === "queued" || q.state === "running")
          ) {
            q.state = batchState;
            q.completedAt = now;
            // For suite-level entries (group/test both empty), store aggregate counts.
            if (!q.group && !q.test) {
              q.passed = ev.passed;
              q.failed = ev.failed;
              q.total = ev.passed + ev.failed + ev.skipped + ev.unimplemented;
            }
          }
        }
        draft.suiteStates[ev.suite] = "ready";
        // Mark this suite as done for the interactive session.
        if (!draft.doneSuites.includes(ev.suite))
          draft.doneSuites.push(ev.suite);
        // If this batch ran the entire suite (all tests accounted for),
        // mark it as having a full run so partial-run cards can show
        // the progress bar contextualised against the full total.
        const batchTotal =
          ev.passed + ev.failed + ev.skipped + ev.unimplemented;
        const knownTotal = draft.suiteTotal[ev.suite] ?? 0;
        if (knownTotal > 0 && batchTotal >= knownTotal) {
          draft.suiteHasFullRun ??= {};
          draft.suiteHasFullRun[ev.suite] = true;
        }
        // In interactive mode there's no run_complete event. Transition the
        // overall status to "done" once every known suite has a batch_complete
        // and no more queue entries are pending.
        if (
          draft.status === "running" &&
          draft.suites.length > 0 &&
          draft.suites.every((s) => draft.doneSuites.includes(s)) &&
          !draft.queue.some(
            (q) => q.state === "queued" || q.state === "running",
          )
        ) {
          draft.status = "done";
          draft.staleSuites = [];
          draft.runFinishedAt = Date.now();
        }
        break;
      }

      case "cancelled": {
        // Update the test cell to cancelled status
        const svc = findServiceForGroup(draft.services, ev.group);
        if (svc) {
          const grp = svc.groups.get(ev.group);
          if (grp) {
            const prev = grp.tests.get(ev.test) ?? {};
            grp.tests.set(ev.test, {
              ...prev,
              [ev.suite]: { status: "cancelled" },
            });
          }
        }
        // Flip queue entry to cancelled so it lingers for 60s.
        const cancelNow = Date.now();
        for (const q of draft.queue) {
          if (
            q.suite === ev.suite &&
            q.group === ev.group &&
            q.test === ev.test &&
            (q.state === "queued" || q.state === "running")
          ) {
            q.state = "cancelled";
            q.completedAt = cancelNow;
            break;
          }
        }
        break;
      }

      // Unknown event types (e.g. group_setup_error from suite runners) are
      // silently ignored — no draft mutations → original state returned.
    }
  });
}
