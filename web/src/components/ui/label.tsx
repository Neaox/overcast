import * as React from "react"
import * as LabelPrimitive from "@radix-ui/react-label"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

/** A form label is a field label — the 9px/.14em spec, same as a column header. */
const Label = React.forwardRef<
  React.ComponentRef<typeof LabelPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof LabelPrimitive.Root>
>(({ className, ...props }, ref) => (
  <LabelPrimitive.Root
    ref={ref}
    className={cn(
      fieldLabel,
      "leading-none text-fg-subtle peer-disabled:cursor-not-allowed peer-disabled:opacity-70",
      className,
    )}
    {...props}
  />
))
Label.displayName = "Label"

export { Label }
