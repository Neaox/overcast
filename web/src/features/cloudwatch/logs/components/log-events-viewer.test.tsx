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

function renderViewer() {
  const queryClient = createTestQueryClient()
  queryClient.setQueryData(logsFilterQueryOptions(GROUP, {}).queryKey, {
    events: EVENTS,
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
