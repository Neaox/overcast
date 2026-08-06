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

export function tryFormatJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}
