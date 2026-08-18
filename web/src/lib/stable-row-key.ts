/**
 * Stable identity for rows of a virtualized list whose items carry no natural
 * key of their own.
 *
 * Rendering a virtualized list keyed by array index remounts rows whenever a
 * prepend, a filter change, or a sort-order flip shifts the indexes — a
 * delete-and-insert storm for React, for layout (every remounted row
 * re-measures), and for every extension watching the document with a
 * MutationObserver. When the row *objects* are stable across renders — a
 * query cache or an append-only buffer handing out the same objects each
 * time — identity can simply be the object: a `WeakMap` assigns each object
 * a number on first sight and lets a row that falls out of the list take its
 * key with it. Duplicate content (two log lines a function really did emit
 * twice) still gets distinct keys, which content-derived keys cannot promise.
 *
 * This generalizes `logEventKey` in `features/cloudwatch/logs/tail.ts`, which
 * is the original of the pattern; the logs feature is to be re-pointed at
 * this module in a follow-up.
 *
 * ## When NOT to use this
 *
 * Rows with a natural string identity should use it directly — an S3 object
 * is its key, a version is its key + versionId, a queue is its ARN. A natural
 * key survives a refetch that rebuilds the row objects, which an
 * object-identity key does not; reach for `stableRowKey` only when no such
 * identity exists (log events, unkeyed stream frames).
 */

const rowKeys = new WeakMap<object, number>()
let nextRowKey = 0

export function stableRowKey(row: object): number {
  let key = rowKeys.get(row)
  if (key === undefined) {
    key = nextRowKey++
    rowKeys.set(row, key)
  }
  return key
}
