import { renderWithData, screen } from "@/test/render"
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

describe("RepositoryList", () => {
  it("renders repositories and docs action", () => {
    renderWithData(<RepositoryList />, [
      [
        ecrRepositoriesQueryOptions().queryKey,
        [
          {
            name: "backend/api",
            uri: "localhost:5111/backend/api",
            createdAt: Date.UTC(2026, 3, 22),
          },
        ],
      ],
    ])

    expect(screen.getByRole("heading", { name: "ECR Repositories" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Docs" })).toBeInTheDocument()
    expect(screen.getByText("backend/api")).toBeInTheDocument()
    expect(screen.getByText("localhost:5111/backend/api")).toBeInTheDocument()
  })
})
