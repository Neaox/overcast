import { describe, it, expect } from "vitest"
import type { BrowserRow } from "@/features/s3/object-browser"
import {
  selectableKeys,
  toggleKey,
  toggleAll,
  selectAllState,
  deselectKey,
  selectionSummary,
} from "./object-selection"

function objectRow(key: string, size = 100): BrowserRow {
  return {
    type: "object",
    name: key,
    key,
    size,
    lastModified: "2026-01-01T00:00:00.000Z",
    storageClass: "STANDARD",
  }
}

function folderRow(prefix: string): BrowserRow {
  return { type: "prefix", name: prefix, prefix }
}

describe("selectableKeys", () => {
  it("offers the object rows and skips the folders", () => {
    expect(selectableKeys([folderRow("logs/"), objectRow("a.txt"), objectRow("b.txt")])).toEqual([
      "a.txt",
      "b.txt",
    ])
  })
})

describe("toggleKey", () => {
  it("adds a key that was not ticked", () => {
    expect([...toggleKey(new Set(), "a.txt")]).toEqual(["a.txt"])
  })

  it("removes a key that was", () => {
    expect([...toggleKey(new Set(["a.txt", "b.txt"]), "a.txt")]).toEqual(["b.txt"])
  })

  it("returns a new set, so a re-render sees the change", () => {
    const before = new Set(["a.txt"])
    expect(toggleKey(before, "b.txt")).not.toBe(before)
    expect([...before]).toEqual(["a.txt"])
  })
})

describe("selectAllState", () => {
  it("is none when nothing is ticked", () => {
    expect(selectAllState(new Set(), ["a.txt", "b.txt"])).toBe("none")
  })

  it("is some when part of the listing is ticked", () => {
    expect(selectAllState(new Set(["a.txt"]), ["a.txt", "b.txt"])).toBe("some")
  })

  it("is all when the whole listing is ticked", () => {
    expect(selectAllState(new Set(["a.txt", "b.txt"]), ["a.txt", "b.txt"])).toBe("all")
  })

  it("is none for an empty listing, whatever is selected off screen", () => {
    expect(selectAllState(new Set(["a.txt"]), [])).toBe("none")
  })

  it("ignores selected keys that are not in the listing", () => {
    expect(selectAllState(new Set(["hidden.txt"]), ["a.txt"])).toBe("none")
  })
})

describe("toggleAll", () => {
  it("ticks everything listed when only some of it is", () => {
    expect([...toggleAll(new Set(["a.txt"]), ["a.txt", "b.txt"])].sort()).toEqual([
      "a.txt",
      "b.txt",
    ])
  })

  it("clears the listing once all of it is ticked", () => {
    expect([...toggleAll(new Set(["a.txt", "b.txt"]), ["a.txt", "b.txt"])]).toEqual([])
  })

  it("leaves keys hidden by a filter alone", () => {
    // Selected under an earlier filter, off screen now: clearing what is
    // listed must not silently drop what is not.
    const selected = new Set(["hidden.txt", "a.txt"])
    expect([...toggleAll(selected, ["a.txt"])]).toEqual(["hidden.txt"])
  })
})

describe("deselectKey", () => {
  it("drops the key", () => {
    expect([...deselectKey(new Set(["a.txt", "b.txt"]), "a.txt")]).toEqual(["b.txt"])
  })

  it("returns the same set when the key was not selected, so no re-render is provoked", () => {
    const before = new Set(["a.txt"])
    expect(deselectKey(before, "b.txt")).toBe(before)
  })
})

describe("selectionSummary", () => {
  it("counts the whole selection and sizes what it can see", () => {
    const rows = [folderRow("logs/"), objectRow("a.txt", 300), objectRow("b.txt", 700)]
    expect(selectionSummary(rows, new Set(["a.txt", "b.txt"]))).toEqual({ count: 2, bytes: 1000 })
  })

  it("counts a key the filter is hiding even though it cannot size it", () => {
    // The download will contain it, so the count says so; the total is what
    // the rows in hand can prove.
    expect(selectionSummary([objectRow("a.txt", 300)], new Set(["a.txt", "hidden.txt"]))).toEqual({
      count: 2,
      bytes: 300,
    })
  })

  it("is empty for an empty selection", () => {
    expect(selectionSummary([objectRow("a.txt")], new Set())).toEqual({ count: 0, bytes: 0 })
  })
})
