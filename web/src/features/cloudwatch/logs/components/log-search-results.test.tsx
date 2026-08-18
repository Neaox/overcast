import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import type { FilteredLogEvent } from "@/types/logs"
import { LogSearchResults } from "./log-search-results"

// jsdom gives every element a zero height, so the real virtualizer renders no
// rows at all and nothing about the results would be observable.
vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 32,
    getVirtualItems: () =>
      Array.from({ length: count }, (_, index) => ({
        index,
        key: index,
        start: index * 32,
        end: index * 32 + 32,
      })),
    measureElement: vi.fn(),
    scrollToIndex: vi.fn(),
    scrollOffset: 0,
  }),
}))

const EVENTS: FilteredLogEvent[] = [
  {
    timestamp: 1_700_000_000_000,
    ingestionTime: 1_700_000_000_000,
    logStreamName: "stream-a",
    message: "ERROR upstream refused",
  },
  {
    timestamp: 1_700_000_001_000,
    ingestionTime: 1_700_000_001_000,
    logStreamName: "stream-b",
    message: "all quiet on stream b",
  },
]

describe("LogSearchResults", () => {
  it("renders every result with its stream and level", () => {
    render(<LogSearchResults events={EVENTS} filterPattern="" onOpenEvent={() => {}} />)

    expect(screen.getByText(/upstream refused/)).toBeInTheDocument()
    expect(screen.getByText(/all quiet/)).toBeInTheDocument()
    expect(screen.getByText("stream-a")).toBeInTheDocument()
    // The search results used to be the one surface without level labelling.
    expect(screen.getByText("error", { exact: true })).toBeInTheDocument()
  })

  it("marks the filter's matches inside the message", () => {
    render(<LogSearchResults events={EVENTS} filterPattern="upstream" onOpenEvent={() => {}} />)

    const marks = screen.getAllByText("upstream", { selector: "mark" })
    expect(marks).toHaveLength(1)
  })

  it("opens the clicked event — stream, timestamp and signature — not just its stream", async () => {
    const user = userEvent.setup()
    const onOpenEvent = vi.fn()
    render(<LogSearchResults events={EVENTS} filterPattern="" onOpenEvent={onOpenEvent} />)

    await user.click(screen.getByText(/all quiet/))

    expect(onOpenEvent).toHaveBeenCalledWith({
      streamName: "stream-b",
      timestamp: 1_700_000_001_000,
      signature: expect.stringMatching(/^[0-9a-z]+$/) as string,
    })
  })
})
