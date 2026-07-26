import * as React from "react"
import { cn } from "@/lib/utils"

export type InputProps = React.InputHTMLAttributes<HTMLInputElement>

const Input = React.forwardRef<HTMLInputElement, InputProps>(({ className, ...props }, ref) => (
  <input
    ref={ref}
    className={cn(
      // 4a types every field's value in mono 12 — what the user types into
      // Overcast is a resource name, a key or a number, not prose.
      "flex h-8 w-full rounded-md border border-border bg-bg px-3 py-1 font-mono text-xs text-fg",
      "placeholder:text-fg-subtle",
      "focus-visible:border-accent focus-visible:ring-1 focus-visible:ring-accent focus-visible:outline-none focus-visible:ring-inset",
      "disabled:cursor-not-allowed disabled:opacity-50",
      "transition-colors",
      className,
    )}
    {...props}
  />
))
Input.displayName = "Input"

export { Input }
