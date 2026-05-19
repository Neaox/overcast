import { useCallback } from "react";
import { useDispatchContext } from "../state/dispatch-context";
import type { QueueEntry } from "../types/index";

export interface RunFilter {
  suite?: string;
  service?: string;
  group?: string;
  test?: string;
  statuses?: string[];
}

/**
 * Returns a stable `triggerRun` function that POSTs a run filter to /run.
 * Returns { ok, batch_id } — ok is true if the run was accepted, batch_id
 * is present in interactive mode.
 *
 * In interactive mode the server returns a `queued` array of QueueEntry
 * objects. This hook automatically dispatches them to the reducer so the
 * result grid shows "queued" cells immediately — before test_start events
 * arrive — giving visual feedback proportional to what was triggered.
 */
export function useRun() {
  const dispatch = useDispatchContext();
  return useCallback(
    async (
      filter: RunFilter = {},
    ): Promise<{ ok: boolean; batch_id?: string }> => {
      const res = await fetch("/run", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(filter),
      }).catch(() => null);
      if (!res?.ok) return { ok: false };
      try {
        const data = await res.json();
        const entries: QueueEntry[] = data.queued ?? [];
        if (entries.length > 0) {
          dispatch({ type: "queued", entries });
        }
        return { ok: true, batch_id: data.batch_id };
      } catch {
        return { ok: res.status === 202 };
      }
    },
    [dispatch],
  );
}
