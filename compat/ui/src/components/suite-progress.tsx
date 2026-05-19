import { useMemo, useState } from "react";
import {
  AlertCircle,
  CheckCircle2,
  Clock,
  Loader2,
  Minus,
  Play,
  XCircle,
} from "lucide-react";
import { tv } from "tailwind-variants";
import { cn } from "../lib/cn";
import { formatDuration, formatETA } from "../lib/format";
import { useNow } from "../hooks/use-now";
import { useRun } from "../hooks/use-run";
import type { ServiceSection, SuiteInfo, SuiteTotals } from "../types/index";

// ─── SuiteCard ────────────────────────────────────────────────────────────────

export function SuiteCard({
  suite,
  info,
  activeGroups,
  startedAt,
  now,
  stale,
  durationMs,
  passDelta,
  totals,
  suiteState,
  hasFullRun = false,
}: {
  suite: string;
  info: SuiteInfo;
  activeGroups: string[];
  startedAt: number | null;
  now: number;
  stale: boolean;
  /** Duration of the completed suite run in ms. undefined = not yet known. */
  durationMs?: number;
  /**
   * Change in passing tests vs. previous run.
   * Positive = more passing, negative = regressions, null = no comparison available.
   */
  passDelta: number | null;
  /** Per-status counts from the result grid. */
  totals?: SuiteTotals;
  /** Interactive mode suite process state. */
  suiteState?: "building" | "ready" | "busy" | "error";
  /** Whether a full (all-tests) run has ever completed for this suite. */
  hasFullRun?: boolean;
}) {
  const { done: rawDone, queued, completed, total } = info;
  // In interactive mode, if the suite process is busy, the suite is not truly
  // "done" yet — override to prevent the card showing conflicting state while
  // tests are running.
  const activelyRunning = suiteState === "busy";
  const done = rawDone && !activelyRunning;
  // A suite is "idle" when its process is ready but nothing has ever run,
  // OR when a batch completed but zero tests actually executed.
  const idle =
    (!done && !queued && !activelyRunning && completed === 0) ||
    (done && completed === 0);
  const safePct = total > 0 && completed <= total;
  // Round down so progress never shows 100% while tests are still running.
  const pct = safePct
    ? completed === 0
      ? 0
      : Math.floor((completed / total) * 100)
    : null;
  const hasFails = (totals?.fail ?? 0) > 0;
  const skipCount = (totals?.skip ?? 0) + (totals?.unimplemented ?? 0);
  const passCount = totals?.pass ?? 0;
  const failCount = totals?.fail ?? 0;
  const allRan = total > 0 && completed >= total;
  // Partial run: done, something ran, but not the full suite.
  const partial = done && !idle && !allRan;
  const cardState = queued
    ? "queued"
    : idle
      ? "idle"
      : done
        ? hasFails
          ? "failed"
          : partial
            ? "partial"
            : "done"
        : "running";

  // Play button for triggering this specific suite.
  const triggerRun = useRun();
  const [playPending, setPlayPending] = useState(false);
  const showPlay = !queued && cardState !== "running";

  function handlePlay(e: React.MouseEvent) {
    e.stopPropagation();
    if (playPending) return;
    setPlayPending(true);
    void triggerRun({ suite }).finally(() => setPlayPending(false));
  }

  let etaStr = "";
  if (!done && !queued && completed > 5 && total > 0 && startedAt) {
    const elapsed = now - startedAt;
    const rate = completed / elapsed; // tests per ms
    const remainingMs = (total - completed) / rate;
    if (remainingMs > 10_000) {
      etaStr = formatETA(remainingMs);
    }
  }

  return (
    <div className={card({ state: cardState, stale })}>
      {/* Header row */}
      <div className="flex items-center gap-1.5 min-w-0">
        {queued ? (
          <Clock size={12} className="text-gray-400 shrink-0" />
        ) : idle ? (
          <Minus size={12} className="text-gray-400 shrink-0" />
        ) : done && hasFails ? (
          <XCircle size={12} className="text-red-500 shrink-0" />
        ) : done ? (
          <CheckCircle2 size={12} className="text-green-500 shrink-0" />
        ) : (
          <Loader2 size={12} className="text-blue-500 animate-spin shrink-0" />
        )}
        <span className="text-xs font-semibold text-gray-700 dark:text-gray-200 font-mono truncate flex-1">
          {suite}
        </span>
        {suiteState && <SuiteStateIndicator state={suiteState} />}
        {showPlay && (
          <button
            title={`Run ${suite}`}
            disabled={playPending}
            onClick={handlePlay}
            className="inline-flex items-center justify-center rounded p-0.5 text-gray-400 hover:text-blue-500 hover:bg-blue-50 dark:hover:text-blue-400 dark:hover:bg-blue-950 transition-colors shrink-0 disabled:opacity-40"
          >
            {playPending ? (
              <Loader2 size={11} className="animate-spin" />
            ) : (
              <Play size={11} strokeWidth={2.5} />
            )}
          </button>
        )}
        {queued && (
          <span className="text-[9px] uppercase tracking-widest text-gray-400 dark:text-gray-500 shrink-0">
            queued
          </span>
        )}
        {idle && (
          <span className="text-[9px] uppercase tracking-widest text-gray-400 dark:text-gray-500 shrink-0">
            not run
          </span>
        )}
        {done && hasFails && (
          <span className="text-[9px] uppercase tracking-widest text-red-500 shrink-0">
            {totals!.fail} failed
          </span>
        )}
        {done && !hasFails && !partial && (
          <span className="text-[9px] uppercase tracking-widest text-green-500 shrink-0">
            done
          </span>
        )}
        {partial && !hasFails && (
          <span className="text-[9px] uppercase tracking-widest text-green-600 dark:text-green-400 shrink-0">
            {completed} ran
          </span>
        )}
        {skipCount > 0 && (done || partial) && (
          <span className="text-[9px] uppercase tracking-widest text-gray-400 dark:text-gray-500 shrink-0">
            {skipCount} skip
          </span>
        )}
        {!queued && !done && etaStr && (
          <span className="text-[9px] text-gray-400 dark:text-gray-500 tabular-nums shrink-0">
            {etaStr}
          </span>
        )}
      </div>

      {/* Progress bar + count.
          For partial runs only show when we have a known full-suite total to
          contextualise the sliver against. Without a prior full run the bar
          would be an almost-invisible 0% and mean nothing. */}
      {!queued && !idle && (!partial || hasFullRun) && (
        <div className="flex flex-col gap-1">
          {total > 0 && safePct ? (
            <>
              <div className="h-1 bg-gray-100 dark:bg-gray-700 rounded-full overflow-hidden">
                <div
                  className={cn(
                    "h-full rounded-full transition-all duration-300",
                    done
                      ? hasFails
                        ? "bg-red-500"
                        : "bg-green-500"
                      : "bg-blue-500",
                  )}
                  style={{ width: `${pct ?? 0}%` }}
                />
              </div>
              <div className="flex items-center justify-between gap-1">
                <span className="text-[10px] text-gray-400 dark:text-gray-500 tabular-nums">
                  {completed} / {total}
                </span>
                {pct !== null && (
                  <span
                    className={cn(
                      "text-[10px] font-medium tabular-nums",
                      done
                        ? hasFails
                          ? "text-red-500"
                          : "text-green-500"
                        : "text-blue-400",
                    )}
                  >
                    {pct === 0 ? "<1" : pct}%
                  </span>
                )}
              </div>
            </>
          ) : completed > 0 ? (
            <span className="text-[10px] text-gray-400 dark:text-gray-500 tabular-nums">
              {completed} tests
            </span>
          ) : null}
        </div>
      )}

      {/* Currently active groups */}
      {!done && !queued && activeGroups.length > 0 && (
        <div className="flex flex-col gap-1">
          <span className="text-[9px] uppercase tracking-widest text-gray-400 dark:text-gray-500">
            Running now
          </span>
          <div className="flex flex-wrap gap-1">
            {activeGroups.slice(0, 6).map((g) => (
              <span
                key={g}
                className="text-[10px] bg-blue-50 dark:bg-blue-900/30 text-blue-600 dark:text-blue-400 px-1.5 py-0.5 rounded-md font-mono leading-tight"
              >
                {g}
              </span>
            ))}
            {activeGroups.length > 6 && (
              <span className="text-[10px] text-gray-400 dark:text-gray-500">
                +{activeGroups.length - 6} more
              </span>
            )}
          </div>
        </div>
      )}

      {/* Done footer: pass/fail/skip counts + duration */}
      {done && (
        <div className="flex items-center gap-2 flex-wrap">
          {hasFails ? (
            <div className="flex items-center gap-1 flex-wrap gap-x-3 gap-y-0.5">
              <span className="flex items-center gap-0.5">
                <XCircle size={10} className="text-red-400 shrink-0" />
                <span className="text-[10px] text-red-500 font-medium">{totals!.fail} fail</span>
              </span>
              <span className="flex items-center gap-0.5">
                <CheckCircle2 size={10} className="text-green-400 shrink-0" />
                <span className="text-[10px] text-green-500 font-medium">{totals!.pass} pass</span>
              </span>
              {skipCount > 0 && (
                <span className="text-[10px] text-gray-400">
                  {skipCount} skip
                </span>
              )}
            </div>
          ) : allRan ? (
            <div className="flex items-center gap-1 flex-wrap gap-x-3 gap-y-0.5">
              <CheckCircle2 size={10} className="text-green-400 shrink-0" />
              <span className="text-[10px] text-green-500 font-medium">
                {totals!.pass} pass
              </span>
              {skipCount > 0 && (
                <span className="text-[10px] text-gray-400">
                  {skipCount} skip
                </span>
              )}
            </div>
          ) : (
            <div className="flex items-center gap-1 flex-wrap gap-x-3 gap-y-0.5">
              <CheckCircle2 size={10} className="text-green-400 shrink-0" />
              <span className="text-[10px] text-green-500 font-medium">
                {completed} / {total} ran
              </span>
              {skipCount > 0 && (
                <span className="text-[10px] text-gray-400">
                  {skipCount} skip
                </span>
              )}
            </div>
          )}
          {durationMs !== undefined && (
            <span className="text-[10px] text-gray-400 dark:text-gray-500 tabular-nums ml-auto">
              {formatDuration(durationMs)}
            </span>
          )}
        </div>
      )}

      {/* Pass delta vs previous run */}
      {passDelta !== null && passDelta !== 0 && (
        <div
          className={cn(
            "flex items-center gap-0.5 text-[10px] font-medium tabular-nums",
            passDelta > 0 ? "text-green-500" : "text-red-500",
          )}
        >
          <span className="text-[11px] leading-none">
            {passDelta > 0 ? "↑" : "↓"}
          </span>
          <span>{Math.abs(passDelta)} passing vs last run</span>
        </div>
      )}

      {stale && (
        <span className="text-[9px] uppercase tracking-widest text-amber-500">
          prev run
        </span>
      )}
    </div>
  );
}

// ─── SuiteStateIndicator ──────────────────────────────────────────────────────

function SuiteStateIndicator({
  state,
}: {
  state: "building" | "ready" | "busy" | "error";
}) {
  switch (state) {
    case "building":
      return (
        <span className="flex items-center gap-1 text-[9px] uppercase tracking-widest text-blue-500 shrink-0">
          <span className="inline-block w-1.5 h-1.5 rounded-full bg-blue-400 animate-pulse" />
          Building…
        </span>
      );
    case "ready":
      return (
        <span className="flex items-center gap-1 text-[9px] uppercase tracking-widest text-green-500 shrink-0">
          <span className="inline-block w-1.5 h-1.5 rounded-full bg-green-400" />
          Ready
        </span>
      );
    case "busy":
      return (
        <span className="flex items-center gap-1 text-[9px] uppercase tracking-widest text-amber-500 shrink-0">
          <Loader2 size={10} className="animate-spin" />
          Running…
        </span>
      );
    case "error":
      return (
        <span className="flex items-center gap-1 text-[9px] uppercase tracking-widest text-red-500 shrink-0">
          <span className="inline-block w-1.5 h-1.5 rounded-full bg-red-400" />
          Error
        </span>
      );
  }
}

// ─── SuiteErrorCard ───────────────────────────────────────────────────────────

function SuiteErrorCard({ suite, error }: { suite: string; error: string }) {
  // Show the last meaningful line of the error (often the most useful part).
  const lines = error.split("\n").filter((l) => l.trim());
  const summary = lines[lines.length - 1] ?? error;
  const hasMore = lines.length > 1;

  return (
    <div className={card({ state: "error" })}>
      <div className="flex items-center gap-1.5 min-w-0">
        <AlertCircle size={12} className="text-red-500 shrink-0" />
        <span className="text-xs font-semibold text-gray-700 dark:text-gray-200 font-mono truncate flex-1">
          {suite}
        </span>
        <span className="text-[9px] uppercase tracking-widest text-red-500 shrink-0">
          error
        </span>
      </div>
      <p
        className="text-[10px] text-red-600 dark:text-red-400 font-mono leading-snug line-clamp-3 break-all"
        title={error}
      >
        {hasMore ? `…${summary}` : summary}
      </p>
    </div>
  );
}

// ─── SuiteProgressPanel ───────────────────────────────────────────────────────

export function SuiteProgressPanel({
  queuedSuites,
  suites,
  allSuiteIds,
  doneSuites,
  staleSuites,
  suiteCompleted,
  suiteTotal,
  suiteStartedAt,
  suiteDurationMs,
  suitePrevPassCount,
  suiteTotals,
  services,
  suiteErrors,
  suiteStates,
  suiteHasFullRun,
}: {
  queuedSuites: string[];
  suites: string[];
  /** Every SDK suite ID from the canonical planned list. Idle cards are
   * rendered for suites that haven't emitted any events yet, so users see
   * every available suite and can trigger it via the play button. */
  allSuiteIds: string[];
  doneSuites: string[];
  staleSuites: string[];
  suiteCompleted: Record<string, number>;
  suiteTotal: Record<string, number>;
  suiteStartedAt: Record<string, number>;
  suiteDurationMs: Record<string, number>;
  suitePrevPassCount: Record<string, number>;
  suiteTotals: Record<string, SuiteTotals>;
  services: Map<string, ServiceSection>;
  suiteErrors: Record<string, string>;
  suiteStates: Record<string, "building" | "ready" | "busy" | "error">;
  suiteHasFullRun: Record<string, boolean>;
}) {
  const now = useNow(2000);
  const staleSet = new Set(staleSuites);
  const doneSet = new Set(doneSuites);
  const queuedSet = new Set(queuedSuites);
  const activeSet = new Set(suites);

  // Derive which groups are currently running per suite.
  const activeGroups = useMemo<Record<string, string[]>>(() => {
    const result: Record<string, string[]> = {};
    for (const suite of suites) {
      const groups: string[] = [];
      for (const svc of services.values()) {
        for (const [grpName, grp] of svc.groups) {
          if (
            [...grp.tests.values()].some((c) => c[suite]?.status === "running")
          ) {
            groups.push(grpName);
          }
        }
      }
      result[suite] = groups;
    }
    return result;
  }, [services, suites]);

  const errorMap = suiteErrors;

  if (allSuiteIds.length === 0) return null;

  return (
    <div className="mb-6">
      <div className="flex items-center gap-2 mb-3">
        <span className="text-[11px] font-bold text-gray-500 dark:text-gray-400 uppercase tracking-widest shrink-0">
          Suite Progress
        </span>
        <div className="h-px flex-1 bg-gray-200 dark:bg-gray-700" />
      </div>
      <div className="flex gap-3 flex-wrap">
        {allSuiteIds.map((s) => {
          if (errorMap[s]) {
            return <SuiteErrorCard key={s} suite={s} error={errorMap[s]} />;
          }
          if (queuedSet.has(s)) {
            return (
              <SuiteCard
                key={s}
                suite={s}
                info={{ done: false, queued: true, completed: 0, total: 0 }}
                activeGroups={[]}
                startedAt={null}
                now={now}
                stale={false}
                durationMs={undefined}
                passDelta={null}
              />
            );
          }
          if (activeSet.has(s)) {
            const isDone = doneSet.has(s);
            const prev = suitePrevPassCount[s];
            const currentPass = suiteTotals[s]?.pass ?? 0;
            const passDelta =
              isDone && prev !== undefined ? currentPass - prev : null;
            return (
              <SuiteCard
                key={s}
                suite={s}
                info={{
                  done: isDone,
                  queued: false,
                  completed: suiteCompleted[s] ?? 0,
                  total: suiteTotal[s] ?? 0,
                }}
                activeGroups={activeGroups[s] ?? []}
                startedAt={suiteStartedAt[s] ?? null}
                now={now}
                stale={staleSet.has(s)}
                durationMs={suiteDurationMs[s]}
                passDelta={passDelta}
                totals={suiteTotals[s]}
                suiteState={suiteStates[s]}
                hasFullRun={suiteHasFullRun[s] ?? false}
              />
            );
          }
          // Suite not yet triggered — render an idle card with a play button.
          return (
            <SuiteCard
              key={s}
              suite={s}
              info={{ done: false, queued: false, completed: 0, total: 0 }}
              activeGroups={[]}
              startedAt={null}
              now={now}
              stale={false}
              durationMs={undefined}
              passDelta={null}
            />
          );
        })}
      </div>
    </div>
  );
}

const card = tv({
  base: "flex flex-col gap-2 rounded-xl border px-3.5 py-2.5 bg-white dark:bg-gray-800 shadow-sm min-w-44 max-w-60",
  variants: {
    state: {
      queued: "border-gray-200 dark:border-gray-700 opacity-60",
      idle: "border-gray-200 dark:border-gray-700",
      done: "border-green-200 dark:border-green-800",
      partial: "border-yellow-200 dark:border-yellow-800",
      failed: "border-red-200 dark:border-red-800",
      running: "border-blue-200 dark:border-blue-800",
      error: "border-red-300 dark:border-red-800",
    },
    stale: {
      true: "opacity-50",
    },
  },
});
