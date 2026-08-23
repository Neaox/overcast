import type { InfiniteData } from "@tanstack/react-query"
import type { TraceListResponse, TraceSummary } from "@/types"

export function nsToHuman(ns: number): string {
  if (ns < 1_000_000) return `${(ns / 1000).toFixed(0)}µs`
  if (ns < 1_000_000_000) return `${(ns / 1_000_000).toFixed(1)}ms`
  return `${(ns / 1_000_000_000).toFixed(2)}s`
}

export function statusColor(code: number): string {
  if (code >= 500) return "text-danger"
  if (code >= 400) return "text-warning"
  return "text-success"
}

/** HTTP status code → standard reason phrase (RFC 9110). */
export function statusMessage(code: number): string {
  if (code === 0) return ""
  const map: Record<number, string> = {
    200: "OK",
    201: "Created",
    202: "Accepted",
    204: "No Content",
    301: "Moved Permanently",
    302: "Found",
    304: "Not Modified",
    400: "Bad Request",
    401: "Unauthorized",
    403: "Forbidden",
    404: "Not Found",
    405: "Method Not Allowed",
    408: "Request Timeout",
    409: "Conflict",
    429: "Too Many Requests",
    500: "Internal Server Error",
    502: "Bad Gateway",
    503: "Service Unavailable",
    504: "Gateway Timeout",
  }
  return map[code] ?? ""
}

/** Format an ISO timestamp with millisecond precision. */
export function formatTimestamp(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${String(d.getMilliseconds()).padStart(3, "0")}`
}

/**
 * Quote a string for safe use as a single POSIX shell argument.
 *
 * Wraps in single quotes; an embedded single quote closes the quote, emits an
 * escaped quote, and reopens (`'\''`) — backslash-escaping inside single
 * quotes is not valid POSIX.
 */
export function shellQuote(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`
}

/**
 * Absolute URL a traced request targeted.
 *
 * Prefers the trace's own captured host (which may carry a bucket subdomain
 * or service-specific hostname) with the scheme of the configured emulator
 * endpoint; when no host was captured (list summaries), falls back to the
 * endpoint base URL itself. Both the list and detail pages build curl URLs
 * through this helper so they stay consistent.
 */
export function traceRequestUrl(baseUrl: string, path: string, host?: string): string {
  const base = baseUrl.replace(/\/$/, "")
  if (!host) return `${base}${path}`
  let scheme = "http"
  try {
    scheme = new URL(base).protocol.replace(/:$/, "")
  } catch {
    // Unparsable endpoint base URL — keep the http default.
  }
  return `${scheme}://${host}${path}`
}

/** Generate a minimal curl command from trace data. */
export function traceToCurl(
  t: {
    method: string
    path: string
    host: string
    requestHeaders: Record<string, string[]>
    requestBody?: string
  },
  baseUrl: string,
): string {
  const lines: string[] = [`curl -X ${t.method}`]
  for (const [k, vs] of Object.entries(t.requestHeaders)) {
    const lk = k.toLowerCase()
    if (lk === "host" || lk === "content-length" || lk === "connection") continue
    for (const v of vs) lines.push(`  -H ${shellQuote(`${k}: ${v}`)}`)
  }
  if (t.requestBody) lines.push(`  -d ${shellQuote(t.requestBody)}`)
  lines.push(`  ${shellQuote(traceRequestUrl(baseUrl, t.path, t.host))}`)
  return lines.join(" \\\n")
}

/**
 * Fold freshly polled traces into an infinite-query result by prepending the
 * new (unseen) traces to the first page. Returns `old` unchanged when the
 * poll brought nothing new, so callers can setQueryData without triggering
 * re-renders on no-op polls.
 */
export function mergePolledTraces(
  old: InfiniteData<TraceListResponse, string | undefined>,
  fresh: TraceSummary[],
): InfiniteData<TraceListResponse, string | undefined> {
  if (fresh.length === 0 || old.pages.length === 0) return old
  const seen = new Set<string>()
  for (const page of old.pages) for (const t of page.traces) seen.add(t.requestId)
  const newTraces = fresh.filter((t) => !seen.has(t.requestId))
  if (newTraces.length === 0) return old
  const first = old.pages[0]
  return {
    ...old,
    pages: [{ ...first, traces: [...newTraces, ...first.traces] }, ...old.pages.slice(1)],
  }
}
