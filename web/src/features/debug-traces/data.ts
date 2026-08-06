import { queryOptions, useQuery } from "@tanstack/react-query"
import { debugTrace } from "@/services/api/misc"
import type { TraceListParams } from "@/types"

export const debugTraceKeys = {
  all: ["debug-trace"] as const,
  detail: (requestId: string) => [...debugTraceKeys.all, "detail", requestId] as const,
  list: (params?: TraceListParams) => [...debugTraceKeys.all, "list", params ?? {}] as const,
  count: () => [...debugTraceKeys.all, "count"] as const,
}

export function traceDetailQueryOptions(requestId: string) {
  return queryOptions({
    queryKey: debugTraceKeys.detail(requestId),
    queryFn: () => debugTrace.get(requestId),
    enabled: !!requestId,
  })
}

export function useTraceDetail(requestId: string) {
  return useQuery(traceDetailQueryOptions(requestId))
}

export function traceListQueryOptions(params?: TraceListParams) {
  return queryOptions({
    queryKey: debugTraceKeys.list(params),
    queryFn: () => debugTrace.list(params),
  })
}

export function useTraceList(params?: TraceListParams) {
  return useQuery(traceListQueryOptions(params))
}

export function traceCountQueryOptions() {
  return queryOptions({
    queryKey: debugTraceKeys.count(),
    queryFn: () => debugTrace.count(),
  })
}

export function useTraceCount() {
  return useQuery(traceCountQueryOptions())
}
