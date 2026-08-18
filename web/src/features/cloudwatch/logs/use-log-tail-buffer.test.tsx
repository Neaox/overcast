/**
 * The tail buffer that feeds the log viewers.
 *
 * Two properties matter enough to pin. Arrivals are folded in per animation
 * frame, not per event — a firehose stream used to mean one state update, one
 * reconciliation and one full re-derive per log line, which is how the viewer
 * ground to a halt exactly when the stream got busy. And the buffer is bounded:
 * a tail left open overnight must not grow without limit, and what the cap
 * drops is counted rather than silently swallowed.
 */

import { StrictMode } from "react"
import { renderHook, waitFor, act } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { useLogTailBuffer } from "./use-log-tail-buffer"

const live = vi.hoisted(() => {
  class Session {
    private queue: unknown[] = []
    private waiter: ((v: IteratorResult<unknown>) => void) | null = null
    closed = false

    push(message: string, timestamp = 1_700_000_000_000) {
      const frame = {
        sessionUpdate: {
          sessionResults: [{ timestamp, ingestionTime: timestamp, logStreamName: "s", message }],
        },
      }
      if (this.waiter) {
        const resolve = this.waiter
        this.waiter = null
        resolve({ value: frame, done: false })
      } else {
        this.queue.push(frame)
      }
    }

    [Symbol.asyncIterator]() {
      return {
        next: (): Promise<IteratorResult<unknown>> => {
          if (this.queue.length) return Promise.resolve({ value: this.queue.shift(), done: false })
          if (this.closed) return Promise.resolve({ value: undefined, done: true })
          return new Promise((resolve) => {
            this.waiter = resolve
          })
        },
        return: (): Promise<IteratorResult<unknown>> => {
          this.closed = true
          if (this.waiter) {
            const resolve = this.waiter
            this.waiter = null
            resolve({ value: undefined, done: true })
          }
          return Promise.resolve({ value: undefined, done: true })
        },
      }
    }
  }

  return {
    Session,
    sessions: [] as InstanceType<typeof Session>[],
    reset() {
      this.sessions = []
    },
  }
})

vi.mock("@/services/aws-clients", () => ({
  awsClients: {
    logs: () => ({
      send: () => {
        const session = new live.Session()
        live.sessions.push(session)
        return Promise.resolve({ responseStream: session })
      },
    }),
  },
}))

const activeSessions = () => live.sessions.filter((s) => !s.closed)

/** The most recent session — pushes go to the one still being read. */
async function session() {
  await waitFor(() => expect(activeSessions()).toHaveLength(1))
  return activeSessions()[0]
}

function renderBuffer(initial: { group?: string; stream?: string; cap?: number } = {}) {
  live.reset()
  return renderHook(
    ({ group, stream, cap }: { group?: string; stream?: string; cap?: number }) =>
      useLogTailBuffer({
        enabled: true,
        groupIdentifier: group ?? "g",
        streamName: stream,
        cap,
      }),
    { initialProps: { group: initial.group ?? "g", stream: initial.stream, cap: initial.cap } },
  )
}

describe("useLogTailBuffer", () => {
  it("delivers what the session pushes, oldest first", async () => {
    const { result } = renderBuffer()
    const s = await session()

    act(() => {
      s.push("one", 1_000)
      s.push("two", 2_000)
    })

    await waitFor(() => expect(result.current.events.map((e) => e.message)).toEqual(["one", "two"]))
    expect(result.current.overflowed).toBe(0)
  })

  it("keeps events ordered when a burst arrives out of order", async () => {
    const { result } = renderBuffer()
    const s = await session()

    act(() => {
      s.push("late", 3_000)
      s.push("early", 1_000)
    })

    await waitFor(() =>
      expect(result.current.events.map((e) => e.message)).toEqual(["early", "late"]),
    )
  })

  it("drops the oldest events beyond the cap, and says how many", async () => {
    const { result } = renderBuffer({ cap: 3 })
    const s = await session()

    act(() => {
      for (let i = 1; i <= 5; i++) s.push(`event ${i}`, i * 1_000)
    })

    await waitFor(() =>
      expect(result.current.events.map((e) => e.message)).toEqual([
        "event 3",
        "event 4",
        "event 5",
      ]),
    )
    expect(result.current.overflowed).toBe(2)
  })

  it("clears on demand without hanging up the session", async () => {
    const { result } = renderBuffer()
    const s = await session()

    act(() => s.push("before", 1_000))
    await waitFor(() => expect(result.current.events).toHaveLength(1))

    act(() => result.current.clear())
    expect(result.current.events).toHaveLength(0)
    expect(result.current.overflowed).toBe(0)

    // The session was left running, so a later event still arrives.
    act(() => s.push("after", 2_000))
    await waitFor(() => expect(result.current.events.map((e) => e.message)).toEqual(["after"]))
  })

  it("starts a fresh buffer when the stream changes", async () => {
    const { result, rerender } = renderBuffer({ stream: "a" })
    const first = await session()

    act(() => first.push("from a", 1_000))
    await waitFor(() => expect(result.current.events).toHaveLength(1))

    rerender({ group: "g", stream: "b", cap: undefined })

    await waitFor(() => expect(result.current.events).toHaveLength(0))
    const second = await session()
    act(() => second.push("from b", 2_000))
    await waitFor(() => expect(result.current.events.map((e) => e.message)).toEqual(["from b"]))
  })

  it("leaves exactly one session running under StrictMode", async () => {
    live.reset()
    renderHook(() => useLogTailBuffer({ enabled: true, groupIdentifier: "g" }), {
      wrapper: StrictMode,
    })

    await waitFor(() => {
      expect(live.sessions.length).toBeGreaterThanOrEqual(2)
      expect(activeSessions()).toHaveLength(1)
    })
  })

  it("hangs up when disabled", async () => {
    live.reset()
    const { rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) => useLogTailBuffer({ enabled, groupIdentifier: "g" }),
      { initialProps: { enabled: true } },
    )
    await session()

    rerender({ enabled: false })

    await waitFor(() => expect(activeSessions()).toHaveLength(0))
  })
})
