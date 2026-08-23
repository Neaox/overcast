import { renderWithData, screen, within } from "@/test/render"
import { webACLsQueryOptions } from "@/features/waf/data"
import { WebACLList } from "./web-acl-list"

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock("@/features/docs/service-docs-modal", () => ({
  ServiceDocsButton: () => <button type="button">Docs</button>,
  useDocsFromHash: () => [false, vi.fn(), vi.fn()],
}))

vi.mock("@/hooks/use-resource-mutation", () => ({
  useResourceMutation: () => ({ mutate: vi.fn(), isPending: false }),
}))

const acls = [
  { id: "b2", name: "edge-acl", scope: "CLOUDFRONT", arn: "arn:2", description: "", lockToken: "" },
  {
    id: "a1",
    name: "api-acl",
    scope: "REGIONAL",
    arn: "arn:1",
    description: "Rate limits the API",
    lockToken: "",
  },
]

/** Body-row order, read from the first cell of each row. */
function rowOrder(): (string | null)[] {
  const [, body] = screen.getAllByRole("rowgroup")
  return within(body)
    .getAllByRole("row")
    .map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("WebACLList", () => {
  it("renders the Web ACLs in the order the service returned them", () => {
    renderWithData(<WebACLList />, [[webACLsQueryOptions().queryKey, acls]])

    expect(screen.getByRole("heading", { name: "WAF Web ACLs" })).toBeInTheDocument()
    expect(screen.getByText("Rate limits the API")).toBeInTheDocument()
    expect(rowOrder()).toEqual(["edge-acl", "api-acl"])
  })

  it("sorts by name when the Name header is clicked", async () => {
    const { user } = renderWithData(<WebACLList />, [[webACLsQueryOptions().queryKey, acls]])

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(rowOrder()).toEqual(["api-acl", "edge-acl"])

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(rowOrder()).toEqual(["edge-acl", "api-acl"])
  })

  it("reports the sort to the page when the route owns it", async () => {
    const onSortChange = vi.fn()
    const { user } = renderWithData(<WebACLList onSortChange={onSortChange} />, [
      [webACLsQueryOptions().queryKey, acls],
    ])

    await user.click(screen.getByRole("button", { name: "Scope" }))
    expect(onSortChange).toHaveBeenCalledWith({ id: "scope", desc: false })
  })

  it("offers view and delete row actions per ACL", () => {
    renderWithData(<WebACLList />, [[webACLsQueryOptions().queryKey, acls]])

    expect(screen.getByRole("button", { name: "View edge-acl" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Delete edge-acl" })).toBeInTheDocument()
  })

  it("shows the empty state when there are no Web ACLs", () => {
    renderWithData(<WebACLList />, [[webACLsQueryOptions().queryKey, []]])

    expect(screen.getByText("No Web ACLs yet")).toBeInTheDocument()
  })
})
