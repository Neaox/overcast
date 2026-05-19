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
import { cn } from "@/lib/utils"

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
  /** When true, auto-scroll is disabled and a "Paused" indicator is shown. */
  paused?: boolean
  className?: string
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

/** Default payload summary: key fields for known sources, truncated JSON otherwise. */
function defaultSummary(event: StreamEvent): string {
  const p = event.payload as Record<string, unknown> | null
  if (!p) return ""

  if (event.type === "service:Error") {
    const service = String(p.service ?? event.source ?? "")
    const op = p.operation ? String(p.operation) + ": " : ""
    const msg = String(p.message ?? "")
    return `${service}: ${op}${msg}`
  }

  if (event.source === "request") {
    const method = String(p.method ?? "")
    const path = String(p.path ?? "")
    const status = p.status != null ? String(p.status) : ""
    const dur = p.durationUs != null ? formatDuration(Number(p.durationUs)) : ""
    return `${method} ${path} ${status} ${dur}`.trim()
  }

  if (event.source === "s3") {
    const bucket = String(p.Bucket ?? "")
    const key = String(p.Key ?? "")
    const size = p.Size != null ? ` (${formatBytes(Number(p.Size))})` : ""
    return `s3://${bucket}/${key}${size}`
  }

  // ESM delivery events — produced by the emulator's esmDeliveryManager.
  if (event.type === "lambda:ESMInvoked") {
    const fn = String(p.functionName ?? "")
    const src = String(p.eventSource ?? "")
    const count = Number(p.recordCount ?? 1)
    const name = String(p.eventName ?? "")
    const nameStr = name ? ` [${name}]` : ""
    return `${src} → ${fn}${nameStr} · ${count} record${count !== 1 ? "s" : ""}`
  }

  if (event.type === "lambda:ESMRecordFiltered") {
    const fn = String(p.functionName ?? "")
    const src = String(p.eventSource ?? "")
    const count = Number(p.recordCount ?? 1)
    const patterns = p.filterPatterns as string[] | undefined
    const name = String(p.eventName ?? "")
    const nameStr = name ? ` [${name}]` : ""
    // Show the first (and usually only) pattern so the reason is immediately visible.
    const reasonStr =
      patterns && patterns.length > 0
        ? ` · no match: ${patterns[0].length > 60 ? patterns[0].slice(0, 60) + "…" : patterns[0]}`
        : ""
    return `${src} → ${fn}${nameStr} filtered ${count} record${count !== 1 ? "s" : ""}${reasonStr}`
  }

  if (event.type === "lambda:ImagePulling") {
    const image = String(p.image ?? "")
    return `Pulling image: ${image}`
  }

  if (event.type === "lambda:ImagePullComplete") {
    const image = String(p.image ?? "")
    const errMsg = p.error ? String(p.error) : ""
    const elapsedMs = Number(p.elapsedMs ?? 0)
    const elapsed = elapsedMs >= 1000 ? `${(elapsedMs / 1000).toFixed(1)}s` : `${elapsedMs}ms`
    if (errMsg) return `Image pull failed: ${image} — ${errMsg}`
    return `Image ready: ${image} (${elapsed})`
  }

  if (event.type === "dynamodb:StreamRecord") {
    const table = String(p.table ?? "")
    const name = String(p.eventName ?? "")
    const ddb = p.dynamodb as Record<string, unknown> | undefined
    // Surface a key attribute from NewImage or Keys so the record is identifiable at a glance.
    const image = (ddb?.NewImage ?? ddb?.OldImage ?? ddb?.Keys) as
      | Record<string, unknown>
      | undefined
    const firstKey = image ? Object.keys(image)[0] : undefined
    const firstVal = firstKey
      ? ((image![firstKey] as Record<string, string> | undefined)?.S ??
        (image![firstKey] as Record<string, string> | undefined)?.N)
      : undefined
    const hint = firstKey && firstVal ? ` · ${firstKey}=${firstVal}` : ""
    return `${table} ${name}${hint}`
  }

  const raw = JSON.stringify(p)
  return raw.length > 120 ? raw.slice(0, 120) + "…" : raw
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

/** Format microsecond duration for human readability. */
function formatDuration(us: number): string {
  if (us < 1000) return `${us}µs`
  if (us < 1_000_000) return `${(us / 1000).toFixed(1)}ms`
  return `${(us / 1_000_000).toFixed(2)}s`
}

/** Short, human-readable event type label. */
function eventLabel(type: string): string {
  // "s3:ObjectCreated:*" → "ObjectCreated"
  const parts = type.split(":")
  if (parts.length >= 2) return parts.slice(1).join(":").replace(":*", "")
  return type
}

/** Color variant for the event type badge, driven by action semantics. */
function eventColor(type: string): "default" | "success" | "danger" | "warning" {
  if (type === "request:Received") return "default"
  if (type === "service:Error") return "danger"
  // ESM-specific colours — check before generic pattern matching.
  if (type === "lambda:ESMRecordFiltered") return "warning"
  if (type === "lambda:ESMInvoked") return "success"
  // Image pull events — pulling is informational, complete is success.
  if (type === "lambda:ImagePulling") return "warning"
  if (type === "lambda:ImagePullComplete") return "success"
  if (
    type.includes("Created") ||
    type.includes("Put") ||
    type.includes("Insert") ||
    type.includes("Started") ||
    type.includes("Launched") ||
    type.includes("Registered")
  )
    return "success"
  if (
    type.includes("Removed") ||
    type.includes("Delete") ||
    type.includes("Remove") ||
    type.includes("Died") ||
    type.includes("OOM") ||
    type.includes("Failed")
  )
    return "danger"
  if (
    type.includes("Modified") ||
    type.includes("Modify") ||
    type.includes("Updated") ||
    type.includes("Stopped")
  )
    return "warning"
  return "default"
}

/** Tailwind color class for the source badge. */
function sourceColor(source: string): string {
  const map: Record<string, string> = {
    request: "text-cyan-400",
    s3: "text-orange-400",
    sqs: "text-yellow-400",
    sns: "text-pink-400",
    dynamodb: "text-blue-400",
    lambda: "text-purple-400",
    kinesis: "text-cyan-300",
    pipes: "text-cyan-400",
    logs: "text-teal-400",
    ec2: "text-sky-400",
    ecs: "text-emerald-400",
    rds: "text-violet-400",
    iam: "text-yellow-300",
    sts: "text-slate-300",
    ssm: "text-orange-300",
    kms: "text-amber-400",
    secretsmanager: "text-red-400",
    ses: "text-amber-500",
    cloudformation: "text-cyan-300",
    cloudfront: "text-purple-300",
    apigateway: "text-green-300",
    appsync: "text-pink-300",
    cognito: "text-indigo-400",
    eventbridge: "text-rose-400",
    stepfunctions: "text-teal-300",
    elasticache: "text-green-500",
    ecr: "text-rose-400",
    msk: "text-sky-500",
    docker: "text-blue-300",
    inbox: "text-fg-muted",
  }
  return map[source.toLowerCase()] ?? "text-fg-muted"
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso)
    const hh = String(d.getUTCHours()).padStart(2, "0")
    const mm = String(d.getUTCMinutes()).padStart(2, "0")
    const ss = String(d.getUTCSeconds()).padStart(2, "0")
    const ms = String(d.getUTCMilliseconds()).padStart(3, "0")
    return `${hh}:${mm}:${ss}.${ms}`
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
  paused = false,
  className,
}: EventConsoleProps) {
  "use no memo"
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

  // Auto-scroll to bottom when new events arrive and we're pinned and not paused.
  useEffect(() => {
    if (!paused && pinned && events.length > 0) {
      virtualizer.scrollToIndex(events.length - 1, { align: "end" })
    }
  }, [events.length, pinned, paused]) // eslint-disable-line react-hooks/exhaustive-deps

  const handleScroll = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    const atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - 32
    setPinned(atBottom)
  }, [])

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      {/* Toolbar */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          {connected ? (
            <Wifi className="h-3.5 w-3.5 text-green-400" />
          ) : (
            <WifiOff className="h-3.5 w-3.5 text-fg-subtle" />
          )}
          <span className="text-xs text-fg-muted">
            {paused ? "Paused" : connected ? "Live" : "Disconnected"}
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
                    <span className={cn("shrink-0 text-xs font-semibold", sourceColor(ev.source))}>
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
