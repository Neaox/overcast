import * as React from "react"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import { X } from "lucide-react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

/**
 * Dialog anatomy, artboard 4a: a header band carrying a 30×30 icon tile plus
 * title and subtitle, the body, and a footer band on `--bg` — a different
 * surface from the body — holding the keyboard hint and the actions.
 *
 * The bands are full-bleed via negative margins against `DialogContent`'s own
 * padding rather than by moving padding onto every child. That keeps the ~50
 * existing call sites — which drop arbitrary content straight into
 * `DialogContent` — correctly inset without touching one of them.
 */

const Dialog = DialogPrimitive.Root
const DialogTrigger = DialogPrimitive.Trigger
const DialogPortal = DialogPrimitive.Portal
const DialogClose = DialogPrimitive.Close

const DialogOverlay = React.forwardRef<
  React.ComponentRef<typeof DialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Overlay
    ref={ref}
    className={cn(
      // Design-system dialog scrim: rgba(9,16,22,0.62) — deep blue-black ink,
      // shared by both themes (see design spec, artboard 4a).
      "fixed inset-0 z-50 bg-[rgba(9,16,22,0.62)] backdrop-blur-sm",
      "data-[state=open]:animate-in data-[state=closed]:animate-out",
      "data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0",
      className,
    )}
    {...props}
  />
))
DialogOverlay.displayName = "DialogOverlay"

/** 440 confirm · 520 form · 560 inspector (4a/4b draw three widths, one anatomy). */
const dialogContentVariants = cva(
  cn(
    "fixed top-1/2 left-1/2 z-50 -translate-x-1/2 -translate-y-1/2",
    "@container flex max-h-[calc(100vh-4rem)] w-full flex-col overflow-x-hidden overflow-y-auto",
    "rounded-card border border-border bg-bg-elevated p-4 shadow-2xl",
    "data-[state=open]:animate-in data-[state=closed]:animate-out",
    "data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0",
    "data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95",
  ),
  {
    variants: {
      size: {
        sm: "max-w-[440px]",
        md: "max-w-[520px]",
        lg: "max-w-[560px]",
      },
    },
    defaultVariants: { size: "md" },
  },
)

/**
 * `⏎` submits the primary action, but only from somewhere it cannot already
 * mean something else: never from a multi-line field (where Enter inserts a
 * newline), never from a control that handles Enter itself and would therefore
 * fire twice, and never with a modifier held.
 */
function isPrimaryActionKey(event: React.KeyboardEvent<HTMLElement>): boolean {
  if (event.key !== "Enter" || event.defaultPrevented) return false
  if (event.nativeEvent.isComposing) return false
  if (event.shiftKey || event.altKey || event.ctrlKey || event.metaKey) return false

  const target = event.target
  if (!(target instanceof HTMLElement)) return true
  if (target.isContentEditable) return false
  if (target.getAttribute("aria-multiline") === "true") return false
  return !["TEXTAREA", "BUTTON", "A", "SELECT"].includes(target.tagName)
}

interface DialogContentProps
  extends
    React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>,
    VariantProps<typeof dialogContentVariants> {
  /**
   * Wires the `⏎ to …` half of the footer contract. Escape is already handled:
   * Radix's dismissable layer closes the dialog on it, which runs the same
   * `onOpenChange(false)` path as Cancel — so there is deliberately no
   * `onCancel` counterpart here.
   *
   * Pass this on dialogs whose primary action is a plain button. A dialog built
   * around a `<form>` already gets `⏎` from native implicit submission, and
   * wiring both would fire the mutation twice.
   */
  onPrimaryAction?: () => void
  /**
   * Suppresses `⏎` while the primary button is disabled or its mutation is in
   * flight — the keyboard path must not do what the button refuses to do.
   */
  primaryActionDisabled?: boolean
}

const DialogContent = React.forwardRef<
  React.ComponentRef<typeof DialogPrimitive.Content>,
  DialogContentProps
>(
  (
    { className, children, size, onPrimaryAction, primaryActionDisabled, onKeyDown, ...props },
    ref,
  ) => (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Content
        ref={ref}
        aria-describedby={props["aria-describedby"] ?? undefined}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(event) => {
          onKeyDown?.(event)
          if (!onPrimaryAction || primaryActionDisabled) return
          if (!isPrimaryActionKey(event)) return
          event.preventDefault()
          onPrimaryAction()
        }}
        className={cn(dialogContentVariants({ size }), className)}
        {...props}
      >
        {children}
        <DialogPrimitive.Close className="absolute top-4 right-4 flex h-7 w-7 items-center justify-center rounded-control text-fg-subtle transition-colors hover:bg-accent-muted hover:text-accent">
          <X aria-hidden className="h-4 w-4" />
          <span className="sr-only">Close</span>
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPortal>
  ),
)
DialogContent.displayName = "DialogContent"

const dialogIconVariants = cva(
  "flex h-[30px] w-[30px] shrink-0 items-center justify-center rounded-control [&_svg]:h-[15px] [&_svg]:w-[15px]",
  {
    variants: {
      tone: {
        accent: "bg-accent-muted text-accent",
        danger: "border border-danger text-danger",
      },
    },
    defaultVariants: { tone: "accent" },
  },
)

interface DialogIconProps
  extends React.HTMLAttributes<HTMLDivElement>, VariantProps<typeof dialogIconVariants> {}

/** 30×30 header tile. Accent-soft fill by default; outlined for destructive dialogs. */
function DialogIcon({ className, tone, ...props }: DialogIconProps) {
  return (
    <div
      aria-hidden
      data-slot="dialog-icon"
      className={cn(dialogIconVariants({ tone }), className)}
      {...props}
    />
  )
}

interface DialogHeaderProps extends React.HTMLAttributes<HTMLDivElement> {
  /** Glyph for the 30×30 tile. Omit it and the header is title-only, as before. */
  icon?: React.ReactNode
  tone?: VariantProps<typeof dialogIconVariants>["tone"]
}

function DialogHeader({ className, children, icon, tone, ...props }: DialogHeaderProps) {
  return (
    <div
      className={cn(
        "-mx-4 -mt-4 mb-4 flex shrink-0 items-start gap-3 border-b border-border p-4 pr-12",
        className,
      )}
      {...props}
    >
      {icon && <DialogIcon tone={tone}>{icon}</DialogIcon>}
      <div className="flex min-w-0 flex-col gap-0.5">{children}</div>
    </div>
  )
}

interface DialogFooterProps extends React.HTMLAttributes<HTMLDivElement> {
  /** Left-aligned keyboard contract — pass `<DialogKeyHint action="create" />`. */
  hint?: React.ReactNode
}

function DialogFooter({ className, children, hint, ...props }: DialogFooterProps) {
  return (
    <div
      className={cn(
        "-mx-4 mt-4 -mb-4 flex shrink-0 items-center gap-3 border-t border-border bg-bg px-4 py-3",
        className,
      )}
      {...props}
    >
      {hint}
      <div className="ml-auto flex items-center gap-2">{children}</div>
    </div>
  )
}

/**
 * The advertised keyboard contract. Only render it where it is honoured — the
 * hint is a promise, and an unwired one is worse than none.
 */
function DialogKeyHint({ action = "confirm", className }: { action?: string; className?: string }) {
  return (
    <span className={cn("font-mono text-[10px] text-fg-subtle", className)}>
      ⏎ to {action} · esc to cancel
    </span>
  )
}

function DialogBody({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("min-h-0 flex-1 overflow-y-auto", className)} {...props} />
}

function DialogTitle({
  className,
  ...props
}: React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      className={cn("font-mono text-sm font-bold text-fg", className)}
      {...props}
    />
  )
}

/** Header subtitle — `CloudWatch Logs · ap-southeast-2`. Sans, never mono. */
function DialogDescription({
  className,
  ...props
}: React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      className={cn("text-[11px] text-fg-subtle", className)}
      {...props}
    />
  )
}

export {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogHeader,
  DialogIcon,
  DialogBody,
  DialogFooter,
  DialogKeyHint,
  DialogTitle,
  DialogDescription,
  DialogClose,
}
