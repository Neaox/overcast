import type { ServiceTierEntry } from "../service-defs"
import { ServiceTile } from "./service-tile"
import { TierMeta } from "./tier-badge"

export function AvailableServiceCard({
  entry,
  onNavigate,
}: {
  entry: ServiceTierEntry
  onNavigate: (key: string) => void
}) {
  const { service, tier } = entry
  const Icon = service.icon

  return (
    <ServiceTile
      entry={entry}
      onNavigate={onNavigate}
      className="flex items-center gap-2.5 rounded-card border border-border p-3"
      enabledClassName="transition-colors hover:border-accent hover:bg-bg-elevated focus-visible:outline-accent"
    >
      <span className="flex h-[26px] w-[26px] flex-none items-center justify-center rounded-control border border-border text-fg-subtle">
        <Icon className="h-[15px] w-[15px]" strokeWidth={1.75} />
      </span>
      <span className="flex min-w-0 flex-col gap-px">
        <span className="truncate font-mono text-xs font-bold text-fg-muted">{service.label}</span>
        <TierMeta tier={tier} />
      </span>
    </ServiceTile>
  )
}
