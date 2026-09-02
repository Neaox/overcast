import { ServiceIconTile } from "@/components/service/service-icon-tile"
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

  return (
    <ServiceTile
      entry={entry}
      onNavigate={onNavigate}
      className="flex items-center gap-2.5 rounded-card border border-border p-3"
      interactiveClassName="transition-colors hover:border-accent hover:bg-bg-elevated focus-visible:outline-accent"
    >
      <ServiceIconTile service={service} variant="outline" size={26} iconSize={15} />
      <span className="flex min-w-0 flex-col gap-px">
        <span className="truncate font-mono text-xs font-bold text-fg-muted">{service.label}</span>
        <TierMeta tier={tier} />
      </span>
    </ServiceTile>
  )
}
