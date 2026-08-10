import { describe, it, expect } from "vitest"
import {
  DEFAULT_SORT,
  buildRows,
  highlightSlices,
  isServerOrder,
  matchesTerm,
  nextSort,
  splitSearch,
  type BrowserRow,
  type ObjectSort,
} from "./object-browser"
import type { S3Object, S3ObjectVersion } from "@/types"

function obj(key: string, over: Partial<S3Object> = {}): S3Object {
  return {
    key,
    size: 100,
    lastModified: "2026-01-01T00:00:00.000Z",
    etag: "abc",
    storageClass: "STANDARD",
    ...over,
  }
}

function version(key: string, over: Partial<S3ObjectVersion> = {}): S3ObjectVersion {
  return {
    key,
    versionId: "v1",
    isLatest: true,
    isDeleteMarker: false,
    lastModified: "2026-01-01T00:00:00.000Z",
    size: 10,
    etag: "abc",
    storageClass: "STANDARD",
    ...over,
  }
}

const names = (rows: BrowserRow[]) => rows.map((r) => r.name)

describe("splitSearch", () => {
  it("sends nothing to S3 when the search has no slash", () => {
    expect(splitSearch("report")).toEqual({ prefixPart: "", term: "report" })
  })

  it("sends everything up to the last slash to S3 as a prefix", () => {
    expect(splitSearch("logs/2024/err")).toEqual({ prefixPart: "logs/2024/", term: "err" })
  })

  it("treats a trailing slash as browsing that folder rather than filtering it", () => {
    expect(splitSearch("logs/")).toEqual({ prefixPart: "logs/", term: "" })
  })

  it("lower-cases the term so matching is case-insensitive", () => {
    expect(splitSearch("Report.CSV").term).toBe("report.csv")
  })

  it("returns an empty term for a blank search", () => {
    expect(splitSearch("   ")).toEqual({ prefixPart: "", term: "" })
  })
})

describe("matchesTerm", () => {
  it("matches every name when the term is empty", () => {
    expect(matchesTerm("anything", "")).toBe(true)
  })

  it("matches a substring regardless of case", () => {
    expect(matchesTerm("Report.CSV", "port")).toBe(true)
  })

  it("does not match a substring that falls before the search's own prefix", () => {
    // Searching "logs/log" must not match "logs/report.txt" on the "log" that
    // is part of the folder name S3 already matched exactly.
    expect(matchesTerm("logs/report.txt", "log", "logs/".length)).toBe(false)
  })

  it("matches a substring that falls after the search's own prefix", () => {
    expect(matchesTerm("logs/logfile.txt", "log", "logs/".length)).toBe(true)
  })
})

describe("highlightSlices", () => {
  it("returns the whole name unmatched when there is no term", () => {
    expect(highlightSlices("report.csv", "")).toEqual([{ text: "report.csv", match: false }])
  })

  it("splits the name around the matched run", () => {
    expect(highlightSlices("report.csv", "port")).toEqual([
      { text: "re", match: false },
      { text: "port", match: true },
      { text: ".csv", match: false },
    ])
  })

  it("preserves the original casing of a matched run", () => {
    expect(highlightSlices("Report.csv", "report")).toEqual([
      { text: "Report", match: true },
      { text: ".csv", match: false },
    ])
  })

  it("marks every occurrence, not just the first", () => {
    expect(highlightSlices("a-b-a", "a").filter((s) => s.match)).toHaveLength(2)
  })

  it("leaves the name whole when the term only appears before the offset", () => {
    expect(highlightSlices("logs/report.txt", "log", "logs/".length)).toEqual([
      { text: "logs/report.txt", match: false },
    ])
  })
})

describe("isServerOrder", () => {
  it("recognises ascending-by-name as the order S3 already returns", () => {
    expect(isServerOrder(DEFAULT_SORT)).toBe(true)
  })

  it.each<ObjectSort>([
    { column: "name", direction: "desc" },
    { column: "size", direction: "asc" },
    { column: "modified", direction: "desc" },
  ])("requires the full listing for $column $direction", (sort) => {
    expect(isServerOrder(sort)).toBe(false)
  })
})

describe("nextSort", () => {
  it("flips direction when the same column is clicked again", () => {
    expect(nextSort({ column: "size", direction: "desc" }, "size")).toEqual({
      column: "size",
      direction: "asc",
    })
  })

  it("starts a name sort ascending", () => {
    expect(nextSort({ column: "size", direction: "desc" }, "name")).toEqual({
      column: "name",
      direction: "asc",
    })
  })

  it("starts a size sort at largest first", () => {
    expect(nextSort(DEFAULT_SORT, "size")).toEqual({ column: "size", direction: "desc" })
  })

  it("starts a modified sort at newest first", () => {
    expect(nextSort(DEFAULT_SORT, "modified")).toEqual({ column: "modified", direction: "desc" })
  })
})

describe("buildRows", () => {
  const base = { browsePrefix: "", listPrefix: "", term: "", sort: DEFAULT_SORT, prefixes: [] }

  it("names rows relative to the folder being browsed", () => {
    const rows = buildRows({
      ...base,
      browsePrefix: "logs/",
      listPrefix: "logs/",
      objects: [obj("logs/app.txt")],
    })
    expect(names(rows)).toEqual(["app.txt"])
  })

  it("shows the path below the browsed folder when the search narrowed the listing", () => {
    const rows = buildRows({
      ...base,
      browsePrefix: "",
      listPrefix: "logs/2024/",
      objects: [obj("logs/2024/app.txt")],
    })
    expect(names(rows)).toEqual(["logs/2024/app.txt"])
  })

  it("puts folders above objects", () => {
    const rows = buildRows({
      ...base,
      prefixes: [{ prefix: "zzz/" }],
      objects: [obj("aaa.txt")],
    })
    expect(rows.map((r) => r.type)).toEqual(["prefix", "object"])
  })

  it("keeps folders above objects even when sorting by size", () => {
    const rows = buildRows({
      ...base,
      sort: { column: "size", direction: "desc" },
      prefixes: [{ prefix: "zzz/" }],
      objects: [obj("big.bin", { size: 9_000 })],
    })
    expect(rows[0].type).toBe("prefix")
  })

  it("drops objects that do not match the term", () => {
    const rows = buildRows({ ...base, term: "csv", objects: [obj("a.csv"), obj("b.txt")] })
    expect(names(rows)).toEqual(["a.csv"])
  })

  it("drops folders that do not match the term", () => {
    const rows = buildRows({
      ...base,
      term: "log",
      prefixes: [{ prefix: "logs/" }, { prefix: "images/" }],
    })
    expect(names(rows)).toEqual(["logs/"])
  })

  it("orders by size when asked", () => {
    const rows = buildRows({
      ...base,
      sort: { column: "size", direction: "desc" },
      objects: [obj("a", { size: 1 }), obj("b", { size: 300 }), obj("c", { size: 20 })],
    })
    expect(names(rows)).toEqual(["b", "c", "a"])
  })

  it("orders by last modified when asked", () => {
    const rows = buildRows({
      ...base,
      sort: { column: "modified", direction: "desc" },
      objects: [
        obj("old", { lastModified: "2020-01-01T00:00:00.000Z" }),
        obj("new", { lastModified: "2026-06-01T00:00:00.000Z" }),
      ],
    })
    expect(names(rows)).toEqual(["new", "old"])
  })

  it("reverses folders too when the name sort is descending", () => {
    const rows = buildRows({
      ...base,
      sort: { column: "name", direction: "desc" },
      prefixes: [{ prefix: "a/" }, { prefix: "b/" }],
    })
    expect(names(rows)).toEqual(["b/", "a/"])
  })

  it("orders names by code unit so an ascending sort agrees with S3's paging", () => {
    const rows = buildRows({ ...base, objects: [obj("b"), obj("A"), obj("a")] })
    expect(names(rows)).toEqual(["A", "a", "b"])
  })

  it("lists versions instead of objects when versions are supplied", () => {
    const rows = buildRows({ ...base, versions: [version("a.txt")], objects: [obj("b.txt")] })
    expect(names(rows)).toEqual(["a.txt"])
  })

  it("keeps S3's newest-first order within one key under a name sort", () => {
    const rows = buildRows({
      ...base,
      versions: [
        version("a.txt", { versionId: "v2", lastModified: "2026-02-01T00:00:00.000Z" }),
        version("a.txt", { versionId: "v1", lastModified: "2026-01-01T00:00:00.000Z" }),
      ],
    })
    expect(rows.map((r) => (r.type === "version" ? r.version.versionId : ""))).toEqual(["v2", "v1"])
  })

  it("sizes a delete marker as zero when sorting by size", () => {
    const rows = buildRows({
      ...base,
      sort: { column: "size", direction: "asc" },
      versions: [
        version("a.txt", { size: 500 }),
        version("b.txt", { isDeleteMarker: true, size: 0 }),
      ],
    })
    expect(names(rows)).toEqual(["b.txt", "a.txt"])
  })

  it("filters versions by the term as well", () => {
    const rows = buildRows({ ...base, term: "b", versions: [version("a.txt"), version("b.txt")] })
    expect(names(rows)).toEqual(["b.txt"])
  })
})
