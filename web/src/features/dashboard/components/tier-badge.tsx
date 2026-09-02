import { Tooltip } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import type { EmulationTier } from "@/types/common"
import { TIER_BADGE, TIER_DESCRIPTIONS } from "../tiers"

export function TierBadge({ tier, className }: { tier: EmulationTier; className?: string }) {
  const badge = TIER_BADGE[tier]
  if (!badge) return null

  return (
    <Tooltip content={TIER_DESCRIPTIONS[tier]} side="top">
      <span
        className={cn(
          "cursor-default rounded-full border px-2 py-0.5 font-mono text-2xs leading-none",
          badge.className,
          className,
        )}
      >
        {badge.label}
      </span>
    </Tooltip>
  )
}

/** Bare tier wording (mono, uppercase) used where a full badge is too heavy. */
export function TierMeta({ tier, className }: { tier: EmulationTier; className?: string }) {
  const badge = TIER_BADGE[tier]
  if (!badge) return null

  return (
    <Tooltip content={TIER_DESCRIPTIONS[tier]} side="top">
      <span
        className={cn(
          "cursor-default font-mono text-2xs tracking-[0.12em] text-fg-subtle uppercase",
          className,
        )}
      >
        {badge.label}
      </span>
    </Tooltip>
  )
}
