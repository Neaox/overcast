import { LayoutGrid, List } from "lucide-react"
import { cn } from "@/lib/utils"

export type DashboardView = "grid" | "list"

const VIEWS = [
  { value: "grid", label: "Grid view", icon: LayoutGrid },
  { value: "list", label: "List view", icon: List },
] as const satisfies readonly { value: DashboardView; label: string; icon: typeof LayoutGrid }[]

export function DashboardHeader({
  totalCount,
  view,
  onViewChange,
}: {
  totalCount: number
  view: DashboardView
  onViewChange: (view: DashboardView) => void
}) {
  return (
    <div className="mb-6 flex items-center gap-4">
      <h1 className="font-mono text-[22px] font-bold tracking-[-0.02em] text-fg">Dashboard</h1>
      <span className="font-mono text-xs text-fg-muted">
        {totalCount} services
      </span>
      <div className="ml-auto flex items-center gap-1 rounded-control border border-border bg-bg-elevated p-[3px]">
        {VIEWS.map(({ value, label, icon: Icon }) => (
          <button
            key={value}
            type="button"
            aria-label={label}
            aria-pressed={view === value}
            onClick={() => onViewChange(value)}
            className={cn(
              "flex h-6 w-7 items-center justify-center rounded-[4px] transition-colors",
              view === value ? "bg-accent-muted text-accent" : "text-fg-subtle hover:text-accent",
            )}
          >
            <Icon className="h-3.5 w-3.5" strokeWidth={1.9} />
          </button>
        ))}
      </div>
    </div>
  )
}
