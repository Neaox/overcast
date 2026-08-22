import { useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useEndpoint } from "@/hooks/use-endpoint"
import { runSearch } from "@/lib/search"
import type { SearchResult } from "@/lib/search"

// Ensure all contributors are registered when this hook is first imported
import "@/lib/search-contributors/index"

const DEBOUNCE_MS = 180

/** Shared empty map, so a blank query keeps handing back the same reference. */
const NO_RESULTS: Map<string, SearchResult[]> = new Map()

export interface SearchState {
  query: string
  setQuery: (q: string) => void
  /** Results grouped by serviceKey. Empty map when query is blank. */
  grouped: Map<string, SearchResult[]>
  /** Flat ordered list of all results for keyboard navigation. */
  flat: SearchResult[]
  isLoading: boolean
  clear: () => void
}

/**
 * The last search that finished, together with the query it answered.
 *
 * `grouped` and `isLoading` are both read off this single value instead of
 * living in state of their own: "the results on hand do not answer what is
 * typed" *is* the loading condition, and a blank box has nothing to show. Both
 * therefore derive during render, which is what keeps the debounce effect from
 * having to call setState synchronously to hold the two in step.
 */
interface Settled {
  query: string
  grouped: Map<string, SearchResult[]>
}

const NOTHING_SETTLED: Settled = { query: "", grouped: NO_RESULTS }

export function useSearch(): SearchState {
  const queryClient = useQueryClient()
  const endpoint = useEndpoint()
  const [query, setQuery] = useState("")
  const [settled, setSettled] = useState<Settled>(NOTHING_SETTLED)
  const abortRef = useRef<AbortController | null>(null)

  const trimmed = query.trim()

  useEffect(() => {
    if (!trimmed) {
      // Nothing to search. Drop any in-flight search on the floor rather than
      // letting it land in a box the user has already emptied.
      abortRef.current?.abort()
      abortRef.current = null
      return
    }

    const timer = setTimeout(() => {
      // Cancel any in-flight search from a previous keystroke.
      abortRef.current?.abort()
      const controller = new AbortController()
      abortRef.current = controller

      void (async () => {
        let results: Map<string, SearchResult[]> | undefined
        try {
          results = await runSearch(trimmed, {
            queryClient,
            endpoint,
            signal: controller.signal,
          })
        } finally {
          // Recording the query as settled is what clears the spinner, so it has
          // to happen when the search fails too — otherwise a throwing
          // contributor leaves the palette spinning for ever. Holding on to the
          // previous results in that case is what a failed search always did.
          //
          // The identity check guards against a slower earlier search resolving
          // after a newer one.
          if (abortRef.current === controller) {
            const found = results
            setSettled((prev) => ({ query: trimmed, grouped: found ?? prev.grouped }))
          }
        }
      })()
    }, DEBOUNCE_MS)

    return () => {
      clearTimeout(timer)
    }
  }, [trimmed, queryClient, endpoint])

  // Derived, never written back from the effect: a blank box shows nothing, and
  // anything typed counts as loading until a search for exactly that text has
  // settled. Results from the previous query stay on screen meanwhile.
  const grouped = trimmed ? settled.grouped : NO_RESULTS
  const isLoading = trimmed !== "" && settled.query !== trimmed

  const flat: SearchResult[] = []
  for (const items of grouped.values()) flat.push(...items)

  return {
    query,
    setQuery,
    grouped,
    flat,
    isLoading,
    clear: () => {
      setQuery("")
      setSettled(NOTHING_SETTLED)
    },
  }
}
