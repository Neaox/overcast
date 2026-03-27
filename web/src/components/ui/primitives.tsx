import * as React from "react"
import { cn } from "@/lib/utils"
import { Loader2 } from "lucide-react"

// ─── Spinner ──────────────────────────────────────────────────────────────
function Spinner({ className }: { className?: string }) {
  return <Loader2 className={cn("h-4 w-4 animate-spin text-fg-muted", className)} />
}

// ─── Empty state ──────────────────────────────────────────────────────────
interface EmptyStateProps {
  icon?: React.ReactNode
  title: string
  description?: string
  action?: React.ReactNode
  className?: string
}

function EmptyState({ icon, title, description, action, className }: EmptyStateProps) {
  return (
    <div
      className={cn("flex flex-col items-center justify-center gap-3 py-16 text-center", className)}
    >
      {icon && <div className="mb-1 text-fg-subtle">{icon}</div>}
      <p className="text-sm font-medium text-fg">{title}</p>
      {description && <p className="max-w-xs text-sm text-fg-muted">{description}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  )
}

// ─── PageHeader ───────────────────────────────────────────────────────────
interface PageHeaderProps {
  title: string
  description?: string
  actions?: React.ReactNode
  breadcrumb?: React.ReactNode
  className?: string
}

function PageHeader({ title, description, actions, breadcrumb, className }: PageHeaderProps) {
  return (
    <div className={cn("flex items-start justify-between gap-4", className)}>
      <div className="flex flex-col gap-0.5">
        {breadcrumb && <div className="mb-1">{breadcrumb}</div>}
        <h1 className="text-base font-semibold text-fg">{title}</h1>
        {description && <p className="text-sm text-fg-muted">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  )
}

// ─── Breadcrumb ───────────────────────────────────────────────────────────
interface BreadcrumbItem {
  label: string
  onClick?: () => void
}

function Breadcrumb({ items }: { items: BreadcrumbItem[] }) {
  return (
    <nav className="flex items-center gap-1 text-sm text-fg-muted">
      {items.map((item, i) => (
        <React.Fragment key={i}>
          {i > 0 && <span className="text-fg-subtle">/</span>}
          {item.onClick ? (
            <button onClick={item.onClick} className="transition-colors hover:text-fg">
              {item.label}
            </button>
          ) : (
            <span className={i === items.length - 1 ? "font-medium text-fg" : ""}>
              {item.label}
            </span>
          )}
        </React.Fragment>
      ))}
    </nav>
  )
}

// ─── Separator ────────────────────────────────────────────────────────────
function Separator({
  className,
  orientation = "horizontal",
}: {
  className?: string
  orientation?: "horizontal" | "vertical"
}) {
  return (
    <div
      className={cn(
        "bg-border",
        orientation === "horizontal" ? "h-px w-full" : "w-px self-stretch",
        className,
      )}
    />
  )
}

// ─── Code / pre ───────────────────────────────────────────────────────────
function Code({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <code className={cn("rounded bg-bg-muted px-1 py-0.5 font-mono text-xs text-fg", className)}>
      {children}
    </code>
  )
}

function CodeBlock({ children, className }: { children: string; className?: string }) {
  return (
    <pre
      className={cn(
        "overflow-auto rounded-md border border-border bg-bg-muted p-3 font-mono text-xs text-fg",
        className,
      )}
    >
      <code>{children}</code>
    </pre>
  )
}

export { Spinner, EmptyState, PageHeader, Breadcrumb, Separator, Code, CodeBlock }
