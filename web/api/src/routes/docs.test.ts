import { describe, expect, it } from "vitest"
import { docsRoutes } from "./docs.js"

/**
 * The dev-mode BFF and the Go BFF answer the same endpoint from the same
 * generated index (internal/docssearch/index.gen.jsonl), so they must rank
 * alike - a query that finds a page in `pnpm run dev` has to find it in a
 * built binary. These are the queries internal/docssearch/search_test.go
 * asserts; if a change makes the two disagree, one of the two test files goes
 * red.
 */
describe("GET /search", () => {
  async function search(query: string, limit = 5) {
    const res = await docsRoutes.request(`/search?q=${encodeURIComponent(query)}&limit=${limit}`)
    expect(res.status).toBe(200)
    return (await res.json()) as { results: Array<{ Href: string; Title: string; Score: number }> }
  }

  it("ranks the focused local VPC guide first for a CDK query", async () => {
    const { results } = await search("cdk local vpc provider")
    expect(results.length).toBeGreaterThan(0)
    expect(results[0].Href).toBe("cdk/local-vpc.md")
  })

  it("finds a CamelCase operation by its words", async () => {
    const { results } = await search("live tail")
    expect(results.length).toBeGreaterThan(0)
    expect(results[0].Href).toBe("services/cloudwatch-logs.md")
  })

  it("returns nothing for a query of only stopwords", async () => {
    const { results } = await search("the and of")
    expect(results).toEqual([])
  })

  it("requires every token to match, not just one", async () => {
    const { results } = await search("lambda zzzznotaword")
    expect(results).toEqual([])
  })

  it("ranks by score, highest first", async () => {
    const { results } = await search("lambda", 10)
    expect(results.length).toBeGreaterThan(1)
    const scores = results.map((r) => r.Score)
    expect(scores).toEqual([...scores].sort((a, b) => b - a))
  })

  it("honours the limit", async () => {
    const { results } = await search("aws", 3)
    expect(results.length).toBeLessThanOrEqual(3)
  })
})
