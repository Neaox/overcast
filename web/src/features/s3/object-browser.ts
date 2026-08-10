/**
 * Search and sort for the bucket object browser.
 *
 * The two halves of a search are answered in different places on purpose. S3's
 * only server-side filter is `Prefix`, so everything up to the last `/` is sent
 * with the request and costs nothing; the fragment after it is a substring
 * match no S3 API offers, so it is applied here over what came back. That split
 * is what keeps `logs/2024/err` a single narrow listing instead of a scan of
 * the whole bucket.
 *
 * Sorting is client-side for the same reason: S3 returns keys in ascending
 * order and offers no other, so every order but that one needs the full listing
 * before its first row is correct. `isServerOrder` is what the browser consults
 * to decide whether it may render a page at a time or has to read to the end.
 */

import type { S3Object, S3ObjectVersion, S3Prefix } from "@/types"

// ─── Sort ──────────────────────────────────────────────────────────────────

export type SortColumn = "name" | "size" | "modified"
export type SortDirection = "asc" | "desc"

export interface ObjectSort {
  column: SortColumn
  direction: SortDirection
}

/** Ascending by key — the order ListObjectsV2 hands back. */
export const DEFAULT_SORT: ObjectSort = { column: "name", direction: "asc" }

/**
 * Whether `sort` is the order the listing already arrives in.
 *
 * Only then can rows be shown a page at a time: any other order would put rows
 * from page 5 above rows from page 1, so the browser has to read the listing to
 * the end before it can draw anything truthful.
 */
export function isServerOrder(sort: ObjectSort): boolean {
  return sort.column === "name" && sort.direction === "asc"
}

/**
 * The sort a header click produces: the same column flips direction, a new
 * column starts at the direction that is useful first — A→Z for names, but
 * largest and newest first for size and time, which is what the question
 * "what is big / what changed" actually asks.
 */
export function nextSort(current: ObjectSort, column: SortColumn): ObjectSort {
  if (current.column === column) {
    return { column, direction: current.direction === "asc" ? "desc" : "asc" }
  }
  return { column, direction: column === "name" ? "asc" : "desc" }
}

// ─── Search ────────────────────────────────────────────────────────────────

export interface SearchTerms {
  /** Sent to S3 as part of `Prefix`: everything up to and including the last `/`. */
  prefixPart: string
  /** Lower-cased substring the row name has to contain. `""` matches everything. */
  term: string
}

/**
 * Splits what the user typed into the part S3 can answer and the part we can.
 *
 * `""` and a value ending in `/` both yield an empty term, which is a listing
 * of that prefix rather than a filter of it — typing `logs/` browses `logs/`.
 */
export function splitSearch(search: string): SearchTerms {
  const trimmed = search.trim()
  const cut = trimmed.lastIndexOf("/")
  if (cut === -1) return { prefixPart: "", term: trimmed.toLowerCase() }
  return { prefixPart: trimmed.slice(0, cut + 1), term: trimmed.slice(cut + 1).toLowerCase() }
}

/**
 * Whether `name` contains `term` at or after `from`.
 *
 * `from` skips the segment that came from the search's own prefix part, which
 * S3 has already matched exactly. Without it, searching `logs/log` would match
 * `logs/report.txt` — the `log` found in the folder name the user did not type
 * as the term.
 */
export function matchesTerm(name: string, term: string, from = 0): boolean {
  if (term === "") return true
  return name.toLowerCase().indexOf(term, from) !== -1
}

export interface NameSlice {
  text: string
  match: boolean
}

/** Splits `name` into alternating plain and matched runs, for highlighting. */
export function highlightSlices(name: string, term: string, from = 0): NameSlice[] {
  if (term === "") return [{ text: name, match: false }]

  const haystack = name.toLowerCase()
  const slices: NameSlice[] = []
  let at = 0
  let searchFrom = from

  for (;;) {
    const found = haystack.indexOf(term, searchFrom)
    if (found === -1) break
    if (found > at) slices.push({ text: name.slice(at, found), match: false })
    slices.push({ text: name.slice(found, found + term.length), match: true })
    at = found + term.length
    searchFrom = at
  }

  if (slices.length === 0) return [{ text: name, match: false }]
  if (at < name.length) slices.push({ text: name.slice(at), match: false })
  return slices
}

// ─── Rows ──────────────────────────────────────────────────────────────────

export interface PrefixRow {
  type: "prefix"
  /** Displayed name, relative to the folder being browsed. */
  name: string
  /** Full key prefix, for navigation and deletion. */
  prefix: string
}

export interface ObjectRow {
  type: "object"
  name: string
  key: string
  size: number
  lastModified: string
  storageClass: string
}

export interface VersionRow {
  type: "version"
  name: string
  version: S3ObjectVersion
}

export type BrowserRow = PrefixRow | ObjectRow | VersionRow

export interface BuildRowsInput {
  /** The folder the breadcrumb is on — what row names are shown relative to. */
  browsePrefix: string
  /** What was actually sent to S3: `browsePrefix` plus the search's prefix part. */
  listPrefix: string
  term: string
  sort: ObjectSort
  prefixes: S3Prefix[]
  /** Present when browsing current objects. */
  objects?: S3Object[]
  /** Present instead of `objects` when browsing version history. */
  versions?: S3ObjectVersion[]
}

/**
 * The flat row list the virtualizer renders: matching folders first, then
 * matching objects (or versions), in the requested order.
 *
 * Folders lead regardless of sort because they are not comparable on size or
 * time — a folder has neither — and burying them among objects loses the only
 * navigation the listing offers.
 */
export function buildRows({
  browsePrefix,
  listPrefix,
  term,
  sort,
  prefixes,
  objects,
  versions,
}: BuildRowsInput): BrowserRow[] {
  // Offset of the part of the name the term applies to: everything before it
  // is the prefix S3 matched exactly.
  const from = Math.max(0, listPrefix.length - browsePrefix.length)
  const relative = (key: string) => key.slice(browsePrefix.length)

  const folders: PrefixRow[] = []
  for (const p of prefixes) {
    const name = relative(p.prefix)
    if (matchesTerm(name, term, from)) folders.push({ type: "prefix", name, prefix: p.prefix })
  }
  // Folders carry no size or timestamp, so they only ever order by name — but
  // they honour the direction when that is the column being sorted, otherwise a
  // Z→A sort would leave them stubbornly A→Z at the top.
  const folderFlip = sort.column === "name" && sort.direction === "desc" ? -1 : 1
  folders.sort((a, b) => folderFlip * compareStrings(a.name, b.name))

  const entries: (ObjectRow | VersionRow)[] = []
  if (versions) {
    for (const v of versions) {
      const name = relative(v.key)
      if (matchesTerm(name, term, from)) entries.push({ type: "version", name, version: v })
    }
  } else {
    for (const o of objects ?? []) {
      const name = relative(o.key)
      if (!matchesTerm(name, term, from)) continue
      entries.push({
        type: "object",
        name,
        key: o.key,
        size: o.size,
        lastModified: o.lastModified,
        storageClass: o.storageClass,
      })
    }
  }
  // Stable, so versions of one key keep S3's newest-first order under a name
  // sort rather than being shuffled within the group.
  entries.sort(rowComparator(sort))

  return [...folders, ...entries]
}

function compareStrings(a: string, b: string): number {
  // Code-unit order, not `localeCompare`: this has to agree with the order S3
  // paginates in, or an ascending sort would reshuffle rows as pages arrive.
  return a < b ? -1 : a > b ? 1 : 0
}

function rowComparator(
  sort: ObjectSort,
): (a: ObjectRow | VersionRow, b: ObjectRow | VersionRow) => number {
  const flip = sort.direction === "desc" ? -1 : 1
  switch (sort.column) {
    case "size":
      return (a, b) => flip * (sizeOf(a) - sizeOf(b))
    case "modified":
      // ISO-8601 UTC strings compare chronologically as text.
      return (a, b) => flip * compareStrings(modifiedOf(a), modifiedOf(b))
    case "name":
      return (a, b) => flip * compareStrings(a.name, b.name)
  }
}

function sizeOf(row: ObjectRow | VersionRow): number {
  return row.type === "object" ? row.size : row.version.isDeleteMarker ? 0 : row.version.size
}

function modifiedOf(row: ObjectRow | VersionRow): string {
  return row.type === "object" ? row.lastModified : row.version.lastModified
}
