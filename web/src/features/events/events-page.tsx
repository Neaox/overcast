/**
 * EventsPage — live event stream viewer at /events.
 *
 * Features: multi-source filtering, free-text search, date-range filtering,
 * heartbeat toggle, pause/resume. Source and text/date
 * filters are all client-side — the SSE connection always receives everything.
 */
import { useState, useCallback, useMemo, useRef, useEffect } from "react"
import {
  Activity,
  Check,
  ChevronDown,
  Heart,
  Pause,
  Play,
  Search,
  X,
} from "lucide-react"
import { useEventStream, type StreamEvent } from "@/hooks/use-event-stream"
import { EventConsole } from "@/components/ui/event-console"
import { PageHeader } from "@/components/ui/primitives"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"
import { toTitleCase } from "@/lib/format"
import { EventType } from "@/services/event-types"
import { SERVICES } from "@/lib/service-registry"

// ─── Derive filterable sources from EventType ────────────────────────────────

type SourceEntry = { id: string; label: string }

const SOURCE_LABELS: Record<string, string> = {
  request: "Requests",
  service: "Service errors",
  inbox: "Inbox",
}

function buildSourceEntries(): SourceEntry[] {
  const entries: SourceEntry[] = []
  for (const key of Object.keys(EventType)) {
    const explicitLabel = SOURCE_LABELS[key]
    if (explicitLabel) {
      entries.push({ id: key, label: explicitLabel })
      continue
    }

    if (key in SERVICES) {
      const svc = SERVICES[key as keyof typeof SERVICES]
      entries.push({ id: key, label: svc.label })
      continue
    }

    entries.push({ id: key, label: toTitleCase(key) })
  }
  return entries
}

const ALL_SOURCES = buildSourceEntries()
const DEFAULT_SELECTED_SOURCES = ALL_SOURCES
  .filter((source) => source.id !== "request" && source.id !== "heartbeat")
  .map((source) => source.id)

// ─── Helpers ─────────────────────────────────────────────────────────────────

function eventMatchesText(ev: StreamEvent, lower: string): boolean {
  if (ev.type.toLowerCase().includes(lower)) return true
  if (ev.source.toLowerCase().includes(lower)) return true
  if (ev.type === "request:Received") {
    const p = ev.payload as Record<string, unknown> | null
    if (p) {
      for (const f of ["method", "path", "service", "operation", "requestId"]) {
        if (String(p[f] ?? "").toLowerCase().includes(lower)) return true
      }
      if (String(p.status ?? "").toLowerCase().includes(lower)) return true
    }
  }
  try {
    if (JSON.stringify(ev.payload).toLowerCase().includes(lower)) return true
  } catch { /* ignore */ }
  return false
}

function topSources(events: readonly StreamEvent[], n: number): { id: string; label: string; count: number }[] {
  const counts = new Map<string, number>()
  for (const e of events) counts.set(e.source, (counts.get(e.source) ?? 0) + 1)
  return Array.from(counts.entries())
    .sort((a, b) => b[1] - a[1])
    .slice(0, n)
    .map(([id, count]) => {
      const entry = ALL_SOURCES.find(s => s.id === id)
      return { id, label: entry?.label ?? id, count }
    })
}

// ─── Component ────────────────────────────────────────────────────────────────

export function EventsPage() {
  const [selectedSources, setSelectedSources] = useState<string[]>(() => DEFAULT_SELECTED_SOURCES)
  const [showHeartbeats, setShowHeartbeats] = useState(false)
  const [paused, setPaused] = useState(false)
  const [frozenEvents, setFrozenEvents] = useState<StreamEvent[]>([])
  const [textFilter, setTextFilter] = useState("")
  const [dateFrom, setDateFrom] = useState("")
  const [dateTo, setDateTo] = useState("")
  const [sourceMenuOpen, setSourceMenuOpen] = useState(false)
  const [sourceSearch, setSourceSearch] = useState("")
  const searchRef = useRef<HTMLInputElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  const { events: rawEvents, connected, clear: streamClear } = useEventStream({
    includeHeartbeats: showHeartbeats,
  })

  // Client-side filtering chain. Sources pick what to include; request
  // events are just another source and are unchecked by default.
  const events = useMemo(() => {
    let filtered = rawEvents

    if (selectedSources.length > 0) {
      const set = new Set(selectedSources)
      filtered = filtered.filter((e) => set.has(e.source))
    }

    const lower = textFilter.toLowerCase().trim()
    if (lower) filtered = filtered.filter((e) => eventMatchesText(e, lower))

    if (dateFrom) {
      const from = new Date(dateFrom).getTime()
      if (!isNaN(from)) filtered = filtered.filter((e) => new Date(e.time).getTime() >= from)
    }
    if (dateTo) {
      const to = new Date(dateTo).getTime()
      if (!isNaN(to)) filtered = filtered.filter((e) => new Date(e.time).getTime() <= to)
    }
    return filtered
  }, [rawEvents, selectedSources, textFilter, dateFrom, dateTo])

  const displayedEvents = paused ? frozenEvents : events

  const clear = useCallback(() => {
    streamClear()
    setFrozenEvents([])
    setPaused(false)
  }, [streamClear])

  // / to focus search.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "/" && !e.ctrlKey && !e.metaKey && document.activeElement === document.body) {
        e.preventDefault()
        searchRef.current?.focus()
      }
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [])

  // Close source menu on outside click.
  useEffect(() => {
    if (!sourceMenuOpen) return
    function onClick(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setSourceMenuOpen(false)
      }
    }
    document.addEventListener("mousedown", onClick)
    return () => document.removeEventListener("mousedown", onClick)
  }, [sourceMenuOpen])

  const toggleSource = useCallback((id: string) => {
    setSelectedSources(prev =>
      prev.includes(id) ? prev.filter(s => s !== id) : [...prev, id],
    )
  }, [])

  const clearFilters = useCallback(() => {
    setSelectedSources(DEFAULT_SELECTED_SOURCES)
    setTextFilter("")
    setDateFrom("")
    setDateTo("")
  }, [])

  const hasSourceFilter = selectedSources.length !== DEFAULT_SELECTED_SOURCES.length
    || selectedSources.some(source => !DEFAULT_SELECTED_SOURCES.includes(source))
  const hasFilter = hasSourceFilter || textFilter !== "" || dateFrom !== "" || dateTo !== ""
  const top5 = useMemo(() => topSources(rawEvents, 5), [rawEvents])

  // Filter sources in the dropdown by search text.
  const filteredSources = useMemo(() => {
    const q = sourceSearch.toLowerCase()
    return q ? ALL_SOURCES.filter(s => s.id.toLowerCase().includes(q) || s.label.toLowerCase().includes(q)) : ALL_SOURCES
  }, [sourceSearch])

  return (
    <div className="flex w-full flex-col gap-3">
      <PageHeader
        title="Event Stream"
        description={
          connected
            ? top5.length > 0
              ? (
                <span className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
                  <span>
                    {rawEvents.length.toLocaleString()} event{rawEvents.length !== 1 ? "s" : ""}
                  </span>
                  {top5.map(s => (
                    <span key={s.id} className="text-fg-subtle">
                      <span className="font-medium text-fg-muted">{s.count}</span>{" "}
                      {s.label}
                    </span>
                  ))}
                </span>
              )
              : "Waiting for events…"
            : "Not connected"
        }
      />

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-2">
        {/* Free text search */}
        <div className="relative">
          <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-subtle" />
          <Input
            ref={searchRef}
            placeholder={hasFilter ? "Filtered…" : "Filter events…"}
            value={textFilter}
            onChange={(e) => setTextFilter(e.target.value)}
            className="h-8 w-52 pl-7 pr-7 text-xs"
          />
          {textFilter && (
            <button
              className="absolute right-2 top-1/2 -translate-y-1/2 text-fg-subtle hover:text-fg"
              onClick={() => setTextFilter("")}
              aria-label="Clear text filter"
            >
              <X className="h-3 w-3" />
            </button>
          )}
        </div>

        {/* Source multi-select dropdown */}
        <div className="relative" ref={menuRef}>
          <Button
            variant="ghost"
            size="sm"
            className={cn("h-8 gap-1 text-xs", hasSourceFilter && "bg-fg/5 font-medium")}
            onClick={() => setSourceMenuOpen(o => !o)}
          >
            {(() => {
              if (selectedSources.length > 0) {
                return `${selectedSources.length} source${selectedSources.length !== 1 ? "s" : ""}`
              }
              return "No sources"
            })()}
            <ChevronDown className="h-3 w-3" />
          </Button>

          {sourceMenuOpen && (
            <div className="absolute left-0 top-full z-50 mt-1 w-56 rounded-lg border border-border bg-bg-elevated shadow-lg">
              {selectedSources.length > 0 && (
                <button
                  className="flex w-full items-center gap-2 rounded-t-lg px-3 py-1.5 text-xs text-fg-muted hover:bg-fg/5"
                  onClick={() => setSelectedSources([])}
                >
                  Clear selection
                </button>
              )}

              <div className="mx-2 h-px bg-border" />

              {/* Search within sources */}
              <div className="px-2 py-1">
                <Input
                  placeholder="Search sources…"
                  value={sourceSearch}
                  onChange={(e) => setSourceSearch(e.target.value)}
                  className="h-7 text-xs"
                  onClick={(e) => e.stopPropagation()}
                />
              </div>

              {/* Source list */}
              <div className="max-h-64 overflow-y-auto">
                {filteredSources.length === 0 ? (
                  <div className="px-3 py-4 text-center text-xs text-fg-subtle">No sources match</div>
                ) : (
                  filteredSources.map(s => (
                    <button
                      key={s.id}
                      role="checkbox"
                      aria-checked={selectedSources.includes(s.id)}
                      className="flex w-full items-center gap-2 px-3 py-1.5 text-xs hover:bg-fg/5"
                      onClick={() => toggleSource(s.id)}
                    >
                      <span className={cn(
                        "flex h-4 w-4 shrink-0 items-center justify-center rounded border",
                        selectedSources.includes(s.id)
                          ? "border-accent bg-accent text-bg"
                          : "border-border",
                      )}>
                        {selectedSources.includes(s.id) && <Check className="h-3 w-3" />}
                      </span>
                      <span className="flex-1 text-left">{s.label}</span>
                    </button>
                  ))
                )}
              </div>
            </div>
          )}
        </div>

        {/* Date filters */}
        <Input
          type="datetime-local"
          value={dateFrom}
          onChange={(e) => setDateFrom(e.target.value)}
          className={cn("h-8 w-40 text-xs", dateFrom && "border-accent/40")}
          aria-label="Filter from date"
        />
        <Input
          type="datetime-local"
          value={dateTo}
          onChange={(e) => setDateTo(e.target.value)}
          className={cn("h-8 w-40 text-xs", dateTo && "border-accent/40")}
          aria-label="Filter to date"
        />

        <span className="mx-0.5 h-5 w-px bg-border" />

        {/* Pause */}
        <Button
          variant={paused ? "secondary" : "ghost"}
          size="sm"
          className="h-8 gap-1 text-xs"
          onClick={() => {
            if (!paused) setFrozenEvents(events)
            setPaused((v) => !v)
          }}
        >
          {paused ? <Play className="h-3.5 w-3.5 text-green-400" /> : <Pause className="h-3.5 w-3.5" />}
          {paused ? "Resume" : "Pause"}
        </Button>

        {/* Heartbeats */}
        <Button
          variant={showHeartbeats ? "secondary" : "ghost"}
          size="sm"
          className="h-8 gap-1 text-xs"
          onClick={() => setShowHeartbeats(v => !v)}
          title={showHeartbeats ? "Hide heartbeat pings" : "Show heartbeat pings"}
        >
          <Heart className={cn("h-3.5 w-3.5", showHeartbeats && "text-fg-muted")} />
          {showHeartbeats ? "Pings" : null}
        </Button>

        {/* Clear filters — always visible */}
        <Button
          variant="ghost"
          size="sm"
          className="h-8 gap-1 text-xs"
          disabled={!hasFilter}
          onClick={clearFilters}
        >
          <X className="h-3 w-3" />
          Clear
        </Button>
      </div>

      <EventConsole
        events={displayedEvents}
        connected={connected}
        onClear={clear}
        paused={paused}
      />
    </div>
  )
}

export { Activity as EventsIcon }
