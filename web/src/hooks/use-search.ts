import { useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useEndpoint } from "@/hooks/use-endpoint"
import { runSearch } from "@/lib/search"
import type { SearchResult } from "@/lib/search"

// Ensure all contributors are registered when this hook is first imported
import "@/lib/search-contributors/index"

const DEBOUNCE_MS = 180

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

export function useSearch(): SearchState {
  const queryClient = useQueryClient()
  const endpoint = useEndpoint()
  const [query, setQuery] = useState("")
  const [grouped, setGrouped] = useState<Map<string, SearchResult[]>>(new Map())
  const [isLoading, setIsLoading] = useState(false)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    const trimmed = query.trim()
    if (!trimmed) {
      setGrouped(new Map())
      setIsLoading(false)
      return
    }

    setIsLoading(true)

    const timer = setTimeout(async () => {
      // Cancel any in-flight search from a previous keystroke
      abortRef.current?.abort()
      abortRef.current = new AbortController()

      const controller = abortRef.current
      try {
        const results = await runSearch(trimmed, { queryClient, endpoint })
        // Guard against a slower earlier search resolving after a newer one.
        if (abortRef.current === controller) {
          setGrouped(results)
        }
      } finally {
        if (abortRef.current === controller) {
          setIsLoading(false)
        }
      }
    }, DEBOUNCE_MS)

    return () => {
      clearTimeout(timer)
    }
  }, [query, queryClient, endpoint])

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
      setGrouped(new Map())
    },
  }
}
