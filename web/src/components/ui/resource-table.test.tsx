import { useState } from "react"
import { Bell } from "lucide-react"
import { render, screen, within } from "@/test/render"
import { ResourceTable, type ResourceTableColumn } from "./resource-table"

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
