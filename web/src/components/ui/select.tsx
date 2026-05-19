import * as React from "react"
import { cn } from "@/lib/utils"

export type SelectProps = React.SelectHTMLAttributes<HTMLSelectElement>

const Select = React.forwardRef<HTMLSelectElement, SelectProps>(({ className, ...props }, ref) => (
  <select
    ref={ref}
    className={cn(
      "flex h-8 w-full rounded-md border border-border bg-bg px-3 py-1 text-sm text-fg",
      "focus-visible:border-accent focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none",
      "disabled:cursor-not-allowed disabled:opacity-50",
      "transition-colors",
      className,
    )}
    {...props}
  />
))
Select.displayName = "Select"

export { Select }
