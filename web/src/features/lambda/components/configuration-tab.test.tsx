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
import { renderWithData, screen } from "@/test/render"
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
})
