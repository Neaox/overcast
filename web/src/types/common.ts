/** Shape of a single event sent over the /_overcast/events SSE stream. */
export interface StreamEvent {
  type: string
  time: string // ISO-8601
  source: string // "s3", "sqs", "dynamodb", …
  /**
   * ARN of the event's primary resource, when known (mirrors
   * internal/events.Event.ResourceARN on the Go side). Best-effort — many
   * events have no single resource or concern a resource type without an
   * ARN in this emulator's model, and omit this field entirely.
   */
  resourceArn?: string
  payload: unknown
}

/** A single timed phase from the server's startup sequence. */
export interface StartupPhase {
  name: string
  start_ms: number
  duration_ms: number
  environment?: boolean
}

/** Snapshot returned by GET /_overcast/metrics (Go runtime stats). */
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
  docker?: DockerHealth
}

export interface DockerHealth {
  available: boolean
  services: DockerServiceHealth[]
  lastEvent?: string
  lastEventAt?: string
}

export interface DockerServiceHealth {
  service: string
  socket: string
  connected: boolean
  error?: string
  lastSeen?: string
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
  /** Filesystem type per /proc/mounts (e.g. "ext4", "9p"); absent when unreadable. */
  fsType?: string
  /** Coarse class the advisory copy keys on: "native" | "shared" | "unknown". */
  mountClass?: string
}

/**
 * Cumulative storage-layer read/write activity since process start
 * (internal/state.StoreCounters), shown as the Metrics & Health page's
 * "Storage activity" card. `reads`/`writes` are populated by every backend;
 * `readsMemory`/`readsSQLite`/`writesFlushedRows` are populated only for a
 * "hybrid" mode store, the only backend that actually serves reads/writes
 * from two distinct tiers — see the Go type's doc comment for the exact
 * semantics of each field.
 */
export interface StoreCounters {
  reads: number
  writes: number
  readsMemory?: number
  readsSQLite?: number
  writesFlushedRows?: number
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
  /**
   * Optional at this layer even though current servers always send it:
   * older servers (dev-BFF proxying a stale binary) and existing test
   * fixtures omit it, and the page must degrade gracefully rather than
   * crash on `undefined.reads` — see storage-activity.tsx's guard.
   */
  counters?: StoreCounters
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
