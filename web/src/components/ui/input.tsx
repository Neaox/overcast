import * as React from "react"
import { cn } from "@/lib/utils"

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {}

const Input = React.forwardRef<HTMLInputElement, InputProps>(({ className, ...props }, ref) => (
  <input
    ref={ref}
    className={cn(
      "flex h-8 w-full rounded-md border border-border bg-bg px-3 py-1 text-sm text-fg",
      "placeholder:text-fg-subtle",
      "focus-visible:border-accent focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none",
      "disabled:cursor-not-allowed disabled:opacity-50",
      "transition-colors",
      className,
    )}
    {...props}
  />
))
Input.displayName = "Input"

export { Input }
