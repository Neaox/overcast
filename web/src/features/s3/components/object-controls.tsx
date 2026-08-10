/**
 * The search box, scope switch and sortable headers above the object listing.
 *
 * Split out of `bucket-detail` because none of it needs the bucket, the queries
 * or the router — it is all props in, callbacks out, which is also what makes
 * it testable without standing up an S3 listing.
 */

import { Search, X, Folder, ListTree } from "lucide-react"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/primitives"
import { TableHead } from "@/components/ui/table"
import { Tooltip } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import type { ListScope } from "@/features/s3/data"
import type { NameSlice, ObjectSort, SortColumn } from "@/features/s3/object-browser"

// ─── Search bar ────────────────────────────────────────────────────────────

export interface ObjectSearchBarProps {
  value: string
  onChange: (value: string) => void
  scope: ListScope
  onScopeChange: (scope: ListScope) => void
  /** What the user is looking at: rows left after filtering. */
  matches: number
  /** What it cost: entries pulled back from S3 so far, before filtering. */
  scanned: number
  /** Still pulling pages to make the view complete. */
  isScanning: boolean
  /** Set when the scan stopped at its page cap instead of the end of the listing. */
  capped: boolean
  /** Noun for the summary line — objects in the normal view, versions in history. */
  noun: string
}

export function ObjectSearchBar({
  value,
  onChange,
  scope,
  onScopeChange,
  matches,
  scanned,
  isScanning,
  capped,
  noun,
}: ObjectSearchBarProps) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="relative min-w-[16rem] flex-1">
        <Search
          className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-fg-subtle"
          aria-hidden
        />
        <Input
          type="search"
          value={value}
          aria-label={`Search ${noun}`}
          placeholder="Search by name, or type a prefix like logs/2024/"
          spellCheck={false}
          autoComplete="off"
          // `type="search"` is kept for the searchbox role, but WebKit's own
          // clear glyph is suppressed: it would sit beside ours, and only one
          // of the two is keyboard-reachable or labelled.
          className={cn("pl-8 [&::-webkit-search-cancel-button]:appearance-none", value && "pr-8")}
          onChange={(e) => onChange(e.target.value)}
          // Escape clears rather than blurring: with a filter applied, "get me
          // back to the whole listing" is the thing the key is reached for.
          onKeyDown={(e) => {
            if (e.key !== "Escape" || value === "") return
            e.preventDefault()
            e.stopPropagation()
            onChange("")
          }}
        />
        {value && (
          <button
            type="button"
            aria-label="Clear search"
            className="absolute top-1/2 right-1.5 -translate-y-1/2 rounded p-1 text-fg-subtle transition-colors hover:text-fg"
            onClick={() => onChange("")}
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>

      <div
        role="group"
        aria-label="Search scope"
        className="inline-flex overflow-hidden rounded-md border border-border"
      >
        <ScopeButton
          active={scope === "folder"}
          icon={<Folder className="h-3.5 w-3.5" />}
          label="This folder"
          hint="List only what sits directly in this folder."
          onClick={() => onScopeChange("folder")}
        />
        <ScopeButton
          active={scope === "recursive"}
          icon={<ListTree className="h-3.5 w-3.5" />}
          label="All nested"
          hint="List every object beneath this prefix, folders flattened into full keys."
          onClick={() => onScopeChange("recursive")}
        />
      </div>

      <ScanSummary
        matches={matches}
        scanned={scanned}
        isScanning={isScanning}
        capped={capped}
        filtered={value.trim() !== ""}
        noun={noun}
      />
    </div>
  )
}

function ScopeButton({
  active,
  icon,
  label,
  hint,
  onClick,
}: {
  active: boolean
  icon: React.ReactNode
  label: string
  hint: string
  onClick: () => void
}) {
  return (
    <Tooltip content={hint}>
      <button
        type="button"
        aria-pressed={active}
        onClick={onClick}
        className={cn(
          "inline-flex h-8 cursor-pointer items-center gap-1.5 px-2.5 font-mono text-[11px] transition-colors",
          active ? "bg-accent-muted text-fg" : "text-fg-muted hover:bg-bg-muted hover:text-fg",
        )}
      >
        {icon}
        {label}
      </button>
    </Tooltip>
  )
}

/**
 * What the listing is showing and what it took to get there.
 *
 * The scanned count is not decoration: a filter or a non-default sort can only
 * be trusted over the pages that have been read, so the number that was read is
 * the only thing that tells the user whether "no matches" means "not in this
 * bucket" or "not in the part of it we have".
 */
function ScanSummary({
  matches,
  scanned,
  isScanning,
  capped,
  filtered,
  noun,
}: {
  matches: number
  scanned: number
  isScanning: boolean
  capped: boolean
  filtered: boolean
  noun: string
}) {
  if (!filtered && !isScanning && !capped) {
    return (
      <span className="font-mono text-[11px] whitespace-nowrap text-fg-subtle">
        {scanned.toLocaleString()} {noun}
      </span>
    )
  }

  return (
    <span className="flex items-center gap-1.5 font-mono text-[11px] whitespace-nowrap text-fg-muted">
      {isScanning && <Spinner className="h-3 w-3" />}
      {filtered ? (
        <>
          {matches.toLocaleString()} of {scanned.toLocaleString()} {noun}
        </>
      ) : (
        <>
          {scanned.toLocaleString()} {noun}
        </>
      )}
      {isScanning && <span className="text-fg-subtle">scanning…</span>}
      {capped && !isScanning && (
        <Tooltip content="The listing was longer than this view scans. Narrow it with a prefix — type it into the search box — to see the rest.">
          <span className="cursor-help text-warning">partial</span>
        </Tooltip>
      )}
    </span>
  )
}

// ─── Sortable header ───────────────────────────────────────────────────────

export interface SortHeadProps {
  column: SortColumn
  label: string
  sort: ObjectSort
  onSort: (column: SortColumn) => void
  className?: string
}

/**
 * A column header that sorts. The arrow only appears on the active column —
 * a permanent arrow on every header reads as a state rather than an offer.
 */
export function SortHead({ column, label, sort, onSort, className }: SortHeadProps) {
  const active = sort.column === column
  return (
    <TableHead
      className={cn("p-0", className)}
      aria-sort={active ? (sort.direction === "asc" ? "ascending" : "descending") : "none"}
    >
      <button
        type="button"
        onClick={() => onSort(column)}
        className={cn(
          "inline-flex h-full w-full cursor-pointer items-center gap-1 px-4 py-2 transition-colors select-none hover:text-fg",
          active && "text-fg",
        )}
        title={`Sort by ${label.toLowerCase()}`}
      >
        {label}
        <span className={cn("text-[9px] leading-none", !active && "opacity-0")} aria-hidden>
          {sort.direction === "asc" ? "▲" : "▼"}
        </span>
      </button>
    </TableHead>
  )
}

// ─── Match highlighting ────────────────────────────────────────────────────

/**
 * A row name with the searched-for run picked out. Scanning a filtered listing
 * for *why* a row matched is the slow part of a search, and this removes it.
 */
export function HighlightedName({ slices }: { slices: NameSlice[] }) {
  return (
    <>
      {slices.map((slice, i) =>
        slice.match ? (
          <mark key={i} className="rounded-[2px] bg-warning-muted text-inherit">
            {slice.text}
          </mark>
        ) : (
          <span key={i}>{slice.text}</span>
        ),
      )}
    </>
  )
}
