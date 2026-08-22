import { useEffect, useRef } from "react";
import { readPersisted, writePersisted } from "../lib/persisted-storage";

const KEY = "scrollY";

/**
 * Restores the scroll position saved from the last visit once real content
 * has rendered, and keeps saving it as the user scrolls — so a burn-down
 * session picks back up where it left off instead of resetting to the top
 * on every reload (issue #1184).
 *
 * `ready` gates the one-time restore: the dashboard's content streams in
 * asynchronously (registry seed, /results, SSE), so restoring against an
 * empty page would scroll to nowhere. Best-effort only — a dashboard that
 * reflows as data arrives cannot guarantee the exact same pixel, only
 * "roughly where you were".
 */
export function useScrollPersistence(ready: boolean): void {
  const restored = useRef(false);

  useEffect(() => {
    if (!ready || restored.current) return;
    restored.current = true;
    const y = readPersisted<number>(KEY, 0);
    if (y > 0) {
      requestAnimationFrame(() => window.scrollTo(0, y));
    }
  }, [ready]);

  useEffect(() => {
    let frame: number | null = null;
    function onScroll() {
      if (frame !== null) return;
      frame = requestAnimationFrame(() => {
        frame = null;
        writePersisted(KEY, window.scrollY);
      });
    }
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      window.removeEventListener("scroll", onScroll);
      if (frame !== null) cancelAnimationFrame(frame);
    };
  }, []);
}
