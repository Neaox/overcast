/**
 * Filter state for the Request Traces page, expressed as URL search params.
 *
 * The URL is the single source of truth for every filter: the page reads it
 * with `Route.useSearch()` and writes it back with `replace: true`. That is
 * what makes a filtered view survive Back-navigation from a trace's detail
 * page (the history entry carries the filters) and what makes it shareable.
 *
 * Everything here is pure so it can be unit tested without a router: parsing
 * must never throw on a hand-edited URL, and the arrays must be canonical
 * (deduped and sorted) so that ticking A then B produces the same query key —
 * and the same link — as ticking B then A.
 */
import type { CheckboxFilterItem } from "@/components/ui/checkbox-filter-dropdown"
import type { TraceListParams, TraceSummary } from "@/types"

/** Status classes the server understands, in the order they are offered. */
export const STATUS_OPTIONS = ["2xx", "3xx", "4xx", "5xx"] as const

/** Methods offered in the dropdown. A URL may name any other method too. */
export const METHOD_OPTIONS = ["GET", "POST", "PUT", "DELETE", "HEAD", "PATCH"] as const

export const STATUS_ITEMS: CheckboxFilterItem[] = STATUS_OPTIONS.map((id) => ({ id, label: id }))
export const METHOD_ITEMS: CheckboxFilterItem[] = METHOD_OPTIONS.map((id) => ({ id, label: id }))

/** Stable empty selection, so `value` keeps its identity across renders. */
export const NO_SELECTION: readonly string[] = Object.freeze([])

export interface TracesSearch {
  /** Free-text query over request ID, path and service. */
  search?: string
  /** Status classes (or exact codes) to include. Absent means every status. */
  status?: string[]
  /** HTTP methods to include. Absent means every method. */
  method?: string[]
  /**
   * Deny-list of service keys hidden from the table. Client-side only: the
   * server has already sent these rows, so hiding one must not refetch.
   */
  hideServices?: string[]
  /**
   * Show emulator-internal traces. Absent/false keeps them hidden, which is
   * the default — stating the non-default in the URL keeps a plain link short
   * and avoids a param whose meaning is inverted from its name.
   */
  showInternal?: boolean
}

/** `2xx`…`5xx`, or an exact three-digit code, which the server also accepts. */
const STATUS_PATTERN = /^(?:[1-5]xx|\d{3})$/
/** Any alphabetic token; the server compares methods case-insensitively. */
const METHOD_PATTERN = /^[A-Z]+$/

function parseText(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() !== "" ? value : undefined
}

/**
 * Parse a repeatable search param into a canonical list.
 *
 * Tolerates every shape a URL can realistically carry: an array (how TanStack
 * Router round-trips one), a single string, or a comma-separated string (what
 * a human types by hand, and what the server also accepts). Anything that is
 * not a well-formed entry — a number, an object, `<script>` — is dropped
 * rather than thrown, so a mangled URL degrades to a weaker filter instead of
 * a blank page.
 */
export function parseFilterList(
  value: unknown,
  normalise: (raw: string) => string,
  accept: (candidate: string) => boolean,
): string[] | undefined {
  const entries: unknown[] = Array.isArray(value) ? value : [value]
  const out = new Set<string>()
  for (const entry of entries) {
    if (typeof entry !== "string") continue
    for (const part of entry.split(",")) {
      const candidate = normalise(part.trim())
      if (candidate && accept(candidate)) out.add(candidate)
    }
  }
  return out.size > 0 ? [...out].sort() : undefined
}

const lower = (s: string) => s.toLowerCase()
const upper = (s: string) => s.toUpperCase()
const anyValue = () => true

function parseBool(value: unknown): boolean | undefined {
  if (typeof value === "boolean") return value || undefined
  if (value === "true" || value === "1") return true
  return undefined
}

/**
 * `validateSearch` for the traces route. Total: every input maps to a valid
 * `TracesSearch`, never a throw.
 *
 * Every key is named on the way out, `undefined` included. The router merges a
 * validator's result over the raw search params rather than replacing them, so
 * an omitted key leaves the raw value in place — which is how `?status=6xx`
 * would reach the component as the string it was rejected as. `undefined`
 * overwrites it, and the router drops undefined values when it writes a URL.
 */
export function validateTracesSearch(search: Record<string, unknown>): TracesSearch {
  return {
    search: parseText(search.search),
    status: parseFilterList(search.status, lower, (s) => STATUS_PATTERN.test(s)),
    method: parseFilterList(search.method, upper, (m) => METHOD_PATTERN.test(m)),
    hideServices: parseFilterList(search.hideServices, (s) => s, anyValue),
    showInternal: parseBool(search.showInternal),
  }
}

/** Drop an empty list so the param disappears from the URL entirely. */
export function filterListParam(next: string[]): string[] | undefined {
  return next.length > 0 ? next : undefined
}

/**
 * The server-side half of the filter state.
 *
 * `hideServices` and `showInternal` are deliberately absent: they are applied
 * to rows already in hand, so changing them must not invalidate the infinite
 * query and refetch every page.
 */
export function traceListParams(search: TracesSearch, limit: number): TraceListParams {
  const params: TraceListParams = { limit }
  if (search.method?.length) params.method = search.method
  if (search.status?.length) params.status = search.status
  if (search.search) params.search = search.search
  return params
}

// ─── Client-side row filter ───────────────────────────────────────────────
/**
 * Coalesce an empty service name, so the dropdown's option, its count and the
 * deny-list all name the same thing.
 */
export function serviceKey(service: string): string {
  return service || "(unknown)"
}

/** The two filters applied to rows already in hand rather than by the server. */
export interface TraceRowFilter {
  /** True when "Hide internal" is ticked — i.e. `showInternal` is not set. */
  hideInternal: boolean
  /** Service keys the services dropdown is hiding. */
  hiddenServices: ReadonlySet<string>
}

/**
 * Whether a trace belongs in the table.
 *
 * A predicate rather than a `filter` call inside the component because the page
 * renders rows from two sources — the paginated list and the deep scan — and
 * the deep scan's rows used to skip this test entirely, so a hidden service
 * reappeared the moment a search matched a body. #1613 is the other half: a
 * trace is internal because the *server* said so (`summary.internal`), so a
 * console-polled endpoint the server had not classified was listed with "Hide
 * internal" ticked however this ran.
 */
export function showsTrace(
  summary: Pick<TraceSummary, "service" | "internal">,
  filter: TraceRowFilter,
): boolean {
  if (filter.hideInternal && summary.internal) return false
  return !filter.hiddenServices.has(serviceKey(summary.service))
}
