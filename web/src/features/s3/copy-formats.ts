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
 */
export function s3CopyFormats(baseUrl: string, bucket: string, key?: string): CopyUrlFormat[] {
  return [
    {
      label: "S3 URI",
      value: `s3://${key ? `${bucket}/${key}` : bucket}`,
      description: "aws cli",
    },
    {
      label: "Path-style",
      value: `${baseUrl}/${key ? `${bucket}/${encodeKeyForUrl(key)}` : bucket}`,
      description: "http",
    },
  ]
}
