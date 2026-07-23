import { useMemo, useState, type ReactNode } from "react"
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
  const [keyView, setKeyView] = useState<"flat" | "tree">("tree")

  const summaryQuery = useQuery(debugStateQueryOptions())
  const availableNamespaces = useMemo(
    () => Object.keys(summaryQuery.data ?? {}).sort(),
    [summaryQuery.data],
  )
  const namespaces = useMemo(() => {
    const all = Object.keys(summaryQuery.data ?? {}).sort()
    if (!serviceFilter) return all
    return all.filter((ns) => serviceForDebugNamespace(ns) === serviceFilter)
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

          <div className="grid min-h-0 gap-4 lg:grid-cols-[18rem_minmax(20rem,26rem)_1fr]">
            <section className="flex max-h-[calc(100vh-14rem)] min-h-0 flex-col overflow-hidden rounded-lg border border-border bg-bg-elevated">
              <div className="border-b border-border px-3 py-2 text-sm font-medium text-fg">
                Hierarchy
              </div>
              <div className="min-h-0 flex-1 overflow-auto p-2">
                {groupDebugNamespaces(namespaces).map((group) => (
                  <div key={group.service} className="mb-3 last:mb-0">
                    <div className="mb-1 px-2 text-xs font-medium tracking-wide text-fg-muted uppercase">
                      {DEBUG_SERVICE_LABELS[group.service] ?? group.service}
                    </div>
                    {group.namespaces.map(({ namespace: ns, category }) => (
                      <Button
                        key={ns}
                        variant={ns === activeNamespace ? "secondary" : "ghost"}
                        className="mb-1 w-full justify-between font-mono text-xs"
                        onClick={() => {
                          setNamespace(ns)
                          setSelectedKey("")
                        }}
                      >
                        <span className="truncate">{category}</span>
                        <Badge variant="outline">{summaryQuery.data?.[ns]?.length ?? 0}</Badge>
                      </Button>
                    ))}
                  </div>
                ))}
              </div>
            </section>

            <section className="flex max-h-[calc(100vh-14rem)] min-h-0 flex-col overflow-hidden rounded-lg border border-border bg-bg-elevated">
              <div className="shrink-0 border-b border-border p-3">
                <div className="flex items-center gap-2">
                  <Search className="h-4 w-4 text-fg-muted" />
                  <Input
                    aria-label="Filter raw state keys and values"
                    value={filter}
                    onChange={(e) => setFilter(e.target.value)}
                    placeholder="Search keys and raw values"
                    className="h-8"
                  />
                  <div className="flex rounded-md border border-border p-0.5">
                    <Button
                      variant={keyView === "flat" ? "secondary" : "ghost"}
                      size="sm"
                      className="h-7 px-2 text-xs"
                      onClick={() => setKeyView("flat")}
                    >
                      Flat
                    </Button>
                    <Button
                      variant={keyView === "tree" ? "secondary" : "ghost"}
                      size="sm"
                      className="h-7 px-2 text-xs"
                      onClick={() => setKeyView("tree")}
                    >
                      Tree
                    </Button>
                  </div>
                </div>
                <p className="mt-2 text-xs text-fg-muted">
                  Showing {rows.length.toLocaleString()} of {totalKeys.toLocaleString()} record
                  {totalKeys !== 1 ? "s" : ""} in{" "}
                  <span className="font-mono">{activeNamespace}</span>
                </p>
              </div>
              {valuesQuery.isLoading ? (
                <QueryListState isLoading isEmpty={false} />
              ) : keyView === "tree" ? (
                <KeyTree rows={rows} activeKey={activeKey} onSelect={setSelectedKey} />
              ) : (
                <div className="min-h-0 flex-1 overflow-auto">
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
                </div>
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

interface KeyTreeNode {
  name: string
  path: string
  key?: string
  value?: string
  children: Map<string, KeyTreeNode>
}

function KeyTree({
  rows,
  activeKey,
  onSelect,
}: {
  rows: [string, string][]
  activeKey?: string
  onSelect: (key: string) => void
}) {
  const roots = useMemo(() => buildKeyTree(rows), [rows])

  if (rows.length === 0) {
    return <div className="p-6 text-sm text-fg-muted">No records match the current filter.</div>
  }

  return (
    <div className="min-h-0 flex-1 overflow-auto p-2">
      {roots.map((node) => (
        <KeyTreeNodeRow
          key={node.path}
          node={node}
          depth={0}
          activeKey={activeKey}
          onSelect={onSelect}
        />
      ))}
    </div>
  )
}

function KeyTreeNodeRow({
  node,
  depth,
  activeKey,
  onSelect,
}: {
  node: KeyTreeNode
  depth: number
  activeKey?: string
  onSelect: (key: string) => void
}) {
  const children = Array.from(node.children.values()).sort((a, b) => a.name.localeCompare(b.name))
  const isLeaf = node.key != null

  return (
    <div>
      <button
        type="button"
        disabled={!isLeaf}
        data-selected={node.key === activeKey}
        className={cn(
          "flex w-full items-center justify-between gap-3 rounded-md px-2 py-1.5 text-left font-mono text-xs",
          isLeaf
            ? "text-fg hover:bg-bg-muted data-[selected=true]:bg-accent/10"
            : "cursor-default text-fg-muted",
        )}
        style={{ paddingLeft: `${depth * 0.75 + 0.5}rem` }}
        onClick={() => node.key && onSelect(node.key)}
      >
        <span className="min-w-0 truncate">
          {!isLeaf && <span className="mr-1 text-fg-subtle">/</span>}
          {node.name}
        </span>
        {isLeaf && node.value != null ? (
          <span className="shrink-0 text-fg-muted">{node.value.length.toLocaleString()} B</span>
        ) : null}
      </button>
      {children.map((child) => (
        <KeyTreeNodeRow
          key={child.path}
          node={child}
          depth={depth + 1}
          activeKey={activeKey}
          onSelect={onSelect}
        />
      ))}
    </div>
  )
}

function buildKeyTree(rows: [string, string][]): KeyTreeNode[] {
  const roots = new Map<string, KeyTreeNode>()
  for (const [key, value] of rows) {
    const segments = key.split("/").filter(Boolean)
    const parts = segments.length > 0 ? segments : [key]
    let level = roots
    let path = ""
    for (const [index, part] of parts.entries()) {
      path = path ? `${path}/${part}` : part
      let node = level.get(part)
      if (!node) {
        node = { name: part, path, children: new Map() }
        level.set(part, node)
      }
      if (index === parts.length - 1) {
        node.key = key
        node.value = value
      }
      level = node.children
    }
  }
  return Array.from(roots.values()).sort((a, b) => a.name.localeCompare(b.name))
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
  const parsed = parseStoredValue(value)

  return (
    <section className="flex max-h-[calc(100vh-14rem)] min-h-0 min-w-0 flex-col overflow-hidden rounded-lg border border-border bg-bg-elevated">
      <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2">
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
        <pre className="min-h-0 flex-1 overflow-auto p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap text-fg">
          {parsed.isJSON ? (
            <HighlightedJSON text={parsed.text} />
          ) : (
            <span className="text-fg-muted">{parsed.text}</span>
          )}
        </pre>
      ) : (
        <div className="p-8 text-sm text-fg-muted">
          Select a state key to inspect its raw value.
        </div>
      )}
    </section>
  )
}

function parseStoredValue(value: string | undefined): { text: string; isJSON: boolean } {
  if (value == null || value === "") return { text: "", isJSON: false }
  try {
    return { text: JSON.stringify(decodeNestedJSON(JSON.parse(value)), null, 2), isJSON: true }
  } catch {
    return { text: value, isJSON: false }
  }
}

function decodeNestedJSON(value: unknown, depth = 0): unknown {
  if (depth > 8) return value
  if (typeof value === "string") {
    const trimmed = value.trim()
    if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
      try {
        return decodeNestedJSON(JSON.parse(trimmed), depth + 1)
      } catch {
        return value
      }
    }
    return value
  }
  if (Array.isArray(value)) return value.map((item) => decodeNestedJSON(item, depth + 1))
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value).map(([key, item]) => [key, decodeNestedJSON(item, depth + 1)]),
    )
  }
  return value
}

function HighlightedJSON({ text }: { text: string }) {
  return <>{highlightJSON(text)}</>
}

function highlightJSON(text: string): ReactNode[] {
  const tokenPattern =
    /("(?:\\.|[^"\\])*"(?=\s*:))|("(?:\\.|[^"\\])*")|\b(true|false)\b|\b(null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g
  const nodes: ReactNode[] = []
  let lastIndex = 0
  let tokenIndex = 0
  for (const match of text.matchAll(tokenPattern)) {
    const index = match.index
    if (index > lastIndex) nodes.push(text.slice(lastIndex, index))
    const token = match[0]
    const className = match[1]
      ? "text-accent"
      : match[2]
        ? "text-emerald-400"
        : match[3]
          ? "text-sky-400"
          : match[4]
            ? "text-fg-muted"
            : "text-amber-400"
    nodes.push(
      <span key={`${index}-${tokenIndex++}`} className={className}>
        {token}
      </span>,
    )
    lastIndex = index + token.length
  }
  if (lastIndex < text.length) nodes.push(text.slice(lastIndex))
  return nodes
}

function buildDebugDeepLink(namespace: string, stateKey: string): string {
  const params = new URLSearchParams({ namespace, key: stateKey })
  return `${window.location.origin}/debug?${params.toString()}`
}
