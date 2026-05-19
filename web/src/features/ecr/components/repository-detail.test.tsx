import { render, screen } from "@testing-library/react"
import { RepositoryDetail } from "./repository-detail"

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>()
  return {
    ...actual,
    useQuery: vi.fn(() => ({
      data: {
        name: "backend/api",
        arn: "arn:aws:ecr:us-east-1:000000000000:repository/backend/api",
        uri: "overcast:5111/backend/api",
        registryId: "000000000000",
        createdAt: Date.UTC(2026, 3, 22),
        imageTagMutability: "MUTABLE",
        login: {
          username: "AWS",
          password: "secret",
          proxyEndpoint: "http://overcast:5111",
        },
        images: [
          {
            digest: "sha256:deadbeef",
            tags: ["latest"],
            mediaType: "application/vnd.oci.image.manifest.v1+json",
          },
        ],
      },
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

vi.mock("@/components/application-ownership-banner", () => ({
  ApplicationOwnershipBanner: () => null,
}))

describe("RepositoryDetail", () => {
  it("renders local-registry guidance and docs action", () => {
    render(<RepositoryDetail repositoryName="backend/api" />)

    expect(screen.getByRole("button", { name: "Docs" })).toBeInTheDocument()
    expect(screen.getByText("Local registry usage")).toBeInTheDocument()
    expect(
      screen.getByText(/must allow this hostname as an insecure HTTP registry/i),
    ).toBeInTheDocument()
    expect(
      screen.getByText(/docker login http:\/\/overcast:5111 --username AWS/),
    ).toBeInTheDocument()
    expect(screen.getByText(/docker push overcast:5111\/backend\/api:latest/)).toBeInTheDocument()
  })
})
