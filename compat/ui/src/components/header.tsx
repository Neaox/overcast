import { Cloud, CheckCircle2, Loader2, AlertCircle, Clock } from "lucide-react";
import { cn } from "../lib/cn";
import { formatDuration } from "../lib/format";
import { STATUS_CONFIG } from "./status";
import { StatusIcon, CountChip } from "./status";
import { PassRateBar } from "./pass-rate-bar";
import {
  RunButton,
  RetryButton,
  CancelAllButton,
  ResetAllButton,
} from "./run-controls";
import { QueueButton, isQueueEntryActive } from "./queue-panel";
import type { RunState, SuiteTotals, Status, QueueEntry } from "../types/index";

interface AppHeaderProps {
  state: RunState;
  grand: SuiteTotals;
  totals: Record<string, SuiteTotals>;
  staleSet: Set<string>;
  isRunning: boolean;
  hasData: boolean;
  progressStr: string;
  etaStr: string;
  hiddenStatuses: ReadonlySet<Status>;
  toggleHidden: (s: Status) => void;
  headerRef: React.RefObject<HTMLElement>;
  interactive: boolean;
  queue: QueueEntry[];
  queueOpen: boolean;
  onToggleQueue: () => void;
  /** All SDK suite IDs so every suite shows in the header — even errored/idle ones. */
  allSuiteIds: string[];
  suiteErrors: Record<string, string>;
  queuedSuites: string[];
}

export function AppHeader({
  state,
  grand,
  totals,
  staleSet,
  isRunning,
  hasData,
  progressStr,
  etaStr,
  hiddenStatuses,
  toggleHidden,
  headerRef,
  interactive,
  queue,
  queueOpen,
  onToggleQueue,
  allSuiteIds,
  suiteErrors,
  queuedSuites,
}: AppHeaderProps) {
  const activeQueueCount = queue.filter((q) =>
    isQueueEntryActive(q.state),
  ).length;
  return (
    <header
      ref={headerRef}
      className="bg-white dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700 sticky top-0 z-10 shadow-sm"
    >
      <div className="max-w-7xl mx-auto px-5 py-3">
        <div className="flex items-center gap-3 flex-wrap">
          {/* Brand */}
          <div className="flex items-center gap-2 shrink-0">
            <Cloud size={18} className="text-blue-500" strokeWidth={2} />
            <span className="font-semibold text-gray-900 dark:text-gray-100 tracking-tight">
              Overcast Compat
            </span>
          </div>

          {/* Endpoint chip */}
          {state.endpoint && (
            <span className="text-xs bg-gray-100 dark:bg-gray-700 text-gray-500 dark:text-gray-300 font-mono px-2.5 py-0.5 rounded-full border border-gray-200 dark:border-gray-600">
              {state.endpoint}
            </span>
          )}

          {/* Run state indicator */}
          {state.status === "running" && (
            <span className="flex items-center gap-1.5 text-xs text-blue-600 font-medium">
              <Loader2 size={13} className="animate-spin" />
              {progressStr || "Running…"}
              {etaStr && (
                <span className="text-gray-400 dark:text-gray-500 font-normal">
                  {etaStr}
                </span>
              )}
            </span>
          )}
          {state.status === "done" && (
            <span className="flex items-center gap-1.5 text-xs text-green-600 font-medium">
              <CheckCircle2 size={13} strokeWidth={2.5} />
              Done
              {state.runStartedAt && state.runFinishedAt && (
                <span className="text-gray-400 dark:text-gray-500 font-normal">
                  · {formatDuration(state.runFinishedAt - state.runStartedAt)}
                </span>
              )}
            </span>
          )}

          <RetryButton
            filter={{}}
            hasFailing={grand.fail + grand.skip + grand.unimplemented > 0}
            isRunning={isRunning}
            title="Re-run non-passing tests"
            className="p-1.5 text-amber-400 hover:text-amber-600 dark:text-amber-500 dark:hover:text-amber-400 hover:bg-amber-50 dark:hover:bg-amber-950"
          />
          <RunButton
            filter={{}}
            isRunning={isRunning}
            title="Re-run all tests"
            className="p-1.5 text-gray-400 hover:text-blue-500 dark:text-gray-500 dark:hover:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-950"
          />

          {interactive && (
            <QueueButton
              queue={queue}
              open={queueOpen}
              onToggle={onToggleQueue}
            />
          )}
          {interactive && <CancelAllButton queueCount={activeQueueCount} />}
          <ResetAllButton hasData={hasData} />
        </div>

        {/* Per-suite totals — dedicated row so the controls row stays compact */}
        {(allSuiteIds.length > 0 || interactive) && (
          <div className="flex items-center gap-3 flex-wrap mt-2.5">
            {allSuiteIds.map((suite) => {
              const t = totals[suite];
              const isStale = staleSet.has(suite);
              const error = suiteErrors[suite];
              const isQueued = queuedSuites.includes(suite);
              const hasData = t && (t.pass > 0 || t.fail > 0 || t.skip > 0 || t.unimplemented > 0 || t.na > 0);
              return (
                <div
                  key={suite}
                  className={cn(
                    "flex items-center gap-1.5 rounded-full px-2 py-0.5 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 text-xs transition-opacity",
                    isStale && "opacity-50",
                  )}
                  title={
                    error
                      ? `${suite}: ${error}`
                      : isStale
                        ? `${suite} — showing results from previous run`
                        : undefined
                  }
                >
                  <RunButton
                    filter={{ suite }}
                    isRunning={isRunning}
                    title={`Re-run ${suite}`}
                    className="p-0.5 text-gray-300 hover:text-blue-500 dark:text-gray-600 dark:hover:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-950 rounded"
                  />
                  <span className="text-gray-500 dark:text-gray-400 font-mono">
                    {suite}
                    {isStale && (
                      <span className="ml-0.5 text-[9px] uppercase tracking-widest text-gray-300 dark:text-gray-600">
                        prev
                      </span>
                    )}
                  </span>
                  {error ? (
                    <span className="flex items-center gap-0.5 text-[10px] text-red-500 font-medium">
                      <AlertCircle size={9} />
                      error
                    </span>
                  ) : isQueued ? (
                    <span className="flex items-center gap-0.5 text-[10px] text-gray-400">
                      <Clock size={9} />
                      queued
                    </span>
                  ) : hasData ? (
                    <span className="flex gap-0.5">
                      <CountChip status="pass" count={t.pass} />
                      <CountChip status="fail" count={t.fail} />
                      <CountChip status="unimplemented" count={t.unimplemented} />
                      <CountChip status="skip" count={t.skip} />
                      <CountChip status="na" count={t.na} />
                    </span>
                  ) : (
                    <span className="text-[10px] text-gray-300 dark:text-gray-600 italic">
                      not run
                    </span>
                  )}
                </div>
              );
            })}
          </div>
        )}

        {/* Overall pass-rate bar */}
        {grand.pass + grand.fail > 0 && (
          <div className="mt-2.5 max-w-sm">
            <PassRateBar
              pass={grand.pass}
              fail={grand.fail}
              skip={grand.skip}
              unimplemented={grand.unimplemented}
            />
          </div>
        )}

        {/* Status filter toggles */}
        {hasData && (
          <div className="flex items-center gap-1.5 mt-2.5 flex-wrap">
            <span className="text-xs text-gray-400 dark:text-gray-500 mr-0.5">
              Show:
            </span>
            {(["pass", "fail", "unimplemented", "skip", "na"] as const).map(
              (s) => {
                const hidden = hiddenStatuses.has(s);
                const { pillCls, label } = STATUS_CONFIG[s];
                return (
                  <button
                    key={s}
                    onClick={() => toggleHidden(s)}
                    className={cn(
                      "inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ring-1 transition-all",
                      hidden
                        ? "opacity-30 bg-gray-100 dark:bg-gray-700 text-gray-400 dark:text-gray-500 ring-gray-200 dark:ring-gray-600"
                        : pillCls,
                    )}
                  >
                    <StatusIcon status={s} size={11} />
                    {label}
                  </button>
                );
              },
            )}
          </div>
        )}
      </div>
    </header>
  );
}
