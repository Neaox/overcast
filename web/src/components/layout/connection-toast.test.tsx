/**
 * The toast's job is to be readable at a glance and to sit still while it does
 * it, so alongside the copy these tests pin the two things that would spoil
 * that: the row count must not change with the phase, and the draining
 * hairline must be timed from the schedule rather than from a render.
 *
 * Everything here is `await`ed because the harness router mounts its route
 * asynchronously — hence the `ready` sentinel, which is also what proves the
 * *absence* cases are absent rather than merely not mounted yet.
 */
import { describe, expect, it, beforeEach, afterEach, vi } from "vitest"
import { act, screen } from "@testing-library/react"
import { onlineManager, type QueryClient } from "@tanstack/react-query"
import { createTestQueryClient, renderWithRouter } from "@/test/render"
import { useFakeTimers } from "@/test/fake-timers"
import { ToastContextProvider } from "@/components/ui/toast"
import { ConnectionStatusProvider } from "@/hooks/use-connection-status"
import { eventStreamStatusQueryOptions } from "@/hooks/use-event-stream"
import { DISCONNECTED, type ConnectionState } from "@/workers/event-stream.protocol"
import { formatTimeOfDay } from "@/lib/format"
import { ConnectionToast } from "./connection-toast"

const { reconnect } = vi.hoisted(() => ({ reconnect: vi.fn() }))

vi.mock("@/workers/event-stream.client", () => ({
  subscribe: () => () => {},
  clear: vi.fn(),
  reconnect,
}))

const NOW = new Date("2026-07-29T14:22:07Z").getTime()

/** The endpoint the store is configured with out of the box. */
const HOST = "localhost:4566"

/** A connection state, with `connected` widened as the UI sees it. */
type Status = Omit<Partial<ConnectionState>, "connected"> & { connected?: boolean | null }

/**
 * A stream that was live and has dropped — the only thing the card exists to
 * describe, and so the base every case here builds on. `DISCONNECTED` is a
 * *different* state: a stream that has never been open, which belongs to the
 * connecting screen and is spelled out explicitly by the cases that want it.
 */
const DROPPED: ConnectionState = { ...DISCONNECTED, attempt: 1, lastConnectedAt: NOW - 60_000 }

async function renderToast(status: Status = {}) {
  const queryClient = createTestQueryClient()
  queryClient.setQueryData(eventStreamStatusQueryOptions().queryKey, {
    ...DROPPED,
    ...status,
  })

  const view = renderWithRouter(
    () => (
      <ToastContextProvider>
        <ConnectionStatusProvider>
          <div data-testid="ready" />
          <ConnectionToast />
        </ConnectionStatusProvider>
      </ToastContextProvider>
    ),
    { queryClient },
  )

  await screen.findByTestId("ready")
  return view
}

/** The toast card, or `null` when the toast is not showing. */
function card(container: HTMLElement): HTMLElement | null {
  return container.querySelector("section")
}

/** Rows in the toast's column — the number that must not move. */
function rowCount(container: HTMLElement): number {
  return card(container)?.querySelector("div")?.childElementCount ?? 0
}

function elapse(ms: number) {
  act(() => {
    vi.advanceTimersByTime(ms)
  })
}

describe("ConnectionToast", () => {
  useFakeTimers()

  beforeEach(() => {
    vi.setSystemTime(NOW)
    reconnect.mockClear()
  })

  afterEach(() => {
    // The provider drives TanStack Query's global online flag.
    onlineManager.setOnline(true)
  })

  describe("when it shows at all", () => {
    it("stays out of the way while the stream is live", async () => {
      const { container } = await renderToast({ connected: true })
      expect(card(container)).toBeNull()
    })

    it("leaves the first connection to the cold-boot screen", async () => {
      const { container } = await renderToast({ connected: null })
      expect(card(container)).toBeNull()
    })

    /**
     * The state a fresh tab is actually handed. Both transports start their
     * stream at `DISCONNECTED` and broadcast it before the SSE handshake has
     * had a chance, so "offline" is the first definitive answer of every
     * session — and it is not a drop.
     */
    it("holds off while the stream is still opening for the first time", async () => {
      const { container } = await renderToast(DISCONNECTED)
      expect(card(container)).toBeNull()
    })

    /**
     * A stream that has never opened is the connecting screen's business
     * however long it takes, so the retry schedule alone is not the trigger —
     * the probe behind that screen and the stream share a server, so if one
     * cannot connect the user is already being told.
     */
    it("leaves a stream that has never opened to the connecting screen", async () => {
      const { container } = await renderToast({
        ...DISCONNECTED,
        attempt: 3,
        nextAttemptAt: NOW + 4_000,
      })
      expect(card(container)).toBeNull()
    })

    it("names the host it is reconnecting to", async () => {
      await renderToast({ nextAttemptAt: NOW + 4_000 })
      expect(screen.getByText(`Reconnecting to ${HOST}`)).toBeInTheDocument()
    })

    it("announces the drop once, without the countdown", async () => {
      await renderToast({ nextAttemptAt: NOW + 4_000 })
      expect(screen.getByRole("status")).toHaveTextContent(
        `Lost connection to ${HOST}. Reconnecting automatically — cached data is shown and writes are paused.`,
      )
    })
  })

  describe("the schedule", () => {
    it("counts down to the attempt the worker scheduled", async () => {
      await renderToast({ attempt: 3, nextAttemptAt: NOW + 4_000 })
      expect(screen.getByText("Attempt 3 · next in 4s")).toBeInTheDocument()

      elapse(2_000)
      expect(screen.getByText("Attempt 3 · next in 2s")).toBeInTheDocument()
    })

    it("reads as in flight while an attempt is running", async () => {
      await renderToast({ attempt: 3, nextAttemptAt: null })
      expect(screen.getByText("Attempt 3 · connecting…")).toBeInTheDocument()
    })

    it("reads as in flight once the countdown is spent, rather than stalling on zero", async () => {
      await renderToast({ attempt: 3, nextAttemptAt: NOW + 1_000 })
      elapse(1_000)
      expect(screen.getByText("Attempt 3 · connecting…")).toBeInTheDocument()
    })
  })

  describe("what the cached view means", () => {
    /**
     * There is always a timestamp to show: the card only exists because a live
     * connection dropped, and the open that preceded the drop stamped one.
     */
    it("stamps the data with the moment the stream was last open", async () => {
      const lastConnectedAt = NOW - 60_000
      await renderToast({ nextAttemptAt: NOW + 4_000, lastConnectedAt })
      expect(
        screen.getByText(`Cached at ${formatTimeOfDay(lastConnectedAt)} · writes paused`),
      ).toBeInTheDocument()
    })
  })

  describe("affordances", () => {
    it("brings the reconnect forward on retry now", async () => {
      const { user } = await renderToast({ attempt: 2, nextAttemptAt: NOW + 4_000 })

      await user.click(screen.getByRole("button", { name: "retry now" }))

      expect(reconnect).toHaveBeenCalledOnce()
    })

    it("offers the event stream, which is readable without the emulator", async () => {
      await renderToast({ nextAttemptAt: NOW + 4_000 })
      expect(screen.getByRole("link", { name: "view events" })).toHaveAttribute("href", "/events")
    })
  })

  describe("coming back", () => {
    /** Pushes a new connection state through the cache, as the worker would. */
    function report(queryClient: QueryClient, status: Status) {
      act(() => {
        queryClient.setQueryData(eventStreamStatusQueryOptions().queryKey, {
          ...DISCONNECTED,
          ...status,
        })
        // Query notifies its observers on a scheduler, which is a timer the
        // frozen clock has to be told to run.
        vi.advanceTimersByTime(0)
      })
    }

    it("withdraws the toast and says so", async () => {
      const { container, queryClient } = await renderToast({
        attempt: 2,
        nextAttemptAt: NOW + 4_000,
      })

      report(queryClient, { connected: true, lastConnectedAt: NOW })

      expect(card(container)).toBeNull()
      expect(screen.getByText("Reconnected")).toBeInTheDocument()
      expect(screen.getByText(HOST)).toBeInTheDocument()
    })

    it("stays quiet on the first connection of a session", async () => {
      const { queryClient } = await renderToast({ connected: null })

      report(queryClient, { connected: true, lastConnectedAt: NOW })

      expect(screen.queryByText("Reconnected")).not.toBeInTheDocument()
    })

    /**
     * The regression this guards: a session opens on the worker's pre-connect
     * `DISCONNECTED`, so *every* app load ran `false → true` and announced a
     * reconnect for a connection that had never dropped.
     */
    it("stays quiet when the stream opens after reporting its pre-connect state", async () => {
      const { queryClient } = await renderToast(DISCONNECTED)

      report(queryClient, { connected: true, lastConnectedAt: NOW })

      expect(screen.queryByText("Reconnected")).not.toBeInTheDocument()
    })

    it("announces each return, not just the first", async () => {
      const { queryClient } = await renderToast({ attempt: 1, nextAttemptAt: NOW + 4_000 })

      report(queryClient, { connected: true, lastConnectedAt: NOW })
      // The second drop carries the timestamp of the open it just lost.
      report(queryClient, { attempt: 1, nextAttemptAt: NOW + 4_000, lastConnectedAt: NOW })
      report(queryClient, { connected: true, lastConnectedAt: NOW })

      expect(screen.getAllByText("Reconnected")).toHaveLength(2)
    })
  })

  describe("holding still", () => {
    it("keeps the same rows whether an attempt is scheduled or in flight", async () => {
      const scheduled = await renderToast({ attempt: 2, nextAttemptAt: NOW + 4_000 })
      const rows = rowCount(scheduled.container)
      expect(rows).toBeGreaterThan(0)
      scheduled.unmount()

      const inFlight = await renderToast({ attempt: 2, nextAttemptAt: null })
      expect(rowCount(inFlight.container)).toBe(rows)
    })

    it("drains the hairline over exactly the time left on the schedule", async () => {
      const { container } = await renderToast({ attempt: 1, nextAttemptAt: NOW + 4_000 })

      const drain = container.querySelector<HTMLElement>(".oc-retry-drain")
      expect(drain?.style.animationDuration).toBe(`${NOW + 4_000 - Date.now()}ms`)
    })

    it("leaves a running hairline alone as the countdown ticks", async () => {
      const { container } = await renderToast({ attempt: 1, nextAttemptAt: NOW + 4_000 })
      const drain = container.querySelector<HTMLElement>(".oc-retry-drain")
      const duration = drain?.style.animationDuration

      // Re-timing it every second would jump the bar forward on each tick.
      elapse(2_000)

      expect(container.querySelector<HTMLElement>(".oc-retry-drain")).toBe(drain)
      expect(drain?.style.animationDuration).toBe(duration)
    })

    it("drops the hairline while an attempt is in flight", async () => {
      const { container } = await renderToast({ attempt: 1, nextAttemptAt: null })
      expect(container.querySelector(".oc-retry-drain")).toBeNull()
    })
  })
})
