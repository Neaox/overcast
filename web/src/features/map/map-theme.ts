/**
 * map-theme — map color tokens derived from the central service registry.
 *
 * Service colors, edge colors, and sweep helpers all live here so that
 * map-page.tsx, topology-nodes.tsx, and topology-edges.tsx stay in sync.
 * Add or change service colors in @/lib/service-registry instead.
 */

import { SERVICES } from "@/lib/service-registry"

/** Per-service color tokens used by topology nodes and the minimap.
 *  Derived automatically from the central service registry. */
export const SERVICE_THEME: Record<
  string,
  { hex: string; color: string; bg: string; border: string; letter: string } | undefined
> = Object.fromEntries(
  Object.entries(SERVICES).map(([key, s]) => [
    key,
    { hex: s.hex, color: s.color, bg: s.bg, border: s.border, letter: s.letter },
  ]),
)

/** Edge-type theme tokens. Keys match the topology API edge type strings. */
export const EDGE_THEME: Record<
  string,
  { color: string; dash: boolean; label: string } | undefined
> = {
  notification: { color: "#fb923c", dash: false, label: "S3 notification" },
  subscription: { color: "#f472b6", dash: false, label: "SNS subscription" },
  esm: { color: "#a78bfa", dash: false, label: "Lambda ESM" },
  pipe: { color: "#38bdf8", dash: true, label: "EventBridge Pipe" },
  logs: { color: "#34d399", dash: false, label: "CloudWatch Logs" },
  dlq: { color: "#f87171", dash: true, label: "Dead Letter Queue" },
  "vpc-attachment": { color: "#2dd4bf", dash: false, label: "IGW Attachment" },
  "vpc-member": { color: "#2dd4bf", dash: true, label: "VPC Member" },
  "cfn-export": { color: "#818cf8", dash: true, label: "CFN Export" },
  "cfn-ref": { color: "#94a3b8", dash: true, label: "CFN Reference" },
  "apigw-integration": { color: "#86efac", dash: false, label: "API Gateway → Lambda" },
}

export const DEFAULT_EDGE_COLOR = "#6b7280"

/** Convert a hex color to an rgba sweep-animation color (35% opacity). */
export function hexToSweep(hex: string): string {
  const r = parseInt(hex.slice(1, 3), 16)
  const g = parseInt(hex.slice(3, 5), 16)
  const b = parseInt(hex.slice(5, 7), 16)
  return `rgba(${r},${g},${b},0.35)`
}
