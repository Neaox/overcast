import { afterEach, describe, expect, it, vi } from "vitest"
import {
  appendEvents,
  capacityFor,
  EventBatcher,
  EVENT_BUFFER_CAPACITY,
  FLUSH_INTERVAL_MS,
  flushIntervalFor,
  MAX_FLUSH_INTERVAL_MS,
  MIN_EVENT_BUFFER_CAPACITY,
  setHeapProbe,
} from "./event-buffer"
import type { StreamEvent } from "@/types"

/** Build an event at `seconds` past a fixed epoch. */
function ev(type: string, seconds: number, source = "s3"): StreamEvent {
  const time = new Date(Date.UTC(2026, 0, 1, 0, 0, seconds)).toISOString()
  return { type, time, source, payload: { seq: seconds } }
}

function noise(seconds: number): StreamEvent {
  return ev("request:Received", seconds, "request")
}

function types(events: readonly StreamEvent[]): string[] {
  return events.map((e) => String((e.payload as { seq: number }).seq))
}

describe("appendEvents", () => {
  it("appends in arrival order when the incoming batch is newer", () => {
    const out = appendEvents([ev("s3:BucketCreated", 1)], [ev("s3:BucketCreated", 2)])
    expect(types(out)).toEqual(["1", "2"])
  })

  it("orders a late-arriving event by time, not by arrival", () => {
    const out = appendEvents(
      [ev("s3:BucketCreated", 1), ev("s3:BucketCreated", 3)],
      [ev("s3:BucketCreated", 2)],
    )
    expect(types(out)).toEqual(["1", "2", "3"])
  })

  it("orders sub-second timestamps numerically, not lexicographically", () => {
    // Go's RFC3339Nano drops trailing zeros, so "…:05Z" and "…:05.5Z" cannot
    // be compared as strings: "." sorts before "Z", which would put the later
    // event first.
    const whole: StreamEvent = {
      type: "s3:BucketCreated",
      time: "2026-01-01T00:00:05Z",
      source: "s3",
      payload: { seq: 1 },
    }
    const fractional: StreamEvent = {
      type: "s3:BucketCreated",
      time: "2026-01-01T00:00:05.5Z",
      source: "s3",
      payload: { seq: 2 },
    }
    expect(types(appendEvents([fractional], [whole]))).toEqual(["1", "2"])
  })

  it("returns the same array when there is nothing to add and nothing to evict", () => {
    const existing = [ev("s3:BucketCreated", 1)]
    expect(appendEvents(existing, [])).toBe(existing)
  })

  it("evicts the oldest request telemetry before any state-change event", () => {
    // Capacity 3, already full with one noise event at the front.
    const existing = [noise(1), ev("s3:BucketCreated", 2), ev("s3:BucketCreated", 3)]
    const out = appendEvents(existing, [ev("s3:BucketCreated", 4)], 3)
    expect(types(out)).toEqual(["2", "3", "4"])
  })

  it("evicts noise from anywhere in the buffer, not just the front", () => {
    const existing = [ev("s3:BucketCreated", 1), noise(2), ev("s3:BucketCreated", 3)]
    const out = appendEvents(existing, [ev("s3:BucketCreated", 4)], 3)
    expect(types(out)).toEqual(["1", "3", "4"])
  })

  it("evicts heartbeats as noise", () => {
    const beat = ev("heartbeat", 2, "system")
    const existing = [ev("s3:BucketCreated", 1), beat, ev("s3:BucketCreated", 3)]
    const out = appendEvents(existing, [ev("s3:BucketCreated", 4)], 3)
    expect(types(out)).toEqual(["1", "3", "4"])
  })

  it("falls back to oldest-first once no noise is left", () => {
    const existing = [ev("s3:BucketCreated", 1), ev("s3:BucketCreated", 2)]
    const out = appendEvents(existing, [ev("s3:BucketCreated", 3)], 2)
    expect(types(out)).toEqual(["2", "3"])
  })

  it("evicts several events at once when a batch overshoots capacity", () => {
    const existing = [noise(1), ev("s3:BucketCreated", 2)]
    const out = appendEvents(existing, [ev("s3:BucketCreated", 3), ev("s3:BucketCreated", 4)], 3)
    expect(types(out)).toEqual(["2", "3", "4"])
  })

  it("never exceeds capacity", () => {
    let buf: StreamEvent[] = []
    for (let i = 0; i < 50; i++) buf = appendEvents(buf, [ev("s3:BucketCreated", i)], 10)
    expect(buf).toHaveLength(10)
    expect(types(buf)).toEqual(["40", "41", "42", "43", "44", "45", "46", "47", "48", "49"])
  })

  it("caps a batch larger than capacity, keeping the newest signal", () => {
    const batch = [noise(1), ev("s3:BucketCreated", 2), ev("s3:BucketCreated", 3)]
    expect(types(appendEvents([], batch, 2))).toEqual(["2", "3"])
  })

  it("keeps a debugging-sized default history", () => {
    // Matches internal/events.HistoryCapacity — the server replays that many
    // on connect, so a smaller client cap would silently throw the tail away.
    expect(EVENT_BUFFER_CAPACITY).toBe(10_000)
  })

  it("keeps signal events even under a flood of request telemetry", () => {
    let buf: StreamEvent[] = appendEvents([], [ev("s3:BucketCreated", 0)], 100)
    for (let i = 1; i <= 500; i++) buf = appendEvents(buf, [noise(i)], 100)
    expect(buf.some((e) => e.type === "s3:BucketCreated")).toBe(true)
  })
})

describe("capacityFor", () => {
  it("keeps the baseline when the heap cannot be read", () => {
    expect(capacityFor(null, 500, 10_000)).toBe(10_000)
  })

  it("halves the buffer when the heap is near its limit", () => {
    expect(capacityFor({ used: 90, limit: 100 }, 10_000, 10_000)).toBe(5_000)
  })

  it("keeps halving while pressure persists, down to a usable floor", () => {
    let cap = 10_000
    for (let i = 0; i < 20; i++) cap = capacityFor({ used: 95, limit: 100 }, cap, 10_000)
    expect(cap).toBe(MIN_EVENT_BUFFER_CAPACITY)
  })

  it("grows back once there is room, no further than the baseline", () => {
    expect(capacityFor({ used: 10, limit: 100 }, 2_000, 10_000)).toBe(4_000)
    expect(capacityFor({ used: 10, limit: 100 }, 8_000, 10_000)).toBe(10_000)
  })

  it("holds steady between the two water marks", () => {
    expect(capacityFor({ used: 70, limit: 100 }, 2_500, 10_000)).toBe(2_500)
  })
})

describe("appendEvents under memory pressure", () => {
  afterEach(() => setHeapProbe(null))

  it("prunes the backscroll when the heap is nearly full", () => {
    setHeapProbe(() => ({ used: 99, limit: 100 }))
    const events = Array.from({ length: 9_000 }, (_, i) => ev("s3:BucketCreated", i))
    // Baseline halves on the first sample under pressure.
    expect(appendEvents([], events)).toHaveLength(5_000)
  })
})

describe("flushIntervalFor", () => {
  it("widens the window when a batch arrives at burst rate", () => {
    // 20 events in a 50 ms window is 400/s — well past the high water mark.
    expect(flushIntervalFor(20, FLUSH_INTERVAL_MS)).toBe(FLUSH_INTERVAL_MS * 2)
  })

  it("keeps widening under sustained pressure, no further than the ceiling", () => {
    let interval = FLUSH_INTERVAL_MS
    for (let i = 0; i < 20; i++) interval = flushIntervalFor(interval * 10, interval)
    expect(interval).toBe(MAX_FLUSH_INTERVAL_MS)
  })

  it("narrows back once traffic subsides, no lower than the base interval", () => {
    expect(flushIntervalFor(1, 200)).toBe(100)
    expect(flushIntervalFor(1, FLUSH_INTERVAL_MS)).toBe(FLUSH_INTERVAL_MS)
  })

  it("holds steady between the two water marks", () => {
    // 5 events per 50 ms window is 100/s — busy, but not a burst.
    expect(flushIntervalFor(5, FLUSH_INTERVAL_MS)).toBe(FLUSH_INTERVAL_MS)
  })
})

describe("EventBatcher", () => {
  afterEach(() => vi.useRealTimers())

  function collect(): { batches: StreamEvent[][]; batcher: EventBatcher } {
    const batches: StreamEvent[][] = []
    return { batches, batcher: new EventBatcher((b) => batches.push(b)) }
  }

  it("delivers everything pushed within one flush window as a single batch", () => {
    vi.useFakeTimers()
    const { batches, batcher } = collect()

    batcher.push(ev("s3:BucketCreated", 1))
    batcher.push(ev("s3:BucketCreated", 2))
    expect(batches).toHaveLength(0)

    vi.advanceTimersByTime(FLUSH_INTERVAL_MS)
    expect(batches).toHaveLength(1)
    expect(batches[0]).toHaveLength(2)
  })

  it("keeps the snapshot and the next batch disjoint", () => {
    vi.useFakeTimers()
    const { batches, batcher } = collect()

    batcher.push(ev("s3:BucketCreated", 1))
    // A tab subscribing mid-window must not see the queued event twice.
    expect(batcher.snapshot()).toHaveLength(0)

    vi.advanceTimersByTime(FLUSH_INTERVAL_MS)
    expect(batcher.snapshot()).toHaveLength(1)
    expect(batches[0]).toHaveLength(1)
  })

  it("forwards heartbeats without retaining them", () => {
    vi.useFakeTimers()
    const { batches, batcher } = collect()

    batcher.push(ev("heartbeat", 1, "system"))
    batcher.push(ev("s3:BucketCreated", 2))
    vi.advanceTimersByTime(FLUSH_INTERVAL_MS)

    expect(batches[0]).toHaveLength(2)
    expect(batcher.snapshot()).toHaveLength(1)
    expect(batcher.snapshot()[0].type).toBe("s3:BucketCreated")
  })

  it("drops queued events on clear so they cannot resurrect the buffer", () => {
    vi.useFakeTimers()
    const { batches, batcher } = collect()

    batcher.push(ev("s3:BucketCreated", 1))
    batcher.clear()
    vi.advanceTimersByTime(FLUSH_INTERVAL_MS * 4)

    expect(batches).toHaveLength(0)
    expect(batcher.snapshot()).toHaveLength(0)
  })

  /** Push `count` events and advance past one flush of `windowMs`. */
  function burst(batcher: EventBatcher, count: number, windowMs: number): void {
    for (let i = 0; i < count; i++) batcher.push(ev("s3:BucketCreated", i))
    vi.advanceTimersByTime(windowMs)
  }

  it("widens the flush window during a sustained burst", () => {
    vi.useFakeTimers()
    const { batches, batcher } = collect()

    // A burst-rate batch doubles the next window: 50 ms is no longer enough.
    burst(batcher, 20, FLUSH_INTERVAL_MS)
    expect(batches).toHaveLength(1)

    batcher.push(ev("s3:BucketCreated", 99))
    vi.advanceTimersByTime(FLUSH_INTERVAL_MS)
    expect(batches).toHaveLength(1)
    vi.advanceTimersByTime(FLUSH_INTERVAL_MS)
    expect(batches).toHaveLength(2)
  })

  it("narrows back to the base interval once the burst subsides", () => {
    vi.useFakeTimers()
    const { batches, batcher } = collect()

    burst(batcher, 20, FLUSH_INTERVAL_MS) // window is now 100 ms
    burst(batcher, 1, FLUSH_INTERVAL_MS * 2) // sparse flush narrows it again
    expect(batches).toHaveLength(2)

    burst(batcher, 1, FLUSH_INTERVAL_MS)
    expect(batches).toHaveLength(3)
  })

  it("resets the adaptive window on clear", () => {
    vi.useFakeTimers()
    const { batches, batcher } = collect()

    burst(batcher, 20, FLUSH_INTERVAL_MS) // window is now 100 ms
    batcher.clear()

    burst(batcher, 1, FLUSH_INTERVAL_MS)
    expect(batches).toHaveLength(2)
  })
})
