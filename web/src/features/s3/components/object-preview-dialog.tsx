import { useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { Download } from "lucide-react"
import { s3ObjectPreviewQueryOptions } from "@/features/s3/data"
import { s3 } from "@/services/api"
import { useEndpoint } from "@/hooks/use-endpoint"
import { Button } from "@/components/ui/button"
import { CopyUrlButton } from "@/components/ui/copy-url-button"
import { s3CopyFormats } from "@/features/s3/copy-formats"
import { Definition, DefinitionList } from "@/components/ui/definition-card"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner, CodeBlock } from "@/components/ui/primitives"
import { formatBytes, formatDate } from "@/lib/format"
import { describeObjectReadError } from "@/features/s3/object-read-error"
import { formatPreviewText, isImagePreviewable, isTextPreviewable } from "./object-preview-format"

const TEXT_PREVIEW_LIMIT = 1024 * 1024

interface ObjectMetadata {
  contentType: string
  contentLength: number
  lastModified: string
  etag: string
  metadata: Record<string, string>
  storageClass?: string
  /** S3's x-amz-expiration hint — present only when a rule will expire this object. */
  expiration?: { expiryDate: string; ruleId: string }
}

interface ObjectPreviewDialogProps {
  bucket: string
  objectKey: string | undefined
  /**
   * The stored revision being inspected, when the dialog was opened from the
   * version history. Undefined means whichever version is current. `"null"` is
   * a real version id, not an absent one, so every test here is for presence.
   */
  versionId?: string
  metadata: ObjectMetadata | undefined
  loading: boolean
  /** Why the metadata read failed, if it did. */
  error?: Error | null
  onClose: () => void
}

export function ObjectPreviewDialog({
  bucket,
  objectKey,
  versionId,
  metadata,
  loading,
  error,
  onClose,
}: ObjectPreviewDialogProps) {
  const endpoint = useEndpoint()
  const previewUrl = objectKey ? s3.getObjectDownloadUrl(bucket, objectKey, versionId) : undefined
  const canPreviewImage = !!metadata && isImagePreviewable(metadata.contentType)
  const canPreviewText =
    !!objectKey &&
    !!metadata &&
    metadata.contentLength <= TEXT_PREVIEW_LIMIT &&
    isTextPreviewable(metadata.contentType, objectKey)
  const { data: previewText, isLoading: previewLoading } = useQuery({
    ...s3ObjectPreviewQueryOptions(bucket, objectKey ?? "", versionId),
    enabled: canPreviewText && !!previewUrl,
  })
  const formattedPreview = useMemo(
    () =>
      previewText && metadata && objectKey
        ? formatPreviewText(previewText.text, metadata.contentType, objectKey)
        : undefined,
    [metadata, objectKey, previewText],
  )

  return (
    <Dialog open={!!objectKey} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-4xl overflow-hidden">
        <DialogHeader>
          <DialogTitle
            className="flex items-center gap-2 truncate font-mono text-sm"
            title={objectKey}
          >
            <span className="truncate">{objectKey}</span>
            {objectKey && (
              <CopyUrlButton
                compact
                formats={s3CopyFormats(endpoint.baseUrl, bucket, objectKey, versionId)}
                noun="URL"
              />
            )}
          </DialogTitle>
        </DialogHeader>
        {loading ? (
          <div className="flex justify-center py-8">
            <Spinner />
          </div>
        ) : error ? (
          <div
            role="alert"
            className="rounded-lg border border-border bg-bg-muted px-3 py-2 text-sm text-fg-muted"
          >
            {describeObjectReadError(error, versionId)}
          </div>
        ) : metadata ? (
          <div className="flex min-h-0 flex-col gap-3 overflow-hidden">
            <DefinitionList layout="inline">
              {versionId !== undefined && <Definition label="Version" value={versionId} />}
              <Definition label="Content-Type" value={metadata.contentType} />
              <Definition label="Size" value={formatBytes(metadata.contentLength)} />
              <Definition label="Last Modified" value={formatDate(metadata.lastModified)} />
              <Definition label="ETag" value={metadata.etag} />
              {metadata.storageClass && metadata.storageClass !== "STANDARD" && (
                <Definition label="Storage Class" value={metadata.storageClass} />
              )}
              {metadata.expiration && (
                <Definition
                  label="Expires"
                  value={`${metadata.expiration.expiryDate} (lifecycle rule ${metadata.expiration.ruleId})`}
                />
              )}
            </DefinitionList>
            {previewUrl && canPreviewImage && (
              <div className="min-h-0 overflow-auto rounded-lg border border-border bg-bg-muted p-3">
                <img
                  src={previewUrl}
                  alt={objectKey ?? "S3 object preview"}
                  className="mx-auto max-h-[55vh] max-w-full rounded object-contain"
                />
              </div>
            )}
            {canPreviewText && (
              <div className="min-h-0 overflow-hidden rounded-lg border border-border bg-bg-muted">
                <div className="border-b border-border px-3 py-2 text-xs font-medium text-fg-muted">
                  Preview{previewText?.truncated ? " (first 1 MiB)" : ""}
                  {formattedPreview?.highlightSkipped
                    ? " — shown as plain text; too large to format"
                    : ""}
                </div>
                {previewLoading ? (
                  <div className="flex justify-center py-8">
                    <Spinner />
                  </div>
                ) : formattedPreview?.html ? (
                  <pre
                    className="max-h-[55vh] overflow-auto p-3 font-mono text-xs leading-relaxed whitespace-pre text-fg"
                    dangerouslySetInnerHTML={{ __html: formattedPreview.html }}
                  />
                ) : (
                  <pre className="max-h-[55vh] overflow-auto p-3 font-mono text-xs leading-relaxed wrap-break-word whitespace-pre-wrap text-fg">
                    {formattedPreview?.text ?? ""}
                  </pre>
                )}
              </div>
            )}
            {objectKey && !canPreviewImage && !canPreviewText && (
              <div className="rounded-lg border border-border bg-bg-muted px-3 py-2 text-sm text-fg-muted">
                Preview is available for common image files and text-like objects up to 1 MiB.
              </div>
            )}
            {Object.keys(metadata.metadata).length > 0 && (
              <div>
                <p className="mb-1.5 font-mono text-sm font-medium text-fg-muted">User metadata</p>
                <CodeBlock>{JSON.stringify(metadata.metadata, null, 2)}</CodeBlock>
              </div>
            )}
          </div>
        ) : null}
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            Close
          </Button>
          {/* Gated on the metadata rather than the key: when the read failed
              there is nothing at that address to download, and offering the
              button anyway just moves the same failure into a new tab. */}
          {objectKey && metadata && (
            <Button asChild>
              <a href={s3.getObjectDownloadUrl(bucket, objectKey, versionId)} download>
                <Download className="h-4 w-4" /> Download
              </a>
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
