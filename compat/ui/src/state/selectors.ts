import { createSelector } from "reselect";
import type { RunState, SuiteTotals, SuiteInfo } from "../types/index";

// ─── Input selectors ──────────────────────────────────────────────────────────
//
// All stats are derived from the canonical `services` cell grid via
// memoized selectors rather than maintained as running counters.
//
// Correctness: each (service, group, test, suite) cell is last-write-wins.
// Duplicate events overwrite the same cell and are counted exactly once.
//
// Performance: reselect only re-runs a selector when its specific inputs
// change by reference. Mutative's structural sharing means unrelated state
// slices keep their identity — e.g. run_end touches only doneSuites, so
// selectTotals (which depends on services + suites) returns its cached result.
//
// Dependency graph:
//   services + suites ──► selectTotals
//                              │
//                    ┌─────────┼─────────┐
//                    ▼         ▼         ▼
//           selectSuiteCompleted  selectGrand
//                    │
//                    ▼
//           selectCompletedCount
//   suiteTotal ────► selectTotalCount
//   suites + queuedSuites + doneSuites + suiteTotal + selectSuiteCompleted
//                    ──► selectSuiteInfos

export const selectServices = (s: RunState) => s.services;
export const selectSuites = (s: RunState) => s.suites;
export const selectSuiteTotal = (s: RunState) => s.suiteTotal;

/** Per-suite breakdown scanned from the cell grid. */
export const selectTotals = createSelector(
  [selectServices, selectSuites],
  (services, suites): Record<string, SuiteTotals> => {
    const totals: Record<string, SuiteTotals> = {};
    for (const suite of suites)
      totals[suite] = { pass: 0, fail: 0, skip: 0, unimplemented: 0, na: 0 };
    for (const svc of services.values())
      for (const grp of svc.groups.values())
        for (const cells of grp.tests.values())
          for (const suite of suites) {
            const s = cells[suite]?.status;
            const t = totals[suite];
            if (!t || !s || s === "running") continue;
            if (s === "pass") t.pass++;
            else if (s === "fail") t.fail++;
            else if (s === "skip") t.skip++;
            else if (s === "unimplemented") t.unimplemented++;
            else if (s === "na") t.na++;
          }
    return totals;
  },
);

/** How many tests have a final (non-running) result per suite. */
export const selectSuiteCompleted = createSelector(
  [selectTotals],
  (totals): Record<string, number> =>
    Object.fromEntries(
      Object.entries(totals).map(([suite, t]) => [
        suite,
        t.pass + t.fail + t.skip + t.unimplemented + t.na,
      ]),
    ),
);

/** Completed test count across all suites. */
export const selectCompletedCount = createSelector(
  [selectSuiteCompleted],
  (sc): number => Object.values(sc).reduce((a, b) => a + b, 0),
);

/** Expected test count across all suites (from run_start total_tests). */
export const selectTotalCount = createSelector(
  [selectSuiteTotal],
  (st): number => Object.values(st).reduce((a, b) => a + b, 0),
);

/** Grand totals across all active suites (for the header pass-rate bar). */
export const selectGrand = createSelector(
  [selectTotals],
  (totals): SuiteTotals =>
    Object.values(totals).reduce(
      (acc, t) => ({
        pass: acc.pass + t.pass,
        fail: acc.fail + t.fail,
        skip: acc.skip + t.skip,
        unimplemented: acc.unimplemented + t.unimplemented,
        na: acc.na + t.na,
      }),
      { pass: 0, fail: 0, skip: 0, unimplemented: 0, na: 0 },
    ),
);

/** Per-suite display info for progress cards and table column headers. */
export const selectSuiteInfos = createSelector(
  [
    (s: RunState) => s.suites,
    (s: RunState) => s.queuedSuites,
    (s: RunState) => s.doneSuites,
    selectSuiteTotal,
    selectSuiteCompleted,
  ],
  (
    suites,
    queued,
    done,
    suiteTotal,
    suiteCompleted,
  ): Record<string, SuiteInfo> => {
    const doneSet = new Set(done);
    const result: Record<string, SuiteInfo> = {};
    for (const s of suites)
      result[s] = {
        done: doneSet.has(s),
        queued: false,
        completed: suiteCompleted[s] ?? 0,
        total: suiteTotal[s] ?? 0,
      };
    for (const s of queued)
      result[s] = { done: false, queued: true, completed: 0, total: 0 };
    return result;
  },
);

export const selectSuiteStates = (s: RunState) => s.suiteStates;
export const selectQueue = (s: RunState) => s.queue;
export const selectIsInteractive = (s: RunState) => s.interactive;
