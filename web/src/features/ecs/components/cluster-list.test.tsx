import { renderWithData, screen, within } from "@/test/render"
import { ecsClustersQueryOptions } from "@/features/ecs/data"
import type { EcsCluster } from "@/types"
import { ClusterList } from "./cluster-list"

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock("@/features/docs/service-docs-modal", () => ({
  ServiceDocsButton: () => <button type="button">Docs</button>,
  useDocsFromHash: () => [false, vi.fn(), vi.fn()],
}))

vi.mock("@/components/docker-banner", () => ({
  DockerBanner: () => null,
}))

vi.mock("@/hooks/use-resource-mutation", () => ({
  useResourceMutation: () => ({ mutate: vi.fn(), isPending: false }),
}))

function cluster(name: string, runningTasksCount: number): EcsCluster {
  return {
    clusterName: name,
    clusterArn: `arn:aws:ecs:us-east-1:000000000000:cluster/${name}`,
    status: "ACTIVE",
    runningTasksCount,
    pendingTasksCount: 0,
    activeServicesCount: 0,
    registeredContainerInstancesCount: 0,
  }
}

/** Cluster names in the order the table currently renders them. */
function renderedNames(): (string | null)[] {
  return screen
    .getAllByRole("row")
    .slice(1) // drop the header row
    .map((row) => within(row).getAllByRole("cell")[0].textContent)
}

const clusters = [cluster("beta", 5), cluster("alpha", 1), cluster("gamma", 3)]

describe("ClusterList", () => {
  it("renders clusters in the order the API returned them", () => {
    renderWithData(<ClusterList />, [[ecsClustersQueryOptions().queryKey, clusters]])

    expect(screen.getByRole("heading", { name: /ECS Clusters/ })).toBeInTheDocument()
    expect(renderedNames()).toEqual(["beta", "alpha", "gamma"])
  })

  it("sorts by cluster name, then reverses, on header clicks", async () => {
    const { user } = renderWithData(<ClusterList />, [
      [ecsClustersQueryOptions().queryKey, clusters],
    ])

    await user.click(screen.getByRole("button", { name: /Cluster name/ }))
    expect(renderedNames()).toEqual(["alpha", "beta", "gamma"])

    await user.click(screen.getByRole("button", { name: /Cluster name/ }))
    expect(renderedNames()).toEqual(["gamma", "beta", "alpha"])
  })

  it("sorts a count column busiest-first on the first click", async () => {
    const { user } = renderWithData(<ClusterList />, [
      [ecsClustersQueryOptions().queryKey, clusters],
    ])

    // A numeric column starts descending — "most running tasks" is the answer
    // one click should give, the way a text column starts A→Z.
    await user.click(screen.getByRole("button", { name: /Running tasks/ }))
    expect(renderedNames()).toEqual(["beta", "gamma", "alpha"])
  })

  it("confirms before deleting a cluster", async () => {
    const { user } = renderWithData(<ClusterList />, [
      [ecsClustersQueryOptions().queryKey, clusters],
    ])

    await user.click(screen.getByRole("button", { name: "Delete beta" }))

    expect(screen.getByRole("heading", { name: "Delete Cluster" })).toBeInTheDocument()
    expect(screen.getByText("beta", { selector: "strong" })).toBeInTheDocument()
  })

  it("offers the create action from the empty state", () => {
    renderWithData(<ClusterList />, [[ecsClustersQueryOptions().queryKey, []]])

    expect(screen.getByText("No clusters yet")).toBeInTheDocument()
    expect(screen.getAllByRole("button", { name: /Create cluster/ }).length).toBeGreaterThan(1)
  })
})
