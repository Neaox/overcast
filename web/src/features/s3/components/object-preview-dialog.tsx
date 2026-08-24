import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Download } from "lucide-react"
import { s3ObjectHistoryQueryOptions, s3ObjectPreviewQueryOptions } from "@/features/s3/data"
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
import { HighlightedCode } from "@/components/ui/highlighted-code"
import { Tab, TabList, TabPanel, Tabs } from "@/components/ui/tabs"
import { Spinner, CodeBlock } from "@/components/ui/primitives"
import { ObjectRevisionBar, ObjectVersionList } from "./object-version-list"
import { cn } from "@/lib/utils"
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
  /**
   * Whether the bucket keeps history at all. An unversioned bucket has one
   * revision per key and nothing to switch between, so it gets no tab strip.
   */
  isVersioned?: boolean
  /** Point the inspector at another revision of the same key. */
  onSelectVersion: (versionId: string) => void
  onClose: () => void
}

/** Which pane of the inspector is showing. */
type InspectorTab = "details" | "versions"

export function ObjectPreviewDialog({
  bucket,
  objectKey,
  versionId,
  metadata,
  loading,
  error,
  isVersioned = false,
  onSelectVersion,
  onClose,
}: ObjectPreviewDialogProps) {
  const endpoint = useEndpoint()
  const [tab, setTab] = useState<InspectorTab>("details")
  // A different object opens on its details, whatever pane the last one was
  // left on. Keyed on the object rather than on the revision: moving between
  // revisions of one key is a drill-down that lands on the details of its own
  // accord (see showVersion), and the two must not fight over the tab.
  //
  // Adjusted during render rather than in an effect — React re-runs this pass
  // before committing anything, so the first paint of a new object is already
  // on its details, where an effect would paint the wrong tab and then correct
  // it.
  const [tabbedKey, setTabbedKey] = useState(objectKey)
  if (objectKey !== tabbedKey) {
    setTabbedKey(objectKey)
    setTab("details")
  }

  // Picking a revision is a drill-down, so it lands on that revision's details.
  // A click that only moved a tick in a list would leave the user to work out
  // for themselves that the thing they asked to see is behind another tab.
  const showVersion = (nextVersionId: string) => {
    setTab("details")
    onSelectVersion(nextVersionId)
  }

  // Fetched as soon as the inspector opens rather than when the tab is picked,
  // so the tab can say how many revisions there are before it is clicked. One
  // listing per object, on versioned buckets only.
  const { data: history, isLoading: historyLoading } = useQuery({
    ...s3ObjectHistoryQueryOptions(bucket, objectKey ?? ""),
    enabled: !!objectKey && isVersioned,
  })
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

  const details = loading ? (
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
            {formattedPreview?.skipped ? " — shown as plain text; too large to format" : ""}
          </div>
          {previewLoading ? (
            <div className="flex justify-center py-8">
              <Spinner />
            </div>
          ) : (
            <HighlightedCode
              text={formattedPreview?.text ?? ""}
              language={formattedPreview?.language ?? null}
              className={cn(
                "max-h-[55vh] overflow-auto p-3 font-mono text-xs leading-relaxed text-fg",
                // Policy, not accident (kept from the dialog's first
                // commit): a document we chose a language for is code —
                // it keeps its line shape and scrolls horizontally —
                // while arbitrary plain text wraps. The skipped case
                // (a 1 MiB minified bundle) lands in the wrapped branch,
                // where it would otherwise be one mile-wide line.
                formattedPreview?.language != null
                  ? "whitespace-pre"
                  : "wrap-break-word whitespace-pre-wrap",
              )}
            />
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
  ) : null

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
        {isVersioned ? (
          <Tabs
            selectedKey={tab}
            onSelectionChange={(key) => setTab(key === "versions" ? "versions" : "details")}
            className="flex min-h-0 flex-col gap-3"
          >
            <TabList aria-label="Object">
              <Tab id="details">Details</Tab>
              {/* The count is the reason the history is fetched on open rather
                  than on click: "Versions · 1" and "Versions · 12" are
                  different invitations. */}
              <Tab id="versions">Versions{history ? ` · ${history.versions.length}` : ""}</Tab>
            </TabList>
            {/* Both panels live outside the loading/error branch on purpose: a
                delete marker has nothing to read, and the history is exactly
                where the user goes next when the details pane says so. */}
            <TabPanel id="details" className="flex min-h-0 flex-col gap-3">
              {/* Above the details rather than inside them: it has to say where
                  the user is even when there is nothing to read, which is
                  exactly the delete-marker case. */}
              {versionId !== undefined && history && (
                <ObjectRevisionBar
                  versions={history.versions}
                  selectedVersionId={versionId}
                  onSelect={showVersion}
                  onShowAll={() => setTab("versions")}
                />
              )}
              {details}
            </TabPanel>
            <TabPanel id="versions" className="flex min-h-0 flex-col">
              <ObjectVersionList
                versions={history?.versions ?? []}
                selectedVersionId={versionId}
                isTruncated={history?.isTruncated ?? false}
                loading={historyLoading}
                onSelect={showVersion}
              />
            </TabPanel>
          </Tabs>
        ) : (
          details
        )}
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
