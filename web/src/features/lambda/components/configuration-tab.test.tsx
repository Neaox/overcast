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

  // Nothing finer than "in this VPC or not" is enforced on any host: security
  // groups, NACLs and the public/private subnet distinction are stored and
  // never applied — so a test that "proves" the security-group wiring works
  // passes locally whether or not it is correct. Placement itself is enforced
  // only where Overcast's DNS resolver runs, which is why the headline claims
  // the first and the body carries the second. The notice is shown only where
  // someone could be relying on it: on a function that has a VPC configured.
  describe("the VPC-not-enforced notice", () => {
    it("appears when the function is in a VPC", async () => {
      renderTab(TabWithVpc)
      expect(await screen.findByText("Security groups and subnets are not enforced")).toBeInTheDocument()
      expect(
        screen.getByText(/are stored and returned but never applied/),
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
      expect(await screen.findByText(/cannot reach resources inside one/)).toBeInTheDocument()
      expect(screen.queryByText("Security groups and subnets are not enforced")).not.toBeInTheDocument()
    })
  })

  // The dead-letter target is the only part of a function's asynchronous
  // invocation config this page edits. The rest is configurable, just not from
  // here, so the section has to name the API that does it — otherwise the page
  // reads as though the retry policy were fixed, which it no longer is.
  describe("the asynchronous invocation section", () => {
    // The target renders as an ArnLink, which is a router link — so these two
    // need the router harness rather than the bare query seeds.
    it("shows the configured dead-letter target", async () => {
      renderTab(TabWithDeadLetterQueue)
      expect(
        await screen.findByRole("heading", { name: "Asynchronous invocation" }),
      ).toBeInTheDocument()
      expect(screen.getByText("arn:aws:sqs:us-east-1:000000000000:orders-dlq")).toBeInTheDocument()
    })

    it("offers to remove the target only when there is one", async () => {
      renderTab(TabWithDeadLetterQueue)
      expect(await screen.findByRole("button", { name: "Remove DLQ" })).toBeInTheDocument()
    })

    it("has nothing to remove on a function without one", () => {
      renderWithData(<ConfigurationTab fn={fn} />, baseSeeds)
      expect(screen.getByRole("heading", { name: "Asynchronous invocation" })).toBeInTheDocument()
      expect(screen.queryByRole("button", { name: "Remove DLQ" })).not.toBeInTheDocument()
    })

    // The policy became configurable, so the note has to point at the API that
    // changes it — and say that this page is not where you do it.
    it("names the API that changes the retry policy", () => {
      renderWithData(<ConfigurationTab fn={fn} />, baseSeeds)
      expect(screen.getByText(/PutFunctionEventInvokeConfig/)).toBeInTheDocument()
      expect(screen.getByText(/edits the dead-letter target only/)).toBeInTheDocument()
    })

    // Without a target the retries still happen, so the note has to say what
    // becomes of the event — "sent here" points at nothing on this function.
    it("says the event is dropped when there is no target", () => {
      renderWithData(<ConfigurationTab fn={fn} />, baseSeeds)
      expect(screen.getByText(/the event is then dropped/)).toBeInTheDocument()
    })

    it("says the event is sent to the target when there is one", async () => {
      renderTab(TabWithDeadLetterQueue)
      expect(await screen.findByText(/retried before the event is sent here/)).toBeInTheDocument()
    })
  })
})

const withDeadLetterQueue: LambdaFunction = {
  ...fn,
  DeadLetterConfig: { TargetArn: "arn:aws:sqs:us-east-1:000000000000:orders-dlq" },
}

function TabWithDeadLetterQueue() {
  return <ConfigurationTab fn={withDeadLetterQueue} />
}

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
