import type { LucideIcon } from "lucide-react"
import { cn } from "@/lib/utils"
import { useServiceIconColor } from "@/hooks/use-service-icon-color"

/** The subset of ServiceEntry/ServiceDefinition a tile needs to paint itself. */
export interface ServiceVisual {
  icon: LucideIcon
  /** The service's ramp-slot text colour class — see service-registry.ts. */
  color: string
  /** The same ramp slot as a soft background tint, paired with `color`. */
  bg: string
  /** The same ramp slot as a hairline border, paired with `color`. */
  border: string
}

/**
 * A service's glyph in its own tinted tile — the one place every surface that
 * shows a service icon in a rounded square (dashboard cards, the global
 * search mega menu) renders it, so the categorical-colour switch
 * (`useServiceIconColor`) only has to be threaded through here.
 *
 * `variant="filled"` is the in-use dashboard card's tinted square.
 * `variant="outline"` is the available-service card's duller, not-yet-in-use
 * treatment — colour still returns on the glyph and hairline, but the tile
 * stays unfilled so that tier distinction (in-use vs available) survives the
 * colour toggle. Both fall back to the design system's neutral accent
 * treatment when the setting is off.
 */
export function ServiceIconTile({
  service,
  variant = "filled",
  size = 30,
  iconSize = 17,
  className,
}: {
  service: ServiceVisual
  variant?: "filled" | "outline"
  /** Tile edge length in px. */
  size?: number
  /** Icon glyph size in px. */
  iconSize?: number
  className?: string
}) {
  const { enabled } = useServiceIconColor()
  const Icon = service.icon

  const tintedFilled = cn(service.bg, service.border, service.color, "border")
  const tintedOutline = cn("border-border", service.border, service.color, "border")
  const neutralFilled = "border border-transparent bg-accent-muted text-accent"
  const neutralOutline = "border border-border text-fg-subtle"

  const paint = enabled
    ? variant === "filled"
      ? tintedFilled
      : tintedOutline
    : variant === "filled"
      ? neutralFilled
      : neutralOutline

  return (
    <span
      className={cn(
        "flex shrink-0 items-center justify-center rounded-control",
        paint,
        className,
      )}
      style={{ height: size, width: size }}
    >
      <Icon style={{ height: iconSize, width: iconSize }} strokeWidth={1.75} />
    </span>
  )
}
