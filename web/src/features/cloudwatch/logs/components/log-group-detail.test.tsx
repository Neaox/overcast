/*
 * The group page's DescribeLogStreams answering ResourceNotFoundException
 * means the group itself is missing from the selected region. It used to
 * render the ordinary "No log streams" empty state — the wrong-region trap
 * reads as an application that never logged, when the real story is the
 * region selector pointing away from where the group lives.
 */
import { beforeEach, describe, expect, it, vi } from "vitest"
import { within } from "@testing-library/react"
import { createTestQueryClient, renderWithRouter, screen } from "@/test/render"
import { LogGroupDetail } from "./log-group-detail"

const backend = vi.hoisted(() => ({
  streamsError: null as Error | null,
  streams: [] as Record<string, unknown>[],
}))

vi.mock("@/services/aws-clients", () => ({
  awsClients: {
    logs: () => ({
      send: (command: { constructor: { name: string } }) => {
        if (command.constructor.name === "DescribeLogStreamsCommand") {
          if (backend.streamsError) return Promise.reject(backend.streamsError)
          return Promise.resolve({ logStreams: backend.streams })
        }
        if (command.constructor.name === "DescribeLogGroupsCommand") {
          return Promise.resolve({ logGroups: [] })
        }
        return Promise.resolve({ tags: {} })
      },
    }),
  },
}))

const GROUP = "/aws/lambda/checkout"

function renderDetail() {
  return renderWithRouter(() => <LogGroupDetail groupName={GROUP} />, {
    queryClient: createTestQueryClient(),
  })
}

describe("LogGroupDetail > resource not found", () => {
  it("shows a not-found state naming the group and region, not the empty-streams state", async () => {
    const error = new Error(`The specified log group does not exist: ${GROUP}`)
    error.name = "ResourceNotFoundException"
    backend.streams = []
    backend.streamsError = error

    renderDetail()

    expect(await screen.findByText("Log group not found")).toBeInTheDocument()
    expect(
      screen.getByText(/No log group named .*checkout.* exists in us-east-1/),
    ).toBeInTheDocument()
    expect(screen.queryByText("No log streams")).not.toBeInTheDocument()
  })

  it("keeps the ordinary empty state for a group that exists and has no streams", async () => {
    backend.streamsError = null
    backend.streams = []

    renderDetail()

    expect(await screen.findByText("No log streams")).toBeInTheDocument()
    expect(screen.queryByText("Log group not found")).not.toBeInTheDocument()
  })
})

/*
 * The streams table moved onto `ResourceTable variant="embedded"` in #1327
 * wave C. Selection is the part that had to survive that: `rowSelectionFeature`
 * is deliberately unregistered, so the checkbox is an ordinary leading column
 * whose cell has to keep its click away from the row's navigate handler.
 */
describe("LogGroupDetail > streams table", () => {
  beforeEach(() => {
    backend.streamsError = null
    // Creation order is the inverse of ingestion order, so "sorted by Created"
    // and "the page's default order" cannot pass for one another.
    backend.streams = [
      { logStreamName: "2024/06/01/[$LATEST]aaa", creationTime: 10, lastIngestionTime: 3 },
      { logStreamName: "2024/06/02/[$LATEST]bbb", creationTime: 30, lastIngestionTime: 1 },
      { logStreamName: "2024/06/03/[$LATEST]ccc", creationTime: 20, lastIngestionTime: 2 },
    ]
  })

  it("keeps the most-recently-written-first order the page sorts in", async () => {
    renderDetail()

    await screen.findByText("2024/06/01/[$LATEST]aaa")
    const rows = screen.getAllByRole("row").slice(1)
    expect(rows.map((row) => within(row).getAllByRole("cell")[1].textContent)).toEqual([
      "2024/06/01/[$LATEST]aaa",
      "2024/06/03/[$LATEST]ccc",
      "2024/06/02/[$LATEST]bbb",
    ])
  })

  // A timestamp column's first click is descending — "most recent first" in
  // one click, which is what `ResourceTable` infers for a date/count column.
  it("re-orders by a sortable header without losing the selection column", async () => {
    const { user } = renderDetail()

    await screen.findByText("2024/06/01/[$LATEST]aaa")
    await user.click(screen.getByRole("button", { name: "Created" }))

    const rows = screen.getAllByRole("row").slice(1)
    expect(rows.map((row) => within(row).getAllByRole("cell")[1].textContent)).toEqual([
      "2024/06/02/[$LATEST]bbb",
      "2024/06/03/[$LATEST]ccc",
      "2024/06/01/[$LATEST]aaa",
    ])
    // Three row checkboxes plus the header's select-all.
    expect(screen.getAllByRole("checkbox")).toHaveLength(4)
  })

  it("selects a stream without navigating to it", async () => {
    const { user } = renderDetail()

    await screen.findByText("2024/06/01/[$LATEST]aaa")
    await user.click(screen.getByRole("checkbox", { name: "Select 2024/06/01/[$LATEST]aaa" }))

    expect(screen.getByText("1 stream selected")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Delete Selected" })).toBeInTheDocument()
  })
})
