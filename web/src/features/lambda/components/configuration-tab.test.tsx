/**
 * The tab as a whole, which the per-section tests do not cover.
 *
 * Every section here syncs local edit state from server data during render.
 * That pattern is correct only while the value it compares is referentially
 * stable between renders — a `= {}` fallback in a `useQuery` destructure is
 * not, and one of those took the whole tab out with "Too many re-renders" the
 * moment a query was still pending. The section tests all seed their queries,
 * so none of them could see it; only mounting the tab with a query left
 * pending does.
 */
import { createTestQueryClient, renderWithData, renderWithRouter, screen } from "@/test/render"
import { ec2SecurityGroupsQueryOptions, ec2SubnetsQueryOptions } from "@/features/ec2/data"
import {
  functionTagsQueryOptions,
  lambdaRuntimesQueryOptions,
  layersQueryOptions,
} from "@/features/lambda/data"
import type { LambdaFunction } from "@/types"
import { ConfigurationTab } from "./configuration-tab"

const fn: LambdaFunction = {
  FunctionName: "text-logs",
  FunctionArn: "arn:aws:lambda:us-east-1:000000000000:function:text-logs",
  Runtime: "nodejs20.x",
  Handler: "index.handler",
  LoggingConfig: { LogGroup: "/aws/lambda/text-logs", LogFormat: "Text" },
}

/** Everything the tab reads except the tags, which each test decides. */
const baseSeeds: [readonly unknown[], unknown][] = [
  [lambdaRuntimesQueryOptions().queryKey, []],
  [layersQueryOptions().queryKey, []],
  [ec2SubnetsQueryOptions().queryKey, []],
  [ec2SecurityGroupsQueryOptions().queryKey, []],
]

describe("ConfigurationTab", () => {
  it("renders while the tags query is still pending", () => {
    // No tags seed: `data` is undefined, exactly as on first paint.
    renderWithData(<ConfigurationTab fn={fn} />, baseSeeds)
    expect(screen.getByRole("heading", { name: "Logging configuration" })).toBeInTheDocument()
  })

  it("renders once the tags query has answered", () => {
    renderWithData(<ConfigurationTab fn={fn} />, [
      ...baseSeeds,
      [functionTagsQueryOptions(fn.FunctionArn!).queryKey, { env: "dev" }],
    ])
    expect(screen.getByRole("heading", { name: "Logging configuration" })).toBeInTheDocument()
  })

  it("shows the logging configuration below the general section", () => {
    renderWithData(<ConfigurationTab fn={fn} />, baseSeeds)
    const headings = screen.getAllByRole("heading").map((h) => h.textContent)
    expect(headings.indexOf("Logging configuration")).toBeGreaterThan(
      headings.indexOf("General configuration"),
    )
  })

  // VPC placement is real connectivity here but restricts nothing, and security
  // groups are never applied — so a test that "proves" the VPC wiring works
  // passes locally whether or not it is correct. The notice is what stops that
  // being a silent divergence, and it is shown only where someone could be
  // relying on it: on a function that actually has a VPC configured.
  describe("the VPC-not-enforced notice", () => {
    it("appears when the function is in a VPC", async () => {
      renderTab(TabWithVpc)
      expect(await screen.findByText("Placement is real — isolation is not")).toBeInTheDocument()
      expect(
        screen.getByText(/Security groups are stored and returned but never applied/),
      ).toBeInTheDocument()
    })

    it("links to the section of the docs that explains what to do", async () => {
      renderTab(TabWithVpc)
      const link = await screen.findByRole("link", { name: /What this means for your tests/ })
      expect(link).toHaveAttribute("href", "/docs?path=networking.md#lambda-ecs-and-vpcs")
    })

    it("stays out of the way when no VPC is configured", async () => {
      renderTab(TabWithoutVpc)
      // The empty state still carries the divergence, so someone about to add a
      // VPC config learns before they do — and awaiting it proves the tab
      // rendered, which is what makes the absence below meaningful.
      expect(await screen.findByText(/on AWS it could not/)).toBeInTheDocument()
      expect(screen.queryByText("Placement is real — isolation is not")).not.toBeInTheDocument()
    })
  })
})

const withVpc: LambdaFunction = {
  ...fn,
  VpcConfig: { VpcId: "vpc-abc", SubnetIds: ["subnet-1"], SecurityGroupIds: ["sg-1"] },
}

function TabWithVpc() {
  return <ConfigurationTab fn={withVpc} />
}

function TabWithoutVpc() {
  return <ConfigurationTab fn={fn} />
}

/**
 * The notice links into the in-app docs, so these cases need a router as well
 * as the seeded queries every section of the tab reads.
 */
function renderTab(component: React.FC) {
  const queryClient = createTestQueryClient()
  for (const [queryKey, data] of baseSeeds) {
    queryClient.setQueryData(queryKey, data)
  }
  return renderWithRouter(component, { queryClient })
}
