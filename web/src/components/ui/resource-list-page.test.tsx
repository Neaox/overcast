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
  it("is disabled while a refetch is in flight", () => {
    render(<RefreshAction isFetching onClick={() => {}} />)
    expect(screen.getByRole("button", { name: "Refresh" })).toBeDisabled()
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
