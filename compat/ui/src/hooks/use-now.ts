import { useEffect, useRef, useState } from "react";

/**
 * Returns the current timestamp (ms since epoch), updated on a recurring
 * interval. Uses recursive setTimeout rather than setInterval to avoid
 * overlapping executions, accumulating drift, and silent error continuation.
 */
export function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  const tidRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    function tick() {
      setNow(Date.now());
      tidRef.current = setTimeout(tick, intervalMs);
    }
    tidRef.current = setTimeout(tick, intervalMs);
    return () => {
      if (tidRef.current !== null) clearTimeout(tidRef.current);
    };
  }, [intervalMs]);

  return now;
}
