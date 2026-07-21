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
import Prism from "@/lib/prism"
import { cn } from "@/lib/utils"

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

function isImagePreviewable(contentType: string): boolean {
  const mediaType = normalizedContentType(contentType)
  return /^image\/(png|jpe?g|gif|webp|svg\+xml|bmp|avif)$/i.test(mediaType)
}

function isTextPreviewable(contentType: string, key: string): boolean {
  const mediaType = normalizedContentType(contentType)
  if (mediaType.startsWith("text/")) return true
  if (
    /^(application\/(json|xml|javascript|x-ndjson|xhtml\+xml)|.+\+(json|xml))$/i.test(mediaType)
  ) {
    return true
  }
  return /\.(json|jsonl|ndjson|txt|log|md|csv|xml|xhtml|html|htm|css|js|ts|tsx|jsx|yaml|yml|toml|ini|env)$/i.test(
    key,
  )
}

type PreviewLanguage = "json" | "markup" | "css" | "javascript"

function normalizedContentType(contentType: string): string {
  return contentType.split(";", 1)[0].trim().toLowerCase()
}

function previewLanguage(contentType: string, key: string): PreviewLanguage | null {
  const mediaType = normalizedContentType(contentType)
  if (/(^application\/(json|x-ndjson)$|\+json$)/i.test(mediaType) || /\.json$/i.test(key)) {
    return "json"
  }
  if (/(^text\/(html|xml)$|xml$|\+xml$)/i.test(mediaType) || /\.(xml|xhtml|html|htm)$/i.test(key)) {
    return "markup"
  }
  if (/^text\/css$/i.test(mediaType) || /\.css$/i.test(key)) return "css"
  if (
    /^(application|text)\/javascript$/i.test(mediaType) ||
    /\.(mjs|cjs|js|jsx|ts|tsx)$/i.test(key)
  ) {
    return "javascript"
  }
  return null
}

function formatMarkup(text: string): string {
  const compact = text.trim()
  if (!compact) return text
  let depth = 0
  const lines = compact
    .replace(/>\s+</g, "><")
    .split(/(?=<)|(?<=>)/g)
    .map((part) => part.trim())
    .filter(Boolean)
  return lines
    .map((line) => {
      const isClosing = /^<\//.test(line)
      const isDeclaration = /^<\?/.test(line)
      const isComment = /^<!--/.test(line)
      const isDoctype = /^<!DOCTYPE\b/i.test(line)
      const isSelfClosing = /\/>$/.test(line)
      const isOpening = /^<[^!?/][^>]*>$/.test(line)

      if (isClosing) depth = Math.max(0, depth - 1)
      const rendered = `${"  ".repeat(depth)}${line}`
      if (
        isOpening &&
        !isSelfClosing &&
        !isDeclaration &&
        !isComment &&
        !isDoctype &&
        !/^<(area|base|br|col|embed|hr|img|input|link|meta|param|source|track|wbr)\b/i.test(line)
      ) {
        depth += 1
      }
      return rendered
    })
    .join("\n")
}

function formatPreviewText(
  text: string,
  contentType: string,
  key: string,
): { text: string; html?: string } {
  const language = previewLanguage(contentType, key)
  if (language === "json") {
    try {
      const formatted = JSON.stringify(JSON.parse(text), null, 2)
      return { text: formatted, html: Prism.highlight(formatted, Prism.languages.json, "json") }
    } catch {
      return { text }
    }
  }
  if (language === "markup") {
    const formatted = formatMarkup(text)
    return { text: formatted, html: Prism.highlight(formatted, Prism.languages.markup, "markup") }
  }
  if (language) {
    return { text, html: Prism.highlight(text, Prism.languages[language], language) }
  }
  return { text }
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
