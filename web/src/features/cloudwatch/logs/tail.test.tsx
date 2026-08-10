/**
 * Live-tail subscription teardown.
 *
 * `tailLogEvents` is driven by `useEffect`s whose cleanup aborts an
 * `AbortController`. Those cleanups run constantly — on every filter change,
 * every navigation, and (twice, on mount) under StrictMode — so an abort that
 * does not actually hang up leaves a StartLiveTail session running on the
 * emulator with nothing reading it.
 */

import { StrictMode } from "react"
import { QueryClientProvider } from "@tanstack/react-query"
import { render, screen, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { createTestQueryClient } from "@/test/render"

// ─── StartLiveTail double ──────────────────────────────────────────────────

const live = vi.hoisted(() => {
  /**
   * A push-based stand-in for the SDK's event stream. `next()` parks until a
   * frame is pushed, which is what makes an un-torn-down session observable:
   * a real session sits idle between log events too.
   */
  class Session {
    private queue: unknown[] = []
    private waiter: ((v: IteratorResult<unknown>) => void) | null = null
    /** Set when the consumer hangs up — the thing an abort is supposed to do. */
    closed = false

    push(message: string) {
      const frame = {
        sessionUpdate: {
          sessionResults: [
            {
              timestamp: 1_700_000_000_000,
              ingestionTime: 1_700_000_000_001,
              logStreamName: "s",
              message,
            },
          ],
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
    /** Delay before `send` resolves, so a test can abort mid-request. */
    sendDelayMs: 0,
    /** Whether `send` rejects on an aborted signal, as the real SDK does. */
    rejectOnAbort: false,
    reset() {
      this.sessions = []
      this.sendDelayMs = 0
      this.rejectOnAbort = false
    },
  }
})

vi.mock("@/services/aws-clients", () => ({
  awsClients: {
    logs: () => ({
      send: async (
        command: { constructor: { name: string } },
        opts?: { abortSignal?: AbortSignal },
      ) => {
        if (command.constructor.name === "GetLogEventsCommand") {
          return { events: [], nextBackwardToken: undefined, nextForwardToken: undefined }
        }
        if (live.sendDelayMs > 0) {
          await new Promise((r) => setTimeout(r, live.sendDelayMs))
        }
        if (live.rejectOnAbort && opts?.abortSignal?.aborted) {
          throw Object.assign(new Error("Request aborted"), { name: "AbortError" })
        }
        const session = new live.Session()
        live.sessions.push(session)
        return { responseStream: session }
      },
    }),
  },
}))

const { tailLogEvents } = await import("@/features/cloudwatch/logs/tail")
const { LogStreamPeek } = await import("@/features/map/log-stream-peek")

// The peek panel pins itself to the newest line on every append; jsdom has no
// layout and so no `scrollTo`.
Element.prototype.scrollTo = () => {}

/** Let every already-scheduled microtask and zero-delay timer run. */
async function settle(times = 4) {
  for (let i = 0; i < times; i++) await new Promise((r) => setTimeout(r, 0))
}

const openSessions = () => live.sessions.filter((s) => !s.closed).length

// ─── Tests ─────────────────────────────────────────────────────────────────

describe("tailLogEvents", () => {
  it("hangs up the session as soon as the subscription is aborted", async () => {
    live.reset()
    const controller = new AbortController()
    const generator = tailLogEvents({ groupIdentifier: "g", signal: controller.signal })

    const pending = generator.next()
    await waitFor(() => expect(live.sessions).toHaveLength(1))

    controller.abort()
    await settle()

    // No further log event is ever going to arrive on this session — the
    // component that wanted it is gone — so nothing may be waiting on one.
    expect(openSessions()).toBe(0)
    await expect(pending).resolves.toEqual({ value: undefined, done: true })
  })

  it("treats an abort during StartLiveTail as a cancellation, not a failure", async () => {
    live.reset()
    live.sendDelayMs = 5
    live.rejectOnAbort = true

    const controller = new AbortController()
    const generator = tailLogEvents({ groupIdentifier: "g", signal: controller.signal })

    const pending = generator.next()
    controller.abort()

    // The abort races the request that opens the session. Losing that race is
    // the ordinary teardown path, so it must not surface as a rejection — an
    // effect body cannot catch it, and it lands as an unhandled rejection.
    await expect(pending).resolves.toEqual({ value: undefined, done: true })
  })

  it("stops yielding once aborted", async () => {
    live.reset()
    const controller = new AbortController()
    const generator = tailLogEvents({ groupIdentifier: "g", signal: controller.signal })

    const pending = generator.next()
    await waitFor(() => expect(live.sessions).toHaveLength(1))
    live.sessions[0].push("before")
    await expect(pending).resolves.toMatchObject({ value: { message: "before" } })

    controller.abort()
    live.sessions[0].push("after")
    await expect(generator.next()).resolves.toEqual({ value: undefined, done: true })
  })
})

describe("LogStreamPeek live tail", () => {
  const target = {
    title: "fn",
    subtitle: "abc123",
    logGroup: "/aws/lambda/fn",
    logStream: "2026/08/10/[$LATEST]abc123",
  }

  it("leaves exactly one session running when StrictMode double-invokes the effect", async () => {
    live.reset()
    render(
      <StrictMode>
        <QueryClientProvider client={createTestQueryClient()}>
          <LogStreamPeek target={target} onClose={() => {}} />
        </QueryClientProvider>
      </StrictMode>,
    )

    await waitFor(() => expect(live.sessions.length).toBeGreaterThan(0))
    await settle()

    // StrictMode deliberately mounts, tears down, and remounts. The discarded
    // first subscription must be hung up by its own cleanup rather than left
    // reading the emulator alongside the surviving one.
    expect(openSessions()).toBe(1)
  })

  it("renders a live event once", async () => {
    live.reset()
    render(
      <StrictMode>
        <QueryClientProvider client={createTestQueryClient()}>
          <LogStreamPeek target={target} onClose={() => {}} />
        </QueryClientProvider>
      </StrictMode>,
    )

    await waitFor(() => expect(live.sessions.length).toBeGreaterThan(0))
    await settle()
    for (const session of live.sessions) session.push("hello from the function")
    await settle()

    expect(await screen.findAllByText("hello from the function")).toHaveLength(1)
  })
})
