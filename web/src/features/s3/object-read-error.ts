/**
 * Explains why a read of an object — or of one named version of it — came back
 * with nothing.
 *
 * Addressing a version rather than a key adds two answers S3 gives no other
 * way, and both are ordinary states of a version listing rather than faults:
 *
 *   - `405 MethodNotAllowed` — the id names a delete marker. That version
 *     exists; it is a tombstone, and reading one is not an operation S3 offers.
 *     A version listing shows markers by design, so this is reachable from a
 *     row that is doing nothing wrong.
 *   - `404 NoSuchVersion` — the id names nothing. A listing goes stale the
 *     moment another client deletes that version, so the row can outlive what
 *     it points at.
 *
 * The HTTP status is what the two verbs agree on. A HEAD carries no body, so
 * the SDK cannot recover the XML `<Code>` that an unmodelled error would
 * otherwise be named after, and a HeadObject rejection arrives as a generic
 * error with only `$metadata.httpStatusCode` to go on. On these paths each
 * status has exactly one cause, so the status is enough — and where a code
 * does survive (a GET through the BFF), it is preferred.
 */

/** What a failed read turned out to be. */
export type ObjectReadFailure = "delete-marker" | "no-such-version" | "not-found" | "other"

/**
 * The HTTP status behind a failure, from whichever shape carries it: an AWS SDK
 * exception's `$metadata`, or the `status` this codebase's own fetch wrappers
 * attach.
 */
function httpStatus(error: unknown): number | undefined {
  if (typeof error !== "object" || error === null) return undefined
  const err = error as { status?: unknown; $metadata?: { httpStatusCode?: unknown } }
  if (typeof err.status === "number") return err.status
  const code = err.$metadata?.httpStatusCode
  return typeof code === "number" ? code : undefined
}

/** The AWS error code, when the response body survived far enough to carry one. */
function awsErrorCode(error: unknown): string | undefined {
  if (typeof error !== "object" || error === null) return undefined
  const name = (error as { name?: unknown }).name
  return typeof name === "string" ? name : undefined
}

/**
 * Classifies a failed GetObject/HeadObject.
 *
 * `versionId` decides how a 404 reads: with one, the version is gone; without,
 * the key is. Pass it whenever the read named a version — `"null"` included,
 * which is a real id and not an absent one.
 */
export function classifyObjectReadError(error: unknown, versionId?: string): ObjectReadFailure {
  const code = awsErrorCode(error)
  if (code === "MethodNotAllowed") return "delete-marker"
  if (code === "NoSuchVersion") return "no-such-version"

  switch (httpStatus(error)) {
    case 405:
      return "delete-marker"
    case 404:
      return versionId === undefined ? "not-found" : "no-such-version"
    default:
      return "other"
  }
}

/**
 * A sentence to show in place of the object, saying what happened and — where
 * there is one — what the user can do about it.
 */
export function describeObjectReadError(error: unknown, versionId?: string): string {
  switch (classifyObjectReadError(error, versionId)) {
    case "delete-marker":
      return "This version is a delete marker — a tombstone with no body to read. Removing it restores whatever version sits beneath it."
    case "no-such-version":
      return `Version ${versionId} no longer exists. It may have been deleted since this listing was loaded.`
    case "not-found":
      return "This object no longer exists. It may have been deleted since this listing was loaded."
    case "other":
      return error instanceof Error && error.message
        ? error.message
        : "This object could not be read."
  }
}
