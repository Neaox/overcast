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
    "font-mono text-[10px] tracking-[0.12em] whitespace-nowrap uppercase",
  ),
  {
    variants: {
      variant: {
        default: "bg-bg-muted text-fg-muted border border-border",
        outline: "border border-border text-fg-muted bg-transparent",
        accent: "bg-accent-muted text-accent",
        success: "bg-success/15 text-success",
        warning: "bg-warning/15 text-warning",
        danger: "bg-danger-muted text-danger",
        info: "bg-blue-500/15 text-blue-400",
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
