import { createFileRoute } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { useState, Fragment, useMemo } from "react"
import { ArrowLeft, Clock, Server, AlertCircle, Loader2, ChevronDown, ChevronRight, Terminal, Check } from "lucide-react"
import { traceDetailQueryOptions, debugTraceKeys } from "@/features/debug-traces/data"
import { debugTrace } from "@/services/api/misc"
import { Spinner } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/ui/copy-button"
import { cn } from "@/lib/utils"
import { useCopyToClipboard } from "@/hooks/use-clipboard"
import { Link as RouterLink } from "@tanstack/react-router"
import type { useNavigate } from "@tanstack/react-router"
import { nsToHuman, statusColor, statusMessage, formatTimestamp, traceToCurl } from "@/features/debug-traces/utils"
import { formatBodyForDisplay, bodyHintFromHeaders, formatStackTrace } from "@/lib/format-body"
import { Waterfall } from "@/features/debug-traces/components/waterfall"
import { SequenceDiagram } from "@/features/debug-traces/components/sequence-diagram"
import { FlowMap } from "@/features/debug-traces/components/flow-map"
import type { TraceEntry, TraceHop, TraceLogEntry, TraceSummary } from "@/types"

export const Route = createFileRoute("/debug/traces/$requestId")({
  head: ({ params }) => ({
    meta: [{ title: `Trace ${params.requestId.slice(0, 12)}… — Overcast` }],
  }),
  component: function TraceDetailRoute() {
    const { requestId } = Route.useParams()
    return <TraceDetailPage key={requestId} />
  },
})

const tabs = ["Overview", "Hops", "Logs", "Errors"] as const
type Tab = (typeof tabs)[number]
type HopView = "sequence" | "waterfall" | "flow"

function TraceDetailPage() {
  const { requestId } = Route.useParams()
  const navigate = Route.useNavigate()
  const [tab, setTab] = useState<Tab>("Overview")

  const { data: trace, isLoading, error } = useQuery({
    ...traceDetailQueryOptions(requestId),
    refetchInterval: (query) => {
      const t = query.state.data
      if (!t) return false
      return t.statusCode === 0 || t.duration === 0 ? 1000 : false
    },
  })

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

  const inFlight = trace.statusCode === 0 || trace.duration === 0

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
        <div className="flex flex-col gap-0.5">
          <h2 className="text-lg font-semibold">{trace.method} {trace.path}</h2>
          <span className="flex items-center gap-2 text-sm text-fg-muted">
            {inFlight ? (
              <span className="flex items-center gap-1 text-amber-400">
                <Loader2 className="h-3 w-3 animate-spin" />
                Processing…
              </span>
            ) : (
              <span className={cn("font-mono", statusColor(trace.statusCode))}>
                {trace.statusCode}
              </span>
            )}
            <span>·</span>
            <span className="flex items-center gap-1">
              <Clock className="h-3 w-3" />
              {trace.duration > 0 ? nsToHuman(trace.duration) : "…"}
            </span>
            <span>·</span>
            <span className="flex items-center gap-1">
              <Server className="h-3 w-3" />
              {trace.service}
              {trace.operation ? ` / ${trace.operation}` : ""}
            </span>
          </span>
        </div>
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
        {tab === "Hops" && <HopsTab hops={trace.hops ?? []} requestId={requestId} navigate={navigate} />}
        {tab === "Logs" && <LogsTab entries={trace.logEntries ?? []} />}
        {tab === "Errors" && <ErrorsTab trace={trace} />}
      </div>
    </div>
  )
}

function OverviewTab({ trace }: { trace: TraceEntry }) {
  const [hopView, setHopView] = useState<HopView>("sequence")
  const [selectedHopId, setSelectedHopId] = useState<string | null>(null)

  const hops = trace.hops ?? []
  const hasHops = hops.length > 0
  const inFlight = trace.statusCode === 0 || trace.duration === 0
  const reqHint = bodyHintFromHeaders(trace.requestHeaders)
  const resHint = bodyHintFromHeaders(trace.responseHeaders)

  return (
    <div className="flex flex-col gap-6">
      {/* Summary header */}
      <div className="flex flex-wrap items-center gap-3 rounded-lg border border-border bg-bg-elevated p-4">
        <div className="flex items-center gap-2">
          <span className="font-mono font-semibold">
            {trace.method} {trace.path}
          </span>
        </div>
        <div className="flex items-center gap-2">
          {inFlight ? (
            <span className="flex items-center gap-1 text-sm text-amber-400">
              <Loader2 className="h-3 w-3 animate-spin" />
              Processing…
            </span>
          ) : (
            <span className={cn("font-mono text-sm font-medium", statusColor(trace.statusCode))}>
              {trace.statusCode}
              {statusMessage(trace.statusCode) && ` (${statusMessage(trace.statusCode)})`}
            </span>
          )}
          <span className="text-fg-muted text-sm">·</span>
          <span className="flex items-center gap-1 text-sm text-fg-muted">
            <Clock className="h-3.5 w-3.5" />
            {trace.duration > 0 ? nsToHuman(trace.duration) : "…"}
          </span>
          <span className="text-fg-muted text-sm">·</span>
          <span className="flex items-center gap-1 text-sm text-fg-muted">
            <Server className="h-3.5 w-3.5" />
            {trace.service}
            {trace.operation ? ` / ${trace.operation}` : ""}
          </span>
        </div>
        <div className="ml-auto">
          <CopyButton value={traceToCurl(trace)} noun="curl command" />
        </div>
      </div>

      {/* Metadata grid */}
      <div className="grid grid-cols-2 gap-4">
        <Field label="Request ID" value={trace.requestId} />
        <Field label="Timestamp" value={formatTimestamp(trace.timestamp)} />
        <Field label="Duration" value={trace.duration > 0 ? nsToHuman(trace.duration) : "…"} />
        <Field label="Method" value={trace.method} />
        <Field label="Path" value={trace.path} />
        <Field label="Host" value={trace.host} />
        <Field label="Service" value={trace.service} />
        <Field label="Operation" value={trace.operation ?? "—"} />
        <Field label="Region" value={trace.region} />
        <Field
          label="Status"
          value={
            inFlight
              ? "…"
              : statusMessage(trace.statusCode)
                ? `${trace.statusCode} (${statusMessage(trace.statusCode)})`
                : String(trace.statusCode)
          }
          className={cn(!inFlight && statusColor(trace.statusCode))}
        />
        <Field label="Remote Addr" value={trace.remoteAddr ?? "—"} />
        <Field label="User Agent" value={trace.userAgent ?? "—"} />
        {trace.referer && <Field label="Referer" value={trace.referer} />}
        {trace.awsErrorCode && (
          <Field label="AWS Error" value={`${trace.awsErrorCode}: ${trace.awsErrorMessage ?? ""}`} />
        )}
        {trace.streaming && <Field label="Streaming" value="Yes" />}
      </div>

      {trace.parentRequestId && (
        <div className="text-sm flex items-center gap-1">
          <span className="text-fg-muted">Called by</span>
          <RouterLink to="/debug/traces/$requestId" params={{ requestId: trace.parentRequestId }} className="font-mono text-accent hover:underline text-xs">{trace.parentRequestId}</RouterLink>
        </div>
      )}

      {/* Side-by-side Request / Response */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <h3 className="text-sm font-medium text-fg-muted mb-2">Request</h3>
          <HeadersBodyView
            headers={trace.requestHeaders}
            body={trace.requestBody}
            truncated={trace.requestBodyTruncated}
            hint={reqHint}
          />
        </div>
        <div>
          <h3 className="text-sm font-medium text-fg-muted mb-2">Response</h3>
          <HeadersBodyView
            headers={trace.responseHeaders}
            body={trace.responseBody}
            truncated={trace.responseBodyTruncated}
            streaming={trace.streaming}
            hint={resHint}
          />
        </div>
      </div>

      {/* Call graph */}
      {hasHops && (
        <div>
          <div className="flex items-center gap-2 mb-2">
            <h3 className="text-sm font-medium text-fg-muted">Call Graph</h3>
            <div className="flex gap-0.5 ml-auto">
              {(["sequence", "waterfall", "flow"] as HopView[]).map((v) => (
                <button
                  key={v}
                  onClick={() => setHopView(v)}
                  className={cn(
                    "px-2 py-1 text-xs rounded border transition-colors",
                    hopView === v
                      ? "border-accent text-accent bg-accent/10"
                      : "border-border text-fg-muted hover:text-fg",
                  )}
                >
                  {v === "sequence" ? "Sequence" : v === "waterfall" ? "Waterfall" : "Flow"}
                </button>
              ))}
            </div>
          </div>
          <div className="overflow-x-auto">
            {hopView === "sequence" && (
              <SequenceDiagram
                hops={hops}
                onSelectHop={setSelectedHopId}
                selectedHopId={selectedHopId}
              />
            )}
            {hopView === "waterfall" && (
              <Waterfall
                hops={hops}
                totalDuration={trace.duration}
                startTime={trace.timestamp}
                onSelectHop={setSelectedHopId}
                selectedHopId={selectedHopId}
              />
            )}
            {hopView === "flow" && (
              <FlowMap trace={trace} onSelectHop={setSelectedHopId} selectedHopId={selectedHopId} />
            )}
          </div>
        </div>
      )}
      {trace.stack && (
        <details className="mt-2">
          <summary className="text-sm font-medium text-fg-muted cursor-pointer hover:text-fg">Stack trace</summary>
          <pre className="bg-bg rounded-lg border border-border p-4 text-xs font-mono overflow-x-auto max-h-64 mt-2 whitespace-pre-wrap" dangerouslySetInnerHTML={{ __html: formatStackTrace(trace.stack) }} />
        </details>
      )}
      {selectedHopId && (
        <HopDetailPanel hopId={selectedHopId} hops={hops} onClose={() => setSelectedHopId(null)} />
      )}
    </div>
  )
}

function HopDetailPanel({ hopId, hops, onClose }: { hopId: string; hops: TraceHop[]; onClose: () => void }) {
  const hop = hops.find((h) => h.id === hopId)
  const { copy, copied } = useCopyToClipboard()
  if (!hop) return null

  return (
    <div className="rounded-lg border border-accent/40 bg-bg-elevated p-4 mt-3">
      <div className="flex items-center justify-between mb-3">
        <h4 className="text-sm font-medium text-accent">
          {hop.callerService} → {hop.service}.{hop.operation}
        </h4>
        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="icon-sm"
            title="Copy as curl"
            aria-label="Copy hop as curl"
            onClick={() => copy(hopCurl(hop), { noun: "hop curl" })}
          >
            {copied ? <Check className="h-3.5 w-3.5 text-success" /> : <Terminal className="h-3.5 w-3.5" />}
          </Button>
          <button onClick={onClose} className="text-xs text-fg-muted hover:text-fg">✕</button>
        </div>
      </div>
      <div className="grid grid-cols-3 gap-3 text-xs mb-3">
        <div>
          <span className="text-fg-muted">Status: </span>
          <span className={cn("font-mono", statusColor(hop.responseStatus))}>
            {hop.responseStatus}
            {statusMessage(hop.responseStatus) && ` (${statusMessage(hop.responseStatus)})`}
          </span>
        </div>
        <div><span className="text-fg-muted">Duration: </span><span className="font-mono">{nsToHuman(hop.duration)}</span></div>
        {hop.targetUri && <div className="col-span-3"><span className="text-fg-muted">Target: </span><span className="font-mono">{hop.targetUri}</span></div>}
        {hop.error && <div className="col-span-3 text-red-400 font-mono">{hop.error}</div>}
      </div>
      {hop.requestBody && (
        <div className="mb-2">
          <div className="text-xs text-fg-muted mb-1">Request Body</div>
          <HopBody raw={String(hop.requestBody)} headers={hop.requestHeaders} />
        </div>
      )}
      {hop.responseBody && (
        <div>
          <div className="text-xs text-fg-muted mb-1">Response Body</div>
          <HopBody raw={String(hop.responseBody)} headers={hop.requestHeaders} />
        </div>
      )}
    </div>
  )
}

function hopCurl(hop: TraceHop): string {
  if (!hop.targetUri) return ""
  try {
    const url = new URL(hop.targetUri)
    const lines: string[] = ["curl"]
    if (hop.requestHeaders) {
      for (const [k, vs] of Object.entries(hop.requestHeaders)) {
        const lk = k.toLowerCase()
        if (lk === "host" || lk === "content-length" || lk === "connection") continue
        for (const v of vs) lines.push(`  -H '${k}: ${v}'`)
      }
    }
    if (hop.requestBody) lines.push(`  -d '${hop.requestBody.replace(/'/g, "\\'")}'`)
    lines.push(`  '${url}'`)
    return lines.join(" \\\n")
  } catch {
    return ""
  }
}

function Field({ label, value, className }: { label: string; value: string; className?: string }) {
  return (
    <div className="rounded-lg border border-border bg-bg-elevated p-3">
      <div className="text-xs text-fg-muted mb-1">{label}</div>
      <div className="flex items-center gap-1">
        <span className={cn("text-sm font-mono truncate", className)}>{value}</span>
        <CopyButton value={value} noun={label} />
      </div>
    </div>
  )
}

function HeadersBodyView({
  headers,
  body,
  truncated,
  streaming,
  hint,
}: {
  headers: Record<string, string[]>
  body?: string
  truncated?: boolean
  streaming?: boolean
  hint: "json" | "xml" | "text"
}) {
  const ct = (headers["Content-Type"] ?? headers["content-type"] ?? [""])[0]
  const formatted = body !== undefined ? formatBodyForDisplay(body, hint, ct) : null
  return (
    <div className="flex flex-col gap-3">
      {streaming && (
        <div className="rounded-md bg-amber-500/10 border border-amber-500/30 px-3 py-2 text-sm text-amber-400">
          Streaming response — body was not captured.
        </div>
      )}
      <div>
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-fg-muted text-left">
                <th className="px-3 py-2 font-medium text-xs">Name</th>
                <th className="px-3 py-2 font-medium text-xs">Value</th>
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
          <div className="text-xs text-fg-muted mb-1">
            Body
            {truncated && (
              <span className="ml-2 text-amber-400">(truncated at 1 MiB)</span>
            )}
          </div>
          {formatted?.html ? (
            <pre className="rounded-lg border border-border bg-bg-elevated p-4 text-xs font-mono overflow-x-auto max-h-96" dangerouslySetInnerHTML={{ __html: formatted.html }} />
          ) : (
            <pre className="rounded-lg border border-border bg-bg-elevated p-4 text-xs font-mono overflow-x-auto max-h-96">{body}</pre>
          )}
        </div>
      )}
    </div>
  )
}

function HopsTab({ hops, requestId, navigate }: { hops: TraceHop[]; requestId: string; navigate: ReturnType<typeof useNavigate> }) {
  const [expanded, setExpanded] = useState<string | null>(null)
  const { copy, copied } = useCopyToClipboard()

  const { data: upstream } = useQuery({
    queryKey: [...debugTraceKeys.list(), "hopsFor", requestId],
    queryFn: () => debugTrace.list({ hopsFor: requestId, limit: 50 }),
  })
  const upstreamTraces = useMemo(() => (upstream as unknown as { traces?: TraceSummary[] })?.traces ?? [], [upstream])

  if (hops.length === 0 && upstreamTraces.length === 0) {
    return (
      <div className="text-center text-fg-muted py-8">
        No service calls recorded for this request.
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      {upstreamTraces.length > 0 && (
        <div>
          <h3 className="text-sm font-medium text-emerald-400 mb-2">↑ Called by ({upstreamTraces.length})</h3>
          <div className="overflow-x-auto rounded-lg border border-border">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border text-fg-muted text-left">
                  <th className="px-3 py-2 font-medium">Service</th>
                  <th className="px-3 py-2 font-medium">Operation</th>
                  <th className="px-3 py-2 font-medium">Status</th>
                  <th className="px-3 py-2 font-medium">Duration</th>
                  <th className="px-3 py-2 font-medium">Request ID</th>
                </tr>
              </thead>
              <tbody>
                {upstreamTraces.map((t) => (
                  <tr key={t.requestId} className="border-b border-border hover:bg-bg-elevated cursor-pointer" onClick={() => navigate({ to: "/debug/traces/$requestId", params: { requestId: t.requestId } })}>
                    <td className="px-3 py-2"><Badge variant="outline" className="text-xs">{t.service}</Badge></td>
                    <td className="px-3 py-2 font-mono text-xs">{t.operation ?? "—"}</td>
                    <td className={cn("px-3 py-2 font-mono text-xs", statusColor(t.statusCode))}>{t.statusCode > 0 ? `${t.statusCode}${statusMessage(t.statusCode) ? ` (${statusMessage(t.statusCode)})` : ""}` : "—"}</td>
                    <td className="px-3 py-2 font-mono text-xs text-fg-muted">{nsToHuman(t.duration)}</td>
                    <td className="px-3 py-2">
                      <RouterLink to="/debug/traces/$requestId" params={{ requestId: t.requestId }} className="font-mono text-xs text-accent hover:underline" onClick={(e) => e.stopPropagation()}>{t.requestId.slice(0, 12)}…</RouterLink>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {hops.length > 0 && (
        <div>
          <h3 className="text-sm font-medium text-amber-400 mb-2">↓ Called ({hops.length})</h3>
          <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-fg-muted text-left">
            <th className="px-3 py-2 font-medium w-6" />
            <th className="px-3 py-2 font-medium w-8">#</th>
            <th className="px-3 py-2 font-medium">Caller</th>
            <th className="px-3 py-2 font-medium">Service</th>
              <th className="px-3 py-2 font-medium">Operation</th>
              <th className="px-3 py-2 font-medium">Request ID</th>
              <th className="px-3 py-2 font-medium">Status</th>
            <th className="px-3 py-2 font-medium">Duration</th>
          </tr>
        </thead>
        <tbody>
          {hops.map((hop) => {
            const isExpanded = expanded === hop.id
            return (
              <Fragment key={hop.id}>
                <tr
                  className="border-b border-border hover:bg-bg-elevated cursor-pointer transition-colors"
                  onClick={() => setExpanded(isExpanded ? null : hop.id)}
                >
                  <td className="px-3 py-2 text-fg-muted">
                    {isExpanded ? (
                      <ChevronDown className="h-3.5 w-3.5" />
                    ) : (
                      <ChevronRight className="h-3.5 w-3.5" />
                    )}
                  </td>
                  <td className="px-3 py-2 font-mono text-xs text-fg-muted">{hop.order}</td>
                  <td className="px-3 py-2">
                    <Badge variant="outline" className="text-xs">{hop.callerService}</Badge>
                  </td>
                  <td className="px-3 py-2">
                    <Badge variant="outline" className="text-xs">{hop.service}</Badge>
                  </td>
                  <td className="px-3 py-2 font-mono text-xs">{hop.operation}</td>
                  <td className="px-3 py-2">
                    {hop.requestId ? (
                      <RouterLink to="/debug/traces/$requestId" params={{ requestId: hop.requestId }} className="font-mono text-xs text-accent hover:underline" onClick={(e) => e.stopPropagation()}>
                        {hop.requestId.slice(0, 12)}…
                      </RouterLink>
                    ) : (
                      <span className="text-fg-muted text-xs">—</span>
                    )}
                  </td>
                  <td className={cn("px-3 py-2 font-mono text-xs", statusColor(hop.responseStatus))}>
                    {hop.responseStatus}
                    {statusMessage(hop.responseStatus) && ` (${statusMessage(hop.responseStatus)})`}
                  </td>
                  <td className="px-3 py-2 font-mono text-xs text-fg-muted">
                    {nsToHuman(hop.duration)}
                  </td>
                </tr>
                {isExpanded && (
                  <tr key={`${hop.id}-detail`} className="bg-bg-elevated">
                    <td colSpan={8} className="px-4 py-3">
                      <div className="text-xs space-y-2">
                        {hop.requestId && (
                          <div>
                            <span className="text-fg-muted">Request ID: </span>
                            <RouterLink to="/debug/traces/$requestId" params={{ requestId: hop.requestId }} className="font-mono text-accent hover:underline">{hop.requestId}</RouterLink>
                          </div>
                        )}
                        {hop.targetUri && (
                          <div className="flex items-center gap-2">
                            <span className="text-fg-muted">Target: </span>
                            <span className="font-mono">{hop.targetUri}</span>
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              title="Copy as curl"
                              aria-label="Copy hop as curl"
                              onClick={() => copy(hopCurl(hop), { noun: "hop curl" })}
                            >
                              {copied ? <Check className="h-3.5 w-3.5 text-success" /> : <Terminal className="h-3.5 w-3.5" />}
                            </Button>
                          </div>
                        )}
                        {hop.requestBody && (
                          <div>
                            <div className="text-fg-muted mb-1">Request Body:</div>
          <HopBody raw={String(hop.requestBody)} headers={hop.requestHeaders} />
                          </div>
                        )}
                        {hop.responseBody && (
                          <div>
                            <div className="text-fg-muted mb-1">Response Body:</div>
                            <HopBody raw={String(hop.responseBody)} headers={hop.requestHeaders} />
                          </div>
                        )}
                        {hop.error && (
                          <div className="text-red-400 font-mono">{hop.error}</div>
                        )}
                        {hop.stack && (
                          <details className="mt-2">
                            <summary className="text-xs text-fg-muted cursor-pointer hover:text-fg">Stack trace</summary>
                            <pre className="bg-bg p-2 rounded text-xs font-mono overflow-x-auto max-h-48 mt-1 whitespace-pre-wrap" dangerouslySetInnerHTML={{ __html: formatStackTrace(hop.stack) }} />
                          </details>
                        )}
                      </div>
                    </td>
                  </tr>
                )}
              </Fragment>
            )
          })}
        </tbody>
      </table>
    </div>
      </div>
      )}
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
            <th className="px-3 py-2 font-medium w-6" />
            <th className="px-3 py-2 font-medium w-24">Level</th>
            <th className="px-3 py-2 font-medium w-44">Timestamp</th>
            <th className="px-3 py-2 font-medium">Message</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((entry, i) => {
            const isExpanded = expanded === i
            return (
              <Fragment key={`${entry.timestamp}-${entry.level}-${i}`}>
                <tr
                  className="border-b border-border hover:bg-bg-elevated cursor-pointer transition-colors"
                  onClick={() => setExpanded(isExpanded ? null : i)}
                >
                  <td className="px-3 py-2 text-fg-muted">
                    {isExpanded ? (
                      <ChevronDown className="h-3.5 w-3.5" />
                    ) : (
                      <ChevronRight className="h-3.5 w-3.5" />
                    )}
                  </td>
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
                    {formatTimestamp(entry.timestamp)}
                  </td>
                  <td className="px-3 py-2 text-xs">{entry.message}</td>
                </tr>
                {isExpanded && entry.fields && Object.keys(entry.fields).length > 0 && (
                  <tr key={`${i}-detail`} className="bg-bg-elevated">
                    <td colSpan={4} className="px-4 py-3">
                      <HopBody raw={JSON.stringify(entry.fields)} />
                    </td>
                  </tr>
                )}
              </Fragment>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function ErrorsTab({ trace }: { trace: TraceEntry }) {
  const hopErrors = (trace.hops ?? []).filter((h) => h.error || h.responseStatus >= 400)
  const hasEntryError = !!trace.awsErrorCode
  const hasHopErrors = hopErrors.length > 0

  if (!hasEntryError && !hasHopErrors) {
    return <div className="text-center text-fg-muted py-8">No errors recorded.</div>
  }

  return (
    <div className="flex flex-col gap-6">
      {hasEntryError && (
        <div>
          <h3 className="text-sm font-medium text-fg-muted mb-2">Request Error</h3>
          <div className="rounded-lg border border-red-400/30 bg-red-500/10 p-4">
            <div className="flex items-center gap-2 mb-1">
              <AlertCircle className="h-4 w-4 text-red-400" />
              <span className="font-mono text-sm text-red-400">{trace.awsErrorCode}</span>
            </div>
            {trace.awsErrorMessage && <p className="text-sm text-fg-muted">{trace.awsErrorMessage}</p>}
          </div>
        </div>
      )}
      {hasHopErrors && (
        <div>
          <h3 className="text-sm font-medium text-fg-muted mb-2">Hop Errors ({hopErrors.length})</h3>
          <div className="flex flex-col gap-3">
            {hopErrors.map((hop) => (
              <div key={hop.id} className="rounded-lg border border-border bg-bg-elevated p-3">
                <div className="flex items-center gap-2 mb-1">
                  <Badge variant="outline" className="text-xs">{hop.callerService} → {hop.service}</Badge>
                  <span className="text-xs font-mono text-fg-muted">{hop.operation}</span>
                  <span className={cn("text-xs font-mono", statusColor(hop.responseStatus))}>
                    {hop.responseStatus}
                    {statusMessage(hop.responseStatus) && ` (${statusMessage(hop.responseStatus)})`}
                  </span>
                </div>
                {hop.error && <pre className="text-xs font-mono text-red-400 mt-1 overflow-x-auto max-h-32">{hop.error}</pre>}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function HopBody({ raw, headers }: { raw: string; headers?: Record<string, string[]> }) {
  const ct = (headers?.["Content-Type"] ?? headers?.["content-type"] ?? [""])[0]
  if (ct) {
    const formatted = formatBodyForDisplay(raw, "text", ct)
    if (formatted.html && formatted.html !== formatted.text) return <pre className="bg-bg p-2 rounded text-xs font-mono overflow-x-auto max-h-48" dangerouslySetInnerHTML={{ __html: formatted.html }} />
  }
  // Fallback: try to detect format from content.
  for (const hint of ["text", "json", "xml"] as const) {
    const formatted = formatBodyForDisplay(raw, hint)
    if (formatted.html && formatted.html !== formatted.text) return <pre className="bg-bg p-2 rounded text-xs font-mono overflow-x-auto max-h-48" dangerouslySetInnerHTML={{ __html: formatted.html }} />
  }
  return <pre className="bg-bg p-2 rounded text-xs font-mono overflow-x-auto max-h-48">{raw}</pre>
}
