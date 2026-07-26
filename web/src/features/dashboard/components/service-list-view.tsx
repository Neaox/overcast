import type { ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import { cn } from "@/lib/utils"
import type { ServiceTierEntry } from "../service-defs"
import { TIER_BADGE } from "../tiers"
import type { SectionTone } from "./dashboard-section"

const GRID = "grid grid-cols-[26px_200px_1fr_120px_96px] gap-3 items-center"

const TONE_CLASS: Record<SectionTone, string> = {
  strong: "text-fg",
  muted: "text-fg-muted",
  subtle: "text-fg-subtle",
}

const COLUMNS = ["", "service", "scope", "tier", "state"]

export interface ServiceListGroup {
  title: string
  tone: SectionTone
  entries: ServiceTierEntry[]
}

export function ServiceListView({
  groups,
  onNavigate,
}: {
  groups: ServiceListGroup[]
  onNavigate: (key: string) => void
}) {
  return (
    <div
      role="table"
      aria-label="Services"
      className="overflow-hidden rounded-card border border-border bg-bg-elevated"
    >
      <div
        role="row"
        className={cn(
          GRID,
          "border-b border-border bg-bg px-4 py-[7px] font-mono text-[9px] tracking-[0.14em] text-fg-subtle uppercase",
        )}
      >
        {COLUMNS.map((column, i) => (
          <span role="columnheader" key={column || i}>
            {column}
          </span>
        ))}
      </div>
      {groups.map((group) => (
        <div key={group.title} role="rowgroup">
          <div
            role="row"
            className={cn(
              GRID,
              "border-b border-border bg-bg px-4 py-1.5 font-mono text-[9px] tracking-[0.16em] uppercase",
              TONE_CLASS[group.tone],
            )}
          >
            <span role="rowheader" className="col-span-5">
              {group.title} &middot; {group.entries.length}
            </span>
          </div>
          {group.entries.map((entry) => (
            <ServiceListRow key={entry.service.name} entry={entry} onNavigate={onNavigate} />
          ))}
        </div>
      ))}
    </div>
  )
}

function ServiceListRow({
  entry,
  onNavigate,
}: {
  entry: ServiceTierEntry
  onNavigate: (key: string) => void
}) {
  const { service, tier, enabled } = entry
  const Icon = service.icon
  const active = enabled && tier !== "stub" && tier !== "unsupported"

  const cells: ReactNode = (
    <>
      <span role="cell">
        <Icon
          className={cn("h-4 w-4", active ? "text-accent" : "text-fg-subtle")}
          strokeWidth={1.75}
        />
      </span>
      <span
        role="cell"
        className={cn(
          "truncate font-mono text-[13px]",
          active ? "font-bold text-fg" : "text-fg-muted",
        )}
      >
        {service.label}
      </span>
      <span role="cell" className="truncate text-xs text-fg-subtle">
        {service.description}
      </span>
      <span
        role="cell"
        className={cn("font-mono text-xs", active ? "text-accent" : "text-fg-subtle")}
      >
        {TIER_BADGE[tier]?.label ?? "Full"}
      </span>
      <span
        role="cell"
        className="flex items-center gap-1.5 font-mono text-[10px] tracking-[0.1em] uppercase"
      >
        {enabled && <span className="h-1.5 w-1.5 rounded-full bg-success" />}
        <span className={cn(enabled ? "text-fg-muted" : "text-fg-subtle")}>
          {enabled ? "active" : "off"}
        </span>
      </span>
    </>
  )

  const className = cn(GRID, "border-b border-border px-4 py-2 last:border-b-0")

  return enabled ? (
    <Link
      role="row"
      to={service.to}
      onClick={() => onNavigate(service.to)}
      className={cn(
        className,
        "transition-colors hover:bg-accent-muted focus-visible:outline-accent",
      )}
    >
      {cells}
    </Link>
  ) : (
    <div role="row" className={cn(className, "opacity-60")}>
      {cells}
    </div>
  )
}
