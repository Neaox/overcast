import { useEffect, useState } from "react";
import {
  Check,
  Copy,
  Loader2,
  Play,
  RefreshCw,
  RotateCcw,
  RotateCw,
  XCircle,
} from "lucide-react";
import { cn } from "../lib/cn";
import { useRun } from "../hooks/use-run";
import { useCancel } from "../hooks/use-cancel";
import { useDispatchContext } from "../state/dispatch-context";
import { RETRY_STATUSES } from "../lib/constants";
import type { RunFilter } from "../hooks/use-run";

// Re-export RunFilter so callers don't need to reach into hooks/
export type { RunFilter };

// ─── RunButton ────────────────────────────────────────────────────────────────

/** A small play/spinner button — triggers a full (re-)run of the given filter. */
export function RunButton({
  filter,
  isRunning,
  title,
  className,
}: {
  filter: RunFilter;
  isRunning: boolean;
  title: string;
  className?: string;
}) {
  return (
    <ActionButton
      isRunning={isRunning}
      title={title}
      className={className}
      idleIcon={<Play size={12} strokeWidth={2.5} />}
      buildFilter={(f) => f}
      baseFilter={filter}
    />
  );
}

// ─── RetryButton ─────────────────────────────────────────────────────────────

/**
 * Re-run button that only renders when there are non-passing tests in scope.
 * Sends the same filter but with statuses=[fail,skip,unimplemented].
 */
export function RetryButton({
  filter,
  hasFailing,
  isRunning,
  title,
  className,
}: {
  filter: RunFilter;
  hasFailing: boolean;
  isRunning: boolean;
  title: string;
  className?: string;
}) {
  return (
    <ActionButton
      isRunning={isRunning}
      title={title}
      className={className}
      idleIcon={<RotateCw size={12} strokeWidth={2.5} />}
      buildFilter={(f) => ({ ...f, statuses: [...RETRY_STATUSES] })}
      baseFilter={filter}
      visible={hasFailing}
    />
  );
}

// ─── Shared base ─────────────────────────────────────────────────────────────

interface ActionButtonProps {
  isRunning: boolean;
  title: string;
  className?: string;
  /** Icon to show in the idle state. */
  idleIcon: React.ReactNode;
  /** Transform the filter before POSTing (e.g. to append statuses). */
  buildFilter: (base: RunFilter) => RunFilter;
  baseFilter: RunFilter;
  /** If false the button renders null (used by RetryButton). */
  visible?: boolean;
}

function ActionButton({
  isRunning,
  title,
  className,
  idleIcon,
  buildFilter,
  baseFilter,
  visible = true,
}: ActionButtonProps) {
  const [pending, setPending] = useState(false);
  const triggerRun = useRun();

  if (!visible) return null;

  const busy = isRunning || pending;

  return (
    <button
      title={busy && !isRunning ? "Starting…" : title}
      disabled={busy}
      onClick={(e) => {
        e.stopPropagation();
        if (busy) return;
        setPending(true);
        void triggerRun(buildFilter(baseFilter)).then((result) => {
          // Always clear pending once the server responds. In legacy mode the
          // button stays disabled via isRunning while the run proceeds. In
          // interactive/batch mode there is no global isRunning signal, so we
          // clear immediately and let the queue panel show ongoing progress.
          setPending(false);
          if (!result.ok) return; // 409 or network error — button re-enables
        });
      }}
      className={cn(
        "inline-flex items-center justify-center rounded transition-colors disabled:opacity-40 disabled:cursor-not-allowed",
        className,
      )}
    >
      {isRunning ? (
        <Loader2 size={12} className="animate-spin" />
      ) : pending ? (
        <RefreshCw size={12} className="animate-spin" />
      ) : (
        idleIcon
      )}
    </button>
  );
}

// ─── CancelAllButton ──────────────────────────────────────────────────────────

/** Cancel all running/queued tests. Only renders when interactive and has queue items. */
export function CancelAllButton({
  queueCount,
  className,
}: {
  queueCount: number;
  className?: string;
}) {
  const [pending, setPending] = useState(false);
  const cancel = useCancel();

  if (queueCount === 0) return null;

  return (
    <button
      title="Cancel all queued & running tests"
      disabled={pending}
      onClick={(e) => {
        e.stopPropagation();
        if (pending) return;
        setPending(true);
        void cancel({ all: true }).finally(() => setPending(false));
      }}
      className={cn(
        "inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-medium transition-colors",
        "bg-red-50 text-red-600 ring-1 ring-red-200 hover:bg-red-100 dark:bg-red-950 dark:text-red-400 dark:ring-red-800 dark:hover:bg-red-900",
        "disabled:opacity-40 disabled:cursor-not-allowed",
        className,
      )}
    >
      {pending ? (
        <Loader2 size={12} className="animate-spin" />
      ) : (
        <XCircle size={12} />
      )}
      Cancel All
    </button>
  );
}

// ─── ResetAllButton ───────────────────────────────────────────────────────────

/** Clear all results and cancel any active tests. Visible whenever there is data. */
export function ResetAllButton({
  hasData,
  className,
}: {
  hasData: boolean;
  className?: string;
}) {
  const [pending, setPending] = useState(false);
  const dispatch = useDispatchContext();

  if (!hasData) return null;

  return (
    <button
      title="Clear all results"
      disabled={pending}
      onClick={(e) => {
        e.stopPropagation();
        if (pending) return;
        setPending(true);
        // Clear results locally — keeps services structure so the grid stays
        // visible with empty placeholder cells. Also tell the server to clear
        // its replay buffer so newly-connecting clients start fresh.
        dispatch({ type: "clear_results" });
        void fetch("/reset", { method: "POST" })
          .catch(() => null)
          .finally(() => setPending(false));
      }}
      className={cn(
        "inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-xs font-medium transition-colors",
        "bg-gray-50 text-gray-500 ring-1 ring-gray-200 hover:bg-gray-100 hover:text-gray-700",
        "dark:bg-gray-800 dark:text-gray-400 dark:ring-gray-700 dark:hover:bg-gray-700 dark:hover:text-gray-200",
        "disabled:opacity-40 disabled:cursor-not-allowed",
        className,
      )}
    >
      {pending ? (
        <Loader2 size={12} className="animate-spin" />
      ) : (
        <RotateCcw size={12} />
      )}
      Reset All
    </button>
  );
}

// ─── CancelCellButton ─────────────────────────────────────────────────────────

/** Small inline cancel button for individual queued/running cells. */
export function CancelCellButton({
  suite,
  group,
  test,
  className,
}: {
  suite: string;
  group: string;
  test: string;
  className?: string;
}) {
  const cancel = useCancel();
  return (
    <button
      title={`Cancel ${test} in ${suite}`}
      onClick={(e) => {
        e.stopPropagation();
        void cancel({ suite, group, test });
      }}
      className={cn(
        "inline-flex items-center justify-center w-4 h-4 rounded-full text-gray-400 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-950 transition-colors",
        className,
      )}
    >
      <XCircle size={10} />
    </button>
  );
}

// ─── RunCellButton ────────────────────────────────────────────────────────────

/** Small inline play button to re-run a single test in a single suite. */
export function RunCellButton({
  suite,
  group,
  test,
  className,
}: {
  suite: string;
  group: string;
  test: string;
  className?: string;
}) {
  const triggerRun = useRun();
  const [pending, setPending] = useState(false);
  return (
    <button
      title={`Re-run ${test} in ${suite}`}
      disabled={pending}
      onClick={(e) => {
        e.stopPropagation();
        if (pending) return;
        setPending(true);
        void triggerRun({ suite, group, test }).finally(() =>
          setPending(false),
        );
      }}
      className={cn(
        "inline-flex items-center justify-center w-4 h-4 rounded-full text-gray-400 hover:text-blue-500 hover:bg-blue-50 dark:hover:bg-blue-950 transition-colors disabled:opacity-40",
        className,
      )}
    >
      {pending ? (
        <Loader2 size={10} className="animate-spin" />
      ) : (
        <Play size={10} />
      )}
    </button>
  );
}

// ─── CopyButton ───────────────────────────────────────────────────────────────

/**
 * Copy-to-clipboard button with a transient "copied!" check-icon state.
 * Used to surface reproduce commands and request IDs on failed tests so
 * developers can paste them into a terminal or log grep without retyping.
 */
export function CopyButton({
  value,
  title,
  className,
  label,
}: {
  value: string;
  title: string;
  className?: string;
  label?: string;
}) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(t);
  }, [copied]);
  return (
    <button
      type="button"
      title={title}
      onClick={(e) => {
        e.stopPropagation();
        navigator.clipboard.writeText(value).then(() => setCopied(true));
      }}
      className={cn(
        "inline-flex items-center gap-1 rounded transition-colors",
        className,
      )}
    >
      {copied ? <Check size={12} /> : <Copy size={12} />}
      {label && <span>{copied ? "copied" : label}</span>}
    </button>
  );
}
