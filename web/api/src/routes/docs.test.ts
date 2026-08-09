import { describe, expect, it } from "vitest"
import { docsRoutes, parseDocsIndex, searchDocs } from "./docs.js"

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

  /**
   * Mirrors TestSearch_servicePageOutranksTheOperationManifest. Every service
   * name appears in docs/operation-manifest.md, a generated listing of every
   * modelled AWS operation, so a query naming a service used to land on the
   * listing rather than on the page that documents it.
   */
  it("ranks a service's own page above the generated operation manifest", async () => {
    for (const [query, href] of [
      ["msk cluster", "services/msk.md"],
      ["opensearch domain", "services/opensearch.md"],
      ["autoscaling group", "services/autoscaling.md"],
      ["log group retention", "services/cloudwatch-logs.md"],
    ]) {
      const { results } = await search(query)
      expect(results[0]?.Href, query).toBe(href)
    }
  })
})

/**
 * Mirrors TestSearch_operationNameInCodeOutranksIncidentalProse in
 * internal/docssearch/score_test.go. Ranking questions are about the shape of
 * a corpus, so both implementations ask them of a synthetic one rather than of
 * docs/, whose answer changes whenever someone edits a page.
 *
 * The Go test builds these two documents from their fields and scores them
 * with docssearch.ScoreDocument. TypeScript has no scorer and must not grow
 * one - it consumes the artifact the Go generator writes - so the fixture
 * below is that scorer's output, pasted. The Go test asserts the same
 * ordering from the same documents; if the weights change, it moves and this
 * fixture has to move with it.
 */
describe("searchDocs ranking", () => {
  const SYNTHETIC = [
    {
      path: "docs/lambda.md",
      href: "lambda.md",
      title: "Lambda",
      description: "",
      section: "Service Reference",
      tags: [],
      terms:
        "across:1 after:1 aggregate:1 aliases:1 allocation:1 attempt:1 batch:1 budget:1 " +
        "budgeted:1 containers:2 deleting:5 dropped:1 efs:1 expected:1 lambda:10 latency:1 " +
        "live:8 memory:1 mode:1 mounts:1 off:1 only:1 operations:1 path:1 reference:6 " +
        "reports:1 resolve:1 retries:1 service:6 spike:1 tags:5 tail:2 these:1 third:1 " +
        "under:1 version:5 versioned:1 where:5",
    },
    {
      path: "docs/cloudwatch-logs.md",
      href: "cloudwatch-logs.md",
      title: "CloudWatch Logs",
      description: "",
      section: "Service Reference",
      tags: [],
      terms:
        "aws:1 cloud:10 endpoints:5 event-stream:1 live:7 logs:10 reference:6 response:1 " +
        "service:6 session:1 start:6 supported:1 tail:6 watch:10",
    },
  ]

  it("ranks an operation named in a code span above incidental prose", () => {
    // Given: a page using "live" and "tail" repeatedly in unrelated prose, and
    // one holding StartLiveTail once, as an inline code span.
    const index = parseDocsIndex(SYNTHETIC.map((doc) => JSON.stringify(doc)).join("\n"))

    // When: the operation is searched for by its words.
    const results = searchDocs(index, "live tail", 5)

    // Then: the page that owns the operation ranks first.
    expect(results.map((r) => r.Href)).toEqual(["cloudwatch-logs.md", "lambda.md"])
  })
})
