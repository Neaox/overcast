import { renderWithData, screen, within } from "@/test/render"
import { cloudfrontDistributionsQueryOptions } from "@/features/cloudfront/data"
import type { CloudFrontDistribution } from "@/types/cloudfront"
import { DistributionList } from "./distribution-list"

const navigate = vi.fn()

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
}))

vi.mock("@/features/docs/service-docs-modal", () => ({
  ServiceDocsButton: () => null,
  useDocsFromHash: () => [false, vi.fn(), vi.fn()],
}))

function distribution(overrides: Partial<CloudFrontDistribution>): CloudFrontDistribution {
  return {
    id: "E1",
    arn: "arn:aws:cloudfront::000000000000:distribution/E1",
    status: "Deployed",
    domainName: "e1.cloudfront.net",
    enabled: true,
    comment: "",
    lastModifiedTime: "2026-01-01T00:00:00Z",
    origins: [],
    originGroups: [],
    defaultRootObject: "",
    priceClass: "PriceClass_All",
    httpVersion: "2",
    aliases: [],
    ...overrides,
  }
}

// Deliberately neither alphabetical nor origin-count order, so a sorted view is
// distinguishable from the order the API returned.
const distributions: CloudFrontDistribution[] = [
  distribution({ id: "E30", domainName: "c.cloudfront.net", origins: [] }),
  distribution({
    id: "E2",
    domainName: "a.cloudfront.net",
    origins: [
      { id: "o1", domainName: "one.example.com", originPath: "" },
      { id: "o2", domainName: "two.example.com", originPath: "" },
    ],
  }),
  distribution({
    id: "E10",
    domainName: "b.cloudfront.net",
    origins: [{ id: "o1", domainName: "one.example.com", originPath: "" }],
  }),
]

function seed(items: CloudFrontDistribution[]) {
  return [[cloudfrontDistributionsQueryOptions().queryKey, items]] as [
    readonly unknown[],
    unknown,
  ][]
}

/** Body-row order, read from the first cell (the distribution ID) of each row. */
function rowOrder(): (string | null)[] {
  const [, body] = screen.getAllByRole("rowgroup")
  return within(body)
    .getAllByRole("row")
    .map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("DistributionList", () => {
  beforeEach(() => navigate.mockClear())

  it("renders a row per distribution in the order the API returned", () => {
    renderWithData(<DistributionList />, seed(distributions))
    expect(rowOrder()).toEqual(["E30", "E2", "E10"])
  })

  // The identity column is sortable, and IDs sort alphanumerically — so E2
  // precedes E10 rather than following it the way a plain string sort would.
  it("reorders the rows when the ID header is clicked", async () => {
    const { user } = renderWithData(<DistributionList />, seed(distributions))

    await user.click(screen.getByRole("button", { name: "ID" }))
    expect(screen.getByRole("columnheader", { name: "ID" })).toHaveAttribute(
      "aria-sort",
      "ascending",
    )
    expect(rowOrder()).toEqual(["E2", "E10", "E30"])

    await user.click(screen.getByRole("button", { name: "ID" }))
    expect(rowOrder()).toEqual(["E30", "E10", "E2"])
  })

  // Origins is a count, so its first click is largest-first — the question a
  // reader asks of that column ("which distribution fans out the most?").
  it("sorts by origin count", async () => {
    const { user } = renderWithData(<DistributionList />, seed(distributions))

    await user.click(screen.getByRole("button", { name: "Origins" }))
    expect(rowOrder()).toEqual(["E2", "E10", "E30"])
  })

  it("hides a column through the columns menu", async () => {
    const { user } = renderWithData(<DistributionList />, seed(distributions))
    expect(screen.getByRole("columnheader", { name: /Domain name/ })).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: /Columns/ }))
    await user.click(await screen.findByRole("checkbox", { name: "Domain name" }))

    expect(screen.queryByRole("columnheader", { name: /Domain name/ })).not.toBeInTheDocument()
  })

  it("navigates to the detail page when a row is clicked", async () => {
    const { user } = renderWithData(<DistributionList />, seed(distributions))

    await user.click(screen.getByText("a.cloudfront.net"))

    expect(navigate).toHaveBeenCalledWith({
      to: "/cloudfront/$distributionId",
      params: { distributionId: "E2" },
    })
  })

  it("keeps the view and delete row actions", () => {
    renderWithData(<DistributionList />, seed(distributions))

    expect(screen.getByRole("button", { name: "View E30" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Delete E30" })).toBeInTheDocument()
  })

  it("offers the create action from the empty state", () => {
    renderWithData(<DistributionList />, seed([]))

    expect(screen.getByText("No distributions yet")).toBeInTheDocument()
    expect(screen.getAllByRole("button", { name: "Create distribution" }).length).toBeGreaterThan(0)
  })
})
