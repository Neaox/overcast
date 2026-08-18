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
    private rejecter: ((err: Error) => void) | null = null
    private error: Error | null = null
    closed = false

    push(message: string, timestamp = 1_700_000_000_000) {
      this.deliver({
        sessionUpdate: {
          sessionResults: [{ timestamp, ingestionTime: timestamp, logStreamName: "s", message }],
        },
      })
    }

    /** An empty sessionUpdate — the emulator's once-a-second heartbeat. */
    heartbeat() {
      this.deliver({ sessionUpdate: { sessionResults: [] } })
    }

    private deliver(frame: unknown) {
      if (this.waiter) {
        const resolve = this.waiter
        this.waiter = null
        resolve({ value: frame, done: false })
      } else {
        this.queue.push(frame)
      }
    }

    /** Kill the transport mid-stream, the way an emulator restart does. */
    fail(err: Error) {
      this.error = err
      if (this.waiter) {
        const reject = this.rejecter
        this.waiter = null
        this.rejecter = null
        reject?.(err)
      }
    }

    [Symbol.asyncIterator]() {
      return {
        next: (): Promise<IteratorResult<unknown>> => {
          if (this.error) return Promise.reject(this.error)
          if (this.queue.length) return Promise.resolve({ value: this.queue.shift(), done: false })
          if (this.closed) return Promise.resolve({ value: undefined, done: true })
          return new Promise((resolve, reject) => {
            this.waiter = resolve
            this.rejecter = reject
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

  it("keeps what it caught when the session dies mid-stream, and says the session is gone", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    try {
      const { result } = renderBuffer()
      const s = await session()
      expect(result.current.status).toBe("live")

      act(() => s.push("caught", 1_000))
      await waitFor(() => expect(result.current.events).toHaveLength(1))

      // The emulator restarting kills the transport without an abort. That is
      // an ordinary dev-loop event, not an unhandled rejection — but a dead
      // session must not keep reporting itself live, because on a quiet
      // stream the two are otherwise indistinguishable.
      act(() => s.fail(new TypeError("network error")))
      await waitFor(() => expect(result.current.status).toBe("error"))

      expect(warn).toHaveBeenCalled()
      expect(result.current.events.map((e) => e.message)).toEqual(["caught"])
    } finally {
      warn.mockRestore()
    }
  })

  it("marks the session gone when no frame arrives for the staleness window", async () => {
    // Fake timers drive both the watchdog interval and Date.now; the session
    // mock is purely promise-based, and advanceTimersByTimeAsync flushes
    // microtasks between ticks, so the generator keeps consuming.
    vi.useFakeTimers()
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    try {
      live.reset()
      const { result } = renderHook(() => useLogTailBuffer({ enabled: true, groupIdentifier: "g" }))
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0)
      })
      expect(activeSessions()).toHaveLength(1)
      expect(result.current.status).toBe("live")

      // Just short of the threshold: still presumed live.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(9_000)
      })
      expect(result.current.status).toBe("live")

      // Ten missed heartbeats: the connection died without a FIN (machine
      // sleep, NAT timeout) and nothing will ever reject — only silence tells.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2_000)
      })
      expect(result.current.status).toBe("error")
      expect(warn).toHaveBeenCalledWith(expect.stringContaining("quiet"))
    } finally {
      warn.mockRestore()
      vi.useRealTimers()
    }
  })

  it("stays live while only empty heartbeat frames arrive", async () => {
    vi.useFakeTimers()
    try {
      live.reset()
      const { result } = renderHook(() => useLogTailBuffer({ enabled: true, groupIdentifier: "g" }))
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0)
      })
      const s = activeSessions()[0]
      expect(result.current.status).toBe("live")

      // Well past the staleness window in total, but a heartbeat lands every
      // second — a quiet stream on a healthy session must never go red.
      for (let i = 0; i < 15; i++) {
        s.heartbeat()
        await act(async () => {
          await vi.advanceTimersByTimeAsync(1_000)
        })
      }
      expect(result.current.status).toBe("live")
      expect(result.current.events).toHaveLength(0)
    } finally {
      vi.useRealTimers()
    }
  })

  it("hangs up when disabled, and reports idle", async () => {
    live.reset()
    const { result, rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) => useLogTailBuffer({ enabled, groupIdentifier: "g" }),
      { initialProps: { enabled: true } },
    )
    await session()
    expect(result.current.status).toBe("live")

    rerender({ enabled: false })

    await waitFor(() => expect(activeSessions()).toHaveLength(0))
    expect(result.current.status).toBe("idle")
  })
})
