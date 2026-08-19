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
  /**
   * The API call this event was published under, when known (mirrors
   * internal/events.Event.RequestID on the Go side). Every event one request
   * set off carries the same id, which is what lets the Events page link a
   * row to its trace at /debug/traces/$requestId.
   *
   * Best-effort — background work (pollers, timers, container events) has no
   * request behind it and omits this field entirely.
   */
  requestId?: string
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
 * GET /_overcast/debug/metrics's `stores` array (internal/state.DebugMetrics). A
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

/**
 * Where a piece of emulator diagnostic evidence came from.
 *
 * Overcast is allowed to say things AWS never would, but only where it is
 * unmistakably Overcast talking — see docs/dev/architecture.md § "Two things
 * called a bus". The risk that creates is a developer reading a captured
 * container log or an inferred summary, believing real AWS would have handed
 * them the same thing, and writing a fix that depends on a signal production
 * will never produce. Tagging every piece of evidence with its origin is what
 * makes that mistake hard, so the tier is carried on the payload rather than
 * being a blanket "emulator-only" badge on the panel.
 *
 * The vocabulary is deliberately service-neutral. CloudFormation deploy
 * diagnostics is the first consumer; RDS's retained-logs panel — which already
 * draws the same distinction with a bespoke `logSource: "container" |
 * "retained"` field — is the obvious second, and a second vocabulary would put
 * the two panels back to explaining the same idea in different words.
 */
export type DiagnosticProvenance = "aws-api" | "overcast-capture" | "overcast-inference"

export type AdvisorySeverity = "info" | "warning" | "critical"

/**
 * One server-computed diagnostic surfaced by GET /_overcast/debug/metrics's
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

/** Response body of GET /_overcast/debug/metrics. */
export interface DebugMetricsResponse {
  stores: DebugMetrics[]
  advisories: Advisory[]
}
