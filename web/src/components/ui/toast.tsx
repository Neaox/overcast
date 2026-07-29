import * as React from "react"
import * as ToastPrimitive from "@radix-ui/react-toast"
import { Check, CircleAlert, X } from "lucide-react"
import { cva } from "class-variance-authority"
import { OvercastLoader } from "@/components/brand/overcast-loader"
import { createId } from "@/lib/id"
import { cn } from "@/lib/utils"

/**
 * Toast treatment, artboard 4b.
 *
 * The card is the elevated surface with a soft shadow; the only variant-coloured
 * chrome is a 3px full-height bar down the left edge and the leading icon. Both
 * are painted from semantic tokens (`--success` / `--danger` / `--accent`) that
 * already resolve per theme, so light and dark need no separate treatment.
 */

/** `pending` is resolved by the caller — see `useToast().updateToast`. */
export type ToastVariant = "default" | "success" | "danger" | "pending"

/**
 * Typesetting of the detail line beneath the title. Identifiers (ARNs, log
 * group names, object keys) are mono and truncate; prose (an error sentence)
 * is sans and wraps.
 */
export type ToastDetailKind = "identifier" | "prose"

/**
 * The design's register is "short status phrase, then the identifier or the
 * reason" — so a failure explains itself in prose and everything else names the
 * thing it acted on. Callers override per toast with `descriptionKind`.
 */
function defaultDetailKind(variant: ToastVariant | undefined): ToastDetailKind {
  return variant === "danger" ? "prose" : "identifier"
}

const ToastProvider = ToastPrimitive.Provider

/**
 * The bottom-right column every toast lives in. Only its bottom edge is
 * pinned, so the stack grows upward and the corner-most toast never moves.
 */
const TOAST_DOCK = "fixed right-4 bottom-4 z-[10001] flex w-[300px] flex-col gap-2"

/**
 * The card itself: surface, radius, padding, and the room down the left edge
 * that the 3px variant bar occupies. Exported because the docked connection
 * toast is not a Radix toast — it is driven by connection state rather than by
 * `toast()` — but has to be the same object on screen.
 */
export const TOAST_CARD =
  "relative flex items-start gap-2.5 overflow-hidden rounded-control border border-border bg-bg-elevated py-3 pr-3 pl-[15px] shadow-lg"

/** Geometry of the 3px full-height left bar; the fill is the variant's. */
export const TOAST_BAR = "absolute inset-y-0 left-0 w-[3px]"

const ToastViewport = React.forwardRef<
  React.ComponentRef<typeof ToastPrimitive.Viewport>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Viewport>
>(({ className, ...props }, ref) => (
  <ToastPrimitive.Viewport
    ref={ref}
    // Positioned by the dock, not by itself: it is the column's first item, so
    // transient toasts pile above whatever is docked beneath them.
    className={cn("flex max-h-screen w-full flex-col gap-2", className)}
    {...props}
  />
))
ToastViewport.displayName = "ToastViewport"

/** 3px full-height left bar — the variant's only load-bearing colour. */
const toastBarVariants = cva(TOAST_BAR, {
  variants: {
    variant: {
      default: "bg-border",
      success: "bg-success",
      danger: "bg-danger",
      pending: "bg-accent",
    },
  },
  defaultVariants: { variant: "default" },
})

const toastIconVariants = cva("mt-px h-[15px] w-[15px] shrink-0", {
  variants: {
    variant: {
      default: "",
      success: "text-success",
      danger: "text-danger",
      pending: "text-accent",
    },
  },
  defaultVariants: { variant: "default" },
})

/**
 * Leading status glyph. `pending` uses the brand mark's cloud with its prompt
 * cursor blinking — 16px, inside a toast, which is one of the two places the
 * design allows motion at all, and it goes solid rather than hidden under
 * `prefers-reduced-motion` (see `OvercastLoader`).
 */
function ToastIcon({ variant = "default" }: { variant?: ToastVariant }) {
  if (variant === "default") return null
  if (variant === "pending") {
    return <OvercastLoader size={16} label="In progress" className="mt-px shrink-0" />
  }
  const Glyph = variant === "success" ? Check : CircleAlert
  return <Glyph aria-hidden strokeWidth={2} className={toastIconVariants({ variant })} />
}

const Toast = React.forwardRef<
  React.ComponentRef<typeof ToastPrimitive.Root>,
  Omit<React.ComponentPropsWithoutRef<typeof ToastPrimitive.Root>, "duration"> & {
    variant?: ToastVariant
  }
>(({ className, variant = "default", children, ...props }, ref) => (
  <ToastPrimitive.Root
    ref={ref}
    data-slot="toast"
    // A pending toast reports work that is still running, so it must outlive any
    // timer: it closes when the caller resolves or dismisses it, or when the
    // user does. Radix skips the auto-close timer entirely for `Infinity`, and
    // re-arms it when the duration becomes finite again on resolution.
    duration={variant === "pending" ? Infinity : undefined}
    className={cn(
      TOAST_CARD,
      "data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:slide-out-to-right-full",
      className,
    )}
    {...props}
  >
    <span aria-hidden className={toastBarVariants({ variant })} />
    <ToastIcon variant={variant} />
    {children}
  </ToastPrimitive.Root>
))
Toast.displayName = "Toast"

const ToastTitle = React.forwardRef<
  React.ComponentRef<typeof ToastPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Title>
>(({ className, ...props }, ref) => (
  <ToastPrimitive.Title
    ref={ref}
    className={cn("font-mono text-xs font-bold text-fg", className)}
    {...props}
  />
))
ToastTitle.displayName = "ToastTitle"

const toastDescriptionVariants = cva("text-[11px] text-fg-muted", {
  variants: {
    kind: {
      identifier: "truncate font-mono",
      prose: "",
    },
  },
  defaultVariants: { kind: "identifier" },
})

const ToastDescription = React.forwardRef<
  React.ComponentRef<typeof ToastPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Description> & { kind?: ToastDetailKind }
>(({ className, kind, ...props }, ref) => (
  <ToastPrimitive.Description
    ref={ref}
    className={cn(toastDescriptionVariants({ kind }), className)}
    {...props}
  />
))
ToastDescription.displayName = "ToastDescription"

const ToastClose = React.forwardRef<
  React.ComponentRef<typeof ToastPrimitive.Close>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Close>
>(({ className, ...props }, ref) => (
  <ToastPrimitive.Close
    ref={ref}
    className={cn("shrink-0 text-fg-subtle transition-colors hover:text-fg", className)}
    {...props}
  >
    <X aria-hidden className="h-3.5 w-3.5" />
    <span className="sr-only">Dismiss</span>
  </ToastPrimitive.Close>
))
ToastClose.displayName = "ToastClose"

export { ToastProvider, ToastViewport, Toast, ToastIcon, ToastTitle, ToastDescription, ToastClose }

// ─── useToast hook ─────────────────────────────────────────────────────────

export interface ToastOptions {
  /** Short status phrase, mono and bold — `Log group created`. */
  title: string
  /** Detail line: the identifier acted on, or the reason it failed. */
  description?: string
  /**
   * Overrides how `description` is typeset. Defaults to `"prose"` for `danger`
   * (its detail is a sentence) and `"identifier"` for every other variant.
   */
  descriptionKind?: ToastDetailKind
  variant?: ToastVariant
}

type ToastItem = ToastOptions & { id: string }

type ToastContextValue = {
  toasts: ToastItem[]
  /** Shows a toast and returns its id, which is what resolves a `pending` one. */
  toast: (item: ToastOptions) => string
  /**
   * Resolves a toast in place. The pending→settled transition is
   * `updateToast(id, { variant: "success", title: "Uploaded 3 objects" })`;
   * the toast then picks up the normal auto-dismiss timer.
   */
  updateToast: (id: string, patch: Partial<ToastOptions>) => void
  /** Removes a toast immediately — for work that was cancelled, not settled. */
  dismissToast: (id: string) => void
}

const ToastContext = React.createContext<ToastContextValue | null>(null)

/**
 * The dock's footer slot: where a *persistent* status toast mounts.
 *
 * Those toasts are driven by application state rather than by `toast()`, and
 * they live deep in the router tree, so they cannot be items in Radix's
 * viewport list. Portalling them into the bottom of the dock keeps one
 * bottom-right column — the persistent toast holds the corner and transient
 * ones stack above it — instead of two stacks overlapping in the same corner.
 */
const ToastDockContext = React.createContext<HTMLElement | null>(null)

export function ToastContextProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = React.useState<ToastItem[]>([])
  const [dockFooter, setDockFooter] = React.useState<HTMLElement | null>(null)

  // Stable identities: callers hold on to `toast` across renders (and put it in
  // dependency arrays), and a pending toast is resolved long after it was shown.
  const toast = React.useCallback((item: ToastOptions) => {
    const id = createId()
    setToasts((prev) => [...prev, { ...item, id }])
    return id
  }, [])

  const updateToast = React.useCallback((id: string, patch: Partial<ToastOptions>) => {
    setToasts((prev) => prev.map((t) => (t.id === id ? { ...t, ...patch } : t)))
  }, [])

  const dismissToast = React.useCallback((id: string) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
  }, [])

  const value = React.useMemo<ToastContextValue>(
    () => ({ toasts, toast, updateToast, dismissToast }),
    [toasts, toast, updateToast, dismissToast],
  )

  return (
    <ToastContext.Provider value={value}>
      <ToastDockContext.Provider value={dockFooter}>
        <ToastProvider>
          {children}
          {toasts.map((t) => (
            <Toast
              key={t.id}
              variant={t.variant}
              onOpenChange={(open) => {
                if (!open) dismissToast(t.id)
              }}
            >
              <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                <ToastTitle>{t.title}</ToastTitle>
                {t.description && (
                  <ToastDescription kind={t.descriptionKind ?? defaultDetailKind(t.variant)}>
                    {t.description}
                  </ToastDescription>
                )}
              </div>
              <ToastClose />
            </Toast>
          ))}
          <div className={TOAST_DOCK}>
            <ToastViewport />
            {/* `contents` so what mounts here is a column item in its own
                right, rather than a child of an extra wrapper. */}
            <div ref={setDockFooter} className="contents" />
          </div>
        </ToastProvider>
      </ToastDockContext.Provider>
    </ToastContext.Provider>
  )
}

/**
 * The dock's footer element, once mounted — a `createPortal` target for a
 * persistent status toast. `null` outside a `ToastContextProvider`, and on the
 * first render inside one, so callers render nothing until it resolves.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function useToastDockFooter(): HTMLElement | null {
  return React.useContext(ToastDockContext)
}

// eslint-disable-next-line react-refresh/only-export-components
export function useToast() {
  const ctx = React.useContext(ToastContext)
  if (!ctx) throw new Error("useToast must be used within ToastContextProvider")
  return ctx
}
