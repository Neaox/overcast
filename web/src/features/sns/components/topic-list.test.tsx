import { renderWithData, screen, within } from "@/test/render"
import { snsTopicsQueryOptions } from "@/features/sns/data"
import { TopicList } from "./topic-list"

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock("@/features/docs/service-docs-modal", () => ({
  ServiceDocsButton: () => <button type="button">Docs</button>,
  useDocsFromHash: () => [false, vi.fn(), vi.fn()],
}))

vi.mock("@/features/debug/raw-state-link", () => ({
  RawStateLink: () => null,
}))

const deleteMutate = vi.fn()
vi.mock("@/hooks/use-resource-mutation", () => ({
  useResourceMutation: () => ({ mutate: deleteMutate, isPending: false }),
}))

/** Deliberately not alphabetical — the storage order ListTopics returns. */
const topics = [
  { TopicArn: "arn:aws:sns:us-east-1:000000000000:orders" },
  { TopicArn: "arn:aws:sns:us-east-1:000000000000:alerts" },
  { TopicArn: "arn:aws:sns:us-east-1:000000000000:billing" },
]

function renderList(data: unknown = topics) {
  return renderWithData(<TopicList />, [[snsTopicsQueryOptions().queryKey, data]])
}

/** Topic names in render order, header row excluded. */
function nameColumn(): string[] {
  const rows = screen.getAllByRole("row").slice(1)
  return rows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("TopicList", () => {
  it("renders the topics through ResourceTable", () => {
    renderList()
    expect(screen.getByRole("heading", { name: "SNS Topics" })).toBeInTheDocument()
    expect(screen.getByText("orders")).toBeInTheDocument()
    expect(screen.getByText("alerts")).toBeInTheDocument()
  })

  it("orders by name ascending before the user touches a header", () => {
    renderList()
    expect(nameColumn()).toEqual(["alerts", "billing", "orders"])
  })

  it("reverses the order when the Name header is clicked", async () => {
    const { user } = renderList()
    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(nameColumn()).toEqual(["orders", "billing", "alerts"])
  })

  it("marks the sorted column for assistive technology", async () => {
    const { user } = renderList()
    const header = screen.getByRole("columnheader", { name: /Name/ })
    expect(header).toHaveAttribute("aria-sort", "ascending")
    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(header).toHaveAttribute("aria-sort", "descending")
  })

  it("leaves the ARN column unsortable — it has no ordering worth offering", () => {
    renderList()
    expect(screen.queryByRole("button", { name: "ARN" })).not.toBeInTheDocument()
  })

  it("asks for confirmation before deleting, then deletes by name", async () => {
    const { user } = renderList()
    await user.click(screen.getByRole("button", { name: "Delete alerts" }))
    expect(screen.getByText("Delete topic?")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Delete" }))
    expect(deleteMutate).toHaveBeenCalledWith("alerts")
  })

  it("shows the create call-to-action when there are no topics", () => {
    renderList([])
    expect(screen.getByText("No topics yet")).toBeInTheDocument()
    expect(screen.getByText("Create a topic to get started.")).toBeInTheDocument()
  })
})

// Same contract on the messaging side: `routes/sns/index.tsx` owns `sort`.
describe("TopicList — sort bound to the route", () => {
  // The list declares `defaultSort` name-ascending, which stands in while the
  // route has no `?sort=` — so the first click flips it to descending rather
  // than starting the cycle from nothing.
  it("reports the column's own id rather than a slug of the header", async () => {
    const onSortChange = vi.fn()
    const { user } = renderWithData(<TopicList sort={undefined} onSortChange={onSortChange} />, [
      [snsTopicsQueryOptions().queryKey, topics],
    ])

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(onSortChange).toHaveBeenCalledWith({ id: "name", desc: true })
  })

  it("renders the order the route asks for, before any click", () => {
    renderWithData(<TopicList sort={{ id: "name", desc: true }} onSortChange={vi.fn()} />, [
      [snsTopicsQueryOptions().queryKey, topics],
    ])
    expect(nameColumn()).toEqual(["orders", "billing", "alerts"])
  })
})
