import { renderWithData, screen, within } from "@/test/render"
import { cognitoPoolsQueryOptions } from "@/features/cognito/data"
import { CognitoPage } from "./cognito-page"

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

vi.mock("@/features/cognito/components/create-pool-dialog", () => ({
  CreatePoolDialog: () => null,
}))

const pools = [
  { id: "ap-southeast-2_zzz", name: "staff", creationDate: "2026-02-01T00:00:00Z" },
  { id: "ap-southeast-2_aaa", name: "customers", creationDate: "2026-01-01T00:00:00Z" },
]

/** Body-row order, read from the first cell of each row. */
function rowOrder(): (string | null)[] {
  const [, body] = screen.getAllByRole("rowgroup")
  return within(body)
    .getAllByRole("row")
    .map((row) => within(row).getAllByRole("cell")[0].textContent)
}

function renderPage(props: Partial<React.ComponentProps<typeof CognitoPage>> = {}) {
  return renderWithData(<CognitoPage filter="" onFilterChange={vi.fn()} {...props} />, [
    [cognitoPoolsQueryOptions().queryKey, pools],
  ])
}

describe("CognitoPage", () => {
  it("renders the pools the service returned, in that order", () => {
    renderPage()

    expect(screen.getByRole("heading", { name: /Cognito User Pools/ })).toBeInTheDocument()
    expect(rowOrder()).toEqual(["staff", "customers"])
  })

  it("sorts by name when the Name header is clicked", async () => {
    const { user } = renderPage()

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(rowOrder()).toEqual(["customers", "staff"])
  })

  it("hands the sort to the route when it owns the search param", async () => {
    const onSortChange = vi.fn()
    const { user } = renderPage({ onSortChange })

    await user.click(screen.getByRole("button", { name: "Created" }))
    expect(onSortChange).toHaveBeenCalledWith({ id: "created", desc: false })
  })

  it("distinguishes a filter that matches nothing from an empty account", () => {
    const onFilterChange = vi.fn()
    renderWithData(<CognitoPage filter="nope" onFilterChange={onFilterChange} />, [
      [cognitoPoolsQueryOptions().queryKey, []],
    ])

    expect(screen.getByText("No matching user pools")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Clear filter" })).toBeInTheDocument()
  })
})
