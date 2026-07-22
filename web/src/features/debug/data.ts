import { queryOptions } from "@tanstack/react-query"
import { debugState } from "@/services/api/misc"

export const debugKeys = {
  all: ["debug"] as const,
  state: () => [...debugKeys.all, "state"] as const,
  namespace: (namespace: string) => [...debugKeys.state(), namespace] as const,
}

export function debugStateQueryOptions() {
  return queryOptions({
    queryKey: debugKeys.state(),
    queryFn: () => debugState.list(),
  })
}

export function debugNamespaceQueryOptions(namespace: string) {
  return queryOptions({
    queryKey: debugKeys.namespace(namespace),
    queryFn: () => debugState.namespace(namespace),
    enabled: namespace !== "",
  })
}
