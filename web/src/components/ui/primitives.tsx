import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { fieldLabel, sectionLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import { AlertTriangle, Loader2, Search } from "lucide-react"
import { SkeletonCards, SkeletonRows } from "@/components/ui/skeleton"
import { Button } from "@/components/ui/button"

// ─── Labels ───────────────────────────────────────────────────────────────
/**
 * Names one field or one column: mono 9px / .14em / uppercase. The same spec
 * `TableHead` uses, so a detail-page label and a column header read as the
 * same kind of thing. See `@/lib/typography` for why the two specs exist.
 */
function FieldLabel({ className, ...props }: React.HTMLAttributes<HTMLSpanElement>) {
  return <span className={cn(fieldLabel, "text-fg-subtle", className)} {...props} />
}

/**
 * Names a group of things: mono 10px / .16em / uppercase. The wider tracking is
 * what makes it read as a heading, so it must never appear at 9px. Colour is
 * left to the caller — dashboard sections carry their tier's colour.
 */
function SectionLabel({
  className,
  as: Heading = "h2",
  ...props
}: React.HTMLAttributes<HTMLHeadingElement> & { as?: "h2" | "h3" | "h4" }) {
  // `<h2>` by default for the same reason as CardTitle: the page's own <h1> is the only
  // level above, so an <h3> here skips a level. `as` covers the nested case.
  return <Heading className={cn(sectionLabel, "text-fg-subtle", className)} {...props} />
}

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
      {/* Title is mono — it names a thing; the description is prose and stays sans. */}
      <p className="font-mono text-sm font-bold text-fg">{title}</p>
      {description && <p className="max-w-xs text-[13px] text-fg-muted">{description}</p>}
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
  /**
   * True while a filter/search is narrowing the list. Swaps the empty state
   * to "no matches" copy with a clear-filter action instead of the create
   * CTA — a filter turning up nothing is not the same fact as the resource
   * not existing, and must not read as an invitation to create one. Has no
   * effect while `error` is set: a failed fetch is a failed fetch regardless
   * of what the filter box says (see the `isEmpty && error` branch below).
   */
  isFiltered?: boolean
  /** Clears the active filter. Powers the default "Clear filter" action shown when `isFiltered` narrows the list to zero rows. */
  onClearFilter?: () => void
  /** Overrides the filtered-empty title. Defaults to `No matching {resource}` derived from `emptyTitle`/`loadingNoun` context via the caller. */
  filteredEmptyTitle?: string
  /** Overrides the filtered-empty description. Defaults to "No results match your filter." */
  filteredEmptyDescription?: string
  filteredEmptyIcon?: React.ReactNode
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
  isFiltered,
  onClearFilter,
  filteredEmptyTitle,
  filteredEmptyDescription,
  filteredEmptyIcon,
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

  // Checked before the generic `isEmpty` branch (and before `isFiltered`, for
  // the same reason): a fetch that failed must never read as "nothing here" —
  // filtered or otherwise — or the one signal that says "this isn't the real
  // state, go retry" is lost.
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

    if (isFiltered) {
      return (
        <EmptyState
          icon={filteredEmptyIcon ?? <Search className="h-10 w-10" />}
          title={filteredEmptyTitle ?? "No matches"}
          description={filteredEmptyDescription ?? "No results match your filter."}
          action={
            onClearFilter && (
              <Button variant="ghost" size="sm" onClick={onClearFilter}>
                Clear filter
              </Button>
            )
          }
          className={emptyClassName}
        />
      )
    }

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

/**
 * The title block and the page's actions, side by side — until they do not
 * fit.
 *
 * The row wraps rather than overflowing, which is the whole narrow-width
 * contract for a page header (#1611). It used to be `justify-between` with
 * `shrink-0` on the actions: at 400px a Docs / Refresh / Create / Columns row
 * simply ran off the right of the viewport, taking the page's primary action
 * with it, and nothing scrolled to reach it. Wrapping puts the actions on
 * their own line and lets them wrap among themselves; the wide layout, where
 * everything fits on one line, is unchanged.
 *
 * A collapse into an overflow menu was the alternative. Wrapping wins on the
 * ground that a menu hides the create button behind a click at exactly the
 * width where the screen is smallest and the pointer least precise — and the
 * actions are two to four short buttons, which is one wrapped line, not a
 * problem worth a menu.
 */
function PageHeader({ title, count, meta, description, actions, className }: PageHeaderProps) {
  return (
    <div className={cn("flex flex-wrap items-start justify-between gap-x-4 gap-y-2", className)}>
      {/* `min-w-0` so a long mono title truncates its own line instead of
          setting the row's minimum width and pushing the actions off. */}
      <div className="flex min-w-0 flex-col gap-0.5">
        <div className="flex items-baseline gap-2">
          <h1 className="font-mono text-[20px] font-bold tracking-[-0.02em] text-fg">{title}</h1>
          {count !== undefined && (
            <span className="font-mono text-sm text-fg-subtle tabular-nums">{count}</span>
          )}
        </div>
        {/* 3b's meta line — `4 objects · 28.2 KB · created 25 Jul 2026` — is mono 11. */}
        {meta && <p className="font-mono text-2xs text-fg-subtle">{meta}</p>}
        {description && <p className="text-[13px] text-fg-muted">{description}</p>}
      </div>
      {/* No `shrink-0`, and no `flex-1` either: the actions keep their
          max-content width when measured, so the row breaks and drops them
          onto a line of their own rather than squeezing them beside the
          title. `flex-wrap` on the group itself covers the width where even
          a whole line is not enough. */}
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
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
    // Focusable: the block scrolls in both axes, and a scroll container that cannot be
    // focused is reachable by pointer and by nothing else (WCAG 2.1.1).
    <pre
      tabIndex={0}
      className={cn(
        "overflow-auto rounded-md border border-border bg-bg-muted p-3 font-mono text-xs text-fg",
        className,
      )}
    >
      <code>{children}</code>
    </pre>
  )
}

export {
  Spinner,
  EmptyState,
  FieldLabel,
  SectionLabel,
  QueryListState,
  PageHeader,
  Code,
  CodeBlock,
}
