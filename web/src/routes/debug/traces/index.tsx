import { createFileRoute, Link } from "@tanstack/react-router"
import { useMemo, useState, useCallback } from "react"
import { Search, RefreshCw, EyeOff, Terminal, GitFork } from "lucide-react"
import { useInfiniteQuery, useQuery, infiniteQueryOptions, skipToken } from "@tanstack/react-query"
import { traceCountQueryOptions, debugTraceKeys } from "@/features/debug-traces/data"
import { debugTrace } from "@/services/api/misc"
import { nsToHuman, statusColor, statusMessage, formatTimestamp } from "@/features/debug-traces/utils"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { CheckboxFilterDropdown, type CheckboxFilterItem } from "@/components/ui/checkbox-filter-dropdown"
import { useScrollTrigger } from "@/hooks/use-scroll-trigger"
import { cn } from "@/lib/utils"
import { useDebugEnabled } from "@/hooks/use-server-info"
import type { TraceListParams, TraceListResponse, TraceSummary } from "@/types"

type TracesSearch = {
  service?: string
  method?: string
  status?: string
  search?: string
}

export const Route = createFileRoute("/debug/traces/")({
  head: () => ({ meta: [{ title: "Request Traces — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): TracesSearch => ({
    service: typeof search.service === "string" ? search.service : undefined,
    method: typeof search.method === "string" ? search.method : undefined,
    status: typeof search.status === "string" ? search.status : undefined,
    search: typeof search.search === "string" ? search.search : undefined,
  }),
  component: TracesPage,
})

const COL_COUNT = 9

function curlCmd(t: TraceSummary): string {
  return `curl -X ${t.method} 'http://${window.location.hostname}${t.path}'`
}

function TracesPage() {
  const { method, status, search } = Route.useSearch()
  const navigate = Route.useNavigate()
  const debugEnabled = useDebugEnabled()

  const [searchInput, setSearchInput] = useState(search ?? "")
  const [statusFilter, setStatusFilter] = useState(status ?? "")
  const [methodFilter, setMethodFilter] = useState(method ?? "")
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [hideInternal, setHideInternal] = useState(true)
  const [hiddenServices, setHiddenServices] = useState<Set<string>>(new Set())

  const params: TraceListParams = useMemo(() => {
    const p: TraceListParams = { limit: 50 }
    if (methodFilter) p.method = methodFilter
    if (statusFilter) p.status = statusFilter
    if (searchInput) p.search = searchInput
    return p
  }, [methodFilter, statusFilter, searchInput])

  // Infinite-query: page 1 = newest, scroll down loads older via after-cursor.
  const infiniteOpts = infiniteQueryOptions({
    queryKey: [...debugTraceKeys.list(params), debugEnabled],
    queryFn: ({ pageParam }) => debugTrace.list({ ...params, after: pageParam }),
    getNextPageParam: (lastPage: TraceListResponse) => lastPage.nextCursor || undefined,
    initialPageParam: undefined as string | undefined,
    enabled: debugEnabled,
  })
  const { data, isLoading, error, fetchNextPage, hasNextPage, isFetchingNextPage } = useInfiniteQuery(infiniteOpts)

  const bottomSentinelRef = useScrollTrigger({
    onTrigger: fetchNextPage,
    enabled: hasNextPage && !isFetchingNextPage,
  })

  const { data: countData } = useQuery({
    ...traceCountQueryOptions(),
  })

  // Poll for newer records using before-cursor.
  const pages = (data as unknown as { pages: TraceListResponse[] } | undefined)?.pages
  const newestId = pages?.[0]?.traces?.[0]?.requestId
  const { data: pollData } = useQuery({
    queryKey: [...debugTraceKeys.list(params), "poll", debugEnabled],
    queryFn: autoRefresh && newestId
      ? () => debugTrace.list({ before: newestId })
      : skipToken,
    refetchInterval: autoRefresh ? 5000 : false,
  })

  const allTraces = useMemo(() => {
    const base = (pages ?? []).flatMap((p) => p.traces)
    const fresh = pollData?.traces ?? []
    if (base.length === 0 && fresh.length === 0) return []
    if (fresh.length === 0) return base
    const seen = new Set(fresh.map((t) => t.requestId))
    return [...fresh, ...base.filter((t) => !seen.has(t.requestId))]
  }, [pages, pollData])

  const serviceOptions: CheckboxFilterItem[] = useMemo(() => {
    const counts = new Map<string, number>()
    for (const t of allTraces) {
      if (t.internal) continue
      const svc = t.service || "(unknown)"
      counts.set(svc, (counts.get(svc) ?? 0) + 1)
    }
    return Array.from(counts.entries())
      .sort((a, b) => b[1] - a[1])
      .map(([id, count]) => ({ id, label: id.toUpperCase(), count }))
  }, [allTraces])

  const displayTraces = useMemo(() => {
    let filtered = allTraces
    if (hideInternal) filtered = filtered.filter((t) => !t.internal)
    if (hiddenServices.size > 0) filtered = filtered.filter((t) => hiddenServices.has(t.service))
    return filtered
  }, [allTraces, hideInternal, hiddenServices])

  const toggleService = useCallback((id: string) => {
    setHiddenServices((prev) => { const n = new Set(prev); if (n.has(id)) n.delete(id); else n.add(id); return n })
  }, [])
  const selectAllServices = useCallback(() => setHiddenServices(new Set()), [])
  const deselectAllServices = useCallback(() => setHiddenServices(new Set(serviceOptions.map((s) => s.id))), [serviceOptions])
  const serviceTriggerLabel = useMemo(() => {
    if (hiddenServices.size === 0) return "all services"
    if (hiddenServices.size >= serviceOptions.length) return "no services"
    return `${hiddenServices.size} selected`
  }, [hiddenServices, serviceOptions])

  if (!debugEnabled) {
    return (
      <div className="flex flex-col gap-4 p-6">
        <PageHeader title="Request Traces" description="OVERCAST_DEBUG must be enabled on the emulator to use request tracing." />
        <div className="text-fg-muted text-sm">Set OVERCAST_DEBUG=true and restart the emulator.</div>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex items-start justify-between">
        <PageHeader
          title="Request Traces"
          description={countData ? `${countData.count} of ${countData.capacity} buffer slots used` : "Recent HTTP request traces"}
        />
        <Button
          variant={autoRefresh ? "default" : "ghost"}
          size="sm"
          onClick={() => setAutoRefresh((v) => !v)}
          className="mt-1"
        >
          <RefreshCw className={cn("h-4 w-4 mr-1", autoRefresh && "animate-spin")} />
          {autoRefresh ? "Live" : "Auto-refresh"}
        </Button>
      </div>

      <div className="flex flex-wrap items-stretch gap-2">
        <div className="relative flex-1 min-w-[200px] max-w-md">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-fg-muted" />
          <Input className="pl-8" placeholder="Search by request ID, path, or service…" value={searchInput} onChange={(e) => setSearchInput(e.target.value)} />
        </div>
        <CheckboxFilterDropdown items={serviceOptions} model="show" selected={hiddenServices} onToggle={toggleService} onSelectAll={selectAllServices} onDeselectAll={deselectAllServices} triggerLabel={serviceTriggerLabel} />
        <select className="h-9 rounded-md border border-border bg-bg-elevated px-2 py-1.5 text-sm text-fg" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
          <option value="">all status</option>
          <option value="2xx">2xx</option>
          <option value="4xx">4xx</option>
          <option value="5xx">5xx</option>
        </select>
        <select className="h-9 rounded-md border border-border bg-bg-elevated px-2 py-1.5 text-sm text-fg" value={methodFilter} onChange={(e) => setMethodFilter(e.target.value)}>
          <option value="">all methods</option>
          <option value="GET">GET</option>
          <option value="POST">POST</option>
          <option value="PUT">PUT</option>
          <option value="DELETE">DELETE</option>
          <option value="HEAD">HEAD</option>
          <option value="PATCH">PATCH</option>
        </select>
        <label className="flex items-center gap-1.5 rounded-md border border-border bg-bg-elevated px-2.5 py-1.5 text-sm cursor-pointer select-none">
          <EyeOff className="h-3.5 w-3.5 text-fg-muted" />
          <span className="text-fg-muted">Hide internal</span>
          <input type="checkbox" checked={hideInternal} onChange={(e) => setHideInternal(e.target.checked)} className="ml-1" />
        </label>
      </div>

      {isLoading ? (
        <Spinner />
      ) : error ? (
        <div className="text-red-400 text-sm">Failed to load traces.</div>
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg border border-border">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border text-fg-muted text-left">
                  <th className="px-3 py-2 font-medium">Time</th>
                  <th className="px-3 py-2 font-medium">Method</th>
                  <th className="px-3 py-2 font-medium">Path</th>
                  <th className="px-3 py-2 font-medium">Service</th>
                  <th className="px-3 py-2 font-medium">Op</th>
                  <th className="px-3 py-2 font-medium">Status</th>
                  <th className="px-3 py-2 font-medium">Duration</th>
                  <th className="px-3 py-2 font-medium">Request ID</th>
                  <th className="px-3 py-2 font-medium w-10" />
                </tr>
              </thead>
              <tbody>
                {displayTraces.length === 0 ? (
                  <tr><td colSpan={COL_COUNT} className="px-3 py-8 text-center text-fg-muted">No traces yet. Send a request to see it here.</td></tr>
                ) : (
                  displayTraces.map((t) => (
                    <tr key={t.requestId} className="border-b border-border hover:bg-bg-elevated cursor-pointer transition-colors" onClick={() => void navigate({ to: "/debug/traces/$requestId", params: { requestId: t.requestId } })}>
                      <td className="px-3 py-2 whitespace-nowrap text-fg-muted font-mono text-xs">{formatTimestamp(t.timestamp)}</td>
                      <td className="px-3 py-2"><Badge variant="outline" className="text-xs font-mono">{t.method}</Badge></td>
                      <td className="px-3 py-2 truncate font-mono text-xs">{t.path}</td>
                      <td className="px-3 py-2">
                        <div className="flex items-center gap-1">
                          <Badge variant="outline" className="text-xs">{t.service || "—"}</Badge>
                          {t.hopCount ? <span className="inline-flex items-center gap-0.5 text-[10px] text-fg-muted" title={`${t.hopCount} hop${t.hopCount !== 1 ? "s" : ""}`}><GitFork className="h-3 w-3" />{t.hopCount}</span> : null}
                        </div>
                      </td>
                      <td className="px-3 py-2 text-fg-muted text-xs">{t.operation ?? "—"}</td>
                      <td className={cn("px-3 py-2 font-mono text-xs whitespace-nowrap", statusColor(t.statusCode))}>{t.statusCode > 0 ? `${t.statusCode}${statusMessage(t.statusCode) ? ` ${statusMessage(t.statusCode)}` : ""}` : "—"}</td>
                      <td className="px-3 py-2 font-mono text-xs text-fg-muted">{nsToHuman(t.duration)}</td>
                      <td className="px-3 py-2 min-w-0 truncate">
                        <Link to="/debug/traces/$requestId" params={{ requestId: t.requestId }} className="font-mono text-xs text-accent hover:underline" onClick={(e) => e.stopPropagation()}>{t.requestId}</Link>
                      </td>
                      <td className="px-3 py-2">
                        <button className="inline-flex items-center justify-center h-7 w-7 rounded hover:bg-fg/5 text-fg-muted hover:text-fg shrink-0" title="Copy as curl" aria-label="Copy as curl" onClick={(e) => { e.stopPropagation(); void navigator.clipboard.writeText(curlCmd(t)) }}>
                          <Terminal className="h-3.5 w-3.5" />
                        </button>
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
          {hasNextPage && <div ref={bottomSentinelRef} className="h-1" />}
        </>
      )}
    </div>
  )
}
