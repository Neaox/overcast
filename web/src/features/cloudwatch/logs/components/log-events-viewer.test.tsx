import { describe, expect, it, vi } from "vitest"
import { createTestQueryClient, renderWithRouter, screen } from "@/test/render"
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

const GROUP = "/aws/lambda/checkout"

const EVENTS: FilteredLogEvent[] = [
  { timestamp: 1_000, ingestionTime: 1_000, logStreamName: "s1", message: "first message" },
  { timestamp: 2_000, ingestionTime: 2_000, logStreamName: "s1", message: "second message" },
]

function renderViewer(events: FilteredLogEvent[] = EVENTS) {
  const queryClient = createTestQueryClient()
  queryClient.setQueryData(logsFilterQueryOptions(GROUP, {}).queryKey, {
    events,
    searchedLogStreams: [],
  })

  return renderWithRouter(
    () => (
      <ToastContextProvider>
        <TooltipProvider>
          <LogEventsViewer groupName={GROUP} />
        </TooltipProvider>
      </ToastContextProvider>
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
