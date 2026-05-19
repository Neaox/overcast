import * as React from "react"
import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors cursor-pointer select-none disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      variant: {
        default: "bg-accent text-fg-on-accent hover:bg-accent-hover",
        secondary: "bg-bg-muted text-fg hover:bg-bg-elevated border border-border",
        outline: "border border-border bg-transparent hover:bg-bg-muted text-fg",
        ghost: "text-fg hover:bg-bg-muted",
        danger: "bg-danger text-fg-on-accent hover:opacity-90",
        "danger-ghost": "text-danger hover:bg-danger-muted",
        link: "text-accent underline-offset-4 hover:underline p-0 h-auto",
      },
      size: {
        sm: "h-7 px-2.5 text-xs",
        md: "h-8 px-3.5",
        lg: "h-10 px-5 text-base",
        icon: "h-8 w-8 p-0",
        "icon-sm": "h-6 w-6 p-0",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "md",
    },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button"
    return (
      <Comp ref={ref} className={cn(buttonVariants({ variant, size }), className)} {...props} />
    )
  },
)
Button.displayName = "Button"

// eslint-disable-next-line react-refresh/only-export-components
export { Button, buttonVariants }
