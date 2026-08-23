/**
 * AdvisoriesList — renders the server-computed `advisories` array from
 * GET /_overcast/debug/metrics (see internal/router/advisories.go) generically: an
 * icon/color driven only by `severity`, then title/detail/optional docs
 * link. A future advisory rule added server-side needs zero changes here —
 * this component never branches on `code`.
 */
import type { ReactNode } from "react"
import { AlertTriangle, CheckCircle2, Info, ShieldAlert, ExternalLink } from "lucide-react"
import { Link } from "@tanstack/react-router"
import type { Advisory, AdvisorySeverity } from "@/types"
import { Skeleton } from "@/components/ui/skeleton"
import { sectionLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

type SeverityStyle = { icon: typeof Info; iconClass: string; badgeClass: string; label: string }

// DEFAULT_STYLE ("info" look) is the fallback for a severity value the
// frontend doesn't recognize yet — e.g. a new level added server-side before
// the UI catches up. Keeping it outside SEVERITY_STYLES (rather than reusing
// SEVERITY_STYLES.info) means the fallback is a plain SeverityStyle, not
// SeverityStyle | undefined, so the lookup below can never end up undefined.
const DEFAULT_STYLE: SeverityStyle = {
  icon: Info,
  iconClass: "text-accent",
  badgeClass: "bg-accent/15 text-accent",
  label: "Info",
}

// Partial (not Record<AdvisorySeverity, ...>): indexing with a value that
// ultimately comes off the wire as a plain string must be allowed to miss,
// falling through to DEFAULT_STYLE below.
const SEVERITY_STYLES: Partial<Record<AdvisorySeverity, SeverityStyle>> = {
  critical: {
    icon: ShieldAlert,
    iconClass: "text-danger",
    badgeClass: "bg-danger-muted text-danger",
    label: "Critical",
  },
  warning: {
    icon: AlertTriangle,
    iconClass: "text-warning",
    badgeClass: "bg-warning/15 text-warning",
    label: "Warning",
  },
  info: DEFAULT_STYLE,
}

function AdvisoryCard({ advisory }: { advisory: Advisory }) {
  const style = SEVERITY_STYLES[advisory.severity] ?? DEFAULT_STYLE
  const Icon = style.icon
  return (
    <div className="flex gap-3 rounded-lg border border-border bg-bg-elevated p-4">
      <Icon className={cn("mt-0.5 h-4 w-4 shrink-0", style.iconClass)} />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <p className="text-sm font-medium text-fg">{advisory.title}</p>
          <span
            className={cn(
              "rounded-control px-1.5 py-0.5 font-mono text-[10px] tracking-[0.12em] uppercase",
              style.badgeClass,
            )}
          >
            {style.label}
          </span>
        </div>
        <p className="text-sm text-fg-muted">{advisory.detail}</p>
        {advisory.docsPath && (
          <Link
            to="/docs"
            // docsPath may carry a section anchor ("performance.md#data-dir-...");
            // the search param wants only the file path, the anchor rides the hash.
            search={{ path: advisory.docsPath.split("#")[0] }}
            hash={advisory.docsPath.split("#")[1]}
            className="mt-1 inline-flex w-fit items-center gap-1 text-xs text-accent underline underline-offset-2"
          >
            View docs
            <ExternalLink className="h-3 w-3" />
          </Link>
        )}
      </div>
    </div>
  )
}

/**
 * The heading, plus a one-line note on the same row. Nothing-to-report is the
 * normal state of this section — an emulator with no advisories, or the far
 * more common one where OVERCAST_DEBUG is off so there are none to fetch — and
 * a full-height empty state for it pushed the sections around it down a
 * screenful to say nothing. The note stays on the heading's row instead.
 */
function AdvisoriesNote({
  icon: Icon,
  iconClass,
  children,
}: {
  icon: typeof Info
  iconClass: string
  children: ReactNode
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
      <h2 className={cn(sectionLabel, "shrink-0 text-fg-muted")}>Advisories</h2>
      <p className="flex items-start gap-1.5 text-xs text-fg-muted">
        <Icon className={cn("mt-px h-3.5 w-3.5 shrink-0", iconClass)} />
        <span>{children}</span>
      </p>
    </div>
  )
}

export function AdvisoriesList({
  advisories,
  isLoading,
  error,
}: {
  advisories: Advisory[] | undefined
  isLoading: boolean
  /** Why there are no advisories to show — already human-readable (see DebugMetricsResult). */
  error?: string
}) {
  const list = advisories ?? []

  if (isLoading) {
    return (
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h2 className={cn(sectionLabel, "shrink-0 text-fg-muted")}>Advisories</h2>
        <Skeleton className="h-3 w-56" depth="1" />
      </div>
    )
  }

  if (error) {
    return (
      <AdvisoriesNote icon={Info} iconClass="text-fg-subtle">
        {error}
      </AdvisoriesNote>
    )
  }

  if (list.length === 0) {
    return (
      <AdvisoriesNote icon={CheckCircle2} iconClass="text-success">
        No recommendations — no storage or configuration issues detected.
      </AdvisoriesNote>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <h2 className={cn(sectionLabel, "text-fg-muted")}>Advisories</h2>
      <div className="flex flex-col gap-2">
        {list.map((advisory) => (
          <AdvisoryCard key={advisory.code} advisory={advisory} />
        ))}
      </div>
    </div>
  )
}
