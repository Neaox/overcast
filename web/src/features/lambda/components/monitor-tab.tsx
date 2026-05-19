import { Link } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { LogViewer } from "@/components/logs/log-viewer"
import { Spinner } from "@/components/ui/primitives"
import { logsFilterQueryOptions } from "@/features/cloudwatch/logs/data"
import type { FilteredLogEvent, LambdaFunction } from "@/types"

export function MonitorTab({ fn }: { fn: LambdaFunction }) {
  const logGroup = fn.LoggingConfig?.LogGroup ?? ""

  const { data, isLoading, isError } = useQuery(logsFilterQueryOptions(logGroup))

  const events: FilteredLogEvent[] = data?.events.slice(-100) ?? []

  return (
    <div className="flex flex-col gap-4">
      {/* Log group link */}
      <div className="flex items-center gap-2 text-sm">
        <span className="text-fg-muted">Log group:</span>
        {logGroup ? (
          <Link
            to="/cloudwatch/logs/group"
            search={{ groupName: logGroup }}
            className="font-mono text-xs text-accent hover:underline"
          >
            {logGroup}
          </Link>
        ) : (
          <span className="font-mono text-xs text-fg-muted">—</span>
        )}
      </div>

      {/* Log viewer */}
      {!logGroup ? (
        <p className="text-sm text-fg-muted">No log group configured for this function.</p>
      ) : isLoading ? (
        <div className="flex items-center justify-center py-16">
          <Spinner className="h-5 w-5" />
        </div>
      ) : isError || events.length === 0 ? (
        <div className="flex flex-col items-center gap-2 rounded-md border border-border bg-bg-elevated py-16 text-center">
          <p className="text-sm text-fg-muted">No log events yet.</p>
          <p className="text-xs text-fg-muted">Invoke the function to generate logs.</p>
        </div>
      ) : (
        <div className="max-h-[60vh] rounded-md border border-border bg-bg-elevated p-3">
          <LogViewer
            events={events}
            loading={false}
            emptyMessage="No log events yet."
            defaultMode="plain"
          />
        </div>
      )}

      {events.length > 0 && (
        <p className="text-right text-xs text-fg-muted">
          Showing last {events.length} event{events.length !== 1 ? "s" : ""} · auto-refreshes every
          5 s
        </p>
      )}
    </div>
  )
}
