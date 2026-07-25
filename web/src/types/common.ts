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
  environment?: boolean
}

/** Snapshot returned by GET /_metrics (Go runtime stats). */
export interface MetricsSnapshot {
  timestamp: string
  uptime: string
  uptime_secs: number
  start_time: string
  startup_duration_ms: number
  pre_init_ms: number
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

/** Persistent-backend health snapshot (internal/state.PersistentHealth). */
export interface PersistentHealth {
  mode: string
  healthy: boolean
  pendingWrites: number
  lastError?: string
  lastErrorAt?: string
  lastSuccessAt?: string
}

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
    persistent?: PersistentHealth
  }
}

/** One attempted flush of buffered writes (internal/state.DebugFlushRecord). */
export interface DebugFlushRecord {
  timestamp: string
  durationMillis: number
  entries: number
  committed: boolean
  chunks?: number
}

/** Outcome of the startup data-dir fsync probe (internal/state.DataDirProbeResult). */
export interface DataDirProbeResult {
  fsyncMillis: number
  slow: boolean
  probedAt: string
}

/**
 * One reporting store's storage diagnostics, as returned by
 * GET /_debug/metrics's `stores` array (internal/state.DebugMetrics). A
 * *state.NamespacedStore wrapping more than one distinct backend yields more
 * than one entry here — see that type's Go doc comment for why.
 */
export interface DebugMetrics {
  mode: string
  flushHistory?: DebugFlushRecord[]
  seedDurationMillis?: number
  pendingLogBytes?: number
  namespaceRowCounts?: Record<string, number>
  readRetryCount?: number
  readTimeoutCount?: number
  dataDirProbe?: DataDirProbeResult
  /** Live `PRAGMA journal_mode` readback — see internal/router/advisories.go. */
  journalMode?: string
  /** True once the backend has permanently fallen back to memory-only. */
  degraded?: boolean
}

export type AdvisorySeverity = "info" | "warning" | "critical"

/**
 * One server-computed diagnostic surfaced by GET /_debug/metrics's
 * `advisories` array (internal/router/advisories.go). The web UI renders
 * these generically — a future rule added server-side needs no UI change.
 */
export interface Advisory {
  severity: AdvisorySeverity
  code: string
  title: string
  detail: string
  /** Docs-browser-relative path (see web/src/routes/docs.tsx's `path` search param), when set. */
  docsPath?: string
}

/** Response body of GET /_debug/metrics. */
export interface DebugMetricsResponse {
  stores: DebugMetrics[]
  advisories: Advisory[]
}
