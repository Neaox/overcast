import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { ChevronDown } from "lucide-react"
import { Tooltip } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { CATALOG, CATALOG_CATEGORY_LABELS, type CatalogEntry } from "@/lib/unsupported-services"
import type { ServiceTierEntry } from "../service-defs"
import { TIER_DESCRIPTIONS } from "../tiers"

const CHIP_CLASS =
  "flex items-center gap-[7px] rounded-control border border-dashed border-border bg-bg-elevated px-2.5 py-1.5 font-mono text-[11px] text-fg-subtle"

export function NotEmulatedChips({ entries }: { entries: ServiceTierEntry[] }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex flex-wrap gap-2">
        {entries.map((entry) => (
          <ServiceChip key={entry.service.name} entry={entry} />
        ))}
      </div>
      <OtherServicesExpander />
    </div>
  )
}

function ServiceChip({ entry }: { entry: ServiceTierEntry }) {
  const { service, tier, enabled } = entry
  const Icon = service.icon
  const content = (
    <>
      <Icon className="h-3.5 w-3.5" strokeWidth={1.75} />
      {service.label}
    </>
  )

  return (
    <Tooltip content={enabled ? TIER_DESCRIPTIONS[tier] : "Disabled in this emulator run."}>
      {enabled ? (
        <Link
          to={service.to}
          className={cn(
            CHIP_CLASS,
            "transition-colors hover:border-solid hover:border-accent hover:text-accent focus-visible:outline-accent",
          )}
        >
          {content}
        </Link>
      ) : (
        <span className={cn(CHIP_CLASS, "cursor-not-allowed opacity-60")}>{content}</span>
      )}
    </Tooltip>
  )
}

function OtherServicesExpander() {
  const [open, setOpen] = useState(false)

  const byCategory = CATALOG.reduce<Record<string, CatalogEntry[]>>((acc, entry) => {
    ;(acc[entry.category] ??= []).push(entry)
    return acc
  }, {})

  return (
    <div className="mt-1 rounded-control border border-border bg-bg-elevated">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2.5 text-left"
      >
        <span className="font-mono text-[11px] text-fg-muted">Other AWS Services</span>
        <span className="font-mono text-[11px] text-fg-subtle">{CATALOG.length}</span>
        <ChevronDown
          className={cn(
            "ml-auto h-3.5 w-3.5 text-fg-subtle transition-transform",
            open && "rotate-180",
          )}
        />
      </button>
      {open && (
        <div className="flex flex-col gap-5 border-t border-border px-3 py-4">
          {Object.entries(byCategory).map(([category, catalogEntries]) => (
            <div key={category}>
              <p className="mb-2 font-mono text-[9px] tracking-[0.16em] text-fg-subtle uppercase">
                {CATALOG_CATEGORY_LABELS[category as keyof typeof CATALOG_CATEGORY_LABELS]}
              </p>
              <div className="flex flex-wrap gap-2">
                {catalogEntries.map((catalogEntry) => (
                  <Link
                    key={catalogEntry.id}
                    to="/$service"
                    params={{ service: catalogEntry.id }}
                    className={cn(
                      CHIP_CLASS,
                      "transition-colors hover:border-solid hover:border-accent hover:text-accent focus-visible:outline-accent",
                    )}
                  >
                    {catalogEntry.label}
                  </Link>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
