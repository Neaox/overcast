import { useState } from "react"
import { Bell } from "lucide-react"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { render, renderWithRouter, screen, waitFor, within } from "@/test/render"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"
import { ResourceTable, type ResourceTableColumn, type ResourceTableSort } from "./resource-table"

interface Topic {
  arn: string
  name: string
}

const columns: ResourceTableColumn<Topic>[] = [
  { header: "Name", cell: (t) => t.name },
  { header: "ARN", cell: (t) => t.arn },
]

function TopicsTable(props: {
  data?: Topic[]
  isLoading?: boolean
  error?: unknown
  onRowClick?: (t: Topic) => void
  onDeleteMutate?: (id: string) => void
  isDeletePending?: boolean
  isFiltered?: boolean
  onClearFilter?: () => void
}) {
  const [deleteTarget, setDeleteTarget] = useState<Topic>()

  return (
    <ResourceTable
      query={{ data: props.data, isLoading: props.isLoading ?? false, error: props.error }}
      columns={columns}
      rowKey={(t) => t.arn}
      noun="topics"
      emptyIcon={Bell}
      emptyTitle="No topics yet"
      emptyDescription="Create a topic to get started."
      isFiltered={props.isFiltered}
      onClearFilter={props.onClearFilter}
      onRowClick={props.onRowClick}
      onDelete={
        props.onDeleteMutate
          ? {
              target: deleteTarget,
              onRequest: setDeleteTarget,
              onOpenChange: (open) => !open && setDeleteTarget(undefined),
              mutation: {
                mutate: (id: string) => {
                  props.onDeleteMutate?.(id)
                  setDeleteTarget(undefined)
                },
                isPending: props.isDeletePending ?? false,
              },
              getId: (t) => t.arn,
              label: (t) => t.name,
              noun: "topic",
            }
          : undefined
      }
    />
  )
}

const topics: Topic[] = [
  { arn: "arn:aws:sns:us-east-1:1:a", name: "alerts" },
  { arn: "arn:aws:sns:us-east-1:1:b", name: "billing" },
]

describe("ResourceTable > loading and empty states", () => {
  it("shows a loading skeleton while the query is in flight", () => {
    const { container } = render(<TopicsTable isLoading data={[]} />)
    expect(container.querySelector('[data-slot="skeleton-row"]')).toBeInTheDocument()
  })

  it("shows the empty state when there is no data", () => {
    render(<TopicsTable data={[]} />)
    expect(screen.getByText("No topics yet")).toBeInTheDocument()
    expect(screen.getByText("Create a topic to get started.")).toBeInTheDocument()
  })

  it("shows an error state when the query failed", () => {
    render(<TopicsTable data={[]} error={new Error("network down")} />)
    expect(screen.getByText("Failed to load topics")).toBeInTheDocument()
    expect(screen.getByText("network down")).toBeInTheDocument()
  })
})

// The three states a list page can land in when it's empty: nothing exists
// yet (create CTA), a filter narrowed it to nothing (clear-filter action,
// distinct copy), or the fetch failed (error copy, wins over both).
describe("ResourceTable > filtered vs. true-empty vs. error", () => {
  it("shows the create-CTA empty state when isFiltered is unset", () => {
    render(<TopicsTable data={[]} />)
    expect(screen.getByText("No topics yet")).toBeInTheDocument()
    expect(screen.getByText("Create a topic to get started.")).toBeInTheDocument()
    expect(screen.queryByText("No results match your filter.")).not.toBeInTheDocument()
  })

  it("shows 'no matches' copy and a clear-filter action instead of the create CTA when isFiltered is set", () => {
    const onClearFilter = vi.fn()
    render(<TopicsTable data={[]} isFiltered onClearFilter={onClearFilter} />)

    expect(screen.getByText("No matching topics")).toBeInTheDocument()
    expect(screen.getByText("No topics match your filter.")).toBeInTheDocument()
    // The true-empty copy must not also be showing.
    expect(screen.queryByText("No topics yet")).not.toBeInTheDocument()
    expect(screen.queryByText("Create a topic to get started.")).not.toBeInTheDocument()

    expect(screen.getByRole("button", { name: "Clear filter" })).toBeInTheDocument()
  })

  it("clicking Clear filter calls onClearFilter", async () => {
    const onClearFilter = vi.fn()
    const { user } = render(<TopicsTable data={[]} isFiltered onClearFilter={onClearFilter} />)

    await user.click(screen.getByRole("button", { name: "Clear filter" }))
    expect(onClearFilter).toHaveBeenCalledOnce()
  })

  it("shows the error state, not the filtered-empty state, when a filtered query also failed", () => {
    render(
      <TopicsTable
        data={[]}
        error={new Error("network down")}
        isFiltered
        onClearFilter={() => {}}
      />,
    )
    expect(screen.getByText("Failed to load topics")).toBeInTheDocument()
    expect(screen.getByText("network down")).toBeInTheDocument()
    // Neither empty-state variant should show — the error must win.
    expect(screen.queryByText("No matching topics")).not.toBeInTheDocument()
    expect(screen.queryByText("No topics yet")).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Clear filter" })).not.toBeInTheDocument()
  })
})

describe("ResourceTable > populated table", () => {
  it("renders one row per resource with the configured columns", () => {
    render(<TopicsTable data={topics} />)
    const row = screen.getByRole("row", { name: /alerts/ })
    expect(within(row).getByText("arn:aws:sns:us-east-1:1:a")).toBeInTheDocument()
    expect(screen.getByRole("row", { name: /billing/ })).toBeInTheDocument()
  })

  it("calls onRowClick with the row's item", async () => {
    const onRowClick = vi.fn()
    const { user } = render(<TopicsTable data={topics} onRowClick={onRowClick} />)
    await user.click(screen.getByRole("row", { name: /alerts/ }))
    expect(onRowClick).toHaveBeenCalledWith(topics[0])
  })
})

describe("ResourceTable > delete flow", () => {
  it("opens a confirm dialog naming the row and mutates its id on confirm", async () => {
    const onDeleteMutate = vi.fn()
    const { user } = render(<TopicsTable data={topics} onDeleteMutate={onDeleteMutate} />)

    await user.click(screen.getByRole("button", { name: "Delete alerts" }))
    expect(screen.getByRole("dialog")).toBeInTheDocument()
    expect(screen.getByText("alerts", { selector: "span" })).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Delete" }))
    expect(onDeleteMutate).toHaveBeenCalledWith("arn:aws:sns:us-east-1:1:a")
  })

  it("does not trigger the row click when the delete action is clicked", async () => {
    const onRowClick = vi.fn()
    const { user } = render(
      <TopicsTable data={topics} onRowClick={onRowClick} onDeleteMutate={() => {}} />,
    )
    await user.click(screen.getByRole("button", { name: "Delete alerts" }))
    expect(onRowClick).not.toHaveBeenCalled()
  })

  it("does not render a delete action when onDelete is not configured", () => {
    render(<TopicsTable data={topics} />)
    expect(screen.queryByRole("button", { name: /Delete/ })).not.toBeInTheDocument()
  })
})

// ─── TanStack engine: sorting, visibility, pagination ─────────────────────

interface Stream {
  name: string
  createdAt: Date
  retention: number
}

const streams: Stream[] = [
  { name: "stream-10", createdAt: new Date("2026-03-01T00:00:00Z"), retention: 7 },
  { name: "stream-2", createdAt: new Date("2026-01-01T00:00:00Z"), retention: 30 },
  { name: "stream-30", createdAt: new Date("2026-02-01T00:00:00Z"), retention: 1 },
]

/** Body-row order, read from the first cell of each row. */
function rowOrder(): string[] {
  const [, body] = screen.getAllByRole("rowgroup")
  return within(body)
    .getAllByRole("row")
    .map((row) => within(row).getAllByRole("cell")[0].textContent)
}

function StreamsTable(props: {
  sortable?: boolean
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
  defaultSort?: ResourceTableSort
  pageSize?: number
  columnToggle?: boolean
  hideRetention?: boolean
  data?: Stream[]
}) {
  return (
    <ResourceTable
      query={{ data: props.data ?? streams, isLoading: false }}
      noun="streams"
      rowKey={(s) => s.name}
      sort={props.sort}
      onSortChange={props.onSortChange}
      defaultSort={props.defaultSort}
      pageSize={props.pageSize}
      columnToggle={props.columnToggle}
      columns={[
        {
          header: "Name",
          sortValue: props.sortable === false ? undefined : (s) => s.name,
          cell: (s) => s.name,
        },
        {
          header: "Created",
          sortValue: props.sortable === false ? undefined : (s) => s.createdAt,
          cell: (s) => s.createdAt.toISOString().slice(0, 10),
        },
        {
          id: "retention-days",
          header: "Retention",
          defaultHidden: props.hideRetention,
          sortValue: props.sortable === false ? undefined : (s) => s.retention,
          cell: (s) => `${s.retention}d`,
        },
      ]}
    />
  )
}

describe("ResourceTable > sorting", () => {
  it("leaves a column without sortValue as a plain header", () => {
    render(<StreamsTable sortable={false} />)
    const header = screen.getByRole("columnheader", { name: "Name" })
    expect(header).not.toHaveAttribute("aria-sort")
    expect(within(header).queryByRole("button")).not.toBeInTheDocument()
  })

  it("turns a column with sortValue into an unsorted sort control", () => {
    render(<StreamsTable />)
    expect(screen.getByRole("columnheader", { name: "Name" })).toHaveAttribute("aria-sort", "none")
    expect(screen.getByRole("button", { name: "Name" })).toBeInTheDocument()
  })

  it("cycles none → ascending → descending → none on header clicks", async () => {
    const { user } = render(<StreamsTable />)
    const header = () => screen.getByRole("columnheader", { name: "Name" })

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(header()).toHaveAttribute("aria-sort", "ascending")

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(header()).toHaveAttribute("aria-sort", "descending")

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(header()).toHaveAttribute("aria-sort", "none")
  })

  it("orders names alphanumerically, so stream-2 precedes stream-10", async () => {
    const { user } = render(<StreamsTable />)
    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(rowOrder()).toEqual(["stream-2", "stream-10", "stream-30"])
  })

  // Text sorts A→Z on the first click; anything else — dates, counts — starts
  // descending, so "most recent" and "largest" are one click away.
  it("orders a Date column newest-first on the first click", async () => {
    const { user } = render(<StreamsTable />)
    await user.click(screen.getByRole("button", { name: "Created" }))
    expect(rowOrder()).toEqual(["stream-10", "stream-30", "stream-2"])

    await user.click(screen.getByRole("button", { name: "Created" }))
    expect(rowOrder()).toEqual(["stream-2", "stream-30", "stream-10"])
  })

  it("applies defaultSort before the user touches a header", () => {
    render(<StreamsTable defaultSort={{ id: "retention-days", desc: true }} />)
    expect(rowOrder()).toEqual(["stream-2", "stream-10", "stream-30"])
    expect(screen.getByRole("columnheader", { name: "Retention" })).toHaveAttribute(
      "aria-sort",
      "descending",
    )
  })

  // Only one column is sorted at a time — the URL param is one token.
  it("replaces the active sort rather than adding to it", async () => {
    const { user } = render(<StreamsTable />)
    await user.click(screen.getByRole("button", { name: "Name" }))
    await user.click(screen.getByRole("button", { name: "Created" }))

    expect(screen.getByRole("columnheader", { name: "Name" })).toHaveAttribute("aria-sort", "none")
    expect(screen.getByRole("columnheader", { name: "Created" })).toHaveAttribute(
      "aria-sort",
      "descending",
    )
  })
})

describe("ResourceTable > controlled sort", () => {
  it("reports the clicked column and direction to onSortChange", async () => {
    const onSortChange = vi.fn()
    const { user } = render(<StreamsTable onSortChange={onSortChange} />)

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(onSortChange).toHaveBeenCalledWith({ id: "name", desc: false })
  })

  it("uses the column's explicit id as the sort key", async () => {
    const onSortChange = vi.fn()
    const { user } = render(<StreamsTable onSortChange={onSortChange} />)

    await user.click(screen.getByRole("button", { name: "Retention" }))
    expect(onSortChange).toHaveBeenCalledWith({ id: "retention-days", desc: true })
  })

  it("renders the order the sort prop asks for", () => {
    render(<StreamsTable sort={{ id: "name", desc: true }} onSortChange={() => {}} />)
    expect(rowOrder()).toEqual(["stream-30", "stream-10", "stream-2"])
  })

  it("clears the sort back to undefined at the end of the cycle", async () => {
    const onSortChange = vi.fn()
    const { user } = render(
      <StreamsTable sort={{ id: "name", desc: true }} onSortChange={onSortChange} />,
    )
    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(onSortChange).toHaveBeenCalledWith(undefined)
  })
})

describe("ResourceTable > column visibility", () => {
  it("offers every column but the first, which identifies the row", async () => {
    const { user } = render(<StreamsTable />)
    await user.click(screen.getByRole("button", { name: /Columns/ }))

    expect(screen.getByRole("checkbox", { name: /Created/ })).toBeInTheDocument()
    expect(screen.getByRole("checkbox", { name: /Retention/ })).toBeInTheDocument()
    expect(screen.queryByRole("checkbox", { name: /Name/ })).not.toBeInTheDocument()
  })

  it("hides the column's header and cells when it is toggled off", async () => {
    const { user } = render(<StreamsTable />)
    await user.click(screen.getByRole("button", { name: /Columns/ }))
    await user.click(screen.getByRole("checkbox", { name: /Created/ }))

    expect(screen.queryByRole("columnheader", { name: "Created" })).not.toBeInTheDocument()
    expect(screen.queryByText("2026-03-01")).not.toBeInTheDocument()
    expect(screen.getByRole("columnheader", { name: "Name" })).toBeInTheDocument()
  })

  it("starts a defaultHidden column hidden and lets the menu bring it back", async () => {
    const { user } = render(<StreamsTable hideRetention />)
    expect(screen.queryByRole("columnheader", { name: "Retention" })).not.toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: /Columns/ }))
    await user.click(screen.getByRole("checkbox", { name: /Retention/ }))
    expect(screen.getByRole("columnheader", { name: "Retention" })).toBeInTheDocument()
  })

  it("does not show the menu when only one column can be hidden", () => {
    render(<TopicsTable data={topics} />)
    expect(screen.queryByRole("button", { name: /Columns/ })).not.toBeInTheDocument()
  })

  it("can be forced off", () => {
    render(<StreamsTable columnToggle={false} />)
    expect(screen.queryByRole("button", { name: /Columns/ })).not.toBeInTheDocument()
  })
})

describe("ResourceTable > pagination", () => {
  it("renders no pager without pageSize", () => {
    render(<StreamsTable />)
    expect(screen.queryByRole("button", { name: "Next" })).not.toBeInTheDocument()
  })

  it("pages the rows and walks forward and back", async () => {
    const { user } = render(<StreamsTable pageSize={2} />)
    expect(rowOrder()).toEqual(["stream-10", "stream-2"])
    expect(screen.getByText(/Page 1 of 2/)).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Next" }))
    expect(rowOrder()).toEqual(["stream-30"])
    expect(screen.getByText(/Page 2 of 2/)).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Previous" }))
    expect(rowOrder()).toEqual(["stream-10", "stream-2"])
  })

  it("hides the pager when everything fits on one page", () => {
    render(<StreamsTable pageSize={10} />)
    expect(screen.queryByRole("button", { name: "Next" })).not.toBeInTheDocument()
  })
})

// The deep-link contract: the sort lives in the route's `sort` search param,
// exactly as the filter box lives in `q`. `useSortSearchParam` is the wiring.
describe("ResourceTable > sort bound to the URL", () => {
  function SortedStreamsRoute() {
    const search: { sort?: string } = useSearch({ strict: false })
    const navigate = useNavigate()
    const [sort, setSort] = useSortSearchParam(
      search,
      navigate as unknown as Parameters<typeof useSortSearchParam<{ sort?: string }>>[1],
    )
    return <StreamsTable sort={sort} onSortChange={setSort} />
  }

  it("renders the order named by the sort param on first paint", async () => {
    renderWithRouter(SortedStreamsRoute, { initialEntry: "/?sort=-name" })
    await screen.findByRole("button", { name: "Name" })
    expect(rowOrder()).toEqual(["stream-30", "stream-10", "stream-2"])
    expect(screen.getByRole("columnheader", { name: "Name" })).toHaveAttribute(
      "aria-sort",
      "descending",
    )
  })

  it("writes the clicked column into the URL", async () => {
    const { user, router } = renderWithRouter(SortedStreamsRoute, { initialEntry: "/" })

    await user.click(await screen.findByRole("button", { name: "Created" }))
    await waitFor(() =>
      expect((router.state.location.search as { sort?: string }).sort).toBe("-created"),
    )
    expect(rowOrder()).toEqual(["stream-10", "stream-30", "stream-2"])
  })

  it("drops the param entirely when the sort is cleared", async () => {
    const { user, router } = renderWithRouter(SortedStreamsRoute, {
      initialEntry: "/?sort=-name",
    })

    await user.click(await screen.findByRole("button", { name: "Name" }))
    await waitFor(() =>
      expect((router.state.location.search as { sort?: string }).sort).toBeUndefined(),
    )
    expect(router.state.location.searchStr).not.toContain("sort")
  })
})
