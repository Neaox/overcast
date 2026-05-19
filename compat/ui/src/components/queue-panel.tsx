import { useEffect } from "react";
import {
  CheckCircle2,
  XCircle,
  Loader2,
  MinusCircle,
  X,
  ListOrdered,
} from "lucide-react";
import { cn } from "../lib/cn";
import { useCancel } from "../hooks/use-cancel";
import { useDispatchContext } from "../state/dispatch-context";
import type { QueueEntry } from "../types/index";

// How often to check for entries that have exceeded the linger window.
const PRUNE_INTERVAL_MS = 5_000;

export function isQueueEntryActive(state: QueueEntry["state"]) {
  return state === "queued" || state === "running";
}

function EntryIcon({ state }: { state: QueueEntry["state"] }) {
  switch (state) {
    case "running":
      return (
        <Loader2 size={13} className="text-blue-500 animate-spin shrink-0" />
      );
    case "pass":
      return <CheckCircle2 size={13} className="text-green-500 shrink-0" />;
    case "fail":
      return <XCircle size={13} className="text-red-500 shrink-0" />;
    case "cancelled":
      return (
        <MinusCircle
          size={13}
          className="text-gray-400 dark:text-gray-500 shrink-0"
        />
      );
    default: // "queued"
      return (
        <span className="inline-block w-3 h-3 rounded-full bg-blue-200 dark:bg-blue-800 opacity-70 shrink-0 animate-pulse" />
      );
  }
}

function entryLabel(entry: QueueEntry): string {
  if (!entry.group && !entry.test) {
    // Suite-level batch entry
    if (entry.total !== undefined) {
      const { passed = 0, failed = 0, total } = entry;
      return `all tests — ${passed}/${total} passed${failed > 0 ? `, ${failed} failed` : ""}`;
    }
    return "all tests";
  }
  if (!entry.test) return entry.group;
  return `${entry.group} / ${entry.test}`;
}

// ── Queue icon button (rendered in the sticky header) ──────────────────────

export function QueueButton({
  queue,
  open,
  onToggle,
}: {
  queue: QueueEntry[];
  open: boolean;
  onToggle: () => void;
}) {
  const activeCount = queue.filter((q) => isQueueEntryActive(q.state)).length;
  const hasItems = queue.length > 0;

  return (
    <button
      type="button"
      onClick={onToggle}
      title={open ? "Hide queue" : "Show queue"}
      aria-pressed={open}
      className={cn(
        "relative p-1.5 rounded transition-colors",
        open
          ? "text-blue-500 bg-blue-50 dark:bg-blue-950"
          : "text-gray-400 hover:text-blue-500 dark:text-gray-500 dark:hover:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-950",
      )}
    >
      <ListOrdered size={14} />
      {activeCount > 0 && (
        <span className="absolute -top-1 -right-1 min-w-3.5 h-3.5 px-0.5 rounded-full bg-blue-500 text-white text-[9px] font-bold flex items-center justify-center leading-none">
          {activeCount > 9 ? "9+" : activeCount}
        </span>
      )}
      {activeCount === 0 && hasItems && (
        <span className="absolute -top-0.5 -right-0.5 w-2 h-2 rounded-full bg-gray-300 dark:bg-gray-600" />
      )}
    </button>
  );
}

// ── Queue popover panel (fixed overlay anchored below the sticky header) ───

export function QueuePanel({
  queue,
  open,
  onClose,
  offsetTop,
}: {
  queue: QueueEntry[];
  open: boolean;
  onClose: () => void;
  offsetTop: number;
}) {
  const cancel = useCancel();
  const dispatch = useDispatchContext();

  // Prune completed entries after their 60s linger window.
  // Runs regardless of open state so stale entries are always cleaned up.
  // This is a legitimate Effect: it synchronizes with the wall clock.
  useEffect(() => {
    const id = setInterval(
      () => dispatch({ type: "prune_queue" }),
      PRUNE_INTERVAL_MS,
    );
    return () => clearInterval(id);
  }, [dispatch]);

  if (!open) return null;

  const active = queue.filter((q) => isQueueEntryActive(q.state));
  const running = active.filter((q) => q.state === "running").length;
  const queued = active.filter((q) => q.state === "queued").length;

  return (
    <>
      {/* Transparent backdrop — click-outside closes the popover */}
      <div
        className="fixed inset-0 z-20"
        onClick={onClose}
        aria-hidden="true"
      />

      {/* Popover panel */}
      <div
        className="fixed right-4 z-30 w-80 rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 shadow-xl flex flex-col overflow-hidden"
        style={{ top: offsetTop + 8, maxHeight: "min(420px, 65vh)" }}
      >
        {/* Popover header */}
        <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-100 dark:border-gray-700 shrink-0">
          <span className="text-[11px] font-bold text-gray-500 dark:text-gray-400 uppercase tracking-widest flex-1">
            Queue
          </span>
          {running > 0 && (
            <span className="text-[11px] text-blue-500 tabular-nums shrink-0">
              {running} running
            </span>
          )}
          {queued > 0 && (
            <span className="text-[11px] text-gray-400 dark:text-gray-500 tabular-nums shrink-0">
              {running > 0 && "· "}
              {queued} queued
            </span>
          )}
          <button
            type="button"
            title="Close"
            onClick={onClose}
            className="p-0.5 rounded text-gray-300 hover:text-gray-600 dark:text-gray-600 dark:hover:text-gray-300 transition-colors shrink-0"
          >
            <X size={13} />
          </button>
        </div>

        {/* Scrollable entry list */}
        <div className="overflow-y-auto flex-1">
          {queue.length === 0 ? (
            <p className="px-3 py-4 text-xs text-gray-400 dark:text-gray-500 italic text-center">
              Queue is idle — trigger a run to see activity here.
            </p>
          ) : (
            <div className="divide-y divide-gray-100 dark:divide-gray-700">
              {queue.map((entry) => {
                const terminal = !isQueueEntryActive(entry.state);
                return (
                  <div
                    key={`${entry.batch_id}/${entry.suite}/${entry.group}/${entry.test}`}
                    className={cn(
                      "flex items-center gap-2 px-3 py-2 text-xs transition-colors",
                      terminal && "opacity-60",
                    )}
                  >
                    <EntryIcon state={entry.state} />
                    <span className="text-gray-400 dark:text-gray-500 font-mono shrink-0">
                      {entry.suite}
                    </span>
                    <span
                      className={cn(
                        "font-mono truncate",
                        entry.state === "fail"
                          ? "text-red-600 dark:text-red-400"
                          : entry.state === "pass"
                            ? "text-green-600 dark:text-green-400"
                            : "text-gray-600 dark:text-gray-300",
                      )}
                    >
                      {entryLabel(entry)}
                    </span>
                    {isQueueEntryActive(entry.state) && (
                      <button
                        type="button"
                        title="Cancel"
                        onClick={() =>
                          void cancel({
                            suite: entry.suite,
                            group: entry.group,
                            test: entry.test,
                          })
                        }
                        className="ml-auto p-0.5 rounded text-gray-300 hover:text-red-500 dark:text-gray-600 dark:hover:text-red-400 transition-colors shrink-0"
                      >
                        <X size={12} />
                      </button>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </>
  );
}
