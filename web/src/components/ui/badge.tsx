import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

/**
 * Status pill, 2b:1239 — `padding: 4px 9px; --oc-radius-control; mono 10;
 * letter-spacing .12em; uppercase`. Uppercasing in CSS rather than in the copy
 * keeps `Standard` / `ACTIVE` / `available` readable in the DOM and in tests.
 */
const badgeVariants = cva(
  cn(
    "inline-flex items-center rounded-control px-1.5 py-0.5 transition-colors",
    "font-mono text-2xs tracking-[0.12em] whitespace-nowrap uppercase",
  ),
  {
    variants: {
      variant: {
        default: "bg-bg-muted text-fg-muted border border-border",
        outline: "border border-border text-fg-muted bg-transparent",
        // Opaque `-muted` surfaces, never `/15`: a translucent tint takes its
        // lightness from whatever the badge is sitting on, so the same pill read
        // 4.8:1 on a card and 3.8:1 on the recessed strip behind a table. Each
        // `-muted` token is that colour mixed into --bg-elevated once, which is what
        // lets the foreground be tuned against it and stay tuned.
        accent: "bg-accent-muted text-accent",
        success: "bg-success-muted text-success",
        warning: "bg-warning-muted text-warning",
        danger: "bg-danger-muted text-danger",
        info: "bg-accent-muted text-accent",
      },
    },
    defaultVariants: { variant: "default" },
  },
)

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>, VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />
}

// eslint-disable-next-line react-refresh/only-export-components
export { Badge, badgeVariants }
