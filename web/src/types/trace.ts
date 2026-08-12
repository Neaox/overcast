export interface TraceEntry {
  requestId: string
  timestamp: string
  duration: number
  method: string
  path: string
  host: string
  query?: string
  service: string
  operation?: string
  region: string

  requestHeaders: Record<string, string[]>
  requestBody?: string
  requestBodyTruncated?: boolean
  requestSize?: number

  responseHeaders: Record<string, string[]>
  responseBody?: string
  responseBodyTruncated?: boolean
  statusCode: number
  streaming?: boolean

  hops?: TraceHop[]
  logEntries?: TraceLogEntry[]

  awsErrorCode?: string
  awsErrorMessage?: string

  remoteAddr?: string
  userAgent?: string
  referer?: string
  stack?: string
  parentRequestId?: string

  xrayTraceId?: string
  metadata?: Record<string, unknown>
}

export interface TraceHop {
  id: string
  parent?: string
  requestId?: string
  order: number
  callerService: string
  callerOperation?: string
  service: string
  operation: string
  targetUri?: string
  requestHeaders?: Record<string, string[]>
  requestBody?: string
  responseStatus: number
  responseBody?: string
  duration: number
  error?: string
  timestamp: string
  noisy?: boolean
  stack?: string
}

export interface TraceLogEntry {
  level: string
  message: string
  fields?: Record<string, unknown>
  timestamp: string
  hopId?: string
}

export interface TraceSummary {
  requestId: string
  timestamp: string
  method: string
  path: string
  service: string
  operation?: string
  statusCode: number
  duration: number
  internal?: boolean
  hopCount?: number
  logCount?: number
}

export interface TraceListResponse {
  traces: TraceSummary[]
  nextCursor?: string
}

export interface TraceCountResponse {
  count: number
  capacity: number
}

/** Event-bus event captured while a traced request was in flight. */
export interface TraceEvent {
  type: string
  time: string
  source: string
  resourceArn?: string
  payload: unknown
}

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

/**
 * Where in a trace a deep search found its query. Mirrors trace.MatchField —
 * the server's names, not the UI's, so a new field added there shows up here as
 * a type error rather than silently rendering as an unlabelled match.
 */
export type TraceMatchField =
  | "log"
  | "hopError"
  | "hopResponse"
  | "hopRequest"
  | "requestBody"
  | "responseBody"

/**
 * One trace a deep search matched, and where.
 *
 * `before`/`text`/`after` are the excerpt already split around the match, so
 * rendering the highlight is a matter of three spans rather than searching the
 * text again and guessing which occurrence was the one that matched.
 *
 * A match inside a body that is not text carries `binary` and an `offset`
 * instead of an excerpt: rendering CBOR as a string produces mojibake that
 * reads as corruption in the payload rather than an artefact of the search.
 */
export interface TraceMatch {
  requestId: string
  field: TraceMatchField
  hopId?: string
  label: string
  before?: string
  text?: string
  after?: string
  binary?: boolean
  offset?: number
  summary: TraceSummary
}

export interface TraceSearchResponse {
  matches: TraceMatch[]
  /** Resumes the scan. Absent once `done`. */
  nextCursor?: string
  /** The scan reached the oldest retained trace. */
  done: boolean
  /** The scan stopped because the caller went away, not because it finished. */
  cancelled?: boolean
  scanned: number
  remaining: number
}

export interface TraceSearchParams {
  q: string
  cursor?: string
  internal?: boolean
}
