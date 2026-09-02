import { useState } from "react"
import { BookOpen } from "lucide-react"
import { cn } from "@/lib/utils"
import { ServiceDocsModal } from "@/features/docs/service-docs-modal"
import { ServiceIconTile } from "@/components/service/service-icon-tile"
import type { ServiceTierEntry } from "../service-defs"
import { ServiceTile } from "./service-tile"
import { TierBadge } from "./tier-badge"

export function ServiceCard({
  entry,
  onNavigate,
}: {
  entry: ServiceTierEntry
  onNavigate: (key: string) => void
}) {
  const { service, tier } = entry
  const [docsOpen, setDocsOpen] = useState(false)

  return (
    <>
      <ServiceTile
        entry={entry}
        onNavigate={onNavigate}
        className="group flex flex-col gap-3 rounded-card border border-border bg-bg-elevated p-3"
        interactiveClassName="transition-colors hover:border-accent focus-visible:outline-accent"
      >
        <div className="relative flex h-[30px] items-center justify-between">
          <ServiceIconTile service={service} />
          <span className={cn("transition-opacity", service.docKey && "group-hover:opacity-0")}>
            <TierBadge tier={tier} />
          </span>
          {service.docKey && (
            <button
              type="button"
              title={`View ${service.label} docs`}
              onClick={(e) => {
                e.preventDefault()
                e.stopPropagation()
                setDocsOpen(true)
              }}
              className="absolute top-1/2 right-0 flex -translate-y-1/2 items-center gap-1.5 rounded-control border border-accent px-2 py-1 font-mono text-2xs text-accent opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100"
            >
              <BookOpen className="h-[13px] w-[13px]" strokeWidth={1.75} />
              Docs
            </button>
          )}
        </div>
        <div className="flex min-w-0 flex-col gap-px">
          <span className="truncate font-mono text-[13px] font-bold text-fg">{service.label}</span>
          <span className="line-clamp-2 text-2xs text-fg-subtle">{service.description}</span>
        </div>
      </ServiceTile>
      {service.docKey && (
        <ServiceDocsModal
          service={service.docKey}
          label={service.label}
          open={docsOpen}
          onClose={() => setDocsOpen(false)}
        />
      )}
    </>
  )
}
