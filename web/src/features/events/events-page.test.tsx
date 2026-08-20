import { describe, expect, it, vi } from "vitest"
import { render, renderWithData, screen } from "@/test/render"
import { EventsPage } from "./events-page"
import { eventStreamQueryOptions } from "@/hooks/use-event-stream"
import type { StreamEvent } from "@/hooks/use-event-stream"

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 34,
    getVirtualItems: () =>
      Array.from({ length: count }, (_, index) => ({
        index,
        key: index,
        start: index * 34,
      })),
    measureElement: vi.fn(),
    scrollToIndex: vi.fn(),
  }),
}))

function seedEvents(events: StreamEvent[]): [queryKey: readonly unknown[], data: unknown][] {
  return [[eventStreamQueryOptions().queryKey, events]]
}

describe("EventsPage", () => {
  it("renders when event sources are not navigable services", () => {
    render(<EventsPage />)

    expect(screen.getByRole("heading", { name: "Event Stream" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /sources/i })).toBeInTheDocument()
  })

  it("checks non-request sources by default", async () => {
    const { user } = render(<EventsPage />)

    await user.click(screen.getByRole("button", { name: /sources/i }))

    expect(screen.queryByText("Hide requests")).not.toBeInTheDocument()
    expect(screen.getByRole("checkbox", { name: "Requests" })).not.toBeChecked()
    expect(screen.getByRole("checkbox", { name: "Service errors" })).toBeChecked()
  })

  // Regression test for the default-allow requirement: the source filter
  // must never be a hardcoded enumeration that silently hides a source it
  // doesn't already know about (e.g. a service wired into the bus later).
  it("shows events from a source it has never seen before, by default", () => {
    renderWithData(
      <EventsPage />,
      seedEvents([
        {
          type: "widget:Frobnicated",
          source: "totally-new-service",
          time: "2026-07-25T12:00:00Z",
          payload: { id: "1" },
        },
      ]),
    )

    // The event renders without the user touching any filter.
    expect(screen.getByText("totally-new-service")).toBeInTheDocument()
  })

  it("lists a never-before-seen source as checked in the source dropdown", async () => {
    const { user } = renderWithData(
      <EventsPage />,
      seedEvents([
        {
          type: "widget:Frobnicated",
          source: "totally-new-service",
          time: "2026-07-25T12:00:00Z",
          payload: { id: "1" },
        },
      ]),
    )

    await user.click(screen.getByRole("button", { name: /sources/i }))

    expect(screen.getByRole("checkbox", { name: "Totally New Service" })).toBeChecked()
  })

  // Regression test for the "Pings does nothing" bug: toggling the heartbeat
  // toggle must actually reveal heartbeat events, which requires the
  // "system" source (heartbeats' source) to be visible by default too —
  // previously it was excluded from the hardcoded source enumeration
  // entirely, so no amount of toggling could ever show it.
  it("reveals heartbeat events once the Pings toggle is turned on", async () => {
    const { user } = renderWithData(
      <EventsPage />,
      seedEvents([
        {
          type: "heartbeat",
          source: "system",
          time: "2026-07-25T12:00:00Z",
          payload: null,
        },
      ]),
    )

    // Hidden by default (includeHeartbeats defaults to false at the hook level).
    expect(screen.queryByText("system")).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Show heartbeat pings" }))

    expect(await screen.findByText("system")).toBeInTheDocument()
  })

  // Regression test for the reversed bulk actions: "Hide all" used to be
  // wired to the show-everything callback (and vice versa), so each button
  // did the opposite of its label.
  it("hides every source with Hide all and restores them with Show all", async () => {
    const { user } = renderWithData(
      <EventsPage />,
      seedEvents([
        {
          type: "widget:Frobnicated",
          source: "totally-new-service",
          time: "2026-07-25T12:00:00Z",
          payload: { id: "1" },
        },
      ]),
    )

    expect(screen.getByText("totally-new-service")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: /sources/i }))
    await user.click(screen.getByRole("button", { name: "Hide all" }))

    // Every source is now hidden: the event disappears and its checkbox unchecks.
    expect(screen.queryByText("totally-new-service")).not.toBeInTheDocument()
    expect(screen.getByRole("checkbox", { name: "Totally New Service" })).not.toBeChecked()

    await user.click(screen.getByRole("button", { name: "Show all" }))

    expect(screen.getByText("totally-new-service")).toBeInTheDocument()
    expect(screen.getByRole("checkbox", { name: "Totally New Service" })).toBeChecked()
  })
  // The point of the envelope-level request id: pasting one narrows the
  // console to everything that call set off, not just the request:Received
  // row summarising it. Before the id was on the envelope only that one row
  // matched, which is the least interesting event of the set.
  it("filters to every event one request caused when its id is searched", async () => {
    const requestId = "3f2a1c88-0000-4444-8888-abcdefabcdef"
    const { user } = renderWithData(
      <EventsPage />,
      seedEvents([
        {
          type: "s3:ObjectCreated:*",
          source: "s3",
          time: "2026-07-25T12:00:00Z",
          requestId,
          payload: { Bucket: "assets", Key: "logo.svg" },
        },
        {
          type: "sqs:MessageSent",
          source: "sqs",
          time: "2026-07-25T12:00:01Z",
          requestId,
          payload: { name: "notifications" },
        },
        {
          type: "sqs:MessageSent",
          source: "sqs",
          time: "2026-07-25T12:00:02Z",
          requestId: "a-different-request",
          payload: { name: "unrelated" },
        },
      ]),
    )

    await user.type(screen.getByPlaceholderText(/filter events/i), requestId)

    expect(screen.getByText("ObjectCreated")).toBeInTheDocument()
    expect(screen.getByText(/notifications/)).toBeInTheDocument()
    expect(screen.queryByText(/unrelated/)).not.toBeInTheDocument()
  })
})
