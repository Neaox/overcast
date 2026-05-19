import { render, screen } from "@testing-library/react"
import { RepositoryList } from "./repository-list"

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>()
  return {
    ...actual,
    useQuery: vi.fn(() => ({
      data: [
        {
          name: "backend/api",
          uri: "localhost:5111/backend/api",
          createdAt: Date.UTC(2026, 3, 22),
        },
      ],
      isLoading: false,
      isFetching: false,
      refetch: vi.fn(),
      error: null,
    })),
  }
})

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

describe("RepositoryList", () => {
  it("renders repositories and docs action", () => {
    render(<RepositoryList />)

    expect(screen.getByRole("heading", { name: "ECR Repositories" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Docs" })).toBeInTheDocument()
    expect(screen.getByText("backend/api")).toBeInTheDocument()
    expect(screen.getByText("localhost:5111/backend/api")).toBeInTheDocument()
  })
})
