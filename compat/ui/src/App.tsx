import {
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
  useCallback,
} from "react";
import { loadServerConfig } from "./lib/format";
import { reducer, initial } from "./state/reducer";
import { DispatchContext } from "./state/dispatch-context";
import {
  selectTotals,
  selectSuiteCompleted,
  selectGrand,
  selectCompletedCount,
  selectTotalCount,
  selectSuiteInfos,
  selectSuiteStates,
  selectQueue,
  selectIsInteractive,
} from "./state/selectors";
import { useEventStream } from "./hooks/use-event-stream";
import { useRegistrySeed } from "./hooks/use-registry-seed";
import { useResizeHeight } from "./hooks/use-resize-height";
import { useRun } from "./hooks/use-run";
import { formatETA } from "./lib/format";
import { PLANNED_SUITES } from "./lib/constants";
import { AppHeader } from "./components/header";
import { EmptyState } from "./components/empty-state";
import { SuiteProgressPanel } from "./components/suite-progress";
import { ServiceTable } from "./components/service-table";
import { CdkFlow } from "./components/cdk-flow";
import { ServerLogPanel } from "./components/server-log-panel";
import { QueuePanel } from "./components/queue-panel";
import { AppFooter } from "./components/footer";
import type { Status, ServiceSection } from "./types/index";

// ─── App ─────────────────────────────────────────────────────────────────────

/**
 * Outer shell: owns the reducer and provides DispatchContext so all
 * descendants (including AppInner) can call useDispatchContext().
 */
export default function App() {
  const [state, dispatch] = useReducer(reducer, initial);
  useEventStream(dispatch);
  useRegistrySeed(dispatch);
  useEffect(() => {
    loadServerConfig();
  }, []);

  return (
    <DispatchContext.Provider value={dispatch}>
      <AppInner state={state} />
    </DispatchContext.Provider>
  );
}

/**
 * Inner component: consumes DispatchContext (via useRun etc.) and renders
 * the full UI. Kept as a separate function so hooks like useRun() are always
 * called inside the Provider tree.
 */
function AppInner({ state }: { state: ReturnType<typeof reducer> }) {
  const headerRef = useRef<HTMLElement>(null!);
  const headerH = useResizeHeight(headerRef, 60);

  const [hiddenStatuses, setHiddenStatuses] = useState<ReadonlySet<Status>>(
    new Set(["na", "unimplemented"] as Status[]),
  );
  function toggleHidden(s: Status) {
    setHiddenStatuses((prev) => {
      const next = new Set(prev);
      next.has(s) ? next.delete(s) : next.add(s);
      return next;
    });
  }

  const [queueOpen, setQueueOpen] = useState(false);
  const onToggleQueue = useCallback(() => setQueueOpen((v) => !v), []);
  const onCloseQueue = useCallback(() => setQueueOpen(false), []);

  // ── Derived state via memoized selectors ──────────────────────────────────
  const totals = selectTotals(state);
  const suiteCompleted = selectSuiteCompleted(state);
  const grand = selectGrand(state);
  const completedCount = selectCompletedCount(state);
  const totalCount = selectTotalCount(state);
  const suiteInfos = selectSuiteInfos(state);
  const suiteStates = selectSuiteStates(state);
  const queue = selectQueue(state);
  const interactive = selectIsInteractive(state);

  // Auto-open queue popover when a new batch of tests becomes active.
  const activeQueueCount = useMemo(
    () =>
      queue.filter((q) => q.state === "queued" || q.state === "running").length,
    [queue],
  );
  const prevActiveRef = useRef(0);
  useEffect(() => {
    if (activeQueueCount > 0 && prevActiveRef.current === 0) {
      setQueueOpen(true);
    }
    prevActiveRef.current = activeQueueCount;
  }, [activeQueueCount]);

  const suites = state.suites;
  const staleSet = new Set(state.staleSuites);
  const isRunning = state.status === "running";

  // CDK has its own flow-based view in the IaC section; keep it out of the
  // SDK service tables (both as a column and as a service row).
  const sdkSuites = useMemo(() => suites.filter((s) => s !== "cdk"), [suites]);
  // All SDK suite IDs from the canonical planned list. Every suite gets a
  // progress card (even before it has emitted any events) so users can see
  // which suites exist and trigger them with the play button.
  const allSdkIds = useMemo(
    () => PLANNED_SUITES.filter((p) => p.category === "sdk").map((p) => p.id),
    [],
  );
  // SDK suites from the planned list that have NOT emitted any events yet.
  // Passed to ServiceTable as planned columns so the grid shows their empty
  // cells — giving a visual preview of all columns before any suite runs.
  const plannedSdk = useMemo(
    () =>
      PLANNED_SUITES.filter(
        (p) => p.category === "sdk" && !suites.includes(p.id),
      ).map((p) => ({ id: p.id, label: p.label })),
    [suites],
  );
  const serviceList = useMemo(
    () =>
      sortedServices(state.services, sdkSuites).filter(
        (svc) => svc.service !== "cdk",
      ),
    [state.services, sdkSuites],
  );
  const cdkSection = state.services.get("cdk");
  const cdkActive =
    suites.includes("cdk") || queue.some((q) => q.suite === "cdk");
  const hasData = serviceList.length > 0 || cdkActive;

  // ── Progress / ETA strings (computed inline — no effect needed) ───────────
  const allSuitesCheckedIn =
    state.queuedSuites.length === 0 && suites.length > 0;
  let progressStr = "";
  let etaStr = "";
  if (completedCount > 0) {
    if (allSuitesCheckedIn && totalCount >= completedCount) {
      progressStr = `${completedCount}\u202f/\u202f${totalCount}`;
    } else {
      progressStr = `${completedCount}`;
    }
    if (
      allSuitesCheckedIn &&
      totalCount > 0 &&
      completedCount > 20 &&
      state.runStartedAt
    ) {
      const elapsed = Date.now() - state.runStartedAt;
      const rate = completedCount / elapsed;
      const remainingMs = (totalCount - completedCount) / rate;
      if (remainingMs > 10_000) {
        etaStr = formatETA(remainingMs);
      }
    }
  }

  // ── Interactive handlers ─────────────────────────────────────────────────
  const triggerRun = useRun();
  const onCellRun = useCallback(
    (filter: { suite: string; group: string; test: string }) => {
      void triggerRun(filter);
    },
    [triggerRun],
  );

  return (
    <div className="min-h-screen bg-gray-50 dark:bg-gray-900 font-sans text-gray-900 dark:text-gray-100 antialiased">
      <AppHeader
        state={state}
        grand={grand}
        totals={totals}
        staleSet={staleSet}
        isRunning={isRunning}
        hasData={hasData}
        progressStr={progressStr}
        etaStr={etaStr}
        hiddenStatuses={hiddenStatuses}
        toggleHidden={toggleHidden}
        headerRef={headerRef}
        interactive={interactive}
        queue={queue}
        queueOpen={queueOpen}
        onToggleQueue={onToggleQueue}
        allSuiteIds={allSdkIds}
        suiteErrors={state.suiteErrors}
        queuedSuites={state.queuedSuites}
      />

      {interactive && (
        <QueuePanel
          queue={queue}
          open={queueOpen}
          onClose={onCloseQueue}
          offsetTop={headerH}
        />
      )}

      <main className="px-5 py-7">
        {/* Empty state */}
        {!hasData && state.status !== "running" && <EmptyState status="idle" />}
        {!hasData && state.status === "running" && (
          <EmptyState status="running" />
        )}

        {/* ── SDK Tests section ── */}
        <div className="mb-5">
          <div className="flex items-center gap-2 mb-3 flex-wrap">
            <h2 className="text-[11px] font-bold text-gray-500 dark:text-gray-400 uppercase tracking-widest shrink-0">
              SDK Tests
            </h2>
            <div className="h-px flex-1 bg-gray-200 dark:bg-gray-700 min-w-4" />
          </div>

          {/* Service anchor nav */}
          {hasData && (
            <nav
              className="flex gap-1.5 flex-wrap"
              aria-label="Jump to service"
            >
              {serviceList.map((svc) => (
                <a
                  key={svc.service}
                  href={`#svc-${svc.service}`}
                  className="text-xs px-2.5 py-1 rounded-full bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 text-gray-500 dark:text-gray-400 hover:border-blue-400 hover:text-blue-600 dark:hover:text-blue-400 transition-colors shadow-sm"
                >
                  {svc.service}
                </a>
              ))}
              {cdkActive && (
                <a
                  href="#svc-cdk"
                  className="text-xs px-2.5 py-1 rounded-full bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 text-gray-500 dark:text-gray-400 hover:border-violet-400 hover:text-violet-600 dark:hover:text-violet-400 transition-colors shadow-sm"
                >
                  cdk
                </a>
              )}
            </nav>
          )}
        </div>

        {/* Suite progress panel — shows all SDK suites, even those that
            haven't been triggered yet. Each card includes a play button. */}
        <SuiteProgressPanel
          queuedSuites={state.queuedSuites}
          suites={suites}
          allSuiteIds={allSdkIds}
          doneSuites={state.doneSuites}
          staleSuites={state.staleSuites}
          suiteCompleted={suiteCompleted}
          suiteTotal={state.suiteTotal}
          suiteStartedAt={state.suiteStartedAt}
          suiteDurationMs={state.suiteDurationMs}
          suitePrevPassCount={state.suitePrevPassCount}
          suiteTotals={totals}
          services={state.services}
          suiteErrors={state.suiteErrors}
          suiteStates={suiteStates}
          suiteHasFullRun={state.suiteHasFullRun}
        />

        {/* Service tables (SDK suites only — CDK is rendered below) */}
        {serviceList.map((svc) => (
          <ServiceTable
            key={svc.service}
            section={svc}
            suites={sdkSuites}
            suiteInfos={suiteInfos}
            plannedSuites={plannedSdk}
            headerH={headerH}
            isRunning={isRunning}
            hiddenStatuses={hiddenStatuses}
            prevStatuses={state.prevStatuses}
            interactive={interactive}
            onCellRun={onCellRun}
          />
        ))}

        {/* ── IaC & Deployment Tools section ── */}
        <CdkFlow
          section={cdkSection}
          active={cdkActive}
          isRunning={isRunning}
          prevStatuses={state.prevStatuses}
          queue={queue}
        />
      </main>

      {hasData && (
        <div className="max-w-7xl mx-auto px-5 pb-10">
          <AppFooter />
        </div>
      )}

      <ServerLogPanel endpoint={state.endpoint} />
    </div>
  );
}

// ─── Service sort helper ──────────────────────────────────────────────────────

function sortedServices(
  services: Map<string, ServiceSection>,
  suites: string[],
): ServiceSection[] {
  return [...services.values()].sort((a, b) => {
    let aFail = 0,
      aPass = 0,
      bFail = 0,
      bPass = 0;
    for (const grp of a.groups.values())
      for (const cells of grp.tests.values())
        for (const s of suites) {
          const st = cells[s]?.status;
          if (st === "pass") aPass++;
          else if (st === "fail" || st === "skip" || st === "unimplemented")
            aFail++;
        }
    for (const grp of b.groups.values())
      for (const cells of grp.tests.values())
        for (const s of suites) {
          const st = cells[s]?.status;
          if (st === "pass") bPass++;
          else if (st === "fail" || st === "skip" || st === "unimplemented")
            bFail++;
        }
    const aAllPass = aFail === 0 && aPass > 0;
    const bAllPass = bFail === 0 && bPass > 0;
    if (aAllPass !== bAllPass) return aAllPass ? 1 : -1;
    return (a.service ?? "").localeCompare(b.service ?? "");
  });
}
