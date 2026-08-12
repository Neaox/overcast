import { FileText, ScrollText, AlertTriangle } from "lucide-react"
import { cn } from "@/lib/utils"
import type { TraceMatch, TraceMatchField } from "@/types"

/**
 * What each kind of match is called in the row's badge, and which icon carries
 * it. The point of the badge is that a deep match is a different claim from a
 * path match — this trace *says* the thing, somewhere inside it — and the
 * reader should not have to work out where from the excerpt alone.
 */
const FIELD_LABELS: Record<TraceMatchField, string> = {
  log: "log",
  hopError: "hop error",
  hopResponse: "hop response",
  hopRequest: "hop request",
  requestBody: "request body",
  responseBody: "response body",
}

function FieldIcon({ field }: { field: TraceMatchField }) {
  if (field === "log") return <ScrollText className="h-3 w-3" />
  if (field === "hopError") return <AlertTriangle className="h-3 w-3" />
  return <FileText className="h-3 w-3" />
}

/**
 * The badge naming where a match was found, for the trace's row.
 */
export function MatchBadge({ match }: { match: TraceMatch }) {
  return (
    <span
      className="inline-flex items-center gap-1 rounded border border-border px-1.5 py-0.5 text-[10px] text-fg-muted"
      title={match.label}
    >
      <FieldIcon field={match.field} />
      {FIELD_LABELS[match.field]}
      {match.hopId ? ` · ${match.hopId}` : ""}
    </span>
  )
}

/**
 * The line under a matched row: what the match was found in, and the text
 * around it with the matched span highlighted.
 *
 * The excerpt arrives already split into before/match/after, so the highlight
 * is three spans rather than a second search through the text — which would
 * have to guess which occurrence the server meant, and would guess wrong for
 * any query appearing more than once.
 *
 * A match in a body that is not text says so instead. Rendering CBOR or a
 * gzipped payload as a string produces mojibake that reads as corruption in the
 * payload rather than as an artefact of the search.
 */
export function MatchExcerpt({ match, className }: { match: TraceMatch; className?: string }) {
  return (
    <div className={cn("flex flex-col gap-0.5 pl-3 border-l-2 border-border", className)}>
      <div className="text-[11px] text-fg-muted">{match.label}</div>
      {match.binary ? (
        <div className="font-mono text-xs text-fg-muted italic">
          matched inside a non-text body, at byte {match.offset ?? 0}
        </div>
      ) : (
        <div className="font-mono text-xs break-all">
          <span className="text-fg-muted">{match.before}</span>
          <mark className="bg-accent/25 text-fg rounded-[2px] px-0.5">{match.text}</mark>
          <span className="text-fg-muted">{match.after}</span>
        </div>
      )}
    </div>
  )
}
