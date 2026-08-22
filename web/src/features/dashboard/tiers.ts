import type { EmulationTier } from "@/types/common"

/** Tooltip copy for each emulation tier. */
export const TIER_DESCRIPTIONS: Record<EmulationTier, string> = {
  full: "All operations are implemented and behave like real AWS.",
  partial: "Core operations work. Some endpoints return 501 or have limited behaviour.",
  inert:
    "Service accepts requests but operations have no side effects — always returns success without storing state.",
  stub: "Registered so discovery works: at most a hardcoded, stateless answer to the service's describe call; every other operation returns 501 Not Implemented.",
  unsupported: "Not registered in Overcast.",
}

/** Badge config keyed by emulation tier. `null` = no badge. */
export const TIER_BADGE: Record<EmulationTier, { label: string; className: string } | null> = {
  full: null,
  partial: {
    label: "Partial",
    className: "border-amber-400/40 bg-amber-400/10 text-amber-400",
  },
  inert: {
    label: "Inert",
    className: "border-sky-400/40 bg-sky-400/10 text-sky-400",
  },
  stub: {
    label: "Stub",
    className: "border-border-muted bg-bg-muted text-fg-subtle",
  },
  unsupported: {
    label: "Unsupported",
    className: "border-border-muted bg-bg-muted text-fg-subtle",
  },
}
