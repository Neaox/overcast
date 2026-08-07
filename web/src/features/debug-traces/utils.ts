export function nsToHuman(ns: number): string {
  if (ns < 1_000_000) return `${(ns / 1000).toFixed(0)}µs`
  if (ns < 1_000_000_000) return `${(ns / 1_000_000).toFixed(1)}ms`
  return `${(ns / 1_000_000_000).toFixed(2)}s`
}

export function statusColor(code: number): string {
  if (code >= 500) return "text-red-400"
  if (code >= 400) return "text-amber-400"
  return "text-emerald-400"
}

/** HTTP status code → standard reason phrase (RFC 9110). */
export function statusMessage(code: number): string {
  if (code === 0) return ""
  const map: Record<number, string> = {
    200: "OK", 201: "Created", 202: "Accepted", 204: "No Content",
    301: "Moved Permanently", 302: "Found", 304: "Not Modified",
    400: "Bad Request", 401: "Unauthorized", 403: "Forbidden",
    404: "Not Found", 405: "Method Not Allowed", 408: "Request Timeout",
    409: "Conflict", 429: "Too Many Requests",
    500: "Internal Server Error", 502: "Bad Gateway",
    503: "Service Unavailable", 504: "Gateway Timeout",
  }
  return map[code] ?? ""
}

/** Format an ISO timestamp with millisecond precision. */
export function formatTimestamp(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${String(d.getMilliseconds()).padStart(3, "0")}`
}

export function tryFormatJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

/** Detect content type hint from headers. */
export function contentTypeHint(headers: Record<string, string[]>): "json" | "xml" | "text" {
  const ct = (headers["Content-Type"] ?? headers["content-type"] ?? headers["content_type"] ?? [""])[0]
  if (/json/.test(ct)) return "json"
  if (/xml/.test(ct)) return "xml"
  return "text"
}

/** Best-effort format a body string for display. */
export function formatBody(body: string, hint: "json" | "xml" | "text"): string {
  if (hint === "json" || hint === "text") return tryFormatJSON(body)
  return body
}

/** Generate a minimal curl command from trace data. */
export function traceToCurl(t: { method: string; path: string; host: string; requestHeaders: Record<string, string[]>; requestBody?: string }): string {
  const lines: string[] = [`curl -X ${t.method}`]
  for (const [k, vs] of Object.entries(t.requestHeaders)) {
    const lk = k.toLowerCase()
    if (lk === "host" || lk === "content-length" || lk === "connection") continue
    for (const v of vs) lines.push(`  -H '${k}: ${v}'`)
  }
  if (t.requestBody) lines.push(`  -d '${t.requestBody.replace(/'/g, "\\'")}'`)
  const scheme = t.host === "localhost" ? "http" : "https"
  lines.push(`  '${scheme}://${t.host}${t.path}'`)
  return lines.join(" \\\n")
}
