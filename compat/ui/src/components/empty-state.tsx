import { Cloud, Loader2 } from "lucide-react";

export function EmptyState({ status }: { status: "idle" | "running" }) {
  if (status === "running") {
    return (
      <div className="flex flex-col items-center justify-center gap-3 py-24 text-gray-400 dark:text-gray-500">
        <Loader2 size={36} className="animate-spin" strokeWidth={1.5} />
        <p className="text-sm font-medium">Running tests…</p>
      </div>
    );
  }

  return (
    <div className="flex flex-col items-center justify-center gap-3 py-24 text-gray-300 dark:text-gray-600">
      <Cloud size={40} strokeWidth={1} />
      <p className="text-sm font-medium text-gray-400 dark:text-gray-500">
        Waiting for a test run…
      </p>
    </div>
  );
}
