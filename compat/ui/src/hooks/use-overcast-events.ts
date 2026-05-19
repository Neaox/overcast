import { useEffect, useRef, useState } from "react";

export interface OvercastEvent {
  id: number;
  ts: string;
  source: string;
  type: string;
  raw: Record<string, unknown>;
}

const MAX_EVENTS = 500;

export function useOvercastEvents(endpoint: string, paused: boolean) {
  const [events, setEvents] = useState<OvercastEvent[]>([]);
  const seq = useRef(0);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  useEffect(() => {
    if (!endpoint) return;
    let url: string;
    try {
      url = new URL("/_events", endpoint).toString();
    } catch {
      return;
    }
    const es = new EventSource(url);
    es.onmessage = (e) => {
      if (pausedRef.current) return;
      try {
        const parsed = JSON.parse(e.data) as Record<string, unknown>;
        const type = String(parsed.type ?? parsed.event ?? "");
        // Drop bus heartbeats — they're keep-alive noise, not signal.
        if (type === "heartbeat") return;
        const ev: OvercastEvent = {
          id: ++seq.current,
          ts: String(parsed.time ?? parsed.ts ?? ""),
          source: String(parsed.source ?? ""),
          type,
          raw: parsed,
        };
        setEvents((prev) => {
          const next = prev.length >= MAX_EVENTS ? prev.slice(1) : prev.slice();
          next.push(ev);
          return next;
        });
      } catch {
        /* ignore */
      }
    };
    es.onerror = () => {
      // EventSource auto-reconnects; don't thrash state.
    };
    return () => es.close();
  }, [endpoint]);

  function clear() {
    setEvents([]);
  }

  return { events, clear };
}
