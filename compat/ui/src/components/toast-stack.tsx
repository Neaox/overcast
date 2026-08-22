import { useEffect } from "react";
import { AlertCircle, X } from "lucide-react";
import { useDispatchContext } from "../state/dispatch-context";
import type { Toast } from "../types/index";

const AUTO_DISMISS_MS = 8_000;

/**
 * Fixed-position stack of error toasts (currently only failed run triggers —
 * see hooks/use-run.ts). Each one auto-dismisses itself; a close button lets
 * an impatient reader clear it sooner. Rendered once, at the App root.
 */
export function ToastStack({ toasts }: { toasts: Toast[] }) {
  if (toasts.length === 0) return null;
  return (
    <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2 max-w-sm">
      {toasts.map((t) => (
        <ToastItem key={t.id} toast={t} />
      ))}
    </div>
  );
}

function ToastItem({ toast }: { toast: Toast }) {
  const dispatch = useDispatchContext();

  useEffect(() => {
    const remaining = AUTO_DISMISS_MS - (Date.now() - toast.createdAt);
    const timer = setTimeout(
      () => dispatch({ type: "dismiss_toast", id: toast.id }),
      Math.max(remaining, 0),
    );
    return () => clearTimeout(timer);
  }, [toast.id, toast.createdAt, dispatch]);

  return (
    <div className="flex items-start gap-2 rounded-lg border border-red-200 dark:border-red-800 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 px-3 py-2.5 shadow-lg">
      <AlertCircle size={16} className="shrink-0 mt-0.5 text-red-500" />
      <span className="text-xs leading-snug flex-1">{toast.message}</span>
      <button
        type="button"
        title="Dismiss"
        onClick={() => dispatch({ type: "dismiss_toast", id: toast.id })}
        className="shrink-0 text-red-400 hover:text-red-600 dark:hover:text-red-200 transition-colors"
      >
        <X size={14} />
      </button>
    </div>
  );
}
