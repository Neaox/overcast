import { Wifi, WifiOff, Loader2 } from "lucide-react";
import { cn } from "../lib/cn";
import type { ConnectionInfo } from "../types/index";

/**
 * Live /events connection indicator. Before this, a dropped SSE connection
 * was invisible — the matrix kept showing whatever it last had, with nothing
 * telling the user the data might be stale (issue #1184). Always rendered so
 * "open" is a visible, positive confirmation rather than an absence of a
 * warning — the whole point is to not rely on silence meaning "fine".
 */
export function ConnectionPill({ connection }: { connection: ConnectionInfo }) {
  if (connection.status === "open") {
    return (
      <span
        title="Connected to the compat server"
        className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400 font-medium"
      >
        <Wifi size={12} strokeWidth={2.5} />
        Live
      </span>
    );
  }

  if (connection.status === "connecting") {
    return (
      <span
        title="Connecting to the compat server…"
        className="flex items-center gap-1 text-xs text-gray-400 dark:text-gray-500"
      >
        <Loader2 size={12} className="animate-spin" />
        Connecting…
      </span>
    );
  }

  return (
    <span
      title={`Connection to the compat server dropped — retrying (attempt ${connection.attempt})`}
      className={cn(
        "flex items-center gap-1 text-xs font-medium",
        "text-amber-600 dark:text-amber-400",
      )}
    >
      <WifiOff size={12} strokeWidth={2.5} className="animate-pulse" />
      Reconnecting…
    </span>
  );
}
