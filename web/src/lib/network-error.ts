/**
 * Telling "the request never landed" apart from "the emulator answered, and
 * the answer was no".
 *
 * The distinction is what the failure is worth saying out loud. A transport
 * failure says nothing a developer can act on beyond "the backend is away",
 * and while the connection is down it is true of every request at once. A
 * service error — a 4xx carrying a real AWS error body, a validation message —
 * is specific to what was asked and always worth surfacing.
 */

/**
 * How the three engines word a fetch that never reached a server: Chrome
 * `Failed to fetch`, Firefox `NetworkError when attempting to fetch resource.`,
 * Safari `Load failed`. The AWS SDK wraps them without rewriting the message.
 *
 * The word boundaries earn their keep: without one, Safari's wording is a
 * substring of `Upload failed`, so every failed S3 upload would read as a
 * dropped connection.
 */
const NETWORK_FAILURE = /failed to fetch|\bnetwork\s?error|\bload failed/i

/** Did this error come from the connection rather than from the emulator? */
export function isNetworkError(error: unknown): boolean {
  if (!(error instanceof Error)) return false
  if (error.name === "NetworkError") return true
  return NETWORK_FAILURE.test(error.message)
}

/**
 * The same test against a line of copy rather than an error — a toast's title
 * or its detail, the latter being the message `isNetworkError` would have been
 * handed had the raiser passed the error along instead.
 *
 * Callers test each line on its own. Joining them first lets a match straddle
 * the seam: `Upload failed` followed by `to fetch the manifest` reads as
 * `failed to fetch` only because they were concatenated.
 */
export function readsAsNetworkFailure(text: string): boolean {
  return NETWORK_FAILURE.test(text)
}
