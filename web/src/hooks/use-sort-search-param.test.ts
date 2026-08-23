import { formatSortParam, parseSortParam } from "./use-sort-search-param"

describe("parseSortParam", () => {
  it("reads a bare column id as ascending", () => {
    expect(parseSortParam("name")).toEqual({ id: "name", desc: false })
    expect(parseSortParam("created-at")).toEqual({ id: "created-at", desc: false })
  })

  it("reads a leading dash as descending", () => {
    expect(parseSortParam("-name")).toEqual({ id: "name", desc: true })
    expect(parseSortParam("-created-at")).toEqual({ id: "created-at", desc: true })
  })

  // `ResourceTable` sorts by one column, but a comma list is what JSON:API
  // spells multi-column sort as — read its highest-priority entry rather than
  // failing, so such a link degrades instead of dropping the sort.
  it("takes the first entry of a comma list", () => {
    expect(parseSortParam("-created,name")).toEqual({ id: "created", desc: true })
    expect(parseSortParam("name,-created")).toEqual({ id: "name", desc: false })
  })

  // The param is user-editable and arrives from shared links, so a malformed
  // one degrades to the default order rather than throwing.
  it.each([undefined, "", "-", " ", ",", ",name"])("treats %o as unsorted", (raw) => {
    expect(parseSortParam(raw)).toBeUndefined()
  })

  it("keeps a column id containing punctuation intact", () => {
    expect(parseSortParam("-aws:arn")).toEqual({ id: "aws:arn", desc: true })
  })
})

describe("formatSortParam", () => {
  it("writes the bare id ascending and a dash prefix descending", () => {
    expect(formatSortParam({ id: "name", desc: false })).toBe("name")
    expect(formatSortParam({ id: "name", desc: true })).toBe("-name")
  })

  // `undefined`, not `""`: an unsorted page must have a clean URL, and
  // TanStack Router drops undefined search values entirely.
  it("drops the param when there is no sort", () => {
    expect(formatSortParam(undefined)).toBeUndefined()
  })

  it("round-trips through parseSortParam", () => {
    for (const sort of [
      { id: "name", desc: false },
      { id: "created-at", desc: true },
      { id: "col-3", desc: false },
    ]) {
      expect(parseSortParam(formatSortParam(sort))).toEqual(sort)
    }
  })
})
