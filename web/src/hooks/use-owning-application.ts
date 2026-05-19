/**
 * useOwningApplication — resolves the AppRegistry application that owns a
 * given resource identified by any number of candidate keys, or null if
 * no application claims it.
 *
 * Callers pass an array of candidate keys because different detail pages
 * have different identifiers on hand (ARN, physical ID, bare name). The
 * hook tries each in order against the shared reverse-map cache.
 *
 * Backed by a single react-query cache (appKeys.reverseMap) that fetches
 * every application's associated-resource list once and expands CFN stacks
 * into per-resource entries. All detail pages reuse the same cached map.
 */

import { useQuery } from "@tanstack/react-query"
import {
  applicationReverseMapQueryOptions,
  type OwningApplication,
} from "@/features/applications/data"

export function useOwningApplication(candidates: (string | undefined)[]): {
  app: OwningApplication | null
  isLoading: boolean
} {
  const { data, isLoading } = useQuery(applicationReverseMapQueryOptions())
  if (!data) return { app: null, isLoading }
  for (const key of candidates) {
    if (key && data[key]) return { app: data[key], isLoading }
  }
  return { app: null, isLoading }
}
