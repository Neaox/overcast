import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { ChevronRight, Copy, Database, LinkIcon, RefreshCw, Search } from "lucide-react"
import { PageHeader, QueryListState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { cn } from "@/lib/utils"
import { useToast } from "@/components/ui/toast"
import { debugClipboard } from "./clipboard"
import { debugNamespaceQueryOptions, debugStateQueryOptions } from "./data"
import {
  DEBUG_SERVICE_LABELS,
  DEBUG_SERVICE_NAMESPACES,
  firstDebugNamespaceForService,
  groupDebugNamespaces,
  serviceForDebugNamespace,
} from "./namespaces"

export function DebugPage({
  initialService,
  initialNamespace,
  initialKey,
}: {
  initialService?: string
  initialNamespace?: string
  initialKey?: string
}) {
  const { toast } = useToast()
  const [serviceFilter, setServiceFilter] = useState(initialService ?? "")
  const [namespace, setNamespace] = useState(initialNamespace ?? "")
  const [selectedKey, setSelectedKey] = useState(initialKey ?? "")
  const [filter, setFilter] = useState("")

  const summaryQuery = useQuery(debugStateQueryOptions())
  const availableNamespaces = useMemo(
    () => Object.keys(summaryQuery.data ?? {}).sort(),
    [summaryQuery.data],
  )
  const namespaces = useMemo(() => {
    const all = Object.keys(summaryQuery.data ?? {}).sort()
    if (!serviceFilter) return all
    const allowed = new Set(DEBUG_SERVICE_NAMESPACES[serviceFilter] ?? [])
    return all.filter((ns) => allowed.has(ns))
  }, [serviceFilter, summaryQuery.data])
  const activeNamespace = namespace || namespaces[0] || ""
  const valuesQuery = useQuery(debugNamespaceQueryOptions(activeNamespace))
  const activeService = serviceForDebugNamespace(activeNamespace) ?? serviceFilter

  const rows = useMemo(() => {
    const values = valuesQuery.data ?? {}
    const lower = filter.trim().toLowerCase()
    return Object.entries(values)
      .filter(([key, value]) =>
        lower ? key.toLowerCase().includes(lower) || value.toLowerCase().includes(lower) : true,
      )
      .sort(([a], [b]) => a.localeCompare(b))
  }, [filter, valuesQuery.data])

  const activeKey =
    resolveActiveKey(
      selectedKey,
      rows.map(([key]) => key),
    ) ?? rows[0]?.[0]
  const activeValue = activeKey ? valuesQuery.data?.[activeKey] : undefined
  const totalKeys = Object.keys(valuesQuery.data ?? {}).length

  function copyText(text: string, title: string) {
    void debugClipboard.writeText(text).then(
      () => toast({ title, variant: "success" }),
      (err: Error) => toast({ title: "Copy failed", description: err.message, variant: "danger" }),
    )
  }

  function refreshState() {
    void Promise.all([summaryQuery.refetch(), valuesQuery.refetch()])
  }

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Raw State Debugger"
        description={
          serviceFilter
            ? `Read-only raw state for ${DEBUG_SERVICE_LABELS[serviceFilter] ?? serviceFilter}. Requires OVERCAST_DEBUG=true.`
            : "Read-only view of Overcast's internal state store. Requires OVERCAST_DEBUG=true."
        }
      />

      <QueryListState
        isLoading={summaryQuery.isLoading}
        isEmpty={namespaces.length === 0}
        error={summaryQuery.error}
        emptyIcon={<Database className="h-10 w-10" />}
        emptyTitle="No debug state available"
        emptyDescription={
          serviceFilter
            ? `No raw state found for ${DEBUG_SERVICE_LABELS[serviceFilter] ?? serviceFilter}. Enable OVERCAST_DEBUG=true and create resources to inspect stored values.`
            : "Enable OVERCAST_DEBUG=true and create resources to inspect stored values."
        }
      />

      {namespaces.length > 0 && (
        <div className="flex flex-col gap-3">
          <DebugBreadcrumbs
            service={activeService}
            namespace={activeNamespace}
            stateKey={activeKey}
            onAll={() => {
              setServiceFilter("")
              setNamespace("")
              setSelectedKey("")
            }}
            onService={() => {
              const first = activeService
                ? firstDebugNamespaceForService(activeService, availableNamespaces)
                : ""
              setServiceFilter(activeService)
              setNamespace(first)
              setSelectedKey("")
            }}
            onNamespace={() => setSelectedKey("")}
          />

          <div className="grid min-h-[70vh] gap-4 lg:grid-cols-[18rem_minmax(20rem,26rem)_1fr]">
            <section className="rounded-lg border border-border bg-bg-elevated">
              <div className="border-b border-border px-3 py-2 text-sm font-medium text-fg">
                Hierarchy
              </div>
              <div className="p-2">
                {groupDebugNamespaces(namespaces).map((group) => (
                  <div key={group.service} className="mb-3 last:mb-0">
                    <div className="mb-1 px-2 text-xs font-medium tracking-wide text-fg-muted uppercase">
                      {DEBUG_SERVICE_LABELS[group.service] ?? group.service}
                    </div>
                    {group.namespaces.map((ns) => (
                      <Button
                        key={ns}
                        variant={ns === activeNamespace ? "secondary" : "ghost"}
                        className="mb-1 w-full justify-between font-mono text-xs"
                        onClick={() => {
                          setNamespace(ns)
                          setSelectedKey("")
                        }}
                      >
                        <span className="truncate">{ns}</span>
                        <Badge variant="outline">{summaryQuery.data?.[ns]?.length ?? 0}</Badge>
                      </Button>
                    ))}
                  </div>
                ))}
              </div>
            </section>

            <section className="rounded-lg border border-border bg-bg-elevated">
              <div className="border-b border-border p-3">
                <div className="flex items-center gap-2">
                  <Search className="h-4 w-4 text-fg-muted" />
                  <Input
                    aria-label="Filter raw state keys and values"
                    value={filter}
                    onChange={(e) => setFilter(e.target.value)}
                    placeholder="Search keys and raw values"
                    className="h-8"
                  />
                </div>
                <p className="mt-2 text-xs text-fg-muted">
                  Showing {rows.length.toLocaleString()} of {totalKeys.toLocaleString()} record
                  {totalKeys !== 1 ? "s" : ""} in{" "}
                  <span className="font-mono">{activeNamespace}</span>
                </p>
              </div>
              {valuesQuery.isLoading ? (
                <QueryListState isLoading isEmpty={false} />
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow className="cursor-default hover:bg-transparent">
                      <TableHead>Key</TableHead>
                      <TableHead className="w-16 text-right">Bytes</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {rows.length === 0 ? (
                      <TableEmpty colSpan={2}>No records match the current filter.</TableEmpty>
                    ) : (
                      rows.map(([key, value]) => (
                        <TableRow
                          key={key}
                          data-selected={key === activeKey}
                          onClick={() => setSelectedKey(key)}
                        >
                          <TableCell className="max-w-0 truncate font-mono text-xs">
                            {key}
                          </TableCell>
                          <TableCell className="text-right font-mono text-xs text-fg-muted">
                            {value.length.toLocaleString()}
                          </TableCell>
                        </TableRow>
                      ))
                    )}
                  </TableBody>
                </Table>
              )}
            </section>

            <StateValueViewer
              namespace={activeNamespace}
              stateKey={activeKey}
              value={activeValue}
              isRefreshing={summaryQuery.isFetching || valuesQuery.isFetching}
              onRefresh={refreshState}
              onCopy={copyText}
            />
          </div>
        </div>
      )}
    </div>
  )
}

function DebugBreadcrumbs({
  service,
  namespace,
  stateKey,
  onAll,
  onService,
  onNamespace,
}: {
  service?: string
  namespace: string
  stateKey?: string
  onAll: () => void
  onService: () => void
  onNamespace: () => void
}) {
  return (
    <nav aria-label="Debug hierarchy" className="flex flex-wrap items-center gap-1 text-sm">
      <Button variant="ghost" size="sm" onClick={onAll}>
        Raw state
      </Button>
      {service && (
        <>
          <ChevronRight className="h-3.5 w-3.5 text-fg-subtle" />
          <Button variant="ghost" size="sm" onClick={onService}>
            {DEBUG_SERVICE_LABELS[service] ?? service}
          </Button>
        </>
      )}
      {namespace && (
        <>
          <ChevronRight className="h-3.5 w-3.5 text-fg-subtle" />
          <Button variant="ghost" size="sm" className="font-mono" onClick={onNamespace}>
            {namespace}
          </Button>
        </>
      )}
      {stateKey && (
        <>
          <ChevronRight className="h-3.5 w-3.5 text-fg-subtle" />
          <span className="max-w-96 truncate rounded-md bg-bg-muted px-2.5 py-1 font-mono text-xs text-fg">
            {stateKey}
          </span>
        </>
      )}
    </nav>
  )
}

function resolveActiveKey(requested: string, keys: string[]): string | undefined {
  if (!requested) return undefined
  return (
    keys.find((key) => key === requested) ??
    keys.find((key) => key.endsWith(`/${requested}`)) ??
    keys.find((key) => key.endsWith(`:${requested}`))
  )
}

function StateValueViewer({
  namespace,
  stateKey,
  value,
  isRefreshing,
  onRefresh,
  onCopy,
}: {
  namespace: string
  stateKey?: string
  value?: string
  isRefreshing: boolean
  onRefresh: () => void
  onCopy: (text: string, title: string) => void
}) {
  const parsed = formatStoredValue(value)

  return (
    <section className="min-w-0 rounded-lg border border-border bg-bg-elevated">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Badge variant="info" className="font-mono">
            {namespace}
          </Badge>
          {stateKey ? (
            <span className="truncate font-mono text-xs text-fg-muted">{stateKey}</span>
          ) : null}
        </div>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="sm" onClick={onRefresh} disabled={isRefreshing}>
            <RefreshCw className={cn("h-3.5 w-3.5", isRefreshing && "animate-spin")} />
            Refresh
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => stateKey && onCopy(stateKey, "State key copied")}
            disabled={!stateKey}
          >
            <Copy className="h-3.5 w-3.5" />
            Key
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => value != null && onCopy(value, "Raw value copied")}
            disabled={value == null}
          >
            <Copy className="h-3.5 w-3.5" />
            Value
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              stateKey && onCopy(buildDebugDeepLink(namespace, stateKey), "Debug link copied")
            }
            disabled={!stateKey}
          >
            <LinkIcon className="h-3.5 w-3.5" />
            Link
          </Button>
        </div>
      </div>
      {stateKey ? (
        <pre
          className={cn(
            "max-h-[calc(70vh-3rem)] overflow-auto p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap text-fg",
            !parsed.isJSON && "text-fg-muted",
          )}
        >
          {parsed.text}
        </pre>
      ) : (
        <div className="p-8 text-sm text-fg-muted">
          Select a state key to inspect its raw value.
        </div>
      )}
    </section>
  )
}

function formatStoredValue(value: string | undefined): { text: string; isJSON: boolean } {
  if (value == null || value === "") return { text: "", isJSON: false }
  try {
    return { text: JSON.stringify(JSON.parse(value), null, 2), isJSON: true }
  } catch {
    return { text: value, isJSON: false }
  }
}

function buildDebugDeepLink(namespace: string, stateKey: string): string {
  const params = new URLSearchParams({ namespace, key: stateKey })
  return `${window.location.origin}/debug?${params.toString()}`
}
