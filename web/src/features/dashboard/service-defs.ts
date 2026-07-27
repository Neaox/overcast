import type { LucideIcon } from "lucide-react"
import type { FileRouteTypes } from "@/routeTree.gen"
import { SERVICES, type ServiceEntry } from "@/lib/service-registry"
import type { EmulationTier } from "@/types/common"

export interface ServiceCardDef {
  name: string
  label: string
  to: FileRouteTypes["to"]
  icon: LucideIcon
  description: string
  /** Filename stem in docs/services/{docKey}.md. Omit if no docs exist. */
  docKey?: string
}

/** A service paired with the emulator's live view of it. */
export interface ServiceTierEntry {
  service: ServiceCardDef
  tier: EmulationTier
  enabled: boolean
}

/** Every service that warrants a dashboard entry, derived from the registry. */
export const ALL_SERVICES: ServiceCardDef[] = Object.entries(
  SERVICES as Record<string, ServiceEntry>,
)
  .filter(([, e]) => e.dashboardCard !== false && e.to != null)
  .map(([name, e]) => ({
    name,
    label: e.dashboardLabel ?? e.label,
    to: e.to as FileRouteTypes["to"],
    icon: e.icon,
    description: e.dashboardDescription ?? e.description ?? "",
    ...(e.docKey ? { docKey: e.docKey } : {}),
  }))
