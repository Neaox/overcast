import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { Plus, RefreshCw, Search, type LucideIcon } from "lucide-react"
import { Button, type ButtonProps } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { PageHeader } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"

/**
 * Shared scaffold for service list pages.
 *
 * A page composes the pieces rather than filling in one prop bag:
 *
 * ```tsx
 * <ResourceListPage
 *   title="SQS Queues"
 *   count={queues.length}
 *   actions={
 *     <>
 *       <ServiceDocsButton … />
 *       <RawStateLink service="sqs" />
 *       <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
 *       <CreateAction onClick={openCreate}>Create queue</CreateAction>
 *     </>
 *   }
 * >
 *   <ResourceListCard>…</ResourceListCard>
 * </ResourceListPage>
 * ```
 *
 * Header actions always read Docs → Raw state → Refresh → Create.
 */

// ─── Page shell ───────────────────────────────────────────────────────────
interface ResourceListPageProps {
  title: string
  count?: number
  /** Secondary line beneath the title, e.g. "9 active · 15 resources". */
  meta?: React.ReactNode
  actions?: React.ReactNode
  children: React.ReactNode
  className?: string
}

function ResourceListPage({
  title,
  count,
  meta,
  actions,
  children,
  className,
}: ResourceListPageProps) {
  return (
    <div className={cn("flex w-full flex-col gap-4", className)}>
      <PageHeader title={title} count={count} meta={meta} actions={actions} />
      {children}
    </div>
  )
}

/** Card surface the list table sits on. */
function ResourceListCard({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("overflow-hidden rounded-card border border-border bg-bg-elevated", className)}
      {...props}
    />
  )
}

// ─── Header actions ───────────────────────────────────────────────────────
interface RefreshActionProps extends Omit<ButtonProps, "children"> {
  isFetching?: boolean
}

/**
 * Refreshing is a busy state, not a disabled one: 5b drops the icon, keeps the
 * control at full strength and lets the caret carry the motion, so the button
 * never dims mid-refetch.
 */
function RefreshAction({ isFetching, className, ...props }: RefreshActionProps) {
  return (
    <Button
      size="sm"
      variant="ghost"
      title="Refresh"
      busy={isFetching}
      busyLabel="Refreshing"
      className={className}
      {...props}
    >
      <RefreshCw className="h-3.5 w-3.5" />
      Refresh
    </Button>
  )
}

/** Solid accent button that opens the page's create flow. */
function CreateAction({ children, className, ...props }: ButtonProps) {
  return (
    <Button size="sm" className={className} {...props}>
      <Plus className="h-3.5 w-3.5" />
      {children}
    </Button>
  )
}

// ─── Filter ───────────────────────────────────────────────────────────────
interface ResourceListFilterProps {
  value: string
  onChange: (value: string) => void
  placeholder: string
  className?: string
}

/** Search input that sits between the page header and the list card. */
function ResourceListFilter({ value, onChange, placeholder, className }: ResourceListFilterProps) {
  return (
    <div className={cn("relative", className)}>
      <Search className="absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-fg-subtle" />
      <Input
        aria-label={placeholder}
        placeholder={placeholder}
        className="pl-8"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  )
}

// ─── Row content ──────────────────────────────────────────────────────────
interface ResourceNameProps {
  icon: LucideIcon
  name: React.ReactNode
  /** Trailing adornments such as badges. */
  children?: React.ReactNode
  className?: string
}

/** Primary cell content: type icon, then the resource's name. */
function ResourceName({ icon: Icon, name, children, className }: ResourceNameProps) {
  return (
    <span className={cn("flex items-center gap-2", className)}>
      <Icon className="h-3.5 w-3.5 shrink-0 text-fg-subtle" />
      <span className="truncate font-mono font-medium text-fg">{name}</span>
      {children}
    </span>
  )
}

/** Right-aligned container for the per-row action buttons. */
function RowActions({ className, ...props }: React.HTMLAttributes<HTMLSpanElement>) {
  return <span className={cn("flex items-center justify-end gap-0.5", className)} {...props} />
}

// The hover guard matches `buttonVariants` exactly so tailwind-merge still sees
// the two as the same utility and lets the tone win.
const rowActionVariants = cva("shrink-0 text-fg-subtle", {
  variants: {
    tone: {
      neutral:
        "not-disabled:not-aria-disabled:hover:bg-bg-muted not-disabled:not-aria-disabled:hover:text-fg",
      danger:
        "not-disabled:not-aria-disabled:hover:bg-danger-muted not-disabled:not-aria-disabled:hover:text-danger",
    },
  },
  defaultVariants: { tone: "neutral" },
})

interface RowActionProps extends ButtonProps, VariantProps<typeof rowActionVariants> {
  label: string
}

/**
 * Icon-only row action. Neutral by default; `tone="danger"` turns the icon the
 * danger colour on hover. Pass `asChild` with a `<Link>` for navigation.
 */
function RowAction({ label, tone, className, ...props }: RowActionProps) {
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      title={label}
      aria-label={label}
      className={cn(rowActionVariants({ tone }), className)}
      {...props}
    />
  )
}

// ─── Selection ────────────────────────────────────────────────────────────
interface SelectCheckboxProps {
  label: string
  checked: boolean
  indeterminate?: boolean
  onCheckedChange: (checked: boolean) => void
  className?: string
}

/** Checkbox for the leading selection column, including the select-all header. */
function SelectCheckbox({
  label,
  checked,
  indeterminate,
  onCheckedChange,
  className,
}: SelectCheckboxProps) {
  return (
    <input
      type="checkbox"
      aria-label={label}
      className={cn("h-3.5 w-3.5 cursor-pointer rounded accent-accent", className)}
      checked={checked}
      ref={(el) => {
        if (el) el.indeterminate = indeterminate ?? false
      }}
      onChange={(e) => onCheckedChange(e.target.checked)}
    />
  )
}

export {
  ResourceListPage,
  ResourceListCard,
  ResourceListFilter,
  RefreshAction,
  CreateAction,
  ResourceName,
  RowActions,
  RowAction,
  SelectCheckbox,
}
