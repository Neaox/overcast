/**
 * EventConsole — generic virtualized event stream viewer.
 *
 * Renders a bounded, auto-scrolling, monospace console that shows events
 * received from useEventStream. Designed to stay open indefinitely — the
 * virtual list keeps DOM node count constant regardless of event count.
 *
 * Auto-scroll behaviour:
 *   - Pinned to bottom by default — new events scroll into view.
 *   - Scrolling up unpins; scrolling to the bottom re-pins.
 *
 * Generic — works for any event source. Per-source payload summaries are
 * provided by the renderSummary prop; a sensible default is used if omitted.
 */
import { useRef, useEffect, useState, useCallback } from "react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { X, Wifi, WifiOff } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import type { StreamEvent } from "@/hooks/use-event-stream"

// ─── Types ────────────────────────────────────────────────────────────────────

export interface EventConsoleProps {
  events: StreamEvent[]
  connected: boolean
  onClear: () => void
  /**
   * Optional function to produce a one-line summary string for an event's
   * payload. Return undefined to fall back to the default JSON truncation.
   */
  renderSummary?: (event: StreamEvent) => string | undefined
  className?: string
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

/** Default payload summary: key fields for known sources, truncated JSON otherwise. */
function defaultSummary(event: StreamEvent): string {
  const p = event.payload as Record<string, unknown> | null
  if (!p) return ""

  if (event.source === "s3") {
    const bucket = String(p.Bucket ?? "")
    const key = String(p.Key ?? "")
    const size = p.Size != null ? ` (${formatBytes(Number(p.Size))})` : ""
    return `s3://${bucket}/${key}${size}`
  }

  const raw = JSON.stringify(p)
  return raw.length > 120 ? raw.slice(0, 120) + "…" : raw
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

/** Short, human-readable event type label. */
function eventLabel(type: string): string {
  // "s3:ObjectCreated:*" → "ObjectCreated"
  const parts = type.split(":")
  if (parts.length >= 2) return parts.slice(1).join(":").replace(":*", "")
  return type
}

/** Tailwind color class for the event type badge. */
function eventColor(type: string): "default" | "success" | "danger" | "warning" {
  if (type.includes("Created") || type.includes("Put") || type.includes("Insert")) return "success"
  if (type.includes("Removed") || type.includes("Delete") || type.includes("Remove"))
    return "danger"
  if (type.includes("Modified") || type.includes("Modify")) return "warning"
  return "default"
}

/** Tailwind color class for the source badge. */
function sourceColor(source: string): string {
  const map: Record<string, string> = {
    s3: "text-orange-400",
    sqs: "text-yellow-400",
    sns: "text-pink-400",
    dynamodb: "text-blue-400",
    lambda: "text-purple-400",
  }
  return map[source.toLowerCase()] ?? "text-fg-muted"
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso)
    return d.toTimeString().slice(0, 12) // HH:MM:SS.mmm
  } catch {
    return iso
  }
}

// ─── Component ────────────────────────────────────────────────────────────────

export function EventConsole({
  events,
  connected,
  onClear,
  renderSummary,
  className,
}: EventConsoleProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [pinned, setPinned] = useState(true)
  const [expanded, setExpanded] = useState<number | null>(null)

  const virtualizer = useVirtualizer({
    count: events.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 34,
    measureElement: (el) => el.getBoundingClientRect().height,
    overscan: 20,
  })

  // Auto-scroll to bottom when new events arrive and we're pinned.
  useEffect(() => {
    if (pinned && events.length > 0) {
      virtualizer.scrollToIndex(events.length - 1, { align: "end" })
    }
  }, [events.length, pinned]) // eslint-disable-line react-hooks/exhaustive-deps

  const handleScroll = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    const atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - 32
    setPinned(atBottom)
  }, [])

  return (
    <div className={`flex flex-col gap-2 ${className ?? ""}`}>
      {/* Toolbar */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          {connected ? (
            <Wifi className="h-3.5 w-3.5 text-green-400" />
          ) : (
            <WifiOff className="h-3.5 w-3.5 text-fg-subtle" />
          )}
          <span className="text-xs text-fg-muted">
            {connected ? "Live" : "Disconnected"}
            {" · "}
            {events.length.toLocaleString()} event{events.length !== 1 ? "s" : ""}
          </span>
          {!pinned && (
            <button
              className="text-xs text-accent underline underline-offset-2"
              onClick={() => {
                setPinned(true)
                if (events.length > 0) {
                  virtualizer.scrollToIndex(events.length - 1, { align: "end" })
                }
              }}
            >
              ↓ scroll to latest
            </button>
          )}
        </div>
        <Button variant="ghost" size="icon-sm" title="Clear" onClick={onClear}>
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>

      {/* Console window */}
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="overflow-auto rounded-lg border border-border bg-[#0d0d0d] font-mono text-xs"
        style={{ height: "calc(100vh - 220px)", minHeight: 300 }}
      >
        {events.length === 0 ? (
          <div className="flex h-full items-center justify-center text-fg-subtle">
            {connected ? "Waiting for events…" : "Not connected"}
          </div>
        ) : (
          <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
            {virtualizer.getVirtualItems().map((vr) => {
              const ev = events[vr.index]
              const isExpanded = expanded === vr.index
              const summary = renderSummary?.(ev) ?? defaultSummary(ev)
              const label = eventLabel(ev.type)
              const color = eventColor(ev.type)

              return (
                <div
                  key={vr.key}
                  data-index={vr.index}
                  ref={virtualizer.measureElement}
                  style={{
                    position: "absolute",
                    top: 0,
                    left: 0,
                    width: "100%",
                    transform: `translateY(${vr.start}px)`,
                  }}
                  className="cursor-pointer border-b border-white/5 px-3 py-1.5 hover:bg-white/5"
                  onClick={() => setExpanded(isExpanded ? null : vr.index)}
                >
                  <div className="flex min-w-0 items-baseline gap-2">
                    <span className="shrink-0 text-xs text-fg-subtle tabular-nums">
                      {formatTime(ev.time)}
                    </span>
                    <span className={`shrink-0 text-xs font-semibold ${sourceColor(ev.source)}`}>
                      {ev.source}
                    </span>
                    <Badge variant={color} className="shrink-0 text-xs">
                      {label}
                    </Badge>
                    <span className="min-w-0 truncate text-sm text-fg-muted">{summary}</span>
                  </div>
                  {isExpanded && (
                    <pre className="mt-1 rounded bg-white/5 p-2 text-xs break-all whitespace-pre-wrap text-fg-muted">
                      {JSON.stringify(
                        { type: ev.type, time: ev.time, source: ev.source, payload: ev.payload },
                        null,
                        2,
                      )}
                    </pre>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
