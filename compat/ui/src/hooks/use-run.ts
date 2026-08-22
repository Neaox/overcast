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
 *
 * A failed trigger (409 "run already in progress", a network error, or any
 * other non-2xx response) used to return { ok: false } and stop there — a
 * click that visibly did nothing, indistinguishable from a broken UI
 * (issue #1184). It now also dispatches a toast_error carrying the server's
 * own response body, so the caller never has to remember to check `ok`.
 */
export function useRun() {
  const dispatch = useDispatchContext();
  return useCallback(
    async (
      filter: RunFilter = {},
    ): Promise<{ ok: boolean; batch_id?: string }> => {
      let res: Response;
      try {
        res = await fetch("/run", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(filter),
        });
      } catch {
        dispatch({
          type: "toast_error",
          message:
            "Could not reach the compat server to start the run — check it is still running.",
        });
        return { ok: false };
      }
      if (!res.ok) {
        const detail = await errorDetail(res);
        dispatch({
          type: "toast_error",
          message: `Run trigger failed (${res.status}): ${detail}`,
        });
        return { ok: false };
      }
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

/** Best-effort extraction of a human-readable message from a failed /run
 * response. The server answers JSON `{"error": "..."}` (see serveRun in
 * compat/server.go), but this falls back gracefully if that ever changes. */
async function errorDetail(res: Response): Promise<string> {
  try {
    const body = await res.clone().json();
    if (body && typeof body.error === "string" && body.error) {
      return body.error;
    }
  } catch {
    /* not JSON */
  }
  try {
    const text = await res.text();
    if (text) return text;
  } catch {
    /* body already consumed or unreadable */
  }
  return res.statusText || "unknown error";
}
