import { renderWithData, screen, within } from "@/test/render"
import {
  cloudfrontDistributionQueryOptions,
  cloudfrontInvalidationsQueryOptions,
} from "@/features/cloudfront/data"
import type { CloudFrontDistribution, CloudFrontInvalidation } from "@/types/cloudfront"
import { fieldLabel } from "@/lib/typography"
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

  it("keeps the configuration attribute grid", () => {
    renderWithData(<DistributionDetail />, seed())

    expect(screen.getByText("Distribution ID")).toBeInTheDocument()
    expect(screen.getByText("index.html")).toBeInTheDocument()
  })
})

/**
 * The Configuration tab is an attribute grid, not a list, so it renders through
 * the shared `Definition` component — the term in the field-label spec, the
 * definition in mono. Asserting the spec rather than only the text is what would
 * catch someone hand-rolling the grid back into the page.
 */
describe("DistributionDetail > configuration typography", () => {
  it("renders the attributes as a definition list in the shared label spec", () => {
    renderWithData(<DistributionDetail />, seed())

    const label = screen.getByText("Distribution ID")
    expect(label.tagName).toBe("DT")
    expect(label).toHaveClass(...fieldLabel.split(" "))
    // The domain also appears in the page header, so read the value from its own
    // pair rather than by text.
    const value = label.parentElement?.querySelector("dd")
    expect(value).toHaveClass("font-mono")
  })

  it("shows an em dash for an attribute the distribution does not set", () => {
    renderWithData(<DistributionDetail />, seed())

    // `aliases` is empty in the fixture, so the pair is present and
    // absent-valued rather than silently dropped from the grid.
    expect(screen.getByText("Aliases").parentElement).toHaveTextContent("—")
  })

  it("offers a copy control on the identifiers, named after the field", () => {
    renderWithData(<DistributionDetail />, seed())

    expect(screen.getByRole("button", { name: "Copy ARN" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Copy domain name" })).toBeInTheDocument()
  })
})
