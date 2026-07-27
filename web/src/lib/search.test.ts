import { describe, expect, it } from "vitest"
import { orderGroupsByActiveService, type SearchResult } from "@/lib/search"

function result(serviceKey: string, name: string): SearchResult {
  return {
    id: `${serviceKey}:${name}`,
    label: name,
    service: serviceKey.slice(1).toUpperCase(),
    serviceKey,
    type: "Resource",
    href: `${serviceKey}/${name}`,
  }
}

function groupedFixture(): Map<string, SearchResult[]> {
  return new Map([
    ["/s3", [result("/s3", "my-bucket")]],
    ["/sqs", [result("/sqs", "my-queue"), result("/sqs", "other-queue")]],
    ["/lambda", [result("/lambda", "my-fn")]],
  ])
}

describe("orderGroupsByActiveService", () => {
  it("promotes the current route's group to the front while preserving the relative order of the rest", () => {
    const ordered = orderGroupsByActiveService(groupedFixture(), "/lambda")

    expect(ordered.map(([serviceKey]) => serviceKey)).toEqual(["/lambda", "/s3", "/sqs"])
  })

  it("keeps each group's items intact when a group is promoted", () => {
    const ordered = orderGroupsByActiveService(groupedFixture(), "/sqs")

    expect(ordered[0]).toEqual([
      "/sqs",
      [result("/sqs", "my-queue"), result("/sqs", "other-queue")],
    ])
  })

  it("keeps the original group order when there is no active service", () => {
    const ordered = orderGroupsByActiveService(groupedFixture(), undefined)

    expect(ordered.map(([serviceKey]) => serviceKey)).toEqual(["/s3", "/sqs", "/lambda"])
  })

  it("keeps the original group order when the active service has no result group", () => {
    const ordered = orderGroupsByActiveService(groupedFixture(), "/dynamodb")

    expect(ordered.map(([serviceKey]) => serviceKey)).toEqual(["/s3", "/sqs", "/lambda"])
  })

  it("does not mutate the input map", () => {
    const grouped = groupedFixture()

    orderGroupsByActiveService(grouped, "/lambda")

    expect([...grouped.keys()]).toEqual(["/s3", "/sqs", "/lambda"])
  })
})
