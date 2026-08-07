import { queryOptions } from "@tanstack/react-query"
import { debugTrace } from "@/services/api/misc"
import type { TraceListParams } from "@/types"

const EMPTY_PARAMS: TraceListParams = {}

export const debugTraceKeys = {
  all: ["debug-trace"] as const,
  detail: (requestId: string) => [...debugTraceKeys.all, "detail", requestId] as const,
  list: (params?: TraceListParams) => [...debugTraceKeys.all, "list", params ?? EMPTY_PARAMS] as const,
  count: () => [...debugTraceKeys.all, "count"] as const,
}

export function traceDetailQueryOptions(requestId: string) {
  return queryOptions({
    queryKey: debugTraceKeys.detail(requestId),
    queryFn: () => debugTrace.get(requestId),
    enabled: !!requestId,
    retry: false,
  })
}

export function traceListQueryOptions(params?: TraceListParams, enabled = true) {
  return queryOptions({
    queryKey: debugTraceKeys.list(params),
    queryFn: () => debugTrace.list(params),
    enabled,
  })
}

export function traceCountQueryOptions() {
  return queryOptions({
    queryKey: debugTraceKeys.count(),
    queryFn: () => debugTrace.count(),
  })
}
