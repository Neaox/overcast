import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { AlertTriangle, Loader2 } from "lucide-react"
import { SkeletonCards, SkeletonRows } from "@/components/ui/skeleton"

// ─── Spinner ──────────────────────────────────────────────────────────────
const spinnerSizeVariants = cva("", {
  variants: {
    size: {
      /** 14px — inside a chip or an icon-sized control. */
      sm: "h-3.5 w-3.5",
      /** 16px — inside a button or a toast. */
      md: "h-4 w-4",
    },
  },
  defaultVariants: { size: "md" },
})

interface SpinnerProps extends VariantProps<typeof spinnerSizeVariants> {
  className?: string
}

/**
 * Inline busy indicator.
 *
 * The design system allows spinners at **14-16px only, and only inside a chip,
 * button or toast** — a content area gets a skeleton instead (`SkeletonRows` /
 * `SkeletonCards`). The size class is therefore appended *after* `className`
 * so `tailwind-merge` lets it win: a caller cannot grow the spinner back into
 * a content-area spinner. Spacing, colour and layout utilities passed in
 * `className` are unaffected.
 */
function Spinner({ size, className }: SpinnerProps) {
  return <Loader2 className={cn("animate-spin", className, spinnerSizeVariants({ size }))} />
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
      <p className="font-mono text-sm font-medium text-fg">{title}</p>
      {description && <p className="max-w-xs text-sm text-fg-muted">{description}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  )
}

// ─── Query List State ─────────────────────────────────────────────────────
interface QueryListStateProps {
  isLoading: boolean
  isEmpty: boolean
  error?: unknown
  empty?: React.ReactNode
  emptyTitle?: string
  emptyDescription?: string
  emptyIcon?: React.ReactNode
  emptyAction?: React.ReactNode
  emptyClassName?: string
  loadingClassName?: string
  /**
   * Loading treatment. `"rows"` (default) is the table skeleton; `"cards"` is
   * the 3-up dashboard grid.
   */
  loadingVariant?: "rows" | "cards"
  /**
   * Placeholder row/card count, following the design's `hint-placeholder-count`
   * convention. Defaults to 5 rows / 3 cards.
   */
  loadingCount?: number
  /**
   * Lowercase plural resource noun for the skeleton footer — `"log groups"`
   * renders `loading log groups`.
   */
  loadingNoun?: string
  errorTitle?: string
  errorDescription?: string
}

function queryErrorMessage(error: unknown): string | undefined {
  if (error instanceof Error) return error.message
  if (typeof error === "string") return error
  return undefined
}

function QueryListState({
  isLoading,
  isEmpty,
  error,
  empty,
  emptyTitle = "No data",
  emptyDescription,
  emptyIcon,
  emptyAction,
  emptyClassName,
  loadingClassName,
  loadingVariant = "rows",
  loadingCount,
  loadingNoun,
  errorTitle = "Unable to load data",
  errorDescription,
}: QueryListStateProps) {
  // Every list in the app inherits its loading treatment from here: a static
  // skeleton, never a centred spinner (see components/ui/skeleton.tsx).
  if (isLoading) {
    return loadingVariant === "cards" ? (
      <SkeletonCards cards={loadingCount} noun={loadingNoun} className={loadingClassName} />
    ) : (
      <SkeletonRows rows={loadingCount} noun={loadingNoun} className={loadingClassName} />
    )
  }

  if (isEmpty && error) {
    return (
      <EmptyState
        icon={<AlertTriangle className="h-10 w-10" />}
        title={errorTitle}
        description={errorDescription ?? queryErrorMessage(error) ?? "Please try again."}
      />
    )
  }

  if (isEmpty) {
    if (empty) return <>{empty}</>
    return (
      <EmptyState
        icon={emptyIcon}
        title={emptyTitle}
        description={emptyDescription}
        action={emptyAction}
        className={emptyClassName}
      />
    )
  }

  return null
}

// ─── PageHeader ───────────────────────────────────────────────────────────
interface PageHeaderProps {
  title: string
  /** Resource count, rendered as a lighter number beside the title. */
  count?: number
  /** Secondary line beneath the title, e.g. "9 active · 15 resources". */
  meta?: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
  className?: string
}

function PageHeader({ title, count, meta, description, actions, className }: PageHeaderProps) {
  return (
    <div className={cn("flex items-start justify-between gap-4", className)}>
      <div className="flex flex-col gap-0.5">
        <div className="flex items-baseline gap-2">
          <h1 className="font-mono text-[20px] font-bold tracking-[-0.02em] text-fg">{title}</h1>
          {count !== undefined && (
            <span className="font-mono text-sm text-fg-subtle tabular-nums">{count}</span>
          )}
        </div>
        {meta && <p className="font-mono text-xs text-fg-subtle">{meta}</p>}
        {description && <p className="text-sm text-fg-muted">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
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

export { Spinner, EmptyState, QueryListState, PageHeader, Separator, Code, CodeBlock }
