import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

const badgeVariants = cva(
  "inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium transition-colors",
  {
    variants: {
      variant: {
        default: "bg-bg-muted text-fg-muted border border-border",
        accent: "bg-accent-muted text-accent",
        success: "bg-success/15 text-success",
        warning: "bg-warning/15 text-warning",
        danger: "bg-danger-muted text-danger",
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

export { Badge, badgeVariants }
