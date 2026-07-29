import type { ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import { Tooltip } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import type { ServiceTierEntry } from "../service-defs"

/** Shared wrapper for every dashboard service tile — card, compact card, chip. */
export function ServiceTile({
  entry,
  onNavigate,
  className,
  interactiveClassName,
  tooltip,
  role,
  children,
}: {
  entry: ServiceTierEntry
  onNavigate?: (key: string) => void
  /** Applied in both states. */
  className: string
  /** Hover/focus affordances, merged with className. */
  interactiveClassName?: string
  tooltip?: ReactNode
  /** ARIA role for the tile itself, e.g. "row" inside the list view's table. */
  role?: string
  children: ReactNode
}) {
  const { service } = entry

  const link = (
    <Link
      role={role}
      to={service.to}
      onClick={() => onNavigate?.(service.to)}
      className={cn(className, interactiveClassName)}
    >
      {children}
    </Link>
  )

  return tooltip ? <Tooltip content={tooltip}>{link}</Tooltip> : link
}
