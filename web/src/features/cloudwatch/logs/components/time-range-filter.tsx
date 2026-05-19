import { useState, useRef, useEffect, useCallback } from "react"
import { createPortal } from "react-dom"
import { Clock, ChevronDown } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

// ─── Types ─────────────────────────────────────────────────────────────────────

export interface TimeRange {
  startTime?: number
  endTime?: number
}

type RelativePreset = { label: string; shortLabel: string; ms: number }

type PresetCategory = { label: string; unit: string; presets: RelativePreset[] }

const PRESET_CATEGORIES: PresetCategory[] = [
  {
    label: "Minutes",
    unit: "m",
    presets: [
      { label: "Last 5 minutes", shortLabel: "5m", ms: 5 * 60 * 1000 },
      { label: "Last 15 minutes", shortLabel: "15m", ms: 15 * 60 * 1000 },
      { label: "Last 30 minutes", shortLabel: "30m", ms: 30 * 60 * 1000 },
      { label: "Last 45 minutes", shortLabel: "45m", ms: 45 * 60 * 1000 },
    ],
  },
  {
    label: "Hours",
    unit: "h",
    presets: [
      { label: "Last 1 hour", shortLabel: "1h", ms: 1 * 60 * 60 * 1000 },
      { label: "Last 3 hours", shortLabel: "3h", ms: 3 * 60 * 60 * 1000 },
      { label: "Last 6 hours", shortLabel: "6h", ms: 6 * 60 * 60 * 1000 },
      { label: "Last 12 hours", shortLabel: "12h", ms: 12 * 60 * 60 * 1000 },
    ],
  },
  {
    label: "Days",
    unit: "d",
    presets: [
      { label: "Last 1 day", shortLabel: "1d", ms: 24 * 60 * 60 * 1000 },
      { label: "Last 3 days", shortLabel: "3d", ms: 3 * 24 * 60 * 60 * 1000 },
      { label: "Last 7 days", shortLabel: "7d", ms: 7 * 24 * 60 * 60 * 1000 },
    ],
  },
  {
    label: "Weeks",
    unit: "w",
    presets: [
      { label: "Last 2 weeks", shortLabel: "2w", ms: 14 * 24 * 60 * 60 * 1000 },
      { label: "Last 4 weeks", shortLabel: "4w", ms: 28 * 24 * 60 * 60 * 1000 },
    ],
  },
]

const ALL_PRESETS = PRESET_CATEGORIES.flatMap((c) => c.presets)

type Tab = "relative" | "absolute"

// ─── Helpers ───────────────────────────────────────────────────────────────────

function toDatetimeLocal(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function matchesPreset(range: TimeRange, preset: RelativePreset): boolean {
  if (!range.startTime || range.endTime) return false
  return Math.abs(Date.now() - range.startTime - preset.ms) < 60_000
}

// ─── Component ─────────────────────────────────────────────────────────────────

interface TimeRangeFilterProps {
  value: TimeRange
  onChange: (range: TimeRange) => void
}

export function TimeRangeFilter({ value, onChange }: TimeRangeFilterProps) {
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState({ top: 0, right: 0 })

  const label = getLabel(value)
  const hasRange = value.startTime != null || value.endTime != null

  const updatePosition = useCallback(() => {
    if (!triggerRef.current) return
    const rect = triggerRef.current.getBoundingClientRect()
    const right = window.innerWidth - rect.right
    setPos({ top: rect.bottom + 4, right: Math.max(right, 8) })
  }, [])

  const handleToggle = useCallback(() => {
    if (!open) updatePosition()
    setOpen(!open)
  }, [open, updatePosition])

  useEffect(() => {
    if (!open) return
    updatePosition()
    function handleClick(e: MouseEvent) {
      const target = e.target as Node
      if (triggerRef.current?.contains(target) || panelRef.current?.contains(target)) return
      setOpen(false)
    }
    document.addEventListener("mousedown", handleClick)
    return () => document.removeEventListener("mousedown", handleClick)
  }, [open, updatePosition])

  return (
    <>
      <Button
        ref={triggerRef}
        size="sm"
        variant="ghost"
        onClick={handleToggle}
        className={cn("h-7 gap-1 text-xs", hasRange && "text-fg")}
      >
        <Clock className="h-3.5 w-3.5" />
        {label}
        <ChevronDown className={cn("h-3 w-3 transition-transform", open && "rotate-180")} />
      </Button>

      {open &&
        createPortal(
          <div
            ref={panelRef}
            style={{ position: "fixed", top: pos.top, right: pos.right, zIndex: 9999 }}
          >
            <DropdownPanel
              value={value}
              onChange={(range) => {
                onChange(range)
                setOpen(false)
              }}
            />
          </div>,
          document.body,
        )}
    </>
  )
}

function getLabel(range: TimeRange): string {
  if (!range.startTime && !range.endTime) return "All time"
  if (range.startTime && !range.endTime) {
    const elapsed = Date.now() - range.startTime
    for (const p of ALL_PRESETS) {
      if (Math.abs(elapsed - p.ms) < 60_000) return p.label
    }
  }
  const fmt = (ms: number) =>
    new Date(ms).toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    })
  if (range.startTime && range.endTime) return `${fmt(range.startTime)} – ${fmt(range.endTime)}`
  if (range.startTime) return `From ${fmt(range.startTime)}`
  return `Until ${fmt(range.endTime!)}`
}

// ─── Dropdown ──────────────────────────────────────────────────────────────────

function DropdownPanel({
  value,
  onChange,
}: {
  value: TimeRange
  onChange: (range: TimeRange) => void
}) {
  const initialTab: Tab = value.endTime ? "absolute" : "relative"
  const [tab, setTab] = useState<Tab>(initialTab)

  const [absStart, setAbsStart] = useState(
    value.startTime ? toDatetimeLocal(new Date(value.startTime)) : "",
  )
  const [absEnd, setAbsEnd] = useState(
    value.endTime ? toDatetimeLocal(new Date(value.endTime)) : "",
  )

  return (
    <div className="bg-bg-surface w-72 overflow-hidden rounded-lg border border-border shadow-lg">
      {/* Tab bar */}
      <div className="flex border-b border-border">
        <button
          className={cn(
            "flex-1 px-3 py-2 text-xs font-medium transition-colors",
            tab === "relative" ? "border-b-2 border-fg text-fg" : "text-fg-muted hover:text-fg",
          )}
          onClick={() => setTab("relative")}
        >
          Relative
        </button>
        <button
          className={cn(
            "flex-1 px-3 py-2 text-xs font-medium transition-colors",
            tab === "absolute" ? "border-b-2 border-fg text-fg" : "text-fg-muted hover:text-fg",
          )}
          onClick={() => setTab("absolute")}
        >
          Absolute
        </button>
      </div>

      {/* Tab content */}
      <div className="p-3">
        {tab === "relative" ? (
          <div className="flex flex-col gap-3">
            {/* "All time" option */}
            <button
              className={cn(
                "rounded-md px-3 py-1.5 text-left text-sm transition-colors",
                !value.startTime && !value.endTime
                  ? "bg-bg-muted font-medium text-fg"
                  : "text-fg-muted hover:bg-bg-muted hover:text-fg",
              )}
              onClick={() => onChange({})}
            >
              All time
            </button>

            {/* Categorized preset grid */}
            {PRESET_CATEGORIES.map((cat) => (
              <div key={cat.label} className="flex items-baseline gap-2">
                <span className="w-14 shrink-0 text-xs text-fg-muted">{cat.label}</span>
                <div className="flex flex-1 flex-wrap gap-1.5">
                  {cat.presets.map((p) => {
                    const active = matchesPreset(value, p)
                    const num = p.shortLabel.replace(/[a-z]/g, "")
                    return (
                      <button
                        key={p.shortLabel}
                        className={cn(
                          "rounded-md px-2.5 py-1 text-xs font-medium tabular-nums transition-colors",
                          active
                            ? "bg-fg text-bg"
                            : "bg-bg-muted text-fg-muted hover:bg-border hover:text-fg",
                        )}
                        onClick={() => onChange({ startTime: Date.now() - p.ms })}
                        title={p.label}
                      >
                        {num}
                      </button>
                    )
                  })}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1">
              <label className="text-xs font-medium text-fg-muted">Start date and time</label>
              <Input
                type="datetime-local"
                className="h-8 text-sm"
                value={absStart}
                onChange={(e) => setAbsStart(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-xs font-medium text-fg-muted">End date and time</label>
              <Input
                type="datetime-local"
                className="h-8 text-sm"
                value={absEnd}
                onChange={(e) => setAbsEnd(e.target.value)}
              />
            </div>
            <Button
              size="sm"
              className="h-8 w-full"
              disabled={!absStart && !absEnd}
              onClick={() => {
                const range: TimeRange = {}
                if (absStart) range.startTime = new Date(absStart).getTime()
                if (absEnd) range.endTime = new Date(absEnd).getTime()
                onChange(range)
              }}
            >
              Apply
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}
