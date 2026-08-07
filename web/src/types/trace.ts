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

export interface TraceListParams {
  service?: string
  method?: string
  path?: string
  status?: string
  search?: string
  after?: string
  before?: string
  hopsFor?: string
  limit?: number
}
