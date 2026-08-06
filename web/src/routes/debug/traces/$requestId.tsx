import { createFileRoute } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { useState, Fragment } from "react"
import { ArrowLeft, Clock, Server, AlertCircle } from "lucide-react"
import { traceDetailQueryOptions } from "@/features/debug-traces/data"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/ui/copy-button"
import { cn } from "@/lib/utils"
import { nsToHuman, statusColor, tryFormatJSON } from "@/features/debug-traces/utils"
import { Waterfall } from "@/features/debug-traces/components/waterfall"
import type { TraceEntry, TraceHop, TraceLogEntry } from "@/types"

export const Route = createFileRoute("/debug/traces/$requestId")({
  head: ({ params }) => ({
    meta: [{ title: `Trace ${params.requestId.slice(0, 12)}… — Overcast` }],
  }),
  component: TraceDetailPage,
})

const tabs = ["Overview", "Request", "Response", "Hops", "Logs"] as const
type Tab = (typeof tabs)[number]

function TraceDetailPage() {
  const { requestId } = Route.useParams()
  const navigate = Route.useNavigate()
  const [tab, setTab] = useState<Tab>("Overview")

  const { data: trace, isLoading, error } = useQuery(traceDetailQueryOptions(requestId))

  if (isLoading) return <Spinner />
  if (error || !trace) {
    return (
      <div className="flex flex-col items-center justify-center gap-4 p-12">
        <AlertCircle className="h-12 w-12 text-fg-muted" />
        <p className="text-fg-muted text-lg">
          {error ? "Failed to load trace. Make sure OVERCAST_DEBUG=true." : "Trace not found."}
        </p>
        <Button variant="outline" onClick={() => navigate({ to: "/debug/traces" })}>
          <ArrowLeft className="h-4 w-4 mr-1" />
          Back to traces
        </Button>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex items-center gap-3">
        <Button
          variant="ghost"
          size="icon"
          onClick={() => navigate({ to: "/debug/traces" })}
        >
          <ArrowLeft className="h-5 w-5" />
        </Button>
        <PageHeader
          title={`${trace.method} ${trace.path}`}
          description={
            <span className="flex items-center gap-2">
              <span className={cn("font-mono", statusColor(trace.statusCode))}>
                {trace.statusCode}
              </span>
              <span>·</span>
              <span className="flex items-center gap-1">
                <Clock className="h-3 w-3" />
                {nsToHuman(trace.duration)}
              </span>
              <span>·</span>
              <span className="flex items-center gap-1">
                <Server className="h-3 w-3" />
                {trace.service}
                {trace.operation ? ` / ${trace.operation}` : ""}
              </span>
            </span>
          }
        />
      </div>

      {/* Tabs */}
      <div className="flex gap-1 border-b border-border">
        {tabs.map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={cn(
              "px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors",
              tab === t
                ? "border-accent text-accent"
                : "border-transparent text-fg-muted hover:text-fg",
            )}
          >
            {t}
            {t === "Hops" && trace.hops?.length ? (
              <span className="ml-1 text-xs text-fg-muted">({trace.hops.length})</span>
            ) : null}
            {t === "Logs" && trace.logEntries?.length ? (
              <span className="ml-1 text-xs text-fg-muted">({trace.logEntries.length})</span>
            ) : null}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div className="min-h-0">
        {tab === "Overview" && <OverviewTab trace={trace} />}
        {tab === "Request" && <HeadersBodyView headers={trace.requestHeaders} body={trace.requestBody} truncated={trace.requestBodyTruncated} />}
        {tab === "Response" && <HeadersBodyView headers={trace.responseHeaders} body={trace.responseBody} truncated={trace.responseBodyTruncated} streaming={trace.streaming} />}
        {tab === "Hops" && <HopsTab hops={trace.hops ?? []} />}
        {tab === "Logs" && <LogsTab entries={trace.logEntries ?? []} />}
      </div>
    </div>
  )
}

function OverviewTab({ trace }: { trace: TraceEntry }) {
  return (
    <div className="flex flex-col gap-6">
      <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
        <Field label="Request ID" value={trace.requestId} copyable />
        <Field label="Timestamp" value={new Date(trace.timestamp).toISOString()} />
        <Field label="Duration" value={nsToHuman(trace.duration)} />
        <Field label="Method" value={trace.method} />
        <Field label="Path" value={trace.path} />
        <Field label="Host" value={trace.host} />
        <Field label="Service" value={trace.service} />
        <Field label="Operation" value={trace.operation ?? "—"} />
        <Field label="Region" value={trace.region} />
        <Field label="Status" value={String(trace.statusCode)} />
        <Field label="Remote Addr" value={trace.remoteAddr ?? "—"} />
        <Field label="User Agent" value={trace.userAgent ?? "—"} />
        {trace.awsErrorCode && (
          <Field label="AWS Error" value={`${trace.awsErrorCode}: ${trace.awsErrorMessage ?? ""}`} />
        )}
        {trace.streaming && <Field label="Streaming" value="Yes" />}
      </div>
      {(trace.hops?.length ?? 0) > 0 && (
        <div>
          <h3 className="text-sm font-medium text-fg-muted mb-2">Timeline</h3>
          <div className="overflow-x-auto">
            <Waterfall
              hops={trace.hops ?? []}
              totalDuration={trace.duration}
              startTime={trace.timestamp}
            />
          </div>
        </div>
      )}
    </div>
  )
}

function Field({ label, value, copyable }: { label: string; value: string; copyable?: boolean }) {
  return (
    <div className="rounded-lg border border-border bg-bg-elevated p-3">
      <div className="text-xs text-fg-muted mb-1">{label}</div>
      <div className="text-sm font-mono truncate flex items-center gap-1">
        {value}
        {copyable && <CopyButton value={value} noun={label} />}
      </div>
    </div>
  )
}

function HeadersBodyView({
  headers,
  body,
  truncated,
  streaming,
}: {
  headers: Record<string, string[]>
  body?: string
  truncated?: boolean
  streaming?: boolean
}) {
  return (
    <div className="flex flex-col gap-4">
      {streaming && (
        <div className="rounded-md bg-amber-500/10 border border-amber-500/30 px-3 py-2 text-sm text-amber-400">
          Streaming response — body was not captured.
        </div>
      )}
      <div>
        <h3 className="text-sm font-medium text-fg-muted mb-2">Headers</h3>
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-fg-muted text-left">
                <th className="px-3 py-2 font-medium">Name</th>
                <th className="px-3 py-2 font-medium">Value</th>
              </tr>
            </thead>
            <tbody>
              {Object.entries(headers).map(([key, values]) => (
                <tr key={key} className="border-b border-border">
                  <td className="px-3 py-2 font-mono text-xs text-accent whitespace-nowrap">
                    {key}
                  </td>
                  <td className="px-3 py-2 font-mono text-xs break-all">
                    {values.join(", ")}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      {body !== undefined && (
        <div>
          <h3 className="text-sm font-medium text-fg-muted mb-2">
            Body
            {truncated && (
              <span className="ml-2 text-amber-400 text-xs">(truncated at 1 MiB)</span>
            )}
          </h3>
          <pre className="rounded-lg border border-border bg-bg-elevated p-4 text-xs font-mono overflow-x-auto max-h-96">
            {tryFormatJSON(body)}
          </pre>
        </div>
      )}
    </div>
  )
}

function HopsTab({ hops }: { hops: TraceHop[] }) {
  const [expanded, setExpanded] = useState<string | null>(null)

  if (hops.length === 0) {
    return (
      <div className="text-center text-fg-muted py-8">
        No internal service calls were made during this request.
      </div>
    )
  }

  return (
    <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-fg-muted text-left">
            <th className="px-3 py-2 font-medium w-8">#</th>
            <th className="px-3 py-2 font-medium">Caller</th>
            <th className="px-3 py-2 font-medium">Service</th>
            <th className="px-3 py-2 font-medium">Operation</th>
            <th className="px-3 py-2 font-medium">Status</th>
            <th className="px-3 py-2 font-medium">Duration</th>
          </tr>
        </thead>
        <tbody>
          {hops.map((hop) => (
            <Fragment key={hop.id}>
              <tr
                className="border-b border-border hover:bg-bg-elevated cursor-pointer transition-colors"
                onClick={() => setExpanded(expanded === hop.id ? null : hop.id)}
              >
                <td className="px-3 py-2 font-mono text-xs text-fg-muted">{hop.order}</td>
                <td className="px-3 py-2">
                  <Badge variant="outline" className="text-xs">{hop.callerService}</Badge>
                </td>
                <td className="px-3 py-2">
                  <Badge variant="outline" className="text-xs">{hop.service}</Badge>
                </td>
                <td className="px-3 py-2 font-mono text-xs">{hop.operation}</td>
                <td className={cn("px-3 py-2 font-mono text-xs", statusColor(hop.responseStatus))}>
                  {hop.responseStatus}
                </td>
                <td className="px-3 py-2 font-mono text-xs text-fg-muted">
                  {nsToHuman(hop.duration)}
                </td>
              </tr>
              {expanded === hop.id && (
                <tr key={`${hop.id}-detail`} className="bg-bg-elevated">
                  <td colSpan={6} className="px-4 py-3">
                    <div className="text-xs space-y-2">
                      {hop.targetUri && (
                        <div>
                          <span className="text-fg-muted">Target: </span>
                          <span className="font-mono">{hop.targetUri}</span>
                        </div>
                      )}
                      {hop.requestBody && (
                        <div>
                          <div className="text-fg-muted mb-1">Request Body:</div>
                          <pre className="bg-bg p-2 rounded text-xs font-mono overflow-x-auto max-h-48">
                            {tryFormatJSON(String(hop.requestBody))}
                          </pre>
                        </div>
                      )}
                      {hop.responseBody && (
                        <div>
                          <div className="text-fg-muted mb-1">Response Body:</div>
                          <pre className="bg-bg p-2 rounded text-xs font-mono overflow-x-auto max-h-48">
                            {tryFormatJSON(String(hop.responseBody))}
                          </pre>
                        </div>
                      )}
                      {hop.error && (
                        <div className="text-red-400 font-mono">{hop.error}</div>
                      )}
                    </div>
                  </td>
                </tr>
              )}
            </Fragment>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function LogsTab({ entries }: { entries: TraceLogEntry[] }) {
  const [expanded, setExpanded] = useState<number | null>(null)

  if (entries.length === 0) {
    return (
      <div className="text-center text-fg-muted py-8">
        No log entries captured for this request.
      </div>
    )
  }

  return (
    <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-fg-muted text-left">
            <th className="px-3 py-2 font-medium w-24">Level</th>
            <th className="px-3 py-2 font-medium w-36">Timestamp</th>
            <th className="px-3 py-2 font-medium">Message</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((entry, i) => (
            <Fragment key={i}>
              <tr
                className="border-b border-border hover:bg-bg-elevated cursor-pointer transition-colors"
                onClick={() => setExpanded(expanded === i ? null : i)}
              >
                <td className="px-3 py-2">
                  <Badge
                    variant="outline"
                    className={cn(
                      "text-xs",
                      entry.level === "ERROR"
                        ? "border-red-400/50 text-red-400"
                        : entry.level === "WARN"
                          ? "border-amber-400/50 text-amber-400"
                          : "border-emerald-400/50 text-emerald-400",
                    )}
                  >
                    {entry.level}
                  </Badge>
                </td>
                <td className="px-3 py-2 font-mono text-xs text-fg-muted whitespace-nowrap">
                  {new Date(entry.timestamp).toLocaleTimeString()}
                </td>
                <td className="px-3 py-2 text-xs">{entry.message}</td>
              </tr>
              {expanded === i && entry.fields && Object.keys(entry.fields).length > 0 && (
                <tr key={`${i}-detail`} className="bg-bg-elevated">
                  <td colSpan={3} className="px-4 py-3">
                    <pre className="text-xs font-mono overflow-x-auto">
                      {JSON.stringify(entry.fields, null, 2)}
                    </pre>
                  </td>
                </tr>
              )}
            </Fragment>
          ))}
        </tbody>
      </table>
    </div>
  )
}
