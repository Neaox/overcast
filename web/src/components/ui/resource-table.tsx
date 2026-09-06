import * as React from "react"
import type { LucideIcon } from "lucide-react"
import { Trash2 } from "lucide-react"
import { useVirtualizer } from "@tanstack/react-virtual"
import {
  columnVisibilityFeature,
  createPaginatedRowModel,
  createSortedRowModel,
  rowPaginationFeature,
  rowSortingFeature,
  sortFn_alphanumeric,
  sortFn_datetime,
  sortFn_text,
  tableFeatures,
  useTable,
} from "@tanstack/react-table"
import type {
  ColumnDef,
  PaginationState,
  RowData,
  SortFn,
  SortingState,
  Updater,
} from "@tanstack/react-table"
import {
  Table,
  TableBody,
  TableCell,
  TableCellProse,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Button } from "@/components/ui/button"
import { CheckboxFilterDropdown } from "@/components/ui/checkbox-filter-dropdown"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { QueryListState, EmptyState } from "@/components/ui/primitives"
import { ResourceListCard, RowAction, RowActions } from "@/components/ui/resource-list-page"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

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
 *     {
 *       header: "Name",
 *       sortValue: (t) => shortName(t),
 *       cell: (t) => <ResourceName icon={Bell} name={shortName(t)} />,
 *     },
 *     { header: "ARN", cell: (t) => <ArnText arn={t.TopicArn} /> },
 *   ]}
 *   onDelete={{
 *     target: deleteTarget,
 *     onRequest: setDeleteTarget,
 *     onOpenChange: (open) => !open && setDeleteTarget(undefined),
 *     mutation: del,
 *     getVars: (t) => t.TopicArn ?? "",
 *     label: (t) => shortName(t),
 *     noun: "topic",
 *   }}
 * />
 * ```
 *
 * ## The engine
 *
 * The row model is TanStack Table v9 (`useTable`), so sorting, column
 * visibility and pagination are one implementation shared by every list in the
 * app rather than a per-page `useState`. Only three of v9's sixteen stock
 * features are registered (see `resourceTableFeatures` below) — `tableFeatures`
 * is the tree-shaking boundary, so the other thirteen never reach the bundle.
 *
 * Everything *visible* is still the repo's own primitives: `Table`, `TableRow`,
 * `TableHead`, `TableCell`/`TableCellProse`, `QueryListState`, `EmptyState`,
 * `ResourceListCard`, `ConfirmDialog`. TanStack owns the order and membership
 * of rows and columns; it renders nothing.
 *
 * Row actions are deliberately **not** a TanStack column. They are chrome, not
 * data — never sorted, never hidden — and keeping them out of the column model
 * is what makes the column-visibility menu correct without an exclusion list.
 *
 * ## Sorting
 *
 * A column is sortable exactly when it declares `sortValue`. A bare
 * `sortable: true` could not work: `cell` returns a `ReactNode`, which has no
 * ordering — the accessor is the opt-in. Sorting is single-column, cycling
 * none → asc → desc → none on header click.
 *
 * Sort state is the caller's if it wants it: pass `sort`/`onSortChange` from
 * `useSortSearchParam` and the sorted view deep-links as `?sort=name` (or
 * `?sort=-name` for descending — JSON:API's leading-dash convention), the same
 * contract `q` and `tab` already have. Omit them and `ResourceTable` keeps the
 * sort in local state, so a page gets sorting for free.
 *
 * **A list that refetches sets `defaultSort`.** An SDK `List*` call returns the
 * emulator's storage order, which is not stable across refetches, so a polled
 * list can move a row out from under the cursor between two polls. Sorting by
 * the name column (or by the timestamp, where the table is a feed) costs
 * nothing and makes the order something the reader can rely on — a stable
 * default beats a faithful but jittery one. Sort a timestamp on a `Date`, not
 * on the ISO string it arrived as: a string sorts A→Z, which is only
 * accidentally chronological.
 *
 * ## Rows
 *
 * `rowKey` identifies a row (React's key, and the sort-stable row id); it takes
 * the item's index too, for a feed whose entries carry no id of their own.
 * `rowClassName` styles the row itself — the tone a log level or a failure
 * event paints across every cell, which `cellClassName` cannot reach because a
 * cell background does not fill the row's padding. Mark a column `interactive`
 * when its cell holds a control of its own, so a click that misses the control
 * stops in the cell instead of navigating.
 *
 * ## Delete
 *
 * `onDelete` is deliberately controlled by the caller rather than owning the
 * `deleteTarget` state itself: the mutation's `onSuccess` (from
 * `useResourceMutation`) is what clears it today, and that callback lives on
 * the page, not in this component.
 *
 * `getVars` builds the mutation's variables from the row, and the mutation is
 * generic in them: `DeleteWebACL` needs the whole summary for its lock token,
 * `DeleteRoute` needs `{apiId, routeId}`, `DeleteDistribution` needs an ETag
 * only `GetDistribution` returns — so `getVars` may also be async, and the
 * confirm button stays pending while it resolves. A rejected `getVars` leaves
 * the dialog open and cancels the pending state; reporting it is the caller's,
 * inside `getVars`. `canDelete` hides the action on rows that cannot be
 * deleted (a stack mid-update), which a page otherwise has to drop to
 * `rowActions` and its own `ConfirmDialog` for.
 *
 * A page with a filter box passes `query.data` already filtered, plus
 * `isFiltered`/`onClearFilter` so the empty state can tell "nothing matches
 * the filter" apart from "nothing exists yet" — see `useFilterSearchParam`
 * for wiring the filter itself to the route's search params.
 */

/**
 * The v9 feature set, declared once at module scope so it is referentially
 * stable across renders (v9 rebuilds its models when `features`, `data` or
 * `columns` change identity).
 *
 * Adopted: row sorting, column visibility, row pagination. Deliberately not
 * adopted: filtering (the page filters `query.data` and `useFilterSearchParam`
 * owns `q` — moving it in here would fork that contract), row selection,
 * column sizing/resizing/pinning/ordering, grouping, aggregation, expanding,
 * faceting, cell selection. `stockFeatures` is never used: it bundles all
 * sixteen and defeats the point. Neither is `useLegacyTable`, which is
 * deprecated and bundles every feature.
 *
 * `sortFns` registers only the three built-ins v9's `'auto'` resolution can ask
 * for (`datetime`, `alphanumeric`, `text`); anything else falls back to
 * `sortFn_basic`, which the feature imports itself.
 *
 * Pagination is in the shared set even though it is opt-in per caller: this is
 * one component, so a conditional feature set would mean two `tableFeatures()`
 * objects and two divergent `useTable` types for a few hundred bytes. With
 * `pageSize` unset the page size is larger than any list this UI renders, so
 * the paginated row model is a pass-through.
 */
const resourceTableFeatures = tableFeatures({
  rowSortingFeature,
  columnVisibilityFeature,
  rowPaginationFeature,
  sortedRowModel: createSortedRowModel(),
  paginatedRowModel: createPaginatedRowModel(),
  sortFns: {
    alphanumeric: sortFn_alphanumeric,
    datetime: sortFn_datetime,
    text: sortFn_text,
  },
})

type ResourceTableFeatures = typeof resourceTableFeatures

/** Page size used when the caller did not ask for pagination — effectively "all rows". */
const UNPAGED_PAGE_SIZE = 1_000_000

/** Height a `TableCell` row renders at (`py-2.5` on 12px mono), the virtualizer's default estimate. */
const DEFAULT_ROW_HEIGHT = 37

const EMPTY_ROWS: never[] = []
const EMPTY_SORTING: SortingState = []

/** Narrows an `X | Promise<X>` without assuming the promise is a native one. */
function isPromiseLike<X>(value: X | Promise<X>): value is Promise<X> {
  return typeof (value as { then?: unknown } | null)?.then === "function"
}

/** A value a column can be ordered by. `cell` renders a `ReactNode`, which cannot be compared. */
export type ResourceTableSortValue = string | number | boolean | Date | null | undefined

/** Single-column sort, matching the one-token `?sort=name` / `?sort=-name` search param. */
export interface ResourceTableSort {
  id: string
  desc: boolean
}

export interface ResourceTableColumn<T extends RowData> {
  /**
   * Stable column id — the sort key that appears in the URL and the key the
   * column-visibility menu remembers. Defaults to a slug of `header` when that
   * is a string, else the column's position. Give a sortable column an explicit
   * `id` if its header text is likely to be reworded.
   */
  id?: string
  header: React.ReactNode
  headerClassName?: string
  cellClassName?: string
  /** Renders the cell with `TableCellProse` (sans, for a sentence a human wrote) instead of the mono default. */
  prose?: boolean
  cell: (item: T) => React.ReactNode
  /**
   * Makes the column sortable, and supplies the value to sort by. Ordering is
   * inferred from the values (`Date` → datetime, mixed text and digits →
   * alphanumeric, so `stream-2` sorts before `stream-10`).
   */
  sortValue?: (item: T) => ResourceTableSortValue
  /** Overrides the inferred comparison — a v9 built-in's name or a `SortFn`. */
  sortFn?: "auto" | "alphanumeric" | "datetime" | "text" | SortFn<ResourceTableFeatures, T>
  /**
   * Whether the columns menu may hide this column. Defaults to `true` for every
   * column but the first, which identifies the row and stays put.
   */
  hideable?: boolean
  /** Starts hidden, discoverable through the columns menu. Implies `hideable`. */
  defaultHidden?: boolean
  /**
   * The cell holds a control of its own — a copy button, a link, a switch. A
   * click anywhere in it, its padding included, stops there rather than
   * reaching `onRowClick`, so aiming at the control and missing it by a pixel
   * no longer navigates away from the row you were about to act on.
   */
  interactive?: boolean
}

interface ResourceTableQuery<T> {
  data?: T[]
  isLoading: boolean
  error?: unknown
}

interface ResourceTableDeleteConfig<T, TVars> {
  /** The row currently targeted for deletion — page-owned state (`useState<T>()`). */
  target: T | undefined
  /** Opens the confirm dialog for a row — usually the target's `useState` setter. */
  onRequest: (item: T) => void
  /** Matches `ConfirmDialog`'s prop; closes the dialog by clearing the target. */
  onOpenChange: (open: boolean) => void
  /** The mutation that performs the delete — typically from `useResourceMutation`. */
  mutation: { mutate: (vars: TVars) => void; isPending: boolean }
  /**
   * Builds the mutation's variables from the row — an id or name for most
   * deletes, the whole row where the call needs more of it (a lock token, an
   * ETag, a composite `{apiId, routeId}`). Returning a promise is allowed: the
   * confirm button stays pending until it resolves, and a rejection leaves the
   * dialog open without mutating, so any error reporting belongs inside here.
   */
  getVars: (item: T) => TVars | Promise<TVars>
  /**
   * Hides the delete action on rows that cannot be deleted — a stack mid-update,
   * a resource the emulator owns. Defaults to every row.
   */
  canDelete?: (item: T) => boolean
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

/** Opt-in row virtualization. Only worth reaching for past a few hundred rows. */
interface ResourceTableVirtualizeConfig {
  /** Row height estimate in px. Defaults to the 37px a `TableCell` renders at. */
  estimateRowHeight?: number
  /** Height of the scroll viewport in px. Defaults to 560. */
  maxHeight?: number
  /** Rows rendered beyond the viewport on each side. Defaults to 8. */
  overscan?: number
}

interface ResourceTableProps<T extends RowData, TVars> {
  query: ResourceTableQuery<T>
  columns: ResourceTableColumn<T>[]
  /**
   * Stable identity for a row. The index is there for a feed whose entries
   * carry none of their own — two log lines can share a timestamp and a
   * message — and is what such a table would otherwise key by anyway.
   */
  rowKey: (item: T, index: number) => string | number
  /** Lowercase plural noun, e.g. `"topics"` — feeds the skeleton footer. */
  noun: string
  /** Row click handler — rows navigate to a detail page when this is set. */
  onRowClick?: (item: T) => void
  /**
   * Classes for the `<tr>` itself — the tone a row carries as a whole, such as
   * the danger wash on a failure event or a log line's level tint. A column's
   * `cellClassName` cannot do this: a cell background stops at the cell, so a
   * tinted row faked from cells is a row with gaps in it.
   */
  rowClassName?: (item: T, index: number) => string | undefined
  emptyIcon?: LucideIcon
  emptyTitle?: string
  emptyDescription?: string
  emptyAction?: React.ReactNode
  /**
   * Rendered directly beneath the empty state, inside the same container as the
   * table — where `RegionElsewhereNotice` belongs, tucked under the empty
   * state's bottom padding by its own negative margin. Only on the plain empty
   * state: a filtered-empty list and a failed fetch are different facts, and
   * an explanation of an empty region would be wrong about both.
   */
  emptyExtra?: React.ReactNode
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
  onDelete?: ResourceTableDeleteConfig<T, TVars>
  /**
   * Current sort, when the page owns it — pair with `onSortChange`, usually
   * both straight from `useSortSearchParam` so the sort deep-links. Omit both
   * and the sort lives in this component's own state.
   */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
  /** Sort applied before the user touches a header. Ignored once `sort`/`onSortChange` take over. */
  defaultSort?: ResourceTableSort
  /** Rows per page. Unset means no pagination and no pager. */
  pageSize?: number
  /**
   * Forces the columns menu on or off. It appears by default only on a
   * `"card"` table with at least `COLUMN_TOGGLE_MIN_COLUMNS` columns, more than
   * one of them hideable — see that constant for why the bar is that high.
   */
  columnToggle?: boolean
  /** Virtualizes the rows. `true` accepts every default. */
  virtualize?: boolean | ResourceTableVirtualizeConfig
  /** `"card"` (default) wraps the table in `ResourceListCard`; `"embedded"` renders bare for sub-tables. */
  variant?: "card" | "embedded"
  className?: string
}

/**
 * `"Created at"` → `"created-at"`. Only used when a column has a string header
 * and no explicit `id`, so the URL reads `?sort=-created-at` rather than
 * `?sort=-col-3`.
 */
function slugifyHeader(header: React.ReactNode, index: number): string {
  if (typeof header !== "string") return `col-${index}`
  const slug = header
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
  return slug || `col-${index}`
}

/** Human label for a column — the sort tooltip's and the columns menu's text. */
function columnLabel(header: React.ReactNode | undefined, id: string): string {
  return typeof header === "string" ? header : id
}

/**
 * Where the sort button puts its label. A sortable header is a full-bleed
 * flex `<button>`, and `text-align` does not reach a flex child — so a
 * `headerClassName` of `text-right` left every numeric column's label on the
 * left until callers worked around it with `[&>button]:justify-end`. Read the
 * alignment off the class the column already declares rather than adding a
 * second way to say it, so the two can never disagree.
 */
function sortButtonJustify(headerClassName: string | undefined): string {
  if (!headerClassName) return "justify-start"
  if (/(^|\s)text-right(\s|$)/.test(headerClassName)) return "justify-end"
  if (/(^|\s)text-center(\s|$)/.test(headerClassName)) return "justify-center"
  return "justify-start"
}

/**
 * How many columns a card table needs before it offers a columns menu.
 *
 * The first cut showed the menu whenever two columns could be hidden, which
 * put one on nearly every list: four of the six conversion waves reported it as
 * noise, and 70 call sites ended up turning it off by hand (#1327). A menu
 * earns its place on a wide table, where hiding a column is how the row stops
 * wrapping — not on a four-column list whose every column is read by every
 * reader. An embedded sub-table never gets one at all: its container clips the
 * popover.
 */
const COLUMN_TOGGLE_MIN_COLUMNS = 5

export function ResourceTable<T extends RowData, TVars = string>({
  query,
  columns,
  rowKey,
  noun,
  onRowClick,
  rowClassName,
  emptyIcon: EmptyIcon,
  emptyTitle,
  emptyDescription,
  emptyAction,
  emptyExtra,
  isFiltered,
  onClearFilter,
  filteredEmptyTitle,
  filteredEmptyDescription,
  errorTitle,
  loadingCount,
  rowActions,
  onDelete,
  sort,
  onSortChange,
  defaultSort,
  pageSize,
  columnToggle,
  virtualize,
  variant = "card",
  className,
}: ResourceTableProps<T, TVars>) {
  // True while an async `getVars` is resolving, so the confirm button keeps
  // its pending state across a lookup the mutation itself has not started yet.
  const [isPreparingDelete, setIsPreparingDelete] = React.useState(false)

  const items: T[] = query.data ?? EMPTY_ROWS
  const isEmpty = items.length === 0
  const hasActionsColumn = Boolean(rowActions || onDelete)

  // Every caller writes its `columns` array inline, so the array — and the
  // closures in it — are new on every render. The definitions handed to
  // TanStack therefore read through a ref and are memoized on the shape that
  // actually matters to the row model (ids, sortability, hideability), which
  // keeps the column instances stable while the render closures stay fresh.
  const resolved = columns.map((column, index) => ({
    ...column,
    id: column.id ?? slugifyHeader(column.header, index),
  }))
  const resolvedRef = React.useRef(resolved)
  resolvedRef.current = resolved
  const rowKeyRef = React.useRef(rowKey)
  rowKeyRef.current = rowKey

  const columnSignature = resolved
    .map((c) => `${c.id}|${c.sortValue ? 1 : 0}|${c.hideable ?? ""}|${c.defaultHidden ? 1 : 0}`)
    .join(",")

  const columnDefs = React.useMemo<ColumnDef<ResourceTableFeatures, T, unknown>[]>(
    () =>
      resolvedRef.current.map((column, index) => ({
        id: column.id,
        accessorFn: (item: T) => resolvedRef.current[index]?.sortValue?.(item) ?? null,
        header: () => resolvedRef.current[index]?.header,
        cell: (context) => resolvedRef.current[index]?.cell(context.row.original),
        enableSorting: Boolean(column.sortValue),
        sortFn: column.sortFn ?? "auto",
        enableHiding: column.hideable ?? (column.defaultHidden === true || index > 0),
      })),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- the signature is the memo key; the defs read live columns through the ref
    [columnSignature],
  )

  // ── State ownership ────────────────────────────────────────────────────
  // Sorting is always controlled from TanStack's point of view; who holds the
  // value is the only difference between the URL-bound and the local case, so
  // there is one code path rather than two option shapes.
  const [internalSort, setInternalSort] = React.useState<ResourceTableSort | undefined>(defaultSort)
  const isSortControlled = onSortChange !== undefined
  const activeSort = isSortControlled ? sort : internalSort
  const sortId = activeSort?.id
  const sortDesc = activeSort?.desc

  const sortingState = React.useMemo<SortingState>(
    () => (sortId ? [{ id: sortId, desc: Boolean(sortDesc) }] : EMPTY_SORTING),
    [sortId, sortDesc],
  )
  const sortingStateRef = React.useRef(sortingState)
  sortingStateRef.current = sortingState
  const commitSortRef = React.useRef(isSortControlled ? onSortChange : setInternalSort)
  commitSortRef.current = isSortControlled ? onSortChange : setInternalSort

  const handleSortingChange = React.useCallback((updater: Updater<SortingState>) => {
    const next = typeof updater === "function" ? updater(sortingStateRef.current) : updater
    commitSortRef.current(next.length > 0 ? { id: next[0].id, desc: next[0].desc } : undefined)
  }, [])

  const [columnVisibility, setColumnVisibility] = React.useState<Record<string, boolean>>(() => {
    const initial: Record<string, boolean> = {}
    for (const column of resolved) if (column.defaultHidden) initial[column.id] = false
    return initial
  })
  const handleVisibilityChange = React.useCallback((updater: Updater<Record<string, boolean>>) => {
    setColumnVisibility((previous) => (typeof updater === "function" ? updater(previous) : updater))
  }, [])

  const [pageIndex, setPageIndex] = React.useState(0)
  const effectivePageSize = pageSize ?? UNPAGED_PAGE_SIZE
  const pageSizeRef = React.useRef(effectivePageSize)
  pageSizeRef.current = effectivePageSize
  const paginationState = React.useMemo<PaginationState>(
    () => ({ pageIndex, pageSize: effectivePageSize }),
    [pageIndex, effectivePageSize],
  )
  const handlePaginationChange = React.useCallback((updater: Updater<PaginationState>) => {
    setPageIndex((previous) =>
      typeof updater === "function"
        ? updater({ pageIndex: previous, pageSize: pageSizeRef.current }).pageIndex
        : updater.pageIndex,
    )
  }, [])

  const table = useTable<ResourceTableFeatures, T>({
    features: resourceTableFeatures,
    data: items,
    columns: columnDefs,
    getRowId: (item, index) => String(rowKeyRef.current(item, index)) || String(index),
    enableMultiSort: false,
    state: { sorting: sortingState, columnVisibility, pagination: paginationState },
    onSortingChange: handleSortingChange,
    onColumnVisibilityChange: handleVisibilityChange,
    onPaginationChange: handlePaginationChange,
  })

  // A filter that shrinks the list can strand the pager past the last page.
  const pageCount = table.getPageCount()
  React.useEffect(() => {
    if (pageIndex > 0 && pageIndex >= pageCount) setPageIndex(0)
  }, [pageIndex, pageCount])

  const rows = table.getRowModel().rows

  // ── Virtualization ─────────────────────────────────────────────────────
  // v9 has no virtualization feature — its own guide is explicit that
  // "virtualization is a rendering strategy, not a table feature" — so this is
  // renderer composition over `getRowModel().rows` with the
  // `@tanstack/react-virtual` the app already uses in eight other places. The
  // hook runs unconditionally (with `count: 0` when off) because hooks must;
  // the spacer-row technique below keeps the real `<table>` doing the
  // column-width arithmetic, so a virtualized table looks identical to a
  // plain one.
  const virtualConfig: ResourceTableVirtualizeConfig =
    virtualize && virtualize !== true ? virtualize : {}
  const scrollRef = React.useRef<HTMLDivElement>(null)
  const rowHeight = virtualConfig.estimateRowHeight ?? DEFAULT_ROW_HEIGHT
  const virtualizer = useVirtualizer({
    count: virtualize ? rows.length : 0,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight,
    getItemKey: (index) => rows[index]?.id ?? index,
    overscan: virtualConfig.overscan ?? 8,
  })
  const virtualRows = virtualizer.getVirtualItems()
  const paddingTop = virtualRows.length > 0 ? virtualRows[0].start : 0
  const paddingBottom =
    virtualRows.length > 0
      ? virtualizer.getTotalSize() - virtualRows[virtualRows.length - 1].end
      : 0

  // ── Rendering ──────────────────────────────────────────────────────────
  const columnsById = new Map(resolved.map((column) => [column.id, column]))
  const hideableColumns = table.getAllLeafColumns().filter((column) => column.getCanHide())
  const showColumnToggle =
    columnToggle ??
    (variant === "card" &&
      resolved.length >= COLUMN_TOGGLE_MIN_COLUMNS &&
      hideableColumns.length > 1)

  // `ariaRowIndex` is only meaningful while virtualizing: the DOM then holds a window
  // onto the rows, so without `aria-rowcount` on the table and a 1-based `aria-rowindex`
  // on each row a screen reader reports "row 3 of 20" inside a five-thousand-row table
  // and has no way to tell that scrolling brings more. Row 1 is the header.
  const renderRow = (row: (typeof rows)[number], ariaRowIndex?: number) => (
    <TableRow
      key={row.id}
      aria-rowindex={ariaRowIndex}
      className={rowClassName?.(row.original, row.index)}
      onClick={onRowClick ? () => onRowClick(row.original) : undefined}
    >
      {row.getVisibleCells().map((cell) => {
        const column = columnsById.get(cell.column.id)
        const Cell = column?.prose ? TableCellProse : TableCell
        return (
          <Cell
            key={cell.id}
            className={column?.cellClassName}
            onClick={
              onRowClick && column?.interactive ? (event) => event.stopPropagation() : undefined
            }
          >
            <table.FlexRender cell={cell} />
          </Cell>
        )
      })}
      {hasActionsColumn && (
        <TableCell onClick={(event) => event.stopPropagation()}>
          <RowActions>
            {rowActions?.(row.original)}
            {onDelete && (onDelete.canDelete?.(row.original) ?? true) && (
              <RowAction
                label={
                  onDelete.actionLabel?.(row.original) ?? `Delete ${onDelete.label(row.original)}`
                }
                tone="danger"
                onClick={() => onDelete.onRequest(row.original)}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </RowAction>
            )}
          </RowActions>
        </TableCell>
      )}
    </TableRow>
  )

  const headerRow = (
    <TableRow>
      {table.getHeaderGroups()[0]?.headers.map((header) => {
        const column = columnsById.get(header.column.id)
        const canSort = header.column.getCanSort()
        const sorted = header.column.getIsSorted()
        const label = columnLabel(column?.header, header.column.id)
        return (
          <TableHead
            key={header.id}
            scope="col"
            className={cn(canSort && "p-0", column?.headerClassName)}
            aria-sort={
              sorted === "asc"
                ? "ascending"
                : sorted === "desc"
                  ? "descending"
                  : canSort
                    ? "none"
                    : undefined
            }
          >
            {header.isPlaceholder ? null : canSort ? (
              // The arrow only appears on the active column — a permanent arrow
              // on every header reads as a state rather than an offer. Same
              // shape as `s3/components/object-controls.tsx`'s `SortHead`.
              <button
                type="button"
                onClick={header.column.getToggleSortingHandler()}
                title={`Sort by ${label.toLowerCase()}`}
                // `fieldLabel` is repeated here rather than inherited from the
                // `<th>`: a `<button>` does not pick up the label's case and
                // metrics, so without it a sortable header renders visibly
                // larger and in mixed case next to its plain neighbours.
                className={cn(
                  fieldLabel,
                  "inline-flex h-full w-full cursor-pointer items-center gap-1 px-4 py-2 transition-colors select-none hover:text-fg",
                  sortButtonJustify(column?.headerClassName),
                  sorted && "text-fg",
                )}
              >
                <table.FlexRender header={header} />
                <span className={cn("text-2xs leading-none", !sorted && "opacity-0")} aria-hidden>
                  {sorted === "asc" ? "▲" : "▼"}
                </span>
              </button>
            ) : (
              <table.FlexRender header={header} />
            )}
          </TableHead>
        )
      })}
      {hasActionsColumn && <TableHead className="w-20 text-right" />}
    </TableRow>
  )

  // `emptyExtra` follows the empty state as a sibling, which is the placement
  // its one caller shape — `RegionElsewhereNotice`, whose `-mt-8` tucks it into
  // the `EmptyState`'s bottom padding — is drawn for. Not on a filtered-empty
  // list or a failed fetch: an explanation of "nothing here" is wrong about
  // both, and `QueryListState` renders something else entirely for each.
  const showEmptyExtra =
    Boolean(emptyExtra) && isEmpty && !query.isLoading && !query.error && !isFiltered

  const body =
    query.isLoading || isEmpty ? (
      <>
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
        {showEmptyExtra && emptyExtra}
      </>
    ) : (
      <>
        {virtualize ? (
          <div
            ref={scrollRef}
            data-slot="virtual-scroll"
            className="overflow-auto"
            style={{ maxHeight: virtualConfig.maxHeight ?? 560 }}
          >
            <Table aria-rowcount={rows.length + 1}>
              <TableHeader className="sticky top-0 z-10">
                {React.cloneElement(headerRow, { "aria-rowindex": 1 })}
              </TableHeader>
              <TableBody>
                {paddingTop > 0 && (
                  <tr aria-hidden>
                    <td style={{ height: paddingTop }} />
                  </tr>
                )}
                {virtualRows.map((virtualRow) =>
                  renderRow(rows[virtualRow.index], virtualRow.index + 2),
                )}
                {paddingBottom > 0 && (
                  <tr aria-hidden>
                    <td style={{ height: paddingBottom }} />
                  </tr>
                )}
              </TableBody>
            </Table>
          </div>
        ) : (
          <Table>
            <TableHeader>{headerRow}</TableHeader>
            {/* `rows.map(renderRow)` would hand Array.map's index to `ariaRowIndex`,
                which is 0-based while `aria-rowindex` starts at 1. Unvirtualized the DOM
                holds every row, so it needs no index at all. */}
            <TableBody>{rows.map((row) => renderRow(row))}</TableBody>
          </Table>
        )}

        {pageSize !== undefined && pageCount > 1 && (
          <div
            className={cn(
              "flex items-center justify-between gap-2 text-xs text-fg-subtle",
              variant === "card" ? "border-t border-border px-3 py-2" : "pt-2",
            )}
          >
            <span className="font-mono">
              Page {pageIndex + 1} of {pageCount} · {items.length} {noun}
            </span>
            <span className="flex items-center gap-1">
              <Button
                variant="ghost"
                size="sm"
                disabled={!table.getCanPreviousPage()}
                onClick={() => table.previousPage()}
              >
                Previous
              </Button>
              <Button
                variant="ghost"
                size="sm"
                disabled={!table.getCanNextPage()}
                onClick={() => table.nextPage()}
              >
                Next
              </Button>
            </span>
          </div>
        )}
      </>
    )

  // The columns menu sits *above* the card, not inside it: `ResourceListCard`
  // clips its children (`overflow-hidden`, which is what rounds the table's
  // corners), so a menu rendered inside would be cut off against the card's
  // edge on a short table. Anchored to its right edge for the same reason on
  // the other side — the trigger sits at the end of its row.
  const toolbar =
    showColumnToggle && !(query.isLoading || isEmpty) ? (
      <div className={cn("flex items-center justify-end", variant === "card" ? "mb-2" : "pb-2")}>
        <CheckboxFilterDropdown
          triggerLabel="Columns"
          model="hide"
          align="end"
          items={hideableColumns.map((column) => ({
            id: column.id,
            label: columnLabel(columnsById.get(column.id)?.header, column.id),
          }))}
          selected={
            new Set(hideableColumns.filter((column) => !column.getIsVisible()).map((c) => c.id))
          }
          onToggle={(id) => table.getColumn(id)?.toggleVisibility()}
          onShowAll={() => table.toggleAllColumnsVisible(true)}
          onHideAll={() => table.toggleAllColumnsVisible(false)}
        />
      </div>
    ) : null

  return (
    <>
      {variant === "embedded" ? (
        <div className={className}>
          {toolbar}
          {body}
        </div>
      ) : (
        <>
          {toolbar}
          <ResourceListCard className={cn(className)}>{body}</ResourceListCard>
        </>
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
          isPending={onDelete.mutation.isPending || isPreparingDelete}
          onConfirm={() => {
            if (!onDelete.target) return
            const vars = onDelete.getVars(onDelete.target)
            if (!isPromiseLike(vars)) {
              onDelete.mutation.mutate(vars)
              return
            }
            setIsPreparingDelete(true)
            void Promise.resolve(vars).then(
              (resolved) => {
                setIsPreparingDelete(false)
                onDelete.mutation.mutate(resolved)
              },
              () => setIsPreparingDelete(false),
            )
          }}
        />
      )}
    </>
  )
}
