import { useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { Download } from "lucide-react"
import { s3ObjectPreviewQueryOptions } from "@/features/s3/data"
import { s3 } from "@/services/api"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner, CodeBlock } from "@/components/ui/primitives"
import { formatBytes, formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { formatPreviewText, isImagePreviewable, isTextPreviewable } from "./object-preview-format"

const TEXT_PREVIEW_LIMIT = 1024 * 1024

interface ObjectMetadata {
  contentType: string
  contentLength: number
  lastModified: string
  etag: string
  metadata: Record<string, string>
}

interface ObjectPreviewDialogProps {
  bucket: string
  objectKey: string | undefined
  metadata: ObjectMetadata | undefined
  loading: boolean
  onClose: () => void
}

export function ObjectPreviewDialog({
  bucket,
  objectKey,
  metadata,
  loading,
  onClose,
}: ObjectPreviewDialogProps) {
  const previewUrl = objectKey ? s3.getObjectDownloadUrl(bucket, objectKey) : undefined
  const canPreviewImage = !!metadata && isImagePreviewable(metadata.contentType)
  const canPreviewText =
    !!objectKey &&
    !!metadata &&
    metadata.contentLength <= TEXT_PREVIEW_LIMIT &&
    isTextPreviewable(metadata.contentType, objectKey)
  const { data: previewText, isLoading: previewLoading } = useQuery({
    ...s3ObjectPreviewQueryOptions(bucket, objectKey ?? ""),
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
          <DialogTitle className="truncate font-mono text-sm" title={objectKey}>
            {objectKey}
          </DialogTitle>
        </DialogHeader>
        {loading ? (
          <div className="flex justify-center py-8">
            <Spinner />
          </div>
        ) : metadata ? (
          <div className="flex min-h-0 flex-col gap-3 overflow-hidden">
            <MetaRow label="Content-Type" value={metadata.contentType} />
            <MetaRow label="Size" value={formatBytes(metadata.contentLength)} />
            <MetaRow label="Last Modified" value={formatDate(metadata.lastModified)} />
            <MetaRow label="ETag" value={metadata.etag} mono />
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
                <p className="mb-1.5 text-sm font-medium text-fg-muted">User metadata</p>
                <CodeBlock>{JSON.stringify(metadata.metadata, null, 2)}</CodeBlock>
              </div>
            )}
          </div>
        ) : null}
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            Close
          </Button>
          {objectKey && (
            <Button asChild>
              <a href={s3.getObjectDownloadUrl(bucket, objectKey)} download>
                <Download className="h-4 w-4" /> Download
              </a>
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function MetaRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex gap-3">
      <span className="w-32 shrink-0 text-sm text-fg-muted">{label}</span>
      <span className={cn("text-sm break-all text-fg", mono && "font-mono")}>{value}</span>
    </div>
  )
}
