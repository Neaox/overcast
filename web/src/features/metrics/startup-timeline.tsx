/**
 * StartupTimeline — visual breakdown of server startup phases.
 *
 * Renders a compact stacked bar showing phase groups. Clicking it opens a
 * dialog with a full per-phase flamegraph and a data table.
 */
import * as React from "react"
import { useRef, useState, useEffect, useCallback } from "react"
import type { StartupPhase } from "@/types"
import { cn } from "@/lib/utils"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogBody,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Tooltip } from "@/components/ui/tooltip"

// ─── Phase grouping ─────────────────────────────────────────────────────────

interface PhaseGroup {
  label: string
  color: string // Tailwind bg class
  textColor: string
  phases: StartupPhase[]
  totalMs: number
}

const GROUP_DEFS: Array<{
  label: string
  color: string
  textColor: string
  match: (name: string) => boolean
}> = [
  {
    label: "Go init",
    color: "bg-slate-500",
    textColor: "text-slate-300",
    match: (n) => n === "Go runtime + package init",
  },
  {
    label: "Config & store",
    color: "bg-violet-500",
    textColor: "text-violet-300",
    match: (n) =>
      n.startsWith("config") ||
      n.startsWith("logger") ||
      n.startsWith("buildStore") ||
      n.startsWith("hookRunner"),
  },
  {
    label: "Middleware & bus",
    color: "bg-sky-500",
    textColor: "text-sky-300",
    match: (n) => n.startsWith("middleware") || n.startsWith("bus") || n.startsWith("MCP"),
  },
  {
    label: "Service constructors",
    color: "bg-emerald-500",
    textColor: "text-emerald-300",
    match: (n) => n.trimStart().startsWith("new:") || n.startsWith("service constructors"),
  },
  {
    label: "Route registration",
    color: "bg-amber-500",
    textColor: "text-amber-300",
    match: (n) => n.trimStart().startsWith("routes:") || n.startsWith("RegisterRoutes"),
  },
  {
    label: "Cross-service wiring",
    color: "bg-rose-500",
    textColor: "text-rose-300",
    match: (n) => n.startsWith("cross-service") || n.startsWith("router.New (full)"),
  },
]

const FALLBACK_GROUP = { label: "Other", color: "bg-fg-muted", textColor: "text-fg-muted" }

function goPhases(phases: StartupPhase[]): StartupPhase[] {
  return phases.filter((p) => !p.environment)
}

function groupPhases(phases: StartupPhase[]): PhaseGroup[] {
  const groups: Map<string, PhaseGroup> = new Map()

  for (const phase of phases) {
    const def = GROUP_DEFS.find((g) => g.match(phase.name)) ?? FALLBACK_GROUP
    if (!groups.has(def.label)) {
      groups.set(def.label, {
        label: def.label,
        color: def.color,
        textColor: def.textColor,
        phases: [],
        totalMs: 0,
      })
    }
    const g = groups.get(def.label)!
    g.phases.push(phase)
    g.totalMs += phase.duration_ms
  }

  // Return in definition order so the bar is stable across renders.
  const ordered: PhaseGroup[] = []
  for (const def of [...GROUP_DEFS, FALLBACK_GROUP]) {
    const g = groups.get(def.label)
    if (g) ordered.push(g)
  }
  return ordered
}

// ─── Compact summary bar ─────────────────────────────────────────────────────

interface StartupBarProps {
  phases: StartupPhase[]
  totalMs: number
}

function StartupBar({ phases, totalMs }: StartupBarProps) {
  const groups = groupPhases(phases)

  return (
    <div
      className="flex h-4 w-full overflow-hidden rounded-sm"
      role="img"
      aria-label="Startup phase breakdown"
    >
      {groups.map((g) => {
        const pct = totalMs > 0 ? (g.totalMs / totalMs) * 100 : 0
        if (pct < 0.5) return null
        return (
          <Tooltip
            key={g.label}
            content={`${g.label}: ${g.totalMs.toFixed(1)} ms (${pct.toFixed(0)}%)`}
          >
            <div className={cn(g.color, "h-full shrink-0")} style={{ width: `${pct}%` }} />
          </Tooltip>
        )
      })}
    </div>
  )
}

// ─── Legend ──────────────────────────────────────────────────────────────────

function Legend({ phases, totalMs }: StartupBarProps) {
  const groups = groupPhases(phases)
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1">
      {groups.map((g) => (
        <div key={g.label} className="flex items-center gap-1.5">
          <span className={cn("h-2.5 w-2.5 shrink-0 rounded-sm", g.color)} />
          <span className="text-xs text-fg-muted">
            {g.label} <span className="text-fg tabular-nums">{g.totalMs.toFixed(1)} ms</span>
          </span>
        </div>
      ))}
      <div className="flex items-center gap-1.5">
        <span className="text-xs font-medium text-fg tabular-nums">
          = {totalMs.toFixed(0)} ms total
        </span>
      </div>
    </div>
  )
}

// ─── Phase descriptions ──────────────────────────────────────────────────────

/**
 * Rich descriptions shown in tooltips for well-known phase names.
 * Keyed on the trimmed phase name as reported by the profiler.
 */
const PHASE_DESCRIPTIONS: Record<string, string> = {
  "Go runtime + package init":
    "Approximate Go-side runtime bootstrap and package init time, anchored to the " +
    "earliest timestamp Overcast can capture from Go code. OS loader, antivirus, " +
    "container init, entrypoint, and exec time are reported separately as pre-Go environment time.",
  "config: parse":
    "Parse environment variables and build the typed Config struct. Validates " +
    "all settings and applies defaults.",
  buildStore:
    "Open and migrate the state backend (SQLite / hybrid / WAL / memory). " +
    "Schema migration runs on a background goroutine so only the first request " +
    "to a newly-created table pays the DDL cost.",
  "middleware: chain":
    "Construct the request middleware stack: RequestID, structured logger, " +
    "panic recovery, and SigV4 stub.",
  "bus: init":
    "Initialise the internal event bus used for cross-service notifications " +
    "(e.g. S3 → Lambda trigger fan-out).",
  "MCP: init": "Register the runtime MCP surface (/_mcp) and its tool implementations.",
}

// ─── Time axis ───────────────────────────────────────────────────────────────

function TimeAxis({ viewStart, viewEnd }: { viewStart: number; viewEnd: number }) {
  const span = Math.max(0.001, viewEnd - viewStart)
  // Pick a clean decimal tick interval that gives ~5–8 ticks.
  const target = span / 6
  const magnitude = Math.pow(10, Math.floor(Math.log10(Math.max(target, 0.001))))
  const norm = target / magnitude
  const interval = norm < 2 ? magnitude : norm < 5 ? 2 * magnitude : 5 * magnitude

  const ticks: number[] = []
  const first = Math.ceil((viewStart / interval) * (1 + Number.EPSILON)) * interval
  for (let t = first; t <= viewEnd + interval * 0.01; t += interval) {
    ticks.push(Math.round(t * 1000) / 1000)
  }

  return (
    <div className="relative h-6 border-b border-border/30 text-[9px] text-fg-muted select-none">
      {ticks.map((t) => {
        const pct = ((t - viewStart) / span) * 100
        if (pct < 0 || pct > 100) return null
        return (
          <div
            key={t}
            className="absolute bottom-0 flex flex-col items-center"
            style={{ left: `${pct}%`, transform: "translateX(-50%)" }}
          >
            <span className="tabular-nums">{t.toFixed(0)}ms</span>
            <div className="mt-px h-1.5 w-px bg-border/50" />
          </div>
        )
      })}
    </div>
  )
}

// ─── Minimap (summary bar with viewport window overlay) ─────────────────────

interface MiniMapProps extends StartupBarProps {
  viewStart: number
  viewEnd: number
  onSeek: (centerMs: number) => void
}

function MiniMap({ phases, totalMs, viewStart, viewEnd, onSeek }: MiniMapProps) {
  const isZoomed = viewStart > 0.5 || viewEnd < totalMs - 0.5
  const ref = useRef<HTMLDivElement>(null)
  const miniDragRef = useRef<{ startX: number; moved: boolean } | null>(null)

  const msAtX = useCallback(
    (clientX: number) => {
      if (!ref.current) return 0
      const rect = ref.current.getBoundingClientRect()
      const pct = Math.max(0, Math.min(1, (clientX - rect.left) / rect.width))
      return pct * totalMs
    },
    [totalMs],
  )

  const onMouseDown = useCallback(
    (e: React.MouseEvent) => {
      if (e.button !== 0) return
      e.preventDefault()
      miniDragRef.current = { startX: e.clientX, moved: false }
      onSeek(msAtX(e.clientX))
    },
    [msAtX, onSeek],
  )

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!miniDragRef.current) return
      if (!miniDragRef.current.moved && Math.abs(e.clientX - miniDragRef.current.startX) < 3) return
      miniDragRef.current.moved = true
      onSeek(msAtX(e.clientX))
    }
    const onUp = () => {
      miniDragRef.current = null
    }
    window.addEventListener("mousemove", onMove)
    window.addEventListener("mouseup", onUp)
    return () => {
      window.removeEventListener("mousemove", onMove)
      window.removeEventListener("mouseup", onUp)
    }
  }, [msAtX, onSeek])

  return (
    <div
      ref={ref}
      className="relative cursor-crosshair select-none"
      onMouseDown={onMouseDown}
      title={isZoomed ? "Click or drag to seek" : "Click or drag to seek"}
    >
      <StartupBar phases={phases} totalMs={totalMs} />
      {isZoomed && (
        <>
          {/* Dim regions outside the viewport */}
          <div
            className="pointer-events-none absolute inset-y-0 left-0 rounded-l-sm bg-black/50"
            style={{ width: `${(viewStart / totalMs) * 100}%` }}
          />
          <div
            className="pointer-events-none absolute inset-y-0 right-0 rounded-r-sm bg-black/50"
            style={{ width: `${((totalMs - viewEnd) / totalMs) * 100}%` }}
          />
          {/* Viewport window border */}
          <div
            className="pointer-events-none absolute inset-y-0 rounded-sm border-2 border-white/70"
            style={{
              left: `${(viewStart / totalMs) * 100}%`,
              width: `${((viewEnd - viewStart) / totalMs) * 100}%`,
            }}
          />
        </>
      )}
    </div>
  )
}

// ─── Flamegraph ───────────────────────────────────────────────────────────────

const ROW_H = 22
const AXIS_H = 26
// Max visible height for the rows area before it becomes internally scrollable.
// Keeps the flamegraph at a predictable size regardless of phase count.
const MAX_ROWS_H = 320

/**
 * If one phase dominates (>35% of total), start the viewport just before the
 * next phase so the interesting detail is immediately visible. The dominant
 * bar still shows as a clipped indicator on the left edge.
 */
function getDefaultRange(phases: StartupPhase[], totalMs: number): [number, number] {
  if (phases.length <= 1 || totalMs <= 0) return [0, totalMs]
  const sorted = [...phases].sort((a, b) => a.start_ms - b.start_ms)
  for (const p of sorted) {
    if (p.duration_ms > totalMs * 0.35) {
      const detailStart = p.start_ms + p.duration_ms
      const detailSpan = totalMs - detailStart
      // Show a bit of the dominant bar for context
      const viewStart = Math.max(0, detailStart - detailSpan * 0.1)
      return [viewStart, totalMs]
    }
  }
  return [0, totalMs]
}

interface FlameGraphProps {
  phases: StartupPhase[]
  totalMs: number
}

function FlameGraph({ phases, totalMs }: FlameGraphProps) {
  const [viewRange, setViewRange] = useState<[number, number]>(() =>
    getDefaultRange(phases, totalMs),
  )
  const containerRef = useRef<HTMLDivElement>(null)
  const rowsRef = useRef<HTMLDivElement>(null)
  const dragRef = useRef<{
    startX: number
    startY: number
    startScrollTop: number
    startRange: [number, number]
    moved: boolean
  } | null>(null)
  const [dragMoved, setDragMoved] = useState(false)

  const [viewStart, viewEnd] = viewRange
  const viewSpan = Math.max(0.001, viewEnd - viewStart)
  const isZoomed = viewStart > 0.5 || viewEnd < totalMs - 0.5
  const zoomLevel = totalMs / viewSpan

  const groups = groupPhases(phases)
  const ordered = groups
    .flatMap((g) => g.phases.map((p) => ({ ...p, group: g })))
    .sort((a, b) => a.start_ms - b.start_ms)

  const rowsH = ordered.length * ROW_H
  const visibleRowsH = Math.min(rowsH + 4, MAX_ROWS_H)
  const containerH = AXIS_H + visibleRowsH

  // ── Zoom helpers ────────────────────────────────────────────────────────────

  const clampRange = useCallback(
    (start: number, span: number): [number, number] => {
      const s = Math.max(0, Math.min(totalMs - span, start))
      return [s, Math.min(totalMs, s + span)]
    },
    [totalMs],
  )

  const zoomAtCursor = useCallback(
    (cursorPct: number, factor: number) => {
      setViewRange(([s, e]) => {
        const span = e - s
        const cursorMs = s + cursorPct * span
        const newSpan = Math.max(1, Math.min(totalMs, span * factor))
        return clampRange(cursorMs - cursorPct * newSpan, newSpan)
      })
    },
    [totalMs, clampRange],
  )

  const zoomToRange = useCallback(
    (start: number, end: number) => {
      const pad = Math.max((end - start) * 0.12, 1)
      const s = Math.max(0, start - pad)
      const e = Math.min(totalMs, end + pad)
      setViewRange([s, e])
    },
    [totalMs],
  )

  const resetZoom = useCallback(() => setViewRange([0, totalMs]), [totalMs])

  const seekTo = useCallback(
    (centerMs: number) => {
      setViewRange(([s, e]) => {
        const span = e - s
        return clampRange(centerMs - span / 2, span)
      })
    },
    [clampRange],
  )

  // ── Wheel handler on the rows area ──────────────────────────────────────────
  //
  // Attaching to rowsRef (not containerRef) means plain vertical scroll is
  // handled natively by the browser (scrolls the rows div). We only intercept:
  //   Ctrl/Cmd + scroll  → cursor-anchored zoom
  //   horizontal swipe   → pan

  useEffect(() => {
    const el = rowsRef.current
    if (!el) return
    const handler = (e: WheelEvent) => {
      const isZoomGesture = e.ctrlKey || e.metaKey
      const isPanGesture = !isZoomGesture && Math.abs(e.deltaX) > Math.abs(e.deltaY)
      if (!isZoomGesture && !isPanGesture) return // let browser scroll the rows div
      e.preventDefault()
      const rect = containerRef.current?.getBoundingClientRect()
      if (!rect) return
      const pct = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width))
      if (isZoomGesture) {
        zoomAtCursor(pct, e.deltaY > 0 ? 1.35 : 1 / 1.35)
      } else {
        setViewRange(([s, end]) => {
          const span = end - s
          const dMs = (e.deltaX / rect.width) * span
          return clampRange(s + dMs, span)
        })
      }
    }
    el.addEventListener("wheel", handler, { passive: false })
    return () => el.removeEventListener("wheel", handler)
  }, [zoomAtCursor, clampRange])

  // ── Drag-to-pan ─────────────────────────────────────────────────────────────

  const onMouseDown = useCallback(
    (e: React.MouseEvent) => {
      if (e.button !== 0) return
      dragRef.current = {
        startX: e.clientX,
        startY: e.clientY,
        startScrollTop: rowsRef.current?.scrollTop ?? 0,
        startRange: [viewStart, viewEnd],
        moved: false,
      }
      setDragMoved(false)
    },
    [viewStart, viewEnd],
  )

  const onMouseMove = useCallback(
    (e: MouseEvent) => {
      if (!dragRef.current || !containerRef.current) return
      const dx = e.clientX - dragRef.current.startX
      const dy = e.clientY - dragRef.current.startY
      if (!dragRef.current.moved && Math.sqrt(dx * dx + dy * dy) < 4) return
      dragRef.current.moved = true
      setDragMoved(true)
      const rect = containerRef.current.getBoundingClientRect()
      const dMs = -(dx / rect.width) * viewSpan
      const [s] = dragRef.current.startRange
      setViewRange(clampRange(s + dMs, viewSpan))
      if (rowsRef.current) {
        rowsRef.current.scrollTop = dragRef.current.startScrollTop - dy
      }
    },
    [viewSpan, clampRange],
  )

  const onMouseUp = useCallback(() => {
    dragRef.current = null
    setDragMoved(false)
  }, [])

  useEffect(() => {
    window.addEventListener("mousemove", onMouseMove)
    window.addEventListener("mouseup", onMouseUp)
    return () => {
      window.removeEventListener("mousemove", onMouseMove)
      window.removeEventListener("mouseup", onMouseUp)
    }
  }, [onMouseMove, onMouseUp])

  // ── Keyboard: Esc resets, ← → pans, +/- zooms ───────────────────────────────

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      // Only act when not typing in an input
      if ((e.target as HTMLElement).tagName === "INPUT") return
      if (e.key === "Escape" && isZoomed) {
        e.preventDefault()
        resetZoom()
      } else if (e.key === "ArrowLeft") {
        e.preventDefault()
        setViewRange(([s]) => clampRange(s - viewSpan * 0.2, viewSpan))
      } else if (e.key === "ArrowRight") {
        e.preventDefault()
        setViewRange(([s]) => clampRange(s + viewSpan * 0.2, viewSpan))
      } else if (e.key === "+" || e.key === "=") {
        zoomAtCursor(0.5, 1 / 2)
      } else if (e.key === "-") {
        zoomAtCursor(0.5, 2)
      }
    }
    window.addEventListener("keydown", handler)
    return () => window.removeEventListener("keydown", handler)
  }, [isZoomed, resetZoom, viewSpan, clampRange, zoomAtCursor])

  // ── Render ───────────────────────────────────────────────────────────────────

  return (
    <div className="space-y-2">
      {/* Minimap — summary bar with viewport overlay; click to seek */}
      <MiniMap
        phases={phases}
        totalMs={totalMs}
        viewStart={viewStart}
        viewEnd={viewEnd}
        onSeek={seekTo}
      />

      {/* Controls row */}
      <div className="flex items-center justify-between">
        <p className="text-[10px] font-medium tracking-wider text-fg-muted uppercase">
          Per-phase timeline
        </p>
        <div className="flex items-center gap-2">
          {isZoomed && (
            <span className="text-[10px] text-fg-muted tabular-nums">
              {viewStart.toFixed(1)}–{viewEnd.toFixed(1)} ms · {zoomLevel.toFixed(1)}×
            </span>
          )}
          <div className="flex items-center gap-1">
            <button
              onClick={() => zoomAtCursor(0.5, 1 / 2)}
              className="hover:bg-bg-card flex h-5 w-5 items-center justify-center rounded border border-border text-[12px] leading-none text-fg-muted hover:text-fg"
              title="Zoom in (scroll wheel also works)"
            >
              +
            </button>
            <button
              onClick={() => zoomAtCursor(0.5, 2)}
              disabled={!isZoomed}
              className="hover:bg-bg-card flex h-5 w-5 items-center justify-center rounded border border-border text-[12px] leading-none text-fg-muted hover:text-fg disabled:cursor-not-allowed disabled:opacity-30"
              title="Zoom out"
            >
              −
            </button>
            {isZoomed && (
              <button
                onClick={resetZoom}
                className="hover:bg-bg-card flex h-5 items-center rounded border border-border px-1.5 text-[10px] text-fg-muted hover:text-fg"
                title="Reset zoom (Esc)"
              >
                Reset
              </button>
            )}
          </div>
        </div>
      </div>

      {/* Flamegraph area — fixed height, no scroll; zoom handles navigation */}
      <div
        ref={containerRef}
        className="bg-bg-card relative w-full overflow-hidden rounded border border-border/40 select-none"
        style={{ height: containerH, cursor: dragMoved ? "grabbing" : "crosshair" }}
        onMouseDown={onMouseDown}
      >
        {/* Time axis */}
        <TimeAxis viewStart={viewStart} viewEnd={viewEnd} />

        {/* Phase rows — scrollable vertically when row count exceeds MAX_ROWS_H */}
        <div
          ref={rowsRef}
          className="absolute overflow-x-hidden overflow-y-auto"
          style={{ top: AXIS_H, left: 0, right: 0, height: visibleRowsH }}
        >
          <div className="relative" style={{ height: rowsH + 4 }}>
            {ordered.map((p, i) => {
              const rawLeft = (p.start_ms - viewStart) / viewSpan
              const rawRight = (p.start_ms + p.duration_ms - viewStart) / viewSpan
              const clampedLeft = Math.max(0, rawLeft)
              const clampedRight = Math.min(1, rawRight)
              if (clampedRight <= clampedLeft + 0.0002) return null

              const leftPct = clampedLeft * 100
              const widthPct = (clampedRight - clampedLeft) * 100
              const isClippedLeft = rawLeft < 0
              const pct = (p.duration_ms / totalMs) * 100
              const desc = PHASE_DESCRIPTIONS[p.name.trim()]

              const tooltipContent = (
                <div className="space-y-1.5">
                  <div className="font-medium">{p.name.trim()}</div>
                  <div className="text-fg-muted tabular-nums">
                    {p.duration_ms.toFixed(2)} ms &nbsp;·&nbsp; {pct.toFixed(1)}% of total
                  </div>
                  <div className="text-fg-muted tabular-nums">
                    starts at {p.start_ms.toFixed(2)} ms
                  </div>
                  {desc && (
                    <div className="max-w-65 border-t border-border/50 pt-1.5 text-[10px] leading-relaxed text-fg-muted">
                      {desc}
                    </div>
                  )}
                  {!desc && (
                    <div className="text-[10px] text-fg-muted/50 italic">Click to zoom in</div>
                  )}
                </div>
              )

              return (
                <Tooltip key={i} content={tooltipContent} side="top">
                  <div
                    className="absolute"
                    style={{
                      top: i * ROW_H,
                      height: ROW_H - 3,
                      left: `${leftPct}%`,
                      width: `${widthPct}%`,
                    }}
                    onClick={(e) => {
                      // Only zoom on click, not after a drag
                      if (!dragRef.current?.moved) {
                        e.stopPropagation()
                        zoomToRange(p.start_ms, p.start_ms + p.duration_ms)
                      }
                    }}
                  >
                    <div
                      className={cn("group relative flex h-full cursor-zoom-in items-center overflow-hidden px-1 transition-opacity", p.group.color, isClippedLeft ? "rounded-r-sm" : "rounded-sm", "opacity-70 hover:opacity-100")}
                    >
                      {widthPct > 3 && (
                        <span className="truncate text-[10px] leading-none font-medium text-white/90 select-none">
                          {p.name.trim()}
                        </span>
                      )}
                    </div>
                  </div>
                </Tooltip>
              )
            })}
          </div>
        </div>
      </div>

      {/* Hint row */}
      <p className="text-[10px] text-fg-muted/60">
        Ctrl+scroll to zoom · drag to pan · click a bar to focus &nbsp;
        {isZoomed ? "· Esc or Reset to zoom out" : "· ← → keys to pan when zoomed"}
      </p>
    </div>
  )
}

// ─── Data table ──────────────────────────────────────────────────────────────

type SortKey = "name" | "start_ms" | "duration_ms"
type SortDir = "asc" | "desc"

const COLUMNS: { key: SortKey; label: string; align: "left" | "right" }[] = [
  { key: "name", label: "Phase", align: "left" },
  { key: "start_ms", label: "Start (ms)", align: "right" },
  { key: "duration_ms", label: "Duration (ms)", align: "right" },
]

interface SortHeaderProps {
  col: (typeof COLUMNS)[number]
  active: boolean
  dir: SortDir
  onClick: () => void
}

function SortHeader({ col, active, dir, onClick }: SortHeaderProps) {
  return (
    <th
      onClick={onClick}
      className={cn(
        "cursor-pointer select-none pb-1 pr-3 font-medium tabular-nums transition-colors hover:text-fg",
        active && "text-fg",
        col.align === "right" && "text-right",
      )}
      aria-sort={active ? (dir === "asc" ? "ascending" : "descending") : undefined}
    >
      <span className="inline-flex items-center gap-0.5">
        {col.label}
        <span
          className={cn(
            "text-[9px] leading-none",
            active ? "opacity-100" : "opacity-0",
          )}
          aria-hidden
        >
          {dir === "asc" ? "▲" : "▼"}
        </span>
      </span>
    </th>
  )
}

function PhaseTable({ phases }: { phases: StartupPhase[] }) {
  const [sortKey, setSortKey] = useState<SortKey>("start_ms")
  const [sortDir, setSortDir] = useState<SortDir>("asc")

  const handleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"))
    } else {
      setSortKey(key)
      setSortDir(key === "name" ? "asc" : "desc")
    }
  }

  const sorted = [...phases].sort((a, b) => {
    let va: string | number
    let vb: string | number
    if (sortKey === "name") {
      va = a.name.trim()
      vb = b.name.trim()
    } else {
      va = a[sortKey]
      vb = b[sortKey]
    }
    if (va < vb) return sortDir === "asc" ? -1 : 1
    if (va > vb) return sortDir === "asc" ? 1 : -1
    return 0
  })

  return (
    <table className="w-full text-xs">
      <thead>
        <tr className="border-b border-border text-left text-fg-muted">
          {COLUMNS.map((col) => (
            <SortHeader
              key={col.key}
              col={col}
              active={sortKey === col.key}
              dir={sortDir}
              onClick={() => handleSort(col.key)}
            />
          ))}
        </tr>
      </thead>
      <tbody>
        {sorted.map((p, i) => (
          <tr key={i} className="border-b border-border/40 last:border-0">
            <td className="py-0.5 pr-3 font-mono text-fg">{p.name}</td>
            <td className="py-0.5 pr-3 text-right text-fg-muted tabular-nums">
              {p.start_ms.toFixed(1)}
            </td>
            <td className="py-0.5 text-right text-fg tabular-nums">{p.duration_ms.toFixed(2)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// ─── Public component ────────────────────────────────────────────────────────

interface StartupCardProps {
  totalMs: number
  preInitMs?: number
  phases?: StartupPhase[]
}

/**
 * StartupCard — replaces the plain StatPill for startup time.
 * Shows total ms + a compact phase bar. Clicking opens the full timeline dialog.
 */
export function StartupCard({ totalMs, preInitMs, phases }: StartupCardProps) {
  const hasPhases = Boolean(phases?.length)
  const visiblePhases = phases ? goPhases(phases) : []
  const environmentMs = phases?.find((p) => p.environment)?.duration_ms ?? preInitMs

  const pill = (
    <div
      className="bg-bg-card flex cursor-pointer flex-col gap-1 rounded-md border border-border px-3 py-2 transition-colors hover:border-border-muted hover:bg-bg-elevated"
      role="button"
      tabIndex={0}
      aria-label="Click to view startup timeline"
    >
      <span className="text-[10px] font-medium tracking-wider text-fg-muted uppercase">
        Startup
      </span>
      <span className="font-mono text-sm font-medium text-fg tabular-nums">
        {totalMs.toFixed(0)} ms
      </span>
      {environmentMs !== undefined && environmentMs > 0 && (
        <span className="text-[10px] text-fg-muted tabular-nums">
          + {environmentMs.toFixed(0)} ms pre-Go
        </span>
      )}
      {hasPhases && (
        <div className="mt-1 w-32">
          <StartupBar phases={visiblePhases} totalMs={totalMs} />
        </div>
      )}
    </div>
  )

  if (!hasPhases) {
    // No phase data yet — render plain pill without dialog trigger.
    return (
      <div className="bg-bg-card flex flex-col gap-0.5 rounded-md border border-border px-3 py-2">
        <span className="text-[10px] font-medium tracking-wider text-fg-muted uppercase">
          Startup
        </span>
        <span className="font-mono text-sm font-medium text-fg tabular-nums">
          {totalMs.toFixed(0)} ms
        </span>
        {environmentMs !== undefined && environmentMs > 0 && (
          <span className="text-[10px] text-fg-muted tabular-nums">
            + {environmentMs.toFixed(0)} ms pre-Go
          </span>
        )}
      </div>
    )
  }

  return (
    <Dialog>
      <DialogTrigger asChild>{pill}</DialogTrigger>
      <DialogContent className="max-w-3xl" aria-describedby="startup-timeline-desc">
        <DialogHeader>
          <DialogTitle>Startup Timeline — {totalMs.toFixed(0)} ms Go-side</DialogTitle>
        </DialogHeader>
        {/* Fixed section — never scrolls away */}
        <div className="shrink-0 space-y-4 pb-2">
          <p id="startup-timeline-desc" className="text-xs text-fg-muted">
            Time from Go startup to the server accepting requests, broken down by phase. Pre-Go
            environment time is shown separately so OS loader, antivirus, container init, entrypoint,
            and exec costs do not inflate the Go startup total. Hover any bar for details ·
            Ctrl+scroll to zoom · drag to pan · click or drag the minimap to seek.
          </p>

          {environmentMs !== undefined && (
            <div className="rounded border border-border/50 bg-bg-card/60 px-3 py-2 text-xs text-fg-muted">
              <span className="font-medium text-fg">Pre-Go environment:</span>{" "}
              <span className="font-mono tabular-nums">{environmentMs.toFixed(1)} ms</span>
            </div>
          )}

          {/* Legend */}
          <Legend phases={visiblePhases} totalMs={totalMs} />

          {/* Interactive flamegraph — fixed height, owns zoom/pan */}
          <FlameGraph phases={visiblePhases} totalMs={totalMs} />
        </div>

        {/* Only the data table scrolls */}
        <DialogBody className="border-t border-border/30">
          <div className="space-y-1 pt-4">
            <p className="text-[10px] font-medium tracking-wider text-fg-muted uppercase">
              Phase data
            </p>
            <PhaseTable phases={visiblePhases} />
          </div>
        </DialogBody>
      </DialogContent>
    </Dialog>
  )
}
