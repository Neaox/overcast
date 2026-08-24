/**
 * One object's history, inside the inspector.
 *
 * The bucket-level History toggle answers "what is this bucket still storing";
 * to follow one object through it you have to flip the whole listing into
 * version mode and find your key among everyone else's revisions. This answers
 * the narrower question directly — what happened to *this* object — and lets
 * the inspector move between the answers.
 *
 * A delete marker is drawn as what it is rather than hidden: a key whose latest
 * revision is a tombstone looks identical to a key that was never written
 * unless the marker is on screen, and that is the distinction the history
 * exists to show. Selecting one is allowed, and the details pane explains why
 * there is nothing to read.
 */
import { Check, ChevronDown, ChevronUp, FileX } from "lucide-react"
import type { S3ObjectVersion } from "@/types"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/primitives"
import { shortVersionId } from "@/features/s3/version-id"
import { formatBytes, formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"

interface ObjectVersionListProps {
  versions: S3ObjectVersion[]
  /**
   * The revision the inspector is showing. Undefined means the inspector was
   * opened on the key rather than on a version, which is whichever revision is
   * current — so that is the row marked.
   */
  selectedVersionId?: string
  /** More revisions exist than were read. */
  isTruncated: boolean
  loading: boolean
  onSelect: (versionId: string) => void
}

export function ObjectVersionList({
  versions,
  selectedVersionId,
  isTruncated,
  loading,
  onSelect,
}: ObjectVersionListProps) {
  if (loading) {
    return (
      <div className="flex justify-center py-8">
        <Spinner />
      </div>
    )
  }

  if (versions.length === 0) {
    return (
      <p className="rounded-lg border border-border bg-bg-muted px-3 py-2 text-sm text-fg-muted">
        This object has no stored revisions.
      </p>
    )
  }

  const shownVersionId = selectedVersionId ?? versions.find((v) => v.isLatest)?.versionId

  return (
    <div className="flex min-h-0 flex-col gap-2">
      <ul className="max-h-[55vh] min-h-0 divide-y divide-border-muted overflow-auto rounded-lg border border-border">
        {versions.map((version) => {
          const isShown = version.versionId === shownVersionId
          return (
            <li key={version.versionId}>
              <button
                type="button"
                aria-current={isShown}
                onClick={() => onSelect(version.versionId)}
                className={cn(
                  "flex w-full items-center gap-3 px-3 py-2 text-left transition-colors",
                  isShown ? "bg-accent-muted" : "hover:bg-bg-muted",
                  // A superseded revision is still real, just not the one the
                  // key resolves to — dimming it puts the current one first
                  // without hiding the rest.
                  !version.isLatest && !isShown && "text-fg-muted",
                )}
              >
                <span className="flex w-4 shrink-0 justify-center">
                  {isShown ? (
                    <Check className="h-3.5 w-3.5 text-accent" />
                  ) : version.isDeleteMarker ? (
                    <FileX className="h-3.5 w-3.5 text-fg-subtle" />
                  ) : null}
                </span>
                {/* The id is the row's identity, so it is the last thing
                    allowed to give up room: it holds a floor while the
                    timestamp — the one column a reader can do without —
                    truncates first. Without that ordering the id is the only
                    flexible column and absorbs the whole deficit, leaving a
                    list of rows that name nothing. */}
                <span
                  className="min-w-[6rem] flex-1 truncate font-mono text-xs"
                  title={version.versionId}
                >
                  {shortVersionId(version.versionId)}
                </span>
                <span className="w-16 shrink-0 text-right font-mono text-xs text-fg-muted">
                  {version.isDeleteMarker ? "—" : formatBytes(version.size)}
                </span>
                <span className="min-w-0 truncate text-xs whitespace-nowrap text-fg-muted">
                  {formatDate(version.lastModified)}
                </span>
                {/* Wide enough for the longest badge: a narrower column lets
                    "Delete marker" spill back over the timestamp. */}
                <span className="flex w-32 shrink-0 justify-end">
                  {version.isDeleteMarker ? (
                    <Badge variant="warning">Delete marker</Badge>
                  ) : version.isLatest ? (
                    <Badge variant="success">Current</Badge>
                  ) : null}
                </span>
              </button>
            </li>
          )
        })}
      </ul>
      {isTruncated && (
        // Never let a cap read as the whole picture: the oldest revisions are
        // exactly the ones missing, and they are what someone scrolling to the
        // bottom of a history came for.
        <p className="text-xs text-fg-muted">
          Showing the {versions.length} most recent revisions — this object has more.
        </p>
      )}
    </div>
  )
}

/**
 * Where in the history the inspector currently is, and how to move.
 *
 * Shown once the user has drilled into a specific revision, because that is
 * when "which one am I looking at" stops being obvious — the details pane
 * otherwise looks the same whichever revision it describes. The steppers save
 * the trip back to the list for the common case of reading two or three
 * revisions in a row.
 *
 * Shares the list's ordering: newest first, so "up" is newer.
 */
export function ObjectRevisionBar({
  versions,
  selectedVersionId,
  onSelect,
  onShowAll,
}: {
  versions: S3ObjectVersion[]
  selectedVersionId: string
  onSelect: (versionId: string) => void
  onShowAll: () => void
}) {
  const index = versions.findIndex((v) => v.versionId === selectedVersionId)
  // A revision the listing does not contain — deleted since, or beyond the cap
  // — has no position to report and nothing to step through.
  if (index === -1) return null

  const newer = versions[index - 1]
  const older = versions[index + 1]

  return (
    <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-bg-muted px-3 py-1.5 text-xs">
      <span className="min-w-0 truncate text-fg-muted">
        <span className="font-medium text-fg">
          {index === 0 ? "Current revision" : "Older revision"}
        </span>{" "}
        · {versions.length - index} of {versions.length}
      </span>
      <span className="flex shrink-0 items-center gap-0.5">
        <Button
          variant="ghost"
          size="icon-sm"
          title="Newer revision"
          disabled={!newer}
          onClick={() => newer && onSelect(newer.versionId)}
        >
          <ChevronUp className="h-3.5 w-3.5" />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          title="Older revision"
          disabled={!older}
          onClick={() => older && onSelect(older.versionId)}
        >
          <ChevronDown className="h-3.5 w-3.5" />
        </Button>
        <Button variant="ghost" size="sm" onClick={onShowAll}>
          All {versions.length}
        </Button>
      </span>
    </div>
  )
}
