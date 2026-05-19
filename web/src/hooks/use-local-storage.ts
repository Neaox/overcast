import { useState, useCallback } from "react"

/**
 * Generic hook for state backed by localStorage with JSON serialisation.
 *
 * Handles corrupt/missing values gracefully and catches write failures
 * (e.g. QuotaExceededError in private browsing).
 */
export function useLocalStorage<T>(
  key: string,
  defaultValue: T,
): [T, (value: T | ((prev: T) => T)) => void] {
  const [stored, setStored] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(key)
      return raw !== null ? (JSON.parse(raw) as T) : defaultValue
    } catch {
      return defaultValue
    }
  })

  const setValue = useCallback(
    (value: T | ((prev: T) => T)) => {
      setStored((prev) => {
        const next = value instanceof Function ? value(prev) : value
        try {
          localStorage.setItem(key, JSON.stringify(next))
        } catch {
          // QuotaExceededError or SecurityError in private browsing — state
          // still updates in-memory, next page load falls back to default.
        }
        return next
      })
    },
    [key],
  )

  return [stored, setValue]
}
