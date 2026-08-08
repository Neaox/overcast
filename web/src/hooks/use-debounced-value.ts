import { useEffect, useState } from "react"

/**
 * Returns `value` once it has been stable for `delayMs`.
 *
 * Used to keep fast-changing inputs (e.g. a search box) from refiring
 * queries on every keystroke.
 */
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)

  useEffect(() => {
    const id = window.setTimeout(() => setDebounced(value), delayMs)
    return () => window.clearTimeout(id)
  }, [value, delayMs])

  return debounced
}
