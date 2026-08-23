import { renderWithData, screen, within } from "@/test/render"
import {
  cloudfrontDistributionQueryOptions,
  cloudfrontInvalidationsQueryOptions,
} from "@/features/cloudfront/data"
import type { CloudFrontDistribution, CloudFrontInvalidation } from "@/types/cloudfront"
import { DistributionDetail } from "./distribution-detail"

vi.mock("@tanstack/react-router", () => ({
  useParams: () => ({ distributionId: "E1" }),
}))

vi.mock("@/components/application-ownership-banner", () => ({
  ApplicationOwnershipBanner: () => null,
}))

vi.mock("./monitoring-subscription-panel", () => ({
  MonitoringSubscriptionPanel: () => null,
}))

const dist: CloudFrontDistribution = {
  id: "E1",
  arn: "arn:aws:cloudfront::000000000000:distribution/E1",
  status: "Deployed",
  domainName: "e1.cloudfront.net",
  enabled: true,
  comment: "docs site",
  lastModifiedTime: "2026-01-01T00:00:00Z",
  defaultRootObject: "index.html",
  priceClass: "PriceClass_All",
  httpVersion: "2",
  aliases: [],
  originGroups: [],
  // Deliberately not in ID order, so a sorted view is distinguishable.
  origins: [
    { id: "origin-30", domainName: "c.example.com", originPath: "" },
    { id: "origin-2", domainName: "a.example.com", originPath: "/assets" },
    { id: "origin-10", domainName: "b.example.com", originPath: "" },
  ],
}

const invalidations: CloudFrontInvalidation[] = [
  { id: "I1", status: "Completed", createTime: "2026-01-02T00:00:00Z", paths: ["/*"] },
]

function seed(invs: CloudFrontInvalidation[] = invalidations) {
  return [
    [cloudfrontDistributionQueryOptions("E1").queryKey, { distribution: dist, etag: "etag" }],
    [cloudfrontInvalidationsQueryOptions("E1").queryKey, invs],
  ] as [readonly unknown[], unknown][]
}

/** Body-row order of the currently visible table, read from each row's first cell. */
function rowOrder(): (string | null)[] {
  const [, body] = screen.getAllByRole("rowgroup")
  return within(body)
    .getAllByRole("row")
    .map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("DistributionDetail", () => {
  it("lists the origins in the order the distribution declares them", async () => {
    const { user } = renderWithData(<DistributionDetail />, seed())

    await user.click(screen.getByRole("tab", { name: /Origins/ }))
    expect(rowOrder()).toEqual(["origin-30", "origin-2", "origin-10"])
  })

  // The embedded origins table is sortable on its identity column, and IDs sort
  // alphanumerically — origin-2 before origin-10.
  it("sorts the origins sub-table when its header is clicked", async () => {
    const { user } = renderWithData(<DistributionDetail />, seed())

    await user.click(screen.getByRole("tab", { name: /Origins/ }))
    await user.click(screen.getByRole("button", { name: "Origin ID" }))

    expect(rowOrder()).toEqual(["origin-2", "origin-10", "origin-30"])
  })

  it("shows the invalidations with a create action when there are none", async () => {
    const { user } = renderWithData(<DistributionDetail />, seed([]))

    await user.click(screen.getByRole("tab", { name: /Invalidations/ }))

    expect(screen.getByText("No invalidations yet")).toBeInTheDocument()
    expect(screen.getAllByRole("button", { name: "Create Invalidation" }).length).toBe(2)
  })

  it("renders an invalidation's paths", async () => {
    const { user } = renderWithData(<DistributionDetail />, seed())

    await user.click(screen.getByRole("tab", { name: /Invalidations/ }))

    expect(screen.getByText("I1")).toBeInTheDocument()
    expect(screen.getByText("/*")).toBeInTheDocument()
  })

  // The configuration grid is deliberately still a bespoke <Table> — it is a
  // label/value view of one resource, not a list. Guard that it still renders.
  it("keeps the configuration attribute grid", () => {
    renderWithData(<DistributionDetail />, seed())

    expect(screen.getByText("Distribution ID")).toBeInTheDocument()
    expect(screen.getByText("index.html")).toBeInTheDocument()
  })
})
