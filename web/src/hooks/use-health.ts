/**
 * The shared `/_health` query.
 *
 * Every caller reads it through this one `queryOptions` so they hit the same
 * `["health"]` cache entry the connection gate has already populated, rather
 * than each firing its own request on mount.
 */

import { queryOptions } from "@tanstack/react-query"
import { health } from "@/services/api"

export const healthQueryOptions = queryOptions({
  queryKey: ["health"],
  queryFn: () => health.check(),
  staleTime: 30_000,
  retry: 2,
})
