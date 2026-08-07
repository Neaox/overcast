import { useEffect, useRef, useState, useMemo } from "react"
import { ChevronDown, Check, Search } from "lucide-react"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

export interface CheckboxFilterItem {
  id: string
  label: string
  count?: number
}

interface CheckboxFilterDropdownProps {
  items: CheckboxFilterItem[]
  /** "hide" = checked means visible (deny-list, e.g. events). "show" = checked means filtered in (include-list, e.g. traces). */
  model: "hide" | "show"
  /** IDs whose semantics depend on `model`: hide-model = currently hidden; show-model = currently selected. */
  selected: Set<string>
  onToggle: (id: string) => void
  /** Make every item visible (hide-model: clear the hidden set; show-model: select all). */
  onShowAll: () => void
  /** Make every item hidden (hide-model: hide all; show-model: deselect all). */
  onHideAll: () => void
  triggerLabel: string
}

export function CheckboxFilterDropdown({
  items,
  model,
  selected,
  onToggle,
  onShowAll,
  onHideAll,
  triggerLabel,
}: CheckboxFilterDropdownProps) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState("")
  const menuRef = useRef<HTMLDivElement>(null)

  const filtered = useMemo(() => {
    if (!search.trim()) return items
    const lower = search.toLowerCase()
    return items.filter((s) => s.id.toLowerCase().includes(lower) || s.label.toLowerCase().includes(lower))
  }, [items, search])

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener("mousedown", handler)
    return () => document.removeEventListener("mousedown", handler)
  }, [open])

  const visibleCount = model === "hide"
    ? items.length - selected.size
    : selected.size

  return (
    <div className="relative" ref={menuRef}>
      <Button
        variant="ghost"
        size="sm"
        className={cn("h-9 gap-1 text-xs border border-border", selected.size > 0 && model === "show" && "bg-fg/5 font-medium")}
        onClick={() => { setOpen((o) => !o); setSearch("") }}
      >
        {triggerLabel}
        <ChevronDown className="h-3 w-3" />
      </Button>

      {open && (
        <div className="absolute top-full left-0 z-50 mt-1 w-52 rounded-lg border border-border bg-bg-elevated shadow-lg">
          {visibleCount > 0 ? (
            <button className="flex w-full items-center gap-2 rounded-t-lg px-3 py-1.5 text-xs text-fg-muted hover:bg-fg/5" onClick={onHideAll}>
              {model === "hide" ? "Hide all" : "Deselect all"}
            </button>
          ) : (
            <button className="flex w-full items-center gap-2 rounded-t-lg px-3 py-1.5 text-xs text-fg-muted hover:bg-fg/5" onClick={onShowAll}>
              {model === "hide" ? "Show all" : "Select all"}
            </button>
          )}
          <div className="mx-2 h-px bg-border" />
          <div className="px-2 py-1">
            <div className="relative">
              <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3 w-3 text-fg-muted" />
              <Input
                placeholder="Search…"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="h-7 pl-7 text-xs"
                onClick={(e) => e.stopPropagation()}
              />
            </div>
          </div>
          <div className="max-h-56 overflow-y-auto">
            {filtered.length === 0 ? (
              <div className="px-3 py-4 text-center text-xs text-fg-subtle">No matches</div>
            ) : (
              filtered.map((s) => {
                const checked = model === "hide" ? !selected.has(s.id) : selected.has(s.id)
                return (
                  <button
                    key={s.id}
                    role="checkbox"
                    aria-checked={checked}
                    className="flex w-full items-center gap-2 px-3 py-1.5 text-xs hover:bg-fg/5"
                    onClick={() => onToggle(s.id)}
                  >
                    <span className={cn("flex h-4 w-4 shrink-0 items-center justify-center rounded border", checked ? "border-accent bg-accent text-bg" : "border-border")}>
                      {checked && <Check className="h-3 w-3" />}
                    </span>
                    <span className="flex-1 text-left truncate">{s.label}</span>
                    {s.count !== undefined && (
                      <span className="text-fg-muted tabular-nums">{s.count}</span>
                    )}
                  </button>
                )
              })
            )}
          </div>
        </div>
      )}
    </div>
  )
}
