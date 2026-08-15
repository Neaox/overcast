import { Cloud, Archive, Lightbulb } from "lucide-react"
import { Tooltip } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import type { DiagnosticProvenance } from "@/types"

/**
 * The tag that says where a piece of emulator diagnostic evidence came from.
 *
 * ## Why this is not a badge reading "Overcast-only"
 *
 * A blanket disclaimer is weak — people stop seeing badges, and it tells the
 * reader nothing they can act on. Each tier here is chosen to answer a question
 * the reader actually has while deciding what to fix: *would production have
 * given me this too?* "From the AWS API" means yes; "Captured by Overcast"
 * means no, and the fix had better not depend on it; "Overcast's reading" means
 * a machine guessed, so check it.
 *
 * ## Why it lives in components/ui rather than in the CloudFormation feature
 *
 * The distinction is not CloudFormation's. RDS's retained-logs panel already
 * draws the same line with its own `logSource: "container" | "retained"` field
 * and its own wording, and the whole value of the tag is that it means the same
 * thing everywhere it appears. A second copy inside a feature folder would let
 * the two drift into explaining the same idea in different words, which is
 * exactly the failure this device exists to prevent.
 *
 * ## Colour is never the signal
 *
 * The tiers are told apart by their words first and their icon second; the
 * colour only reinforces an ordering that is already legible without it. Every
 * colour is a semantic token, so all three stay readable in both themes rather
 * than relying on a palette that only works against one background.
 */

interface Tier {
  /** The short label, shown always. */
  label: string
  /** The explanation, on hover/focus and to assistive technology. */
  explanation: string
  icon: typeof Cloud
  className: string
}

const TIERS: Record<DiagnosticProvenance, Tier> = {
  "aws-api": {
    label: "From the AWS API",
    explanation: "Real AWS returns this too — the equivalent AWS API call would have shown it.",
    icon: Cloud,
    className: "border-border bg-bg-muted text-fg-muted",
  },
  "overcast-capture": {
    label: "Captured by Overcast",
    explanation:
      "Overcast saved this before rollback deleted it. Real AWS discards it too — " +
      "you would have needed log forwarding configured beforehand.",
    icon: Archive,
    className: "border-warning/40 bg-warning-muted text-warning",
  },
  "overcast-inference": {
    label: "Overcast's reading",
    explanation: "Overcast's interpretation of the evidence. Not an AWS concept.",
    icon: Lightbulb,
    className: "border-accent/40 bg-accent-muted text-accent",
  },
}

export function ProvenanceTag({
  provenance,
  className,
}: {
  provenance: DiagnosticProvenance
  className?: string
}) {
  const tier = TIERS[provenance]
  const Icon = tier.icon

  return (
    <Tooltip content={tier.explanation}>
      {/*
        A button, not a span: Radix only opens a tooltip for a focusable
        trigger, so a non-interactive element would put the explanation behind
        a mouse. The `sr-only` copy carries it a second way, because a tooltip
        that has to be opened to exist is not reachable at all by a screen
        reader reading straight through — and this explanation is the part that
        stops someone shipping a fix that depends on evidence production will
        never produce.
      */}
      <button
        type="button"
        className={cn(
          "inline-flex cursor-help items-center gap-1 rounded-control border px-1.5 py-0.5",
          "font-mono text-[10px] tracking-[0.08em] whitespace-nowrap uppercase",
          tier.className,
          className,
        )}
      >
        <Icon aria-hidden className="h-3 w-3 shrink-0" />
        {tier.label}
        <span className="sr-only"> — {tier.explanation}</span>
      </button>
    </Tooltip>
  )
}
