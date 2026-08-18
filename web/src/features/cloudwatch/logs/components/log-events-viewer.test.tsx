import { StrictMode } from "react"
import { QueryClientProvider } from "@tanstack/react-query"
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router"
import { render } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { createTestQueryClient, renderWithRouter, screen, userEvent, waitFor } from "@/test/render"
import { ToastContextProvider } from "@/components/ui/toast"
import { TooltipProvider } from "@/components/ui/tooltip"
import { logsFilterQueryOptions } from "@/features/cloudwatch/logs/data"
import type { FilteredLogEvent } from "@/types/logs"
import { LogEventsViewer } from "./log-events-viewer"

// jsdom gives every element a zero height, so the real virtualizer renders no
// rows at all and nothing about the buffer would be observable.
vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 34,
    getVirtualItems: () =>
      Array.from({ length: count }, (_, index) => ({
        index,
        key: index,
        start: index * 34,
        end: index * 34 + 34,
      })),
    measureElement: vi.fn(),
    scrollToIndex: vi.fn(),
    scrollOffset: 0,
  }),
}))

// A live-tail session, plus whatever the next FilterLogEvents should return.
// The viewer shows a fetched page and the session's pushes together, and those
// two overlap once an event is on disk.
const live = vi.hoisted(() => {
  class Session {
    private queue: unknown[] = []
    private waiter: ((v: IteratorResult<unknown>) => void) | null = null
    closed = false

    push(event: { timestamp: number; message: string; logStreamName: string }) {
      const frame = {
        sessionUpdate: { sessionResults: [{ ...event, ingestionTime: event.timestamp }] },
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
    /** What the next FilterLogEvents call returns. */
    stored: [] as { timestamp: number; message: string; logStreamName: string }[],
    filterCalls: 0,
    reset() {
      this.sessions = []
      this.stored = []
      this.filterCalls = 0
    },
  }
})

vi.mock("@/services/aws-clients", () => ({
  awsClients: {
    logs: () => ({
      send: (command: { constructor: { name: string } }) => {
        if (command.constructor.name === "FilterLogEventsCommand") {
          live.filterCalls++
          return Promise.resolve({
            events: live.stored.map((e) => ({ ...e })),
            searchedLogStreams: [],
          })
        }
        const session = new live.Session()
        live.sessions.push(session)
        return Promise.resolve({ responseStream: session })
      },
    }),
  },
}))

const GROUP = "/aws/lambda/checkout"

const EVENTS: FilteredLogEvent[] = [
  { timestamp: 1_000, ingestionTime: 1_000, logStreamName: "s1", message: "first message" },
  { timestamp: 2_000, ingestionTime: 2_000, logStreamName: "s1", message: "second message" },
]

function Providers({ children }: { children: React.ReactNode }) {
  return (
    <ToastContextProvider>
      <TooltipProvider>{children}</TooltipProvider>
    </ToastContextProvider>
  )
}

function renderViewer(events: FilteredLogEvent[] = EVENTS) {
  live.reset()
  const queryClient = createTestQueryClient()
  queryClient.setQueryData(logsFilterQueryOptions(GROUP, {}).queryKey, {
    events,
    searchedLogStreams: [],
  })

  return renderWithRouter(
    () => (
      <Providers>
        <LogEventsViewer groupName={GROUP} />
      </Providers>
    ),
    { queryClient },
  )
}

/** The router mounts the route asynchronously, so the toolbar is a `findBy`. */
async function clearButton() {
  return screen.findByRole("button", { name: /^clear$/i })
}

describe("LogEventsViewer > clearing the buffer", () => {
  it("hides the events on screen when Clear is pressed", async () => {
    const { user } = renderViewer()
    expect(await screen.findByText("first message")).toBeInTheDocument()

    await user.click(await clearButton())

    expect(screen.queryByText("first message")).not.toBeInTheDocument()
    expect(screen.queryByText("second message")).not.toBeInTheDocument()
  })

  it("explains that the buffer was cleared rather than claiming there are no logs", async () => {
    const { user } = renderViewer()

    await user.click(await clearButton())

    expect(screen.getByText("Buffer cleared")).toBeInTheDocument()
    expect(screen.queryByText("No log events")).not.toBeInTheDocument()
  })

  it("offers to bring back the events it hid", async () => {
    const { user } = renderViewer()

    await user.click(await clearButton())

    expect(screen.getByRole("button", { name: /show 2 earlier/i })).toBeInTheDocument()
  })

  it("restores the hidden events when the offer is taken", async () => {
    const { user } = renderViewer()
    await user.click(await clearButton())

    await user.click(screen.getByRole("button", { name: /show 2 earlier/i }))

    expect(screen.getByText("first message")).toBeInTheDocument()
    expect(screen.getByText("second message")).toBeInTheDocument()
  })

  it("disables Clear when there is nothing on screen to clear", async () => {
    const { user } = renderViewer()
    await user.click(await clearButton())

    expect(await clearButton()).toBeDisabled()
  })
})

/*
 * Filter highlighting is driven by a matcher the viewer compiles once per
 * filter and hands to every row, rather than by each row re-parsing the pattern
 * on every scroll frame. That plumbing is invisible from the outside — which is
 * exactly why it is worth a test that looks at the rendered marks.
 */
describe("LogEventsViewer > filter highlighting", () => {
  async function search(term: string, events: FilteredLogEvent[]) {
    const { user } = renderViewer([])
    live.stored = events.map((e) => ({
      timestamp: e.timestamp ?? 0,
      message: e.message ?? "",
      logStreamName: e.logStreamName ?? "s1",
    }))

    await user.type(await screen.findByPlaceholderText(/^Filter/), term)
    await user.click(screen.getByRole("button", { name: /^search$/i }))
    return user
  }

  it("marks the matching term inside a row", async () => {
    await search("timeout", levelled(["connection timeout after 30s"]))

    const marks = await screen.findAllByText("timeout", {
      selector: "mark",
    })
    expect(marks).toHaveLength(1)
  })

  it("marks every row that matches, not just the first", async () => {
    await search(
      "timeout",
      levelled(["connection timeout after 30s", "timeout talking to upstream"]),
    )

    // A shared global regex whose `lastIndex` carried between rows would light
    // up every other row instead of all of them.
    await waitFor(async () =>
      expect(await screen.findAllByText("timeout", { selector: "mark" })).toHaveLength(2),
    )
  })

  it("leaves a non-matching row unmarked", async () => {
    await search("timeout", levelled(["all is well"]))

    expect(await screen.findByText(/all is well/)).toBeInTheDocument()
    expect(document.querySelectorAll("mark")).toHaveLength(0)
  })
})

// ─── Live tail ─────────────────────────────────────────────────────────────

const TAILED = {
  timestamp: 1_700_000_000_000,
  message: "the one and only line",
  logStreamName: "stream-a",
}

/** Renders with an empty page, so only the live session can add rows. */
function renderTailViewer() {
  return renderViewer([])
}

/**
 * The same tree with StrictMode where `main.tsx` puts it — at the root, which
 * is the only place React honours it.
 */
function renderTailViewerUnderStrictMode() {
  live.reset()
  const queryClient = createTestQueryClient()
  queryClient.setQueryData(logsFilterQueryOptions(GROUP, {}).queryKey, {
    events: [],
    searchedLogStreams: [],
  })
  const rootRoute = createRootRoute()
  const router = createRouter({
    routeTree: rootRoute.addChildren([
      createRoute({
        getParentRoute: () => rootRoute,
        path: "/",
        component: () => (
          <Providers>
            <LogEventsViewer groupName={GROUP} />
          </Providers>
        ),
      }),
    ]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })

  render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </StrictMode>,
  )
  return { user: userEvent.setup() }
}

const tailButton = () => screen.findByRole("button", { name: /^tail$/i })

/** The toolbar's refresh control carries an icon and no label. */
function refreshButton() {
  const button = document.querySelector("svg.lucide-refresh-cw")?.closest("button")
  if (!button) throw new Error("refresh button not found")
  return button
}

const rowsShowing = (message: string) => screen.queryAllByText(message).length

describe("LogEventsViewer > live tail", () => {
  it("opens one session when tail is switched on", async () => {
    const { user } = renderTailViewer()

    await user.click(await tailButton())
    await waitFor(() => expect(live.sessions).toHaveLength(1))
    await new Promise((r) => setTimeout(r, 20))

    expect(live.sessions).toHaveLength(1)
  })

  it("opens one session under StrictMode too", async () => {
    const { user } = renderTailViewerUnderStrictMode()

    // StrictMode's extra mount/unmount/remount happens while tail is off, and
    // the effect subscribes to nothing in that state.
    const tail = await tailButton()
    expect(live.sessions).toHaveLength(0)

    // Switching tail on is a dependency change on an already-mounted effect,
    // which React runs once — in development as in production.
    await user.click(tail)
    await waitFor(() => expect(live.sessions).toHaveLength(1))
    await new Promise((r) => setTimeout(r, 20))

    expect(live.sessions).toHaveLength(1)
  })

  it("shows a live event once when a refetch has already picked it up", async () => {
    const { user } = renderTailViewer()

    await user.click(await tailButton())
    await waitFor(() => expect(live.sessions).toHaveLength(1))

    // The emulator now holds the event, so the next FilterLogEvents returns it.
    // A refetch is not something the user asks for — react-query fires one on
    // window focus, which is exactly what happens on the way back from a
    // terminal after `aws logs put-log-events`.
    live.stored = [TAILED]
    await user.click(refreshButton())
    await waitFor(() => expect(rowsShowing(TAILED.message)).toBe(1))

    // …and the session that was already open pushes the same event.
    live.sessions[0].push(TAILED)
    await new Promise((r) => setTimeout(r, 20))

    expect(rowsShowing(TAILED.message)).toBe(1)
  })

  it("keeps a live event that the refetch did not return", async () => {
    const { user } = renderTailViewer()

    await user.click(await tailButton())
    await waitFor(() => expect(live.sessions).toHaveLength(1))

    live.sessions[0].push(TAILED)
    await waitFor(() => expect(rowsShowing(TAILED.message)).toBe(1))

    // A refetch whose snapshot predates the event must not drop it — this is
    // the other half of the overlap, and clearing the buffer on every refetch
    // would lose the line entirely.
    await user.click(refreshButton())
    await waitFor(() => expect(live.filterCalls).toBeGreaterThan(0))
    await new Promise((r) => setTimeout(r, 20))

    expect(rowsShowing(TAILED.message)).toBe(1)
  })

  it("keeps a line the function really did log twice", async () => {
    const { user } = renderTailViewer()

    await user.click(await tailButton())
    await waitFor(() => expect(live.sessions).toHaveLength(1))

    live.sessions[0].push(TAILED)
    live.sessions[0].push(TAILED)
    await waitFor(() => expect(rowsShowing(TAILED.message)).toBe(2))

    // The stored page holds both copies too, so both are accounted for and
    // neither is dropped.
    live.stored = [TAILED, TAILED]
    await user.click(refreshButton())
    await waitFor(() => expect(live.filterCalls).toBeGreaterThan(0))
    await new Promise((r) => setTimeout(r, 20))

    expect(rowsShowing(TAILED.message)).toBe(2)
  })

  it("hides a tailed event when Clear hides the page copy of it", async () => {
    const { user } = renderTailViewer()

    await user.click(await tailButton())
    await waitFor(() => expect(live.sessions).toHaveLength(1))

    live.sessions[0].push(TAILED)
    await waitFor(() => expect(rowsShowing(TAILED.message)).toBe(1))

    // Once the event is on disk both sources carry it, and reconciling leaves
    // one row. Clear has to take that row away whichever source it came from.
    live.stored = [TAILED]
    await user.click(refreshButton())
    await waitFor(() => expect(live.filterCalls).toBeGreaterThan(0))
    await user.click(await clearButton())

    expect(rowsShowing(TAILED.message)).toBe(0)
  })
})

/*
 * The level badge and the row tint are driven by the same `detectLogLevel`
 * result, and they used to disagree about which rows deserved one: a JSON
 * document got both, a plain line got the tint alone unless Format happened to
 * be ticked. These pin that they now agree, because the whole point of the
 * badge is reading a stream that mixes the two.
 */

/** A `console.warn` under the Node runtime's text log format. */
const CONSOLE_WARN =
  "2026-08-10T02:34:39.674Z\t13aa488f-ea9e-4d38-bdfe-74ad3d71e708\tWARN\tCannot push rates for property '934811' as there are no rates available."

/** The same severity, the way Powertools writes it. */
const POWERTOOLS_WARN =
  '{"level":"WARN","message":"Cannot push rates for property \'934811\'","service":"rate-pusher"}'

/** A timed-out invocation's report, as a runtime on `LogFormat: JSON` writes it. */
const TIMED_OUT_REPORT =
  '{"time":"2026-08-10T02:34:42.700Z","type":"platform.report","record":{"requestId":"13aa488f","status":"timeout","metrics":{"durationMs":3003.12}}}'

function levelled(messages: string[]): FilteredLogEvent[] {
  return messages.map((message, i) => ({
    timestamp: 1_000 + i,
    ingestionTime: 1_000 + i,
    logStreamName: "s1",
    message,
  }))
}

/** The badge is the only element whose whole text is the level name. */
const badge = (level: string) => screen.queryByText(level, { exact: true })

describe("LogEventsViewer > level badges", () => {
  it("labels a plain-text line whose level is a column, not a field", async () => {
    renderViewer(levelled([CONSOLE_WARN]))

    expect(await screen.findByText("warn")).toBeInTheDocument()
  })

  it("labels the same severity written as JSON", async () => {
    renderViewer(levelled([POWERTOOLS_WARN]))

    expect(await screen.findByText("warn")).toBeInTheDocument()
  })

  it("labels both shapes in one stream, which is how they arrive", async () => {
    renderViewer(levelled([CONSOLE_WARN, POWERTOOLS_WARN]))

    expect(await screen.findAllByText("warn")).toHaveLength(2)
  })

  it("labels a system log record with the level AWS assigns it", async () => {
    // A run that did not succeed is a warning, per AWS's system log level
    // mapping — the row reads as the REPORT line the record replaced.
    renderViewer(levelled([TIMED_OUT_REPORT]))

    expect(await screen.findByText("warn")).toBeInTheDocument()
    expect(screen.getByText(/REPORT RequestId: 13aa488f/)).toBeInTheDocument()
  })

  it("adds no badge to a line with no level to report", async () => {
    renderViewer(levelled(["Certificate request self-signature ok"]))

    expect(await screen.findByText(/Certificate request/)).toBeInTheDocument()
    expect(badge("warn")).not.toBeInTheDocument()
    expect(badge("info")).not.toBeInTheDocument()
  })

  it("still labels once Format is ticked", async () => {
    const { user } = renderViewer(levelled([CONSOLE_WARN]))
    await screen.findByText("warn")

    await user.click(screen.getByRole("checkbox", { name: /format/i }))

    expect(badge("warn")).toBeInTheDocument()
  })

  it("drops the badge in plaintext mode, which shows the line as it arrived", async () => {
    const { user } = renderViewer(levelled([CONSOLE_WARN]))
    await screen.findByText("warn")

    await user.click(screen.getByRole("button", { name: /^plaintext$/i }))

    expect(badge("warn")).not.toBeInTheDocument()
  })
})
