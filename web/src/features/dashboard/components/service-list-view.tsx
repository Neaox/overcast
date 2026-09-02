import { fieldLabel, sectionLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import { useServiceIconColor } from "@/hooks/use-service-icon-color"
import type { ServiceTierEntry } from "../service-defs"
import { TIER_BADGE } from "../tiers"
import type { SectionTone } from "./dashboard-section"
import { ServiceTile } from "./service-tile"

const GRID = "grid grid-cols-[26px_200px_1fr_120px] gap-3 items-center"

const TONE_CLASS: Record<SectionTone, string> = {
  strong: "text-fg",
  muted: "text-fg-muted",
  subtle: "text-fg-subtle",
}

const COLUMNS = ["", "service", "scope", "tier"]

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
          fieldLabel,
          "border-b border-border bg-bg px-4 py-[7px] text-fg-subtle",
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
              sectionLabel,
              "border-b border-border bg-bg px-4 py-1.5",
              TONE_CLASS[group.tone],
            )}
          >
            <span role="rowheader" className="col-span-4">
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
  const { service, tier } = entry
  const Icon = service.icon
  const { enabled: colorEnabled } = useServiceIconColor()
  return (
    <ServiceTile
      entry={entry}
      onNavigate={onNavigate}
      role="row"
      className={cn(GRID, "border-b border-border px-4 py-2 last:border-b-0")}
      interactiveClassName="transition-colors hover:bg-accent-muted focus-visible:outline-accent"
    >
      <span role="cell">
        <Icon
          className={cn("h-4 w-4", colorEnabled ? service.color : "text-accent")}
          strokeWidth={1.75}
        />
      </span>
      <span role="cell" className="truncate font-mono text-[13px] font-bold text-fg">
        {service.label}
      </span>
      <span role="cell" className="truncate text-xs text-fg-subtle">
        {service.description}
      </span>
      <span role="cell" className="font-mono text-xs text-accent">
        {TIER_BADGE[tier]?.label ?? "Full"}
      </span>
    </ServiceTile>
  )
}
