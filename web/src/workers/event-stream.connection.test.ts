/**
 * The reconnect schedule is a wire-visible contract — the connection toast
 * counts down to `nextAttemptAt` and offers to pre-empt it — so these tests
 * pin the schedule itself, not just "it reconnects eventually".
 *
 * jsdom has no EventSource, which is also why ReconnectingStream takes its
 * opener as an option.
 */
import { describe, expect, it, beforeEach, afterEach, vi } from "vitest"
import { ReconnectingStream, retryDelayMs, type EventSourceLike } from "./event-stream.connection"
import type { ConnectionState } from "./event-stream.protocol"
import type { StreamEvent } from "@/types"

/** A hand-driven stand-in for EventSource: nothing happens until a test says so. */
class FakeEventSource implements EventSourceLike {
  onopen: ((event: Event) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent<string>) => void) | null = null
  closed = false
  readonly url: string

  constructor(url: string) {
    this.url = url
  }

  close(): void {
    this.closed = true
  }

  open(): void {
    this.onopen?.(new Event("open"))
  }

  fail(): void {
    this.onerror?.(new Event("error"))
  }

  send(data: string, lastEventId = ""): void {
    this.onmessage?.(new MessageEvent("message", { data, lastEventId }))
  }
}

/** A stream whose connections are all fakes the test can drive. */
function harness(url = "/api/events") {
  const sources: FakeEventSource[] = []
  const states: ConnectionState[] = []
  const events: StreamEvent[] = []

  const stream = new ReconnectingStream({
    url,
    onEvent: (event) => events.push(event),
    onState: (state) => states.push(state),
    open: (target) => {
      const source = new FakeEventSource(target)
      sources.push(source)
      return source
    },
  })

  return {
    stream,
    sources,
    events,
    states,
    /** The connection currently being driven. */
    get latest(): FakeEventSource {
      return sources[sources.length - 1]
    },
    get state(): ConnectionState {
      return stream.state
    },
  }
}

describe("retryDelayMs", () => {
  it("doubles from a second and holds at five", () => {
    expect([0, 1, 2, 3, 4, 10].map(retryDelayMs)).toEqual([
      1_000, 2_000, 4_000, 5_000, 5_000, 5_000,
    ])
  })
})

describe("ReconnectingStream", () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-07-29T14:22:00Z"))
    vi.spyOn(console, "info").mockImplementation(() => {})
    vi.spyOn(console, "warn").mockImplementation(() => {})
    vi.spyOn(console, "error").mockImplementation(() => {})
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it("connects on construction", () => {
    const h = harness()
    expect(h.sources).toHaveLength(1)
    expect(h.state.connected).toBe(false)
  })

  it("reports the connection, and when it opened", () => {
    const h = harness()
    h.latest.open()

    expect(h.state).toEqual({
      connected: true,
      attempt: 0,
      nextAttemptAt: null,
      lastConnectedAt: Date.now(),
    })
  })

  it("schedules the next attempt on a drop, and says when it is due", () => {
    const h = harness()
    h.latest.open()
    h.latest.fail()

    expect(h.state.connected).toBe(false)
    expect(h.state.attempt).toBe(1)
    expect(h.state.nextAttemptAt).toBe(Date.now() + 1_000)
  })

  it("closes the dropped connection rather than leaving the browser to retry it", () => {
    const h = harness()
    const first = h.latest
    first.open()
    first.fail()

    expect(first.closed).toBe(true)
  })

  it("reconnects when the delay is up, and stops advertising a schedule", () => {
    const h = harness()
    h.latest.fail()

    vi.advanceTimersByTime(1_000)

    expect(h.sources).toHaveLength(2)
    expect(h.state.nextAttemptAt).toBeNull()
    expect(h.state.attempt).toBe(1)
  })

  it("backs off across consecutive failures", () => {
    const h = harness()
    const delays: number[] = []

    for (let i = 0; i < 4; i++) {
      const before = Date.now()
      h.latest.fail()
      delays.push(h.state.nextAttemptAt! - before)
      vi.advanceTimersByTime(delays[i])
    }

    expect(delays).toEqual([1_000, 2_000, 4_000, 5_000])
    expect(h.state.attempt).toBe(4)
  })

  it("forgets the failures once a connection opens", () => {
    const h = harness()
    h.latest.fail()
    vi.advanceTimersByTime(1_000)
    h.latest.open()

    expect(h.state.attempt).toBe(0)
    expect(h.state.connected).toBe(true)
  })

  it("brings the scheduled attempt forward on retryNow, and only once", () => {
    const h = harness()
    h.latest.fail()

    h.stream.retryNow()
    expect(h.sources).toHaveLength(2)

    // The pre-empted timer must not also fire.
    vi.advanceTimersByTime(5_000)
    expect(h.sources).toHaveLength(2)
  })

  it("ignores retryNow while an attempt is already in flight", () => {
    const h = harness()

    h.stream.retryNow()

    expect(h.sources).toHaveLength(1)
  })

  it("resumes from the last event id it saw", () => {
    const h = harness()
    h.latest.open()
    h.latest.send(JSON.stringify({ type: "s3:BucketCreated" }), "run7-42")
    h.latest.fail()
    vi.advanceTimersByTime(1_000)

    expect(h.latest.url).toContain("last_event_id=run7-42")
  })

  it("delivers well-formed frames and survives malformed ones", () => {
    const h = harness()
    h.latest.open()
    h.latest.send("{not json")
    h.latest.send(JSON.stringify({ type: "s3:BucketCreated", source: "s3" }))

    expect(h.events).toEqual([{ type: "s3:BucketCreated", source: "s3" }])
  })

  it("stops for good once closed", () => {
    const h = harness()
    h.latest.fail()
    h.stream.close()

    vi.advanceTimersByTime(60_000)

    expect(h.sources).toHaveLength(1)
    expect(h.sources[0].closed).toBe(true)
  })
})
