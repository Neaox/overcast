import * as React from "react"
import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"
import { BlinkingCursor } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"

/**
 * Hover is a promise that the control will respond, so it is withheld from any
 * control that will not: `:disabled` covers the native case, `[aria-disabled]`
 * covers busy buttons and `asChild` links, which cannot be natively disabled.
 * Guarding here rather than with `pointer-events-none` is what lets the
 * disabled cursor show at all.
 */
const buttonVariants = cva(
  cn(
    "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md",
    "font-mono transition-colors cursor-pointer select-none",
    "disabled:cursor-not-allowed disabled:opacity-50",
    // A busy control keeps its full fill — it is working, not unavailable —
    // and only its cursor says "nothing to click here".
    "aria-busy:cursor-default aria-busy:opacity-100",
  ),
  {
    variants: {
      // A filled control is bold, an outlined or bare one is not: the appendix
      // pairs every primary with `/700` (`Create log group`, `Delete`) and
      // leaves every ghost and outline at the regular weight.
      variant: {
        default:
          "bg-accent font-bold text-fg-on-accent not-disabled:not-aria-disabled:hover:bg-accent-hover",
        secondary:
          "bg-bg-muted text-fg border border-border not-disabled:not-aria-disabled:hover:bg-bg-elevated",
        outline:
          "border border-border bg-transparent text-fg not-disabled:not-aria-disabled:hover:bg-bg-muted",
        ghost: "text-fg not-disabled:not-aria-disabled:hover:bg-bg-muted",
        danger:
          "bg-danger font-bold text-fg-on-accent not-disabled:not-aria-disabled:hover:opacity-90",
        "danger-ghost": "text-danger not-disabled:not-aria-disabled:hover:bg-danger-muted",
        link: cn(
          "text-accent underline-offset-4 p-0 h-auto",
          "not-disabled:not-aria-disabled:hover:underline",
          "not-disabled:not-aria-disabled:hover:text-accent-hover",
        ),
      },
      // Toolbar controls are 30px/mono-11, dialog controls 32px/mono-12 — the
      // two button sizes the design actually draws.
      size: {
        sm: "h-7 px-2.5 text-2xs",
        md: "h-8 px-3.5 text-xs",
        lg: "h-10 px-5 text-sm",
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

/** The busy caret has to read against whatever fill it lands on. */
const busyCursorVariants = cva("", {
  variants: {
    variant: {
      default: "bg-fg-on-accent",
      danger: "bg-fg-on-accent",
      secondary: "",
      outline: "",
      ghost: "",
      "danger-ghost": "",
      link: "",
    },
  },
  defaultVariants: { variant: "default" },
})

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  asChild?: boolean
  /**
   * Work this button started is still running. Per artboard 5b a busy control
   * keeps its fill and is **never dimmed** — dimming it would read as disabled,
   * which is the opposite of what is happening — but it stops accepting input
   * and grows the brand caret after its label.
   *
   * Ignored for `asChild`, where the caret has no single child to live in.
   */
  busy?: boolean
  /** Label while busy — `Creating`, `Refreshing`. Replaces the idle content, icon included. */
  busyLabel?: React.ReactNode
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  (
    {
      className,
      variant,
      size,
      asChild = false,
      busy = false,
      busyLabel,
      children,
      onClick,
      ...props
    },
    ref,
  ) => {
    const Comp = asChild ? Slot : "button"
    const showBusy = busy && !asChild
    return (
      <Comp
        ref={ref}
        {...props}
        aria-busy={busy || undefined}
        aria-disabled={busy || props["aria-disabled"] || undefined}
        onClick={busy ? undefined : onClick}
        className={cn(buttonVariants({ variant, size }), className)}
      >
        {showBusy ? (
          <>
            {busyLabel ?? children}
            <BlinkingCursor size="control" className={busyCursorVariants({ variant })} />
          </>
        ) : (
          children
        )}
      </Comp>
    )
  },
)
Button.displayName = "Button"

// eslint-disable-next-line react-refresh/only-export-components
export { Button, buttonVariants }
