import { renderWithData, screen, within } from "@/test/render"
import { ecrRepositoriesQueryOptions } from "@/features/ecr/data"
import { RepositoryList } from "./repository-list"

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock("@/features/docs/service-docs-modal", () => ({
  ServiceDocsButton: () => <button type="button">Docs</button>,
  useDocsFromHash: () => [false, vi.fn(), vi.fn()],
}))

vi.mock("@/hooks/use-resource-mutation", () => ({
  useResourceMutation: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
}))

const repositories = [
  { name: "backend/api", uri: "localhost:5111/backend/api", createdAt: Date.UTC(2026, 3, 22) },
  { name: "worker", uri: "localhost:5111/worker", createdAt: Date.UTC(2026, 0, 9) },
  { name: "frontend", uri: "localhost:5111/frontend", createdAt: Date.UTC(2026, 6, 1) },
]

/** Repository names in render order, read from the first cell of each body row. */
function rowOrder(): (string | null)[] {
  const [, body] = screen.getAllByRole("rowgroup")
  return within(body)
    .getAllByRole("row")
    .map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("RepositoryList", () => {
  it("renders repositories and docs action", () => {
    renderWithData(<RepositoryList />, [
      [ecrRepositoriesQueryOptions().queryKey, [repositories[0]]],
    ])

    expect(screen.getByRole("heading", { name: "ECR Repositories" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Docs" })).toBeInTheDocument()
    expect(screen.getByText("backend/api")).toBeInTheDocument()
    expect(screen.getByText("localhost:5111/backend/api")).toBeInTheDocument()
  })

  // The list arrives in the order DescribeRepositories returned it; the Name
  // and Created headers are the only way to impose one, so they have to work.
  it("keeps the server's order until a header is clicked", () => {
    renderWithData(<RepositoryList />, [[ecrRepositoriesQueryOptions().queryKey, repositories]])

    expect(rowOrder()).toEqual(["backend/api", "worker", "frontend"])
  })

  it("sorts by name on the first click of the Name header, and reverses on the second", async () => {
    const { user } = renderWithData(<RepositoryList />, [
      [ecrRepositoriesQueryOptions().queryKey, repositories],
    ])

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(rowOrder()).toEqual(["backend/api", "frontend", "worker"])

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(rowOrder()).toEqual(["worker", "frontend", "backend/api"])
  })

  it("sorts by creation time newest-first on the first click of the Created header", async () => {
    const { user } = renderWithData(<RepositoryList />, [
      [ecrRepositoriesQueryOptions().queryKey, repositories],
    ])

    await user.click(screen.getByRole("button", { name: "Created" }))
    expect(rowOrder()).toEqual(["frontend", "backend/api", "worker"])
  })

  it("leaves the URI column unsorted — it repeats the name", () => {
    renderWithData(<RepositoryList />, [[ecrRepositoriesQueryOptions().queryKey, repositories]])

    const uri = screen.getByRole("columnheader", { name: "URI" })
    expect(uri).not.toHaveAttribute("aria-sort")
    expect(within(uri).queryByRole("button")).not.toBeInTheDocument()
  })
})
