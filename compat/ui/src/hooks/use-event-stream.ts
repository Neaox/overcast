import { useEffect } from "react";
import type { Action } from "../state/reducer";
import type { WireEvent } from "../types/index";
import { ReconnectingEventSource } from "../lib/reconnecting-event-source";

/**
 * Bootstraps all data for the compat dashboard:
 * 1. Fetches /results (SSR snapshot) and replays it as reducer events.
 * 2. Opens a reconnecting SSE connection on /events for live updates.
 * 3. Buffers SSE messages that arrive before /results completes to avoid a
 *    race where the SSE replay wipes freshly-loaded snapshot state.
 * 4. Deduplicates events using the FinishedAt timestamp from /results so
 *    replayed SSE events that predate the snapshot are silently dropped.
 * 5. On every reconnect (not just the first connection), re-runs steps 1+3+4
 *    from scratch — see the module doc on ReconnectingEventSource for why a
 *    dropped connection needs this and not just "let EventSource retry":
 *    nothing previously told the reducer (or the user) the stream had gone
 *    stale at all (issue #1184).
 */
export function useEventStream(dispatch: React.Dispatch<Action>): void {
  useEffect(() => {
    // Track the FinishedAt timestamp from the last /results load so we can
    // ignore SSE replay events that predate it (they're already loaded).
    let resultsFinishedAt = "";

    // Buffer SSE events that arrive before /results has been (re-)processed.
    // Without this, a race occurs: the SSE replay (run_reset + run_start +
    // test_results) arrives first while resultsFinishedAt is still ""; then
    // /results fires dispatch({ type:"reset" }) wiping that state; old run_end
    // events re-populate doneSuites; live test_result events increment counts
    // while the suite cards falsely show "All groups finished".
    let sseReady = false;
    let ssePending: string[] = [];

    function applySSEMessage(raw: string) {
      try {
        const parsed = JSON.parse(raw);
        // Skip replayed SSE events that predate the /results snapshot.
        // The server injects a "ts" field (RFC3339Nano) into every event.
        if (
          resultsFinishedAt &&
          parsed.ts &&
          parsed.ts <= resultsFinishedAt
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
      ssePending = [];
    }

    /**
     * Fetches /results and replays it into the reducer, then flushes any SSE
     * messages buffered while that fetch was in flight. Called once on mount
     * and again on every SSE reconnect — a connection that dropped may have
     * missed events, and this re-seed is the only way to know what actually
     * happened while it was down (the server's own replay buffer covers a
     * fresh connection, but re-fetching /results is what re-anchors the
     * dedupe cutoff and guarantees no gap survives silently).
     */
    function bootstrap(): void {
      sseReady = false;
      fetch("/results")
        .then((r) => (r.ok ? r.json() : null))
        .then((data) => {
          if (data) {
            resultsFinishedAt = data.FinishedAt ?? "";
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
    }

    bootstrap();

    const stream = new ReconnectingEventSource({
      url: "/events",
      onMessage: (raw) => {
        if (!sseReady) ssePending.push(raw);
        else applySSEMessage(raw);
      },
      onStatusChange: (status, attempt) =>
        dispatch({ type: "connection_status", status, attempt }),
      onOpen: (priorAttempt) => {
        // priorAttempt > 0 means this open followed one or more failures —
        // a genuine drop-and-recover, not the first connection of the
        // session. Re-seed from /results so nothing missed during the
        // outage is silently absent from the grid.
        if (priorAttempt > 0) bootstrap();
      },
    });

    return () => stream.close();
  }, [dispatch]);
}
