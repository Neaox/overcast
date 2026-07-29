/**
 * event-buffer — the bounded, chronologically-ordered event store shared by
 * every layer of the client that holds onto stream events.
 *
 * There are three of them and they must agree, or the weakest one silently
 * decides how much history a developer actually gets:
 *
 *  1. the SharedWorker's cross-tab cache (event-stream.worker.ts),
 *  2. the direct-EventSource fallback's cache (event-stream.client.ts),
 *  3. each tab's React Query cache (use-event-stream.ts).
 *
 * Capacity and eviction therefore live here rather than being restated at
 * each layer.
 *
 * ## Eviction order
 *
 * Mirrors the server's two-tier policy (internal/events/history.go): when the
 * buffer is full the oldest *noise* event goes first — per-request HTTP
 * telemetry and connection heartbeats — and only once none is left does the
 * oldest event overall go. Without this the client cap would undo the
 * server's policy: the UI's own polling makes request telemetry by far the
 * highest-volume event type, so a flat FIFO of any size fills with it and
 * throws away exactly the state-change events someone debugging came for.
 *
 * ## Memory pressure
 *
 * Backscroll is a debugging convenience, not state the app needs, so it is
 * the first thing to give memory back. The working capacity is re-derived
 * from the JS heap (see currentCapacity) rather than fixed: it starts lower
 * on a low-RAM device and halves while the heap is near its limit, recovering
 * once there is room again. Every layer calls the same function, so a
 * shrink applies to the worker's cross-tab cache and to each tab's copy.
 */

import { isNoiseEventType } from "@/services/event-types"
import type { StreamEvent } from "@/types"

/**
 * Baseline retention. Matches internal/events.HistoryCapacity — the server
 * replays that many on connect, so a smaller cap here would drop part of the
 * replay on the floor before it ever reached the console.
 */
export const EVENT_BUFFER_CAPACITY = 10_000

/** Floor for pressure-driven shrinking — below this the console stops being useful. */
export const MIN_EVENT_BUFFER_CAPACITY = 500

/** Heap-use fraction at which the buffer starts giving memory back. */
const HIGH_WATER = 0.85
/** Heap-use fraction below which it may grow back towards the baseline. */
const LOW_WATER = 0.6

/** How often the heap is sampled. Reading it per event would dominate the append. */
const PRESSURE_SAMPLE_MS = 2_000

// ─── Heap probe ────────────────────────────────────────────────────────────

export interface HeapReading {
  /** Bytes currently allocated. */
  used: number
  /** Bytes the engine will allow before it starts failing allocations. */
  limit: number
}

export type HeapProbe = () => HeapReading | null

/**
 * `performance.memory` is a Chromium extension, not a standard — there is no
 * portable way to observe heap pressure from script. Where it is missing the
 * buffer simply keeps its baseline capacity, which is already bounded.
 */
function defaultHeapProbe(): HeapReading | null {
  const mem = (
    performance as Performance & {
      memory?: { usedJSHeapSize?: number; jsHeapSizeLimit?: number }
    }
  ).memory
  if (!mem || typeof mem.usedJSHeapSize !== "number" || typeof mem.jsHeapSizeLimit !== "number") {
    return null
  }
  if (mem.jsHeapSizeLimit <= 0) return null
  return { used: mem.usedJSHeapSize, limit: mem.jsHeapSizeLimit }
}

let heapProbe: HeapProbe = defaultHeapProbe

/** Test seam. Returns a function that restores the previous probe. */
export function setHeapProbe(probe: HeapProbe | null): () => void {
  const previous = heapProbe
  heapProbe = probe ?? defaultHeapProbe
  resetCapacityForTests()
  return () => {
    heapProbe = previous
    resetCapacityForTests()
  }
}

// ─── Capacity ──────────────────────────────────────────────────────────────

/**
 * Starting capacity for this device. `navigator.deviceMemory` is coarse
 * (rounded to a power of two, capped at 8) and Chromium-only, but a machine
 * that reports 2 GB should not be asked to hold a 10,000-event backscroll per
 * tab before any pressure signal arrives.
 */
function baselineCapacity(): number {
  const gb = (navigator as Navigator & { deviceMemory?: number }).deviceMemory
  if (typeof gb !== "number" || gb <= 0) return EVENT_BUFFER_CAPACITY
  if (gb <= 2) return 2_500
  if (gb <= 4) return 5_000
  return EVENT_BUFFER_CAPACITY
}

/**
 * The capacity `current` should become given `reading`. Pure, so the policy
 * is testable without a heap to put under pressure.
 *
 * Halving on the way down and doubling on the way up (rather than jumping
 * straight to either bound) keeps a buffer that is borderline from
 * oscillating between full size and the floor on every sample.
 */
export function capacityFor(
  reading: HeapReading | null,
  current: number,
  baseline: number = EVENT_BUFFER_CAPACITY,
): number {
  if (!reading) return baseline

  const ratio = reading.used / reading.limit
  if (ratio >= HIGH_WATER) {
    return Math.max(MIN_EVENT_BUFFER_CAPACITY, Math.floor(current / 2))
  }
  if (ratio <= LOW_WATER) {
    return Math.min(baseline, current * 2)
  }
  return Math.min(current, baseline)
}

let capacity: number | null = null
let sampledAt = 0

/** Discards the memoised capacity. Exported for tests and for setHeapProbe. */
export function resetCapacityForTests(): void {
  capacity = null
  sampledAt = 0
}

/**
 * The capacity to use right now, re-sampled at most every PRESSURE_SAMPLE_MS.
 *
 * Memoised per JS realm, which is what we want: the SharedWorker and each tab
 * have their own heap and each decides for itself how much it can hold.
 */
export function currentCapacity(): number {
  const now = Date.now()
  if (capacity !== null && now - sampledAt < PRESSURE_SAMPLE_MS) return capacity
  sampledAt = now
  capacity = capacityFor(heapProbe(), capacity ?? baselineCapacity(), baselineCapacity())
  return capacity
}

// ─── Append ────────────────────────────────────────────────────────────────

/**
 * Whether `e` is sacrificed first when the buffer is full.
 *
 * `isNoiseEventType` is the mirror of the Go bus classification, and covers
 * request telemetry. Heartbeats are added on top of it because they never
 * reach the Go bus at all — the SSE endpoint writes them straight to the
 * response as connection liveness — so the server has no opinion to mirror,
 * and a 15-second ping is the definition of low signal.
 */
function isEvictableFirst(e: StreamEvent): boolean {
  return e.type === "heartbeat" || isNoiseEventType(e.type)
}

/**
 * Epoch milliseconds for an event.
 *
 * Timestamps cannot be compared as strings. Go formats them with
 * RFC3339Nano, which drops trailing zeros in the fractional part, so
 * "…:05Z" and "…:05.5Z" differ in the character right after the seconds —
 * and "." sorts before "Z", putting the later event first.
 */
function timeOf(e: StreamEvent): number {
  const t = Date.parse(e.time)
  return Number.isNaN(t) ? 0 : t
}

/**
 * Merge `incoming` into `existing`, keeping chronological order and the
 * capacity bound. Both inputs are treated as already ordered; `incoming` is
 * sorted defensively because a batch is assembled from whatever the socket
 * delivered.
 *
 * Returns `existing` itself when there is nothing to do, so a React Query
 * consumer does not re-render on an empty flush.
 */
export function appendEvents(
  existing: readonly StreamEvent[],
  incoming: readonly StreamEvent[],
  capacityOverride?: number,
): StreamEvent[] {
  const cap = capacityOverride ?? currentCapacity()
  if (cap <= 0) return []

  if (incoming.length === 0) {
    return existing.length > cap
      ? evictOldest([...existing], existing.length - cap)
      : (existing as StreamEvent[])
  }

  const batch = [...incoming].sort((a, b) => timeOf(a) - timeOf(b))
  const merged = merge(existing, batch)
  return merged.length > cap ? evictOldest(merged, merged.length - cap) : merged
}

/** Standard merge of two ascending runs, with an append fast path. */
function merge(a: readonly StreamEvent[], b: readonly StreamEvent[]): StreamEvent[] {
  if (a.length === 0) return [...b]
  if (timeOf(b[0]) >= timeOf(a[a.length - 1])) return a.concat(b)

  const out: StreamEvent[] = new Array(a.length + b.length)
  let i = 0
  let j = 0
  let k = 0
  while (i < a.length && j < b.length) {
    // `<` not `<=` keeps existing events ahead of incoming ones on a tie,
    // so a same-millisecond arrival stays in the order the server sent it.
    out[k++] = timeOf(b[j]) < timeOf(a[i]) ? b[j++] : a[i++]
  }
  while (i < a.length) out[k++] = a[i++]
  while (j < b.length) out[k++] = b[j++]
  return out
}

/** Drop `count` events: oldest noise first, then oldest overall. */
function evictOldest(events: StreamEvent[], count: number): StreamEvent[] {
  const dropped = new Set<number>()
  for (let i = 0; i < events.length && dropped.size < count; i++) {
    if (isEvictableFirst(events[i])) dropped.add(i)
  }
  for (let i = 0; i < events.length && dropped.size < count; i++) {
    dropped.add(i)
  }

  const out: StreamEvent[] = []
  for (let i = 0; i < events.length; i++) {
    if (!dropped.has(i)) out.push(events[i])
  }
  return out
}

// ─── Batching store ────────────────────────────────────────────────────────

/**
 * How long events are held before being delivered to subscribers.
 *
 * The server replays its whole history buffer on connect, so a fresh page
 * load arrives as thousands of SSE frames in quick succession. Forwarding
 * each one individually costs a postMessage, a cache write and a
 * query-invalidation pass *per event*; batching makes a fresh load
 * proportional to the number of flush windows instead. 50 ms is well under
 * the threshold where the console reads as anything but live.
 */
export const FLUSH_INTERVAL_MS = 50

/**
 * A capacity-bounded event store that hands new events to `onFlush` in
 * batches. Used by the SharedWorker and by the no-SharedWorker fallback.
 *
 * Events are added to the retained buffer *at flush time*, not on arrival.
 * That is what makes `snapshot()` and the flushed batch disjoint: a tab that
 * subscribes mid-window gets the snapshot, then the batch, and sees every
 * event exactly once.
 */
export class EventBatcher {
  #events: StreamEvent[] = []
  #pending: StreamEvent[] = []
  #timer: ReturnType<typeof setTimeout> | null = null
  readonly #onFlush: (batch: StreamEvent[]) => void

  constructor(onFlush: (batch: StreamEvent[]) => void) {
    this.#onFlush = onFlush
  }

  /** Queue an event for the next flush. */
  push(event: StreamEvent): void {
    this.#pending.push(event)
    if (this.#timer === null) {
      this.#timer = setTimeout(() => this.flush(), FLUSH_INTERVAL_MS)
    }
  }

  /** Deliver everything queued, retaining all but heartbeats. */
  flush(): void {
    if (this.#timer !== null) {
      clearTimeout(this.#timer)
      this.#timer = null
    }
    if (this.#pending.length === 0) return

    const batch = this.#pending
    this.#pending = []
    // Heartbeats are forwarded so the UI can show connection liveness, but
    // never retained: replaying a ping to a tab that connects later says
    // nothing about a connection that tab does not share.
    this.#events = appendEvents(
      this.#events,
      batch.filter((e) => e.type !== "heartbeat"),
    )
    this.#onFlush(batch)
  }

  /** A copy of the retained history, oldest first. */
  snapshot(): StreamEvent[] {
    return [...this.#events]
  }

  /** Drop retained history and anything queued. */
  clear(): void {
    if (this.#timer !== null) {
      clearTimeout(this.#timer)
      this.#timer = null
    }
    this.#events = []
    this.#pending = []
  }
}
