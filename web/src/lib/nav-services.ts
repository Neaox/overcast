/**
 * nav-services — sidebar and global search service list.
 *
 * All service metadata comes from service-registry. This module derives
 * the sidebar/search list by filtering to services where nav !== false
 * and both `to` and `category` are set.
 *
 * To add a new service: edit service-registry.ts only.
 */

import { LayoutDashboard, Network, BarChart2, Inbox, Activity, type LucideIcon } from "lucide-react"
import {
  SERVICES,
  type ServiceEntry,
  type ServiceCategory,
  type SubNavItem,
  type SubNavGroup,
  type SubNavChild,
  CATEGORY_LABELS,
  CATEGORY_ORDER,
} from "./service-registry"

export type { ServiceCategory, SubNavItem, SubNavGroup, SubNavChild }
export { CATEGORY_LABELS, CATEGORY_ORDER }

export interface ServiceDefinition {
  /** Unique key — matches the primary route path. */
  key: string
  to: string
  label: string
  icon: LucideIcon
  color: string
  category: ServiceCategory
  description: string
  children?: SubNavChild[]
  /** Whether users can favourite/pin this service. Defaults to true. */
  favouritable?: boolean
}

export const ALL_SERVICES: ServiceDefinition[] = Object.values(
  SERVICES as Record<string, ServiceEntry>,
)
  .filter(
    (e): e is ServiceEntry & { to: string; category: ServiceCategory } =>
      e.nav !== false && e.to != null && e.category != null,
  )
  .map((e) => {
    const def: ServiceDefinition = {
      key: e.to,
      to: e.to,
      label: e.label,
      icon: e.icon,
      color: e.color,
      category: e.category,
      description: e.description ?? "",
    }
    if (e.children) def.children = e.children
    if (e.favouritable !== undefined) def.favouritable = e.favouritable
    return def
  })

/** Dashboard item — always shown at the top of the sidebar. */
export const DASHBOARD_ITEM = {
  key: "/",
  to: "/",
  label: "Dashboard",
  icon: LayoutDashboard,
  color: "text-fg-muted",
}

/** Tool items — always pinned at the bottom of the sidebar. */
export const BOTTOM_ITEMS = [
  { key: "/map", to: "/map", label: "Map", icon: Network, color: "text-emerald-400" },
  { key: "/events", to: "/events", label: "Events", icon: Activity, color: "text-teal-400" },
  { key: "/metrics", to: "/metrics", label: "Metrics", icon: BarChart2, color: "text-sky-400" },
  { key: "/inbox", to: "/inbox", label: "Inbox", icon: Inbox, color: "text-amber-400" },
]
