import { useEffect } from "react";
import type { Action } from "../state/reducer";
import type { WireEvent } from "../types/index";

/**
 * Bootstraps all data for the compat dashboard:
 * 1. Fetches /results (SSR snapshot) and replays it as reducer events.
 * 2. Opens an EventSource on /events for live updates.
 * 3. Buffers SSE messages that arrive before /results completes to avoid a
 *    race where the SSE replay wipes freshly-loaded snapshot state.
 * 4. Deduplicates events using the FinishedAt timestamp from /results so
 *    replayed SSE events that predate the snapshot are silently dropped.
 */
export function useEventStream(dispatch: React.Dispatch<Action>): void {
  useEffect(() => {
    // Track the FinishedAt timestamp from the last /results load so we can
    // ignore SSE replay events that predate it (they're already loaded).
    const resultsFinishedAt = { current: "" };

    // Buffer SSE events that arrive before /results has been processed.
    // Without this, a race occurs: the SSE replay (run_reset + run_start +
    // test_results) arrives first while resultsFinishedAt is still ""; then
    // /results fires dispatch({ type:"reset" }) wiping that state; old run_end
    // events re-populate doneSuites; live test_result events increment counts
    // while the suite cards falsely show "All groups finished".
    let sseReady = false;
    const ssePending: string[] = [];

    function applySSEMessage(raw: string) {
      try {
        const parsed = JSON.parse(raw);
        // Skip replayed SSE events that predate the /results snapshot.
        // The server injects a "ts" field (RFC3339Nano) into every event.
        if (
          resultsFinishedAt.current &&
          parsed.ts &&
          parsed.ts <= resultsFinishedAt.current
        ) {
          return;
        }
        dispatch({ type: "event", payload: parsed as WireEvent });
      } catch {
        /* ignore malformed events */
      }
    }

    function flushPending() {
      sseReady = true;
      for (const raw of ssePending) applySSEMessage(raw);
      ssePending.length = 0;
    }

    fetch("/results")
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (data) {
          resultsFinishedAt.current = data.FinishedAt ?? "";
          dispatch({ type: "reset" });
          for (const suite of data.Suites ?? []) {
            dispatch({
              type: "event",
              payload: {
                event: "run_start",
                suite: suite.Suite,
                endpoint: data.Endpoint,
                total_tests: (suite.Groups ?? []).reduce(
                  // eslint-disable-next-line @typescript-eslint/no-explicit-any
                  (sum: number, g: any) => sum + (g.Tests?.length ?? 0),
                  0,
                ),
              },
            });
            for (const group of suite.Groups ?? []) {
              for (const t of group.Tests ?? []) {
                dispatch({
                  type: "event",
                  payload: { ...t, event: "test_result" },
                });
              }
            }
            dispatch({
              type: "event",
              payload: { event: "run_end", suite: suite.Suite, ...suite },
            });
          }
          // Synthetic run_complete to transition status to "done".
          dispatch({ type: "event", payload: { event: "run_complete" } });
        }
      })
      .catch(() => {})
      .finally(() => {
        // Flush any SSE events that arrived while /results was in-flight,
        // then switch to live passthrough mode.
        flushPending();
      });

    const es = new EventSource("/events");
    es.onmessage = (e) => {
      if (!sseReady) {
        ssePending.push(e.data);
      } else {
        applySSEMessage(e.data);
      }
    };

    return () => es.close();
  }, [dispatch]);
}
