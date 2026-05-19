/** Shape of a single event sent over the /_events SSE stream. */
export interface StreamEvent {
  type: string
  time: string // ISO-8601
  source: string // "s3", "sqs", "dynamodb", …
  payload: unknown
}

/** A single timed phase from the server's startup sequence. */
export interface StartupPhase {
  name: string
  start_ms: number
  duration_ms: number
}

/** Snapshot returned by GET /_metrics (Go runtime stats). */
export interface MetricsSnapshot {
  timestamp: string
  uptime: string
  uptime_secs: number
  start_time: string
  startup_duration_ms: number
  startup_phases?: StartupPhase[]
  // memory (bytes)
  heap_alloc_bytes: number
  heap_sys_bytes: number
  heap_inuse_bytes: number
  sys_bytes: number
  stack_inuse_bytes: number
  // GC
  num_gc: number
  gc_pause_last_ms: number
  gc_pause_total_ms: number
  next_gc_bytes: number
  // runtime
  goroutines: number
  go_version: string
  num_cpu: number
}

export type EmulationTier = "full" | "partial" | "inert" | "stub" | "unsupported"

export interface HealthResponse {
  status: string
  timestamp: string
  version: string
  services: string[]
  serviceTiers?: Record<string, EmulationTier>
  serviceGoalTiers?: Record<string, EmulationTier>
  storage: {
    default: string
    serviceOverrides?: Record<string, string>
  }
}
