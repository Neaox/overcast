/**
 * Shortening an S3 version id to fit a table cell.
 *
 * Overcast mints a version id as base32hex over an inverted timestamp, then a
 * counter, then three bytes of salt (internal/services/s3/version.go). The
 * timestamp comes first so that ids sort newest-first, which means every
 * version of a key written in the same era shares a long leading run: three
 * versions of one key all render as "SSQQC98J…" under a leading-edge
 * truncation, and the column identifies nothing.
 *
 * The end of the id is the counter and the salt — the part that actually
 * varies between two versions of the same key — so that is the part shown.
 */

/** How many trailing characters identify a version. */
export const VERSION_ID_TAIL = 8

export function shortVersionId(versionId: string): string {
  // An id no longer than the truncation is shown whole: "…" plus the tail
  // would cost as much room and say less. The null version's id is one of
  // these, which is why it needs no case of its own.
  if (versionId.length <= VERSION_ID_TAIL + 1) return versionId
  return `…${versionId.slice(-VERSION_ID_TAIL)}`
}
