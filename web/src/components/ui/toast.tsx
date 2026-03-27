import * as React from "react"
import * as ToastPrimitive from "@radix-ui/react-toast"
import { X } from "lucide-react"
import { cn } from "@/lib/utils"

const ToastProvider = ToastPrimitive.Provider
const ToastViewport = React.forwardRef<
  React.ElementRef<typeof ToastPrimitive.Viewport>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Viewport>
>(({ className, ...props }, ref) => (
  <ToastPrimitive.Viewport
    ref={ref}
    className={cn(
      "fixed right-4 bottom-4 z-[100] flex max-h-screen w-80 flex-col gap-2",
      className,
    )}
    {...props}
  />
))
ToastViewport.displayName = "ToastViewport"

const Toast = React.forwardRef<
  React.ElementRef<typeof ToastPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Root> & {
    variant?: "default" | "success" | "danger"
  }
>(({ className, variant = "default", ...props }, ref) => (
  <ToastPrimitive.Root
    ref={ref}
    className={cn(
      "flex items-start gap-3 rounded-lg border p-3 text-sm shadow-lg",
      "border-border bg-bg-elevated text-fg",
      variant === "success" && "border-success/30 bg-success/5",
      variant === "danger" && "border-danger/30 bg-danger/5",
      "data-[state=open]:animate-in data-[state=closed]:animate-out",
      "data-[state=closed]:slide-out-to-right-full",
      className,
    )}
    {...props}
  />
))
Toast.displayName = "Toast"

const ToastTitle = React.forwardRef<
  React.ElementRef<typeof ToastPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Title>
>(({ className, ...props }, ref) => (
  <ToastPrimitive.Title ref={ref} className={cn("font-medium", className)} {...props} />
))
ToastTitle.displayName = "ToastTitle"

const ToastDescription = React.forwardRef<
  React.ElementRef<typeof ToastPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Description>
>(({ className, ...props }, ref) => (
  <ToastPrimitive.Description
    ref={ref}
    className={cn("text-xs text-fg-muted", className)}
    {...props}
  />
))
ToastDescription.displayName = "ToastDescription"

const ToastClose = React.forwardRef<
  React.ElementRef<typeof ToastPrimitive.Close>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Close>
>(({ className, ...props }, ref) => (
  <ToastPrimitive.Close
    ref={ref}
    className={cn("ml-auto text-fg-subtle transition-colors hover:text-fg", className)}
    {...props}
  >
    <X className="h-3.5 w-3.5" />
  </ToastPrimitive.Close>
))
ToastClose.displayName = "ToastClose"

export { ToastProvider, ToastViewport, Toast, ToastTitle, ToastDescription, ToastClose }

// ─── useToast hook ─────────────────────────────────────────────────────────
type ToastItem = {
  id: string
  title: string
  description?: string
  variant?: "default" | "success" | "danger"
}

type ToastContextValue = {
  toasts: ToastItem[]
  toast: (item: Omit<ToastItem, "id">) => void
}

const ToastContext = React.createContext<ToastContextValue | null>(null)

export function ToastContextProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = React.useState<ToastItem[]>([])

  const toast = React.useCallback((item: Omit<ToastItem, "id">) => {
    setToasts((prev) => [...prev, { ...item, id: crypto.randomUUID() }])
  }, [])

  return (
    <ToastContext.Provider value={{ toasts, toast }}>
      <ToastProvider>
        {children}
        {toasts.map((t) => (
          <Toast
            key={t.id}
            variant={t.variant}
            onOpenChange={(open) => {
              if (!open) setToasts((prev) => prev.filter((x) => x.id !== t.id))
            }}
          >
            <div className="flex-1">
              <ToastTitle>{t.title}</ToastTitle>
              {t.description && <ToastDescription>{t.description}</ToastDescription>}
            </div>
            <ToastClose />
          </Toast>
        ))}
        <ToastViewport />
      </ToastProvider>
    </ToastContext.Provider>
  )
}

export function useToast() {
  const ctx = React.useContext(ToastContext)
  if (!ctx) throw new Error("useToast must be used within ToastContextProvider")
  return ctx
}
