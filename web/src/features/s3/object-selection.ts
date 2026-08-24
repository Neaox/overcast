/**
 * Which objects are ticked in the bucket browser, and what that adds up to.
 *
 * The selection is a set of keys rather than of rows: rows are rebuilt on
 * every filter keystroke and every page that arrives, and a selection anchored
 * to them would evaporate underneath the user. Keys are S3's own identity and
 * survive all of it.
 *
 * Only current objects can be selected. Folders are not objects — ticking one
 * would mean recursing a prefix of unknown size — and the version listing is
 * excluded because two versions of one key cannot both be a file of the same
 * name in one archive.
 */

import type { BrowserRow } from "@/features/s3/object-browser"

/** How many objects one archive request carries — mirrors maxArchiveKeys in internal/bff/s3_archive.go. */
export const MAX_ARCHIVE_OBJECTS = 5_000

export type SelectAllState = "none" | "some" | "all"

/** The keys of the object rows currently listed, in listing order. */
export function selectableKeys(rows: readonly BrowserRow[]): string[] {
  const keys: string[] = []
  for (const row of rows) if (row.type === "object") keys.push(row.key)
  return keys
}

/** Adds or removes one key, returning a new set so React sees the change. */
export function toggleKey(selected: ReadonlySet<string>, key: string): Set<string> {
  const next = new Set(selected)
  if (!next.delete(key)) next.add(key)
  return next
}

/**
 * What the header checkbox does next: tick everything currently listed, or —
 * once all of it is ticked — clear it.
 *
 * "Currently listed" is the point. A filter narrows the rows, so select-all
 * under a filter selects what the user can see rather than everything the
 * scan happens to have loaded. Keys selected before the filter was typed are
 * kept: they are still selected, just not on screen, and the count says so.
 */
export function toggleAll(selected: ReadonlySet<string>, listed: readonly string[]): Set<string> {
  const next = new Set(selected)
  if (selectAllState(selected, listed) === "all") {
    for (const key of listed) next.delete(key)
    return next
  }
  for (const key of listed) next.add(key)
  return next
}

/** Whether none, some, or all of the listed rows are ticked. */
export function selectAllState(
  selected: ReadonlySet<string>,
  listed: readonly string[],
): SelectAllState {
  if (listed.length === 0 || selected.size === 0) return "none"
  let hits = 0
  for (const key of listed) if (selected.has(key)) hits++
  if (hits === 0) return "none"
  return hits === listed.length ? "all" : "some"
}

/**
 * Drops one key from the selection.
 *
 * Used when an object is deleted out from under a selection that counted it:
 * a count that includes a key which no longer exists promises a download an
 * archive cannot contain.
 */
export function deselectKey(selected: ReadonlySet<string>, key: string): ReadonlySet<string> {
  if (!selected.has(key)) return selected
  const next = new Set(selected)
  next.delete(key)
  return next
}

export interface SelectionSummary {
  count: number
  /** Total size of the selected objects that are on screen to be measured. */
  bytes: number
}

/**
 * The count and total size behind the download button.
 *
 * Size is summed over the rows in hand, which is every selected row unless a
 * filter is hiding some; the count is the whole selection either way. That
 * asymmetry is deliberate — an understated size is better than a count that
 * disagrees with what the download will contain.
 */
export function selectionSummary(
  rows: readonly BrowserRow[],
  selected: ReadonlySet<string>,
): SelectionSummary {
  let bytes = 0
  for (const row of rows) {
    if (row.type === "object" && selected.has(row.key)) bytes += row.size
  }
  return { count: selected.size, bytes }
}
