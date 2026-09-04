import { HardDrive } from "lucide-react"
import { render, screen, within } from "@/test/render"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
  SelectCheckbox,
} from "./resource-list-page"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./table"

function BucketsPage({ onCreate = () => {} }: { onCreate?: () => void }) {
  return (
    <ResourceListPage
      title="S3 Buckets"
      count={2}
      meta="1.2 GB stored"
      actions={
        <>
          <RefreshAction onClick={() => {}} />
          <CreateAction onClick={onCreate}>Create bucket</CreateAction>
        </>
      }
    >
      <ResourceListCard>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {["assets", "backups"].map((name) => (
              <TableRow key={name}>
                <TableCell>
                  <ResourceName icon={HardDrive} name={name} />
                </TableCell>
                <TableCell>
                  <RowActions>
                    <RowAction label={`Delete ${name}`} tone="danger" onClick={() => {}}>
                      <HardDrive className="h-3.5 w-3.5" />
                    </RowAction>
                  </RowActions>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </ResourceListCard>
    </ResourceListPage>
  )
}

describe("ResourceListPage", () => {
  it("shows the page title as the heading", () => {
    render(<BucketsPage />)
    expect(screen.getByRole("heading", { name: "S3 Buckets" })).toBeInTheDocument()
  })

  it("shows the resource count beside the title", () => {
    render(<BucketsPage />)
    expect(screen.getByText("2")).toBeInTheDocument()
  })

  it("shows the meta line beneath the title", () => {
    render(<BucketsPage />)
    expect(screen.getByText("1.2 GB stored")).toBeInTheDocument()
  })

  it("renders the header actions in Docs to Create order", () => {
    render(<BucketsPage />)
    const labels = screen.getAllByRole("button").map((b) => b.textContent)
    expect(labels.slice(0, 2)).toEqual(["Refresh", "Create bucket"])
  })

  it("calls the create handler when the primary action is clicked", async () => {
    const onCreate = vi.fn()
    const { user } = render(<BucketsPage onCreate={onCreate} />)
    await user.click(screen.getByRole("button", { name: "Create bucket" }))
    expect(onCreate).toHaveBeenCalledOnce()
  })

  it("renders one row per resource", () => {
    render(<BucketsPage />)
    expect(screen.getByRole("row", { name: /assets/ })).toBeInTheDocument()
    expect(screen.getByRole("row", { name: /backups/ })).toBeInTheDocument()
  })

  it("labels each row action with the resource it targets", () => {
    render(<BucketsPage />)
    const row = screen.getByRole("row", { name: /assets/ })
    expect(within(row).getByRole("button", { name: "Delete assets" })).toBeInTheDocument()
  })
})

describe("RefreshAction", () => {
  it("reads Refreshing while a refetch is in flight", () => {
    render(<RefreshAction isFetching onClick={() => {}} />)
    expect(screen.getByRole("button", { name: "Refreshing" })).toHaveAttribute("aria-busy", "true")
  })

  it("does not dim itself while refetching — busy is not disabled", () => {
    render(<RefreshAction isFetching onClick={() => {}} />)
    const button = screen.getByRole("button", { name: "Refreshing" })
    expect(button).toBeEnabled()
    expect(button.className).toContain("aria-busy:opacity-100")
  })

  it("ignores clicks while a refetch is in flight", async () => {
    const onClick = vi.fn()
    const { user } = render(<RefreshAction isFetching onClick={onClick} />)
    await user.click(screen.getByRole("button", { name: "Refreshing" }))
    expect(onClick).not.toHaveBeenCalled()
  })

  it("refetches on click once the previous refetch has settled", async () => {
    const onClick = vi.fn()
    const { user } = render(<RefreshAction onClick={onClick} />)
    await user.click(screen.getByRole("button", { name: "Refresh" }))
    expect(onClick).toHaveBeenCalledOnce()
  })
})

describe("SelectCheckbox", () => {
  it("reports the new checked state to its handler", async () => {
    const onCheckedChange = vi.fn()
    const { user } = render(
      <SelectCheckbox label="Select assets" checked={false} onCheckedChange={onCheckedChange} />,
    )
    await user.click(screen.getByRole("checkbox", { name: "Select assets" }))
    expect(onCheckedChange).toHaveBeenCalledWith(true)
  })

  it("renders an indeterminate box when only some rows are selected", () => {
    render(
      <SelectCheckbox
        label="Select all"
        checked={false}
        indeterminate
        onCheckedChange={() => {}}
      />,
    )
    expect(screen.getByRole("checkbox", { name: "Select all" })).toBePartiallyChecked()
  })
})

/*
 * #1611: at ~400px the header row kept its desktop layout and the Docs /
 * Refresh / Create run overflowed the viewport uncut, taking the page's primary
 * action off-screen with nothing to scroll it back. jsdom does no layout, so
 * what a test can hold is the rule that decides the outcome: the row is allowed
 * to break, and the actions no longer refuse to be moved.
 */
describe("ResourceListPage > narrow-width header", () => {
  function headerRow() {
    // The row is the heading's grandparent: <h1> sits in the title block, which
    // is the first child of the header row.
    return screen.getByRole("heading", { name: "S3 Buckets" }).parentElement?.parentElement
      ?.parentElement
  }

  it("lets the header row wrap, so the actions drop to their own line", () => {
    render(<BucketsPage />)

    expect(headerRow()).toHaveClass("flex-wrap")
  })

  it("no longer pins the actions at full width, which is what pushed them off-screen", () => {
    render(<BucketsPage />)

    const actions = screen.getByRole("button", { name: "Create bucket" }).parentElement
    expect(actions).not.toHaveClass("shrink-0")
    expect(actions).toHaveClass("flex-wrap")
  })
})
