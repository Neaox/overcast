import type { ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import { Tooltip } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import type { ServiceTierEntry } from "../service-defs"

/** Tooltip for a service that is emulated but switched off in this run. */
export const DISABLED_TOOLTIP = "Disabled in this emulator run."

/**
 * Shared wrapper for every dashboard service tile — card, compact card, chip.
 *
 * Being switched off is independent of how completely a service is emulated,
 * so it is expressed here rather than by which section a service lands in:
 * enabled services link to their page, disabled ones stay put, dimmed and
 * inert.
 */
export function ServiceTile({
  entry,
  onNavigate,
  className,
  enabledClassName,
  tooltip,
  role,
  children,
}: {
  entry: ServiceTierEntry
  onNavigate?: (key: string) => void
  /** Applied in both states. */
  className: string
  /** Applied only when the service is enabled — hover/focus affordances. */
  enabledClassName?: string
  /** Tooltip for the enabled state; the disabled state always explains itself. */
  tooltip?: ReactNode
  /** ARIA role for the tile itself, e.g. "row" inside the list view's table. */
  role?: string
  children: ReactNode
}) {
  const { service, enabled } = entry

  if (!enabled) {
    return (
      <Tooltip content={DISABLED_TOOLTIP}>
        <span role={role} className={cn(className, "cursor-not-allowed opacity-60")}>
          {children}
        </span>
      </Tooltip>
    )
  }

  const link = (
    <Link
      role={role}
      to={service.to}
      onClick={() => onNavigate?.(service.to)}
      className={cn(className, enabledClassName)}
    >
      {children}
    </Link>
  )

  return tooltip ? <Tooltip content={tooltip}>{link}</Tooltip> : link
}
