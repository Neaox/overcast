import { useEffect, useRef, useState } from "react";

/**
 * Observes the height of a DOM element via ResizeObserver.
 * Returns the current height in pixels (defaults to `defaultHeight` before
 * the first measurement).
 */
export function useResizeHeight(
  ref: React.RefObject<Element | null>,
  defaultHeight: number,
): number {
  const [height, setHeight] = useState(defaultHeight);
  // Track whether we've done the initial measurement to avoid a stale closure.
  const initialised = useRef(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(() => {
      setHeight((el as HTMLElement).offsetHeight);
    });
    ro.observe(el);
    // Measure immediately so we don't have to wait for a resize event.
    if (!initialised.current) {
      setHeight((el as HTMLElement).offsetHeight);
      initialised.current = true;
    }
    return () => ro.disconnect();
  }, [ref]);

  return height;
}
