import * as React from "react"
import type { LucideIcon } from "lucide-react"
import {
  Table,
  TableBody,
  TableCell,
  TableCellProse,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { QueryListState, EmptyState } from "@/components/ui/primitives"
import { ResourceListCard, RowAction, RowActions } from "@/components/ui/resource-list-page"
import { cn } from "@/lib/utils"
import { Trash2 } from "lucide-react"

/**
 * Shared body for a service list/index page: the `isLoading || empty` branch,
 * the table, the row-action column and (optionally) the delete confirm flow.
 * Sits inside `<ResourceListPage>`, which owns the header instead.
 *
 * ```tsx
 * <ResourceTable
 *   query={{ data: topics, isLoading, error }}
 *   noun="topics"
 *   emptyIcon={Bell}
 *   emptyTitle="No topics yet"
 *   emptyDescription="Create a topic to get started."
 *   rowKey={(t) => t.TopicArn ?? ""}
 *   onRowClick={(t) => navigate({ to: "/sns/$topic", params: { topic: shortName(t) } })}
 *   columns={[
 *     { header: "Name", cell: (t) => <ResourceName icon={Bell} name={shortName(t)} /> },
 *     { header: "ARN",  cell: (t) => <ArnText arn={t.TopicArn} /> },
 *   ]}
 *   onDelete={{
 *     target: deleteTarget,
 *     onRequest: setDeleteTarget,
 *     onOpenChange: (open) => !open && setDeleteTarget(undefined),
 *     mutation: del,
 *     getId: (t) => t.TopicArn ?? "",
 *     label: (t) => shortName(t),
 *     noun: "topic",
 *   }}
 * />
 * ```
 *
 * `onDelete` is deliberately controlled by the caller rather than owning the
 * `deleteTarget` state itself: the mutation's `onSuccess` (from
 * `useResourceMutation`) is what clears it today, and that callback lives on
 * the page, not in this component.
 *
 * A page with a filter box passes `query.data` already filtered, plus
 * `isFiltered`/`onClearFilter` so the empty state can tell "nothing matches
 * the filter" apart from "nothing exists yet" — see `useFilterSearchParam`
 * for wiring the filter itself to the route's search params.
 */

export interface ResourceTableColumn<T> {
  header: React.ReactNode
  headerClassName?: string
  cellClassName?: string
  /** Renders the cell with `TableCellProse` (sans, for a sentence a human wrote) instead of the mono default. */
  prose?: boolean
  cell: (item: T) => React.ReactNode
}

interface ResourceTableQuery<T> {
  data?: T[]
  isLoading: boolean
  error?: unknown
}

interface ResourceTableDeleteConfig<T> {
  /** The row currently targeted for deletion — page-owned state (`useState<T>()`). */
  target: T | undefined
  /** Opens the confirm dialog for a row — usually the target's `useState` setter. */
  onRequest: (item: T) => void
  /** Matches `ConfirmDialog`'s prop; closes the dialog by clearing the target. */
  onOpenChange: (open: boolean) => void
  /** The mutation that performs the delete — typically from `useResourceMutation`. */
  mutation: { mutate: (id: string) => void; isPending: boolean }
  /** Resolve the mutation variable (id/name/arn) from the row. */
  getId: (item: T) => string
  /** Display name used in the confirm dialog's default title/description. */
  label: (item: T) => string
  /** Lowercase singular noun, e.g. `"topic"`. Feeds the default copy. */
  noun: string
  /** Full override of the confirm dialog's title. Defaults to `Delete {noun}?`. */
  title?: string
  /** Full override of the confirm dialog's description. */
  description?: (item: T) => React.ReactNode
  /** Confirm button label. Defaults to `ConfirmDialog`'s own default, "Delete". */
  confirmLabel?: string
  /** Row action's accessible label. Defaults to `Delete {label(item)}`. */
  actionLabel?: (item: T) => string
}

interface ResourceTableProps<T> {
  query: ResourceTableQuery<T>
  columns: ResourceTableColumn<T>[]
  rowKey: (item: T) => string
  /** Lowercase plural noun, e.g. `"topics"` — feeds the skeleton footer. */
  noun: string
  /** Row click handler — rows navigate to a detail page when this is set. */
  onRowClick?: (item: T) => void
  emptyIcon?: LucideIcon
  emptyTitle?: string
  emptyDescription?: string
  emptyAction?: React.ReactNode
  /**
   * True while a filter/search is narrowing the list — swaps the empty state
   * to "no matches" copy with a clear-filter action instead of `emptyAction`,
   * so a filter that turns up nothing never reads as "this doesn't exist,
   * create one". Has no effect on a load error, which always wins (see
   * `QueryListState`).
   */
  isFiltered?: boolean
  /** Clears the active filter. Powers the default "Clear filter" action shown when `isFiltered` narrows the list to zero rows. */
  onClearFilter?: () => void
  filteredEmptyTitle?: string
  filteredEmptyDescription?: string
  errorTitle?: string
  loadingCount?: number
  /** Extra per-row actions rendered before the delete action, if any. */
  rowActions?: (item: T) => React.ReactNode
  onDelete?: ResourceTableDeleteConfig<T>
  /** `"card"` (default) wraps the table in `ResourceListCard`; `"embedded"` renders bare for sub-tables. */
  variant?: "card" | "embedded"
  className?: string
}

export function ResourceTable<T>({
  query,
  columns,
  rowKey,
  noun,
  onRowClick,
  emptyIcon: EmptyIcon,
  emptyTitle,
  emptyDescription,
  emptyAction,
  isFiltered,
  onClearFilter,
  filteredEmptyTitle,
  filteredEmptyDescription,
  errorTitle,
  loadingCount,
  rowActions,
  onDelete,
  variant = "card",
  className,
}: ResourceTableProps<T>) {
  const items = query.data ?? []
  const isEmpty = items.length === 0
  const hasActionsColumn = Boolean(rowActions || onDelete)

  const body =
    query.isLoading || isEmpty ? (
      <QueryListState
        isLoading={query.isLoading}
        isEmpty={isEmpty}
        error={query.error}
        emptyTitle={emptyTitle ?? `No ${noun}`}
        errorTitle={errorTitle ?? `Failed to load ${noun}`}
        loadingNoun={noun}
        loadingCount={loadingCount}
        isFiltered={isFiltered}
        onClearFilter={onClearFilter}
        filteredEmptyTitle={filteredEmptyTitle ?? `No matching ${noun}`}
        filteredEmptyDescription={filteredEmptyDescription ?? `No ${noun} match your filter.`}
        empty={
          !isFiltered && (emptyDescription || emptyAction || EmptyIcon) ? (
            <EmptyState
              icon={EmptyIcon && <EmptyIcon className="h-10 w-10" />}
              title={emptyTitle ?? `No ${noun}`}
              description={emptyDescription}
              action={emptyAction}
            />
          ) : undefined
        }
      />
    ) : (
      <Table>
        <TableHeader>
          <TableRow>
            {columns.map((col, i) => (
              <TableHead key={i} className={col.headerClassName}>
                {col.header}
              </TableHead>
            ))}
            {hasActionsColumn && <TableHead className="w-20 text-right" />}
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((item) => {
            const key = rowKey(item)
            return (
              <TableRow key={key} onClick={onRowClick ? () => onRowClick(item) : undefined}>
                {columns.map((col, i) => {
                  const Cell = col.prose ? TableCellProse : TableCell
                  return (
                    <Cell key={i} className={col.cellClassName}>
                      {col.cell(item)}
                    </Cell>
                  )
                })}
                {hasActionsColumn && (
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <RowActions>
                      {rowActions?.(item)}
                      {onDelete && (
                        <RowAction
                          label={onDelete.actionLabel?.(item) ?? `Delete ${onDelete.label(item)}`}
                          tone="danger"
                          onClick={() => onDelete.onRequest(item)}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </RowAction>
                      )}
                    </RowActions>
                  </TableCell>
                )}
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    )

  return (
    <>
      {variant === "embedded" ? (
        <div className={className}>{body}</div>
      ) : (
        <ResourceListCard className={cn(className)}>{body}</ResourceListCard>
      )}

      {onDelete && (
        <ConfirmDialog
          open={!!onDelete.target}
          onOpenChange={onDelete.onOpenChange}
          title={onDelete.title ?? `Delete ${onDelete.noun}?`}
          confirmLabel={onDelete.confirmLabel}
          description={
            onDelete.target &&
            (onDelete.description?.(onDelete.target) ?? (
              <>
                Permanently delete{" "}
                <span className="font-mono font-medium">{onDelete.label(onDelete.target)}</span>?
                This cannot be undone.
              </>
            ))
          }
          isPending={onDelete.mutation.isPending}
          onConfirm={() =>
            onDelete.target && onDelete.mutation.mutate(onDelete.getId(onDelete.target))
          }
        />
      )}
    </>
  )
}
