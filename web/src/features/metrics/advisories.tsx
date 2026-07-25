/**
 * AdvisoriesList — renders the server-computed `advisories` array from
 * GET /_debug/metrics (see internal/router/advisories.go) generically: an
 * icon/color driven only by `severity`, then title/detail/optional docs
 * link. A future advisory rule added server-side needs zero changes here —
 * this component never branches on `code`.
 */
import { AlertTriangle, CheckCircle2, Info, ShieldAlert, ExternalLink } from "lucide-react"
import { Link } from "@tanstack/react-router"
import type { Advisory, AdvisorySeverity } from "@/types"
import { EmptyState, QueryListState } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"

type SeverityStyle = { icon: typeof Info; iconClass: string; badgeClass: string; label: string }

// DEFAULT_STYLE ("info" look) is the fallback for a severity value the
// frontend doesn't recognize yet — e.g. a new level added server-side before
// the UI catches up. Keeping it outside SEVERITY_STYLES (rather than reusing
// SEVERITY_STYLES.info) means the fallback is a plain SeverityStyle, not
// SeverityStyle | undefined, so the lookup below can never end up undefined.
const DEFAULT_STYLE: SeverityStyle = {
  icon: Info,
  iconClass: "text-blue-400",
  badgeClass: "bg-blue-500/15 text-blue-400",
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
    <div className="bg-bg-card flex gap-3 rounded-lg border border-border p-4">
      <Icon className={cn("mt-0.5 h-4 w-4 shrink-0", style.iconClass)} />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <p className="text-sm font-medium text-fg">{advisory.title}</p>
          <span
            className={cn(
              "rounded-full px-2 py-0.5 text-[10px] font-medium tracking-wide uppercase",
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

export function AdvisoriesList({
  advisories,
  isLoading,
  error,
}: {
  advisories: Advisory[] | undefined
  isLoading: boolean
  error?: unknown
}) {
  const list = advisories ?? []
  return (
    <div className="flex flex-col gap-3">
      <h2 className="text-sm font-medium tracking-wide text-fg-muted uppercase">Advisories</h2>
      <QueryListState
        isLoading={isLoading}
        isEmpty={list.length === 0}
        error={error}
        loadingClassName="py-10"
        errorTitle="Storage advisories unavailable"
        empty={
          <EmptyState
            icon={<CheckCircle2 className="h-8 w-8" />}
            title="No recommendations"
            description="No storage or configuration issues detected."
            className="py-10"
          />
        }
      />
      {!isLoading && list.length > 0 && (
        <div className="flex flex-col gap-2">
          {list.map((advisory) => (
            <AdvisoryCard key={advisory.code} advisory={advisory} />
          ))}
        </div>
      )}
    </div>
  )
}
