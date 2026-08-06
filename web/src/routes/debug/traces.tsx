import { createFileRoute, Link } from "@tanstack/react-router"
import { useState } from "react"
import { Search } from "lucide-react"
import { useQuery } from "@tanstack/react-query"
import { traceListQueryOptions, traceCountQueryOptions } from "@/features/debug-traces/data"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { CopyButton } from "@/components/ui/copy-button"
import { cn } from "@/lib/utils"
import type { TraceListParams } from "@/types"

type TracesSearch = {
  service?: string
  method?: string
  status?: string
  search?: string
}

export const Route = createFileRoute("/debug/traces")({
  head: () => ({ meta: [{ title: "Request Traces — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): TracesSearch => ({
    service: typeof search.service === "string" ? search.service : undefined,
    method: typeof search.method === "string" ? search.method : undefined,
    status: typeof search.status === "string" ? search.status : undefined,
    search: typeof search.search === "string" ? search.search : undefined,
  }),
  component: TracesPage,
})

const msToHuman = (ns: number): string => {
  if (ns < 1_000_000) return `${(ns / 1000).toFixed(0)}µs`
  if (ns < 1_000_000_000) return `${(ns / 1_000_000).toFixed(1)}ms`
  return `${(ns / 1_000_000_000).toFixed(2)}s`
}

const statusColor = (code: number): string => {
  if (code >= 500) return "text-red-400"
  if (code >= 400) return "text-amber-400"
  return "text-emerald-400"
}

function TracesPage() {
  const { service, method, status, search } = Route.useSearch()
  const navigate = Route.useNavigate()

  const [searchInput, setSearchInput] = useState(search ?? "")
  const [serviceFilter, setServiceFilter] = useState(service ?? "")
  const [statusFilter, setStatusFilter] = useState(status ?? "")
  const [methodFilter, setMethodFilter] = useState(method ?? "")

  const params: TraceListParams = {}
  if (serviceFilter) params.service = serviceFilter
  if (methodFilter) params.method = methodFilter
  if (statusFilter) params.status = statusFilter
  if (searchInput) params.search = searchInput
  params.limit = 50

  const { data, isLoading, error } = useQuery(traceListQueryOptions(params))
  const { data: countData } = useQuery(traceCountQueryOptions())

  const applyFilters = () => {
    void navigate({
      search: {
        service: serviceFilter || undefined,
        method: methodFilter || undefined,
        status: statusFilter || undefined,
        search: searchInput || undefined,
      },
      replace: true,
    })
  }

  return (
    <div className="flex flex-col gap-4 p-6">
      <PageHeader
        title="Request Traces"
        description={
          countData
            ? `${countData.count} of ${countData.capacity} buffer slots used`
            : "Recent HTTP request traces (OVERCAST_DEBUG must be enabled)"
        }
      />

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[200px] max-w-md">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-fg-muted" />
          <Input
            className="pl-8"
            placeholder="Search by request ID, path, or service…"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && applyFilters()}
          />
        </div>
        <Input
          className="w-32"
          placeholder="service"
          value={serviceFilter}
          onChange={(e) => setServiceFilter(e.target.value)}
        />
        <select
          className="rounded-md border border-border bg-bg-elevated px-2 py-1.5 text-sm text-fg"
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
        >
          <option value="">all status</option>
          <option value="2xx">2xx</option>
          <option value="4xx">4xx</option>
          <option value="5xx">5xx</option>
        </select>
        <select
          className="rounded-md border border-border bg-bg-elevated px-2 py-1.5 text-sm text-fg"
          value={methodFilter}
          onChange={(e) => setMethodFilter(e.target.value)}
        >
          <option value="">all methods</option>
          <option value="GET">GET</option>
          <option value="POST">POST</option>
          <option value="PUT">PUT</option>
          <option value="DELETE">DELETE</option>
          <option value="HEAD">HEAD</option>
          <option value="PATCH">PATCH</option>
        </select>
        <Button variant="outline" size="sm" onClick={applyFilters}>
          Apply
        </Button>
      </div>

      {isLoading ? (
        <Spinner />
      ) : error ? (
        <div className="text-red-400 text-sm">
          Failed to load traces. Make sure OVERCAST_DEBUG=true is set on the emulator.
        </div>
      ) : (
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-fg-muted text-left">
                <th className="px-3 py-2 font-medium">Time</th>
                <th className="px-3 py-2 font-medium">Method</th>
                <th className="px-3 py-2 font-medium">Path</th>
                <th className="px-3 py-2 font-medium">Service</th>
                <th className="px-3 py-2 font-medium">Operation</th>
                <th className="px-3 py-2 font-medium">Status</th>
                <th className="px-3 py-2 font-medium">Duration</th>
                <th className="px-3 py-2 font-medium">Request ID</th>
              </tr>
            </thead>
            <tbody>
              {(data?.traces ?? []).length === 0 ? (
                <tr>
                  <td colSpan={8} className="px-3 py-8 text-center text-fg-muted">
                    No traces yet. Send a request to see it here.
                  </td>
                </tr>
              ) : (
                (data?.traces ?? []).map((t) => (
                  <tr
                    key={t.requestId}
                    className="border-b border-border hover:bg-bg-elevated cursor-pointer transition-colors"
                    onClick={() =>
                      void navigate({
                        to: "/debug/traces/$requestId",
                        params: { requestId: t.requestId },
                      })
                    }
                  >
                    <td className="px-3 py-2 whitespace-nowrap text-fg-muted font-mono text-xs">
                      {new Date(t.timestamp).toLocaleTimeString()}
                    </td>
                    <td className="px-3 py-2">
                      <Badge variant="outline" className="text-xs font-mono">
                        {t.method}
                      </Badge>
                    </td>
                    <td className="px-3 py-2 max-w-[300px] truncate font-mono text-xs">
                      {t.path}
                    </td>
                    <td className="px-3 py-2">
                      <Badge variant="outline" className="text-xs">
                        {t.service}
                      </Badge>
                    </td>
                    <td className="px-3 py-2 text-fg-muted text-xs">
                      {t.operation ?? "—"}
                    </td>
                    <td className={cn("px-3 py-2 font-mono text-xs", statusColor(t.statusCode))}>
                      {t.statusCode}
                    </td>
                    <td className="px-3 py-2 font-mono text-xs text-fg-muted">
                      {msToHuman(t.duration)}
                    </td>
                    <td className="px-3 py-2">
                      <div className="flex items-center gap-1">
                        <Link
                          to="/debug/traces/$requestId"
                          params={{ requestId: t.requestId }}
                          className="font-mono text-xs text-accent hover:underline truncate max-w-[200px]"
                          onClick={(e) => e.stopPropagation()}
                        >
                          {t.requestId.slice(0, 12)}…
                        </Link>
                        <CopyButton value={t.requestId} noun="request ID" />
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
