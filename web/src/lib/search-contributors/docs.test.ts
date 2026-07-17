import { QueryClient } from "@tanstack/react-query"
import { describe, expect, it, vi } from "vitest"
import { runSearch } from "@/lib/search"
import { searchDocsInWorker } from "./docs-worker-client"
import "./docs"

vi.mock("./docs-worker-client", () => ({
  searchDocsInWorker: vi.fn(() => Promise.resolve([
    {
      id: "docs:local-vpc",
      label: "Local VPCs for CDK",
      sublabel: "Stable local VPC bootstrap and provider pattern.",
      service: "Documentation",
      serviceKey: "/docs",
      type: "CDK",
      href: "/docs?path=cdk%2Flocal-vpc.md",
    },
  ])),
}))

describe("docs search contributor", () => {
  it("adds documentation results to global search", async () => {
    const controller = new AbortController()

    const results = await runSearch("cdk vpc", {
      queryClient: new QueryClient(),
      endpoint: { baseUrl: "http://localhost:4566", region: "us-east-1", label: "Local" },
      signal: controller.signal,
    })

    expect(searchDocsInWorker).toHaveBeenCalledWith("cdk vpc", {
      signal: controller.signal,
      limit: 8,
    })
    expect(results.get("/docs")).toEqual([
      expect.objectContaining({
        label: "Local VPCs for CDK",
        href: "/docs?path=cdk%2Flocal-vpc.md",
      }),
    ])
  })
})
