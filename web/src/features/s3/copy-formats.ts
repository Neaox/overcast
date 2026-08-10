import type { CopyUrlFormat } from "@/components/ui/copy-url-button"

/**
 * Percent-encode an object key for use in a URL path, segment by segment.
 * Keys may contain characters that break a pasted URL (`#` starts a fragment,
 * `?` a query, spaces and unicode need escaping), but `/` separates real path
 * segments and must survive as-is — so encode each segment, not the whole key.
 */
function encodeKeyForUrl(key: string): string {
  return key.split("/").map(encodeURIComponent).join("/")
}

/**
 * Copy-URL formats for a bucket, prefix, or object.
 *
 * The `s3://` form stays raw — the AWS CLI expects the literal key. Only the
 * HTTP path-style form is percent-encoded.
 *
 * `versionId` names one stored revision. It reaches only the path-style URL, as
 * `?versionId=`: an `s3://` URI has nowhere to put it, the CLI taking
 * `--version-id` as a separate argument, so appending one there would produce a
 * URI that addresses a key which does not exist. `"null"` is a real version id
 * and is emitted like any other; presence decides, not truthiness.
 */
export function s3CopyFormats(
  baseUrl: string,
  bucket: string,
  key?: string,
  versionId?: string,
): CopyUrlFormat[] {
  const pathStyle = `${baseUrl}/${key ? `${bucket}/${encodeKeyForUrl(key)}` : bucket}`
  const query =
    key !== undefined && versionId !== undefined
      ? `?versionId=${encodeURIComponent(versionId)}`
      : ""
  return [
    {
      label: "S3 URI",
      value: `s3://${key ? `${bucket}/${key}` : bucket}`,
      description: "aws cli",
    },
    {
      label: "Path-style",
      value: `${pathStyle}${query}`,
      description: "http",
    },
  ]
}
