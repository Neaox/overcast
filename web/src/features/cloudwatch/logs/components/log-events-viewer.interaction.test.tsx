/**
 * Interaction QOL on the stream viewer: requestId click-to-filter, per-row
 * copy-deep-link, and keyboard row navigation. Split from
 * log-events-viewer.test.tsx because these tests wrap `LogMessage` in a
 * render-counting spy (to pin that cursor moves never re-render the memoised
 * row content) and mock the clipboard — file-wide mocks the display tests
 * must not inherit.
 */
import { fireEvent } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import type * as LogMessageModule from "@/components/logs/log-message"
import { createTestQueryClient, renderWithRouter, screen, waitFor } from "@/test/render"
import { ToastContextProvider } from "@/components/ui/toast"
import { TooltipProvider } from "@/components/ui/tooltip"
import { logsFilterInfiniteQueryOptions } from "@/features/cloudwatch/logs/data"
import { logEventSignature } from "@/features/cloudwatch/logs/tail"
import type { FilteredLogEvent } from "@/types/logs"
import { LogEventsViewer } from "./log-events-viewer"

const virt = vi.hoisted(() => ({
  scrollToIndex: vi.fn(),
  scrollToOffset: vi.fn(),
}))

// jsdom gives every element a zero height, so the real virtualizer renders no
// rows at all — same mock as the display test file.
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
    measure: vi.fn(),
    scrollToIndex: virt.scrollToIndex,
    scrollToOffset: virt.scrollToOffset,
    getOffsetForIndex: (index: number) => [index * 34, "start"] as const,
    scrollOffset: 0,
  }),
}))

/** Counts renders of the real (memoised) row content component. */
const rowRenders = vi.hoisted(() => ({ count: 0 }))

vi.mock("@/components/logs/log-message", async (importOriginal) => {
  const actual = await importOriginal<typeof LogMessageModule>()
  const { memo, createElement } = await import("react")
  const Inner = actual.LogMessage
  // Memoised like the real thing, so the counter increments exactly when the
  // props the viewer passes change — which is the property under test.
  const LogMessage = memo(function LogMessageSpy(props: React.ComponentProps<typeof Inner>) {
    rowRenders.count++
    return createElement(Inner, props)
  })
  return { ...actual, LogMessage }
})

const clipboard = vi.hoisted(() => ({ writeClipboardText: vi.fn(() => Promise.resolve()) }))

vi.mock("@/lib/clipboard", () => ({
  writeClipboardText: clipboard.writeClipboardText,
  canReadClipboardText: () => false,
  readClipboardText: () => Promise.reject(new Error("unavailable")),
}))

const live = vi.hoisted(() => ({
  stored: [] as { timestamp: number; message: string; logStreamName: string }[],
  filterInputs: [] as {
    filterPattern?: string
    startTime?: number
    endTime?: number
    nextToken?: string
  }[],
  reset() {
    this.stored = []
    this.filterInputs = []
  },
}))

vi.mock("@/services/aws-clients", () => ({
  awsClients: {
    logs: () => ({
      send: (command: { constructor: { name: string }; input?: Record<string, unknown> }) => {
        if (command.constructor.name === "FilterLogEventsCommand") {
          live.filterInputs.push({ ...(command.input ?? {}) })
          return Promise.resolve({
            events: live.stored.map((e) => ({ ...e })),
            searchedLogStreams: [],
          })
        }
        if (command.constructor.name === "DescribeLogStreamsCommand") {
          return Promise.resolve({ logStreams: [] })
        }
        return Promise.resolve({ responseStream: undefined })
      },
    }),
  },
}))

beforeEach(() => {
  virt.scrollToIndex.mockClear()
  virt.scrollToOffset.mockClear()
  clipboard.writeClipboardText.mockClear()
  rowRenders.count = 0
  localStorage.clear()
})

const GROUP = "/aws/lambda/checkout"
const RID = "13aa488f-ea9e-4d38-bdfe-74ad3d71e708"

const EVENTS: FilteredLogEvent[] = [
  { timestamp: 1_000, ingestionTime: 1_000, logStreamName: "s1", message: "first message" },
  {
    timestamp: 2_000,
    ingestionTime: 2_000,
    logStreamName: "s1",
    message: `2026-08-10T02:34:39.674Z\t${RID}\tINFO\thandled request`,
  },
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
  queryClient.setQueryData(logsFilterInfiniteQueryOptions(GROUP, {}).queryKey, {
    pages: [{ events, searchedLogStreams: [], nextToken: undefined }],
    pageParams: [undefined],
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

const listbox = () => screen.getByRole("listbox", { name: /log events/i })
const rowOf = (text: string | RegExp) => {
  const row = screen.getByText(text).closest("[data-index]")
  if (!row) throw new Error("row not found")
  return row
}

describe("LogEventsViewer > requestId click-to-filter", () => {
  it("offers a filter affordance only on rows that carry a request id", async () => {
    renderViewer()
    await screen.findByText("first message")

    // One row carries an id, one does not — exactly one affordance.
    expect(screen.getAllByRole("button", { name: /filter to request id/i })).toHaveLength(1)
    expect(rowOf(/handled request/).querySelector("[data-request-id]")).not.toBeNull()
    expect(rowOf("first message").querySelector("[data-request-id]")).toBeNull()
  })

  it("populates and runs the filter with the quoted id on click", async () => {
    const { user } = renderViewer()
    await screen.findByText("first message")

    await user.click(screen.getByRole("button", { name: /filter to request id/i }))

    // The id lands in the filter box quoted — FilterLogEvents' phrase-match
    // form, exactly what typing it would do — and the search runs with it.
    expect(screen.getByPlaceholderText<HTMLInputElement>(/^Filter/).value).toBe(`"${RID}"`)
    await waitFor(() =>
      expect(live.filterInputs.some((i) => i.filterPattern === `"${RID}"`)).toBe(true),
    )
  })
})

describe("LogEventsViewer > copy deep link", () => {
  it("copies an absolute link that reopens the viewer anchored on the event", async () => {
    const { user } = renderViewer()
    await screen.findByText("first message")

    const links = screen.getAllByRole("button", { name: /copy link to this event/i })
    expect(links).toHaveLength(2)
    await user.click(links[0])

    const expected =
      `${window.location.origin}/cloudwatch/logs/stream` +
      `?groupName=${encodeURIComponent(GROUP)}&streamName=s1&anchorTs=1000` +
      `&anchorSig=${logEventSignature(EVENTS[0])}`
    await waitFor(() => expect(clipboard.writeClipboardText).toHaveBeenCalledWith(expected))
  })
})

describe("LogEventsViewer > keyboard navigation", () => {
  it("moves a focused-row cursor with the arrow keys and j/k", async () => {
    renderViewer()
    await screen.findByText("first message")
    const box = listbox()
    box.focus()

    fireEvent.keyDown(box, { key: "ArrowDown" })
    expect(rowOf("first message")).toHaveAttribute("data-focused")
    expect(box).toHaveAttribute("aria-activedescendant", rowOf("first message").id)

    fireEvent.keyDown(box, { key: "j" })
    expect(rowOf(/handled request/)).toHaveAttribute("data-focused")
    expect(rowOf("first message")).not.toHaveAttribute("data-focused")
    expect(box).toHaveAttribute("aria-activedescendant", rowOf(/handled request/).id)

    fireEvent.keyDown(box, { key: "k" })
    expect(rowOf("first message")).toHaveAttribute("data-focused")

    fireEvent.keyDown(box, { key: "ArrowUp" })
    // Already at the first row — the cursor stops rather than wrapping.
    expect(rowOf("first message")).toHaveAttribute("data-focused")
  })

  it("scrolls the cursor's row into view without recentring it", async () => {
    renderViewer()
    await screen.findByText("first message")
    const box = listbox()
    box.focus()

    fireEvent.keyDown(box, { key: "ArrowDown" })

    // `align: "auto"` — no scroll at all when the row is already visible,
    // never a jump to centre.
    expect(virt.scrollToIndex).toHaveBeenCalledWith(0, { align: "auto" })
  })

  it("toggles the focused row's expansion with Enter in collapse mode", async () => {
    const { user } = renderViewer()
    await screen.findByText("first message")
    await user.click(await screen.findByRole("checkbox", { name: /collapse/i }))
    const box = listbox()
    box.focus()

    fireEvent.keyDown(box, { key: "ArrowDown" })
    fireEvent.keyDown(box, { key: "Enter" })

    expect(rowOf("first message").querySelector("pre")).not.toBeNull()
    // Only the focused row expanded.
    expect(rowOf(/handled request/).querySelector("pre")).toBeNull()

    fireEvent.keyDown(box, { key: "Enter" })
    expect(rowOf("first message").querySelector("pre")).toBeNull()
  })

  it("leaves Enter alone when collapse mode is off", async () => {
    renderViewer()
    await screen.findByText("first message")
    const box = listbox()
    box.focus()

    fireEvent.keyDown(box, { key: "ArrowDown" })
    fireEvent.keyDown(box, { key: "Enter" })

    // Full rendering already; Enter changed nothing.
    expect(rowOf("first message").querySelector("pre")).not.toBeNull()
  })

  it("copies the focused row's plain message with c", async () => {
    renderViewer()
    await screen.findByText("first message")
    const box = listbox()
    box.focus()

    fireEvent.keyDown(box, { key: "ArrowDown" })
    fireEvent.keyDown(box, { key: "c" })

    // The same value the row's copy button yields: the message sans ANSI.
    await waitFor(() => expect(clipboard.writeClipboardText).toHaveBeenCalledWith("first message"))
  })

  it("copies nothing when no row holds the cursor", async () => {
    renderViewer()
    await screen.findByText("first message")
    const box = listbox()
    box.focus()

    fireEvent.keyDown(box, { key: "c" })

    expect(clipboard.writeClipboardText).not.toHaveBeenCalled()
  })

  it("never captures keys typed into the filter input", async () => {
    const { user } = renderViewer()
    await screen.findByText("first message")

    await user.type(screen.getByPlaceholderText(/^Filter/), "jck")

    expect(document.querySelector("[data-focused]")).toBeNull()
    expect(virt.scrollToIndex).not.toHaveBeenCalled()
    expect(clipboard.writeClipboardText).not.toHaveBeenCalled()
  })

  it("moves the cursor without re-rendering any row's memoised content", async () => {
    renderViewer()
    await screen.findByText("first message")
    const box = listbox()
    box.focus()
    const before = rowRenders.count

    fireEvent.keyDown(box, { key: "ArrowDown" })
    expect(rowOf("first message")).toHaveAttribute("data-focused")
    fireEvent.keyDown(box, { key: "j" })
    expect(rowOf(/handled request/)).toHaveAttribute("data-focused")

    // The cursor lives on the row wrapper; LogMessage receives no
    // cursor-related props, so a cursor move re-renders zero rows' content.
    expect(rowRenders.count).toBe(before)
  })
})
