/**
 * Request-side types for the tracing UI. The response shapes — TraceEntry,
 * TraceHop, TraceLogEntry, TraceSummary, TraceListResponse, TraceCountResponse,
 * TraceMatch, TraceSearchResponse, TraceOmitReason, TraceMatchField — are
 * generated from the Go structs by cmd/tsgen into api.gen.ts (re-exported via
 * common.ts); only what the client sends lives here.
 */
import type { StreamEvent } from "./api.gen"

/**
 * Event-bus event captured while a traced request was in flight. The
 * by-request endpoint serves the same envelope as the live stream (both go
 * through internal/router/events.go's newSSEEnvelope), so this is that type.
 */
export type TraceEvent = StreamEvent

export interface TraceListParams {
  service?: string
  /**
   * HTTP methods to match. Sent as repeated `method=` params; an entry matches
   * if it matches any of them. Compared case-insensitively by the server.
   */
  method?: string[]
  path?: string
  /**
   * Status classes (`2xx`…`5xx`) or exact codes to match. Sent as repeated
   * `status=` params; an entry matches if it matches any of them.
   */
  status?: string[]
  search?: string
  after?: string
  before?: string
  hopsFor?: string
  limit?: number
}

export interface TraceSearchParams {
  q: string
  cursor?: string
  internal?: boolean
}
