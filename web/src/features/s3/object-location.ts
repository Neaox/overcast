/**
 * Where the object browser is pointed, expressed as a URL path.
 *
 * The browser's position — which folder is listed, which object the inspector
 * is open on — lives in the path under `/s3/<bucket>/objects/`, not in
 * component state. That is what makes an object linkable: a reload lands back
 * on the same folder, Back closes the inspector instead of leaving the bucket,
 * and the address bar is a link worth pasting into a ticket.
 *
 * The path after `objects/` is read the way S3 itself reads a key: a trailing
 * "/" is a prefix to list, anything else is an object to open. A key that
 * genuinely ends in "/" — a folder marker written by a console — is therefore
 * read as the folder it is drawn as, which is the same trade every S3 browser
 * makes.
 */

export interface BrowserLocation {
  /**
   * The folder whose listing is shown. `""` is the bucket root; every other
   * value ends in "/".
   */
  prefix: string
  /** The object the inspector is open on, if the path names one. */
  objectKey?: string
}

/** The bucket root — nothing listed below it, nothing open. */
const ROOT: BrowserLocation = { prefix: "" }

/**
 * Put back the trailing slash the router trimmed.
 *
 * A splat param arrives with its trailing slash removed — the URL keeps it,
 * the param does not — and that slash is the entire difference between the
 * folder `logs/` and an object called `logs`. So the marker is read off the
 * pathname the address bar actually holds, and the param supplies the decoded
 * key.
 */
export function browserSplat(splat: string | undefined, pathname: string): string {
  if (!splat) return ""
  return pathname.endsWith("/") ? `${splat}/` : splat
}

/**
 * Read a browser position out of the `objects/$` splat.
 *
 * An object's location carries the folder it sits in as well as the key, so
 * closing the inspector reveals the listing the object came from rather than
 * dropping the user at the bucket root.
 */
export function parseBrowserPath(splat: string | undefined): BrowserLocation {
  if (!splat) return ROOT
  if (splat.endsWith("/")) return { prefix: splat }
  const lastSlash = splat.lastIndexOf("/")
  return { prefix: lastSlash === -1 ? "" : splat.slice(0, lastSlash + 1), objectKey: splat }
}
