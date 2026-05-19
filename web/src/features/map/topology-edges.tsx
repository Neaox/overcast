/**
 * topology-edges — custom animated React Flow edges.
 *
 * Two visual styles:
 *   - "solid"  — direct wiring (S3 notification, SNS subscription, Lambda ESM)
 *   - "dashed" — EventBridge Pipe (labeled with pipe name)
 *
 * When an animation is active (triggered via the `animated` + `data.glowing`
 * props set by use-event-animations), the edge glows and a particle travels
 * along the path.
 */

import { memo } from "react"
import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from "@xyflow/react"
import { cn } from "@/lib/utils"
import { EDGE_THEME, DEFAULT_EDGE_COLOR } from "./map-theme"

export interface TopologyEdgeData extends Record<string, unknown> {
  /** true while an event is animating along this edge */
  glowing?: boolean
  /** edge kind — determines dash style and colour */
  edgeType?:
    | "notification"
    | "subscription"
    | "esm"
    | "pipe"
    | "logs"
    | "dlq"
    | "apigw-integration"
  /** pipe/dlq label shown at mid-point */
  label?: string
  /** pipe state — only relevant when edgeType === "pipe" */
  state?: string
  /**
   * Accumulated event count since the last drain tick (drains 1 every ~2 s).
   * Shown as a small badge so bursts of fast events remain visible after the glow fades.
   */
  burstCount?: number
}

function areEdgePropsEqual(prev: EdgeProps, next: EdgeProps): boolean {
  if (prev.selected !== next.selected) return false
  const pd = (prev.data ?? {}) as TopologyEdgeData
  const nd = (next.data ?? {}) as TopologyEdgeData
  return (
    pd.glowing === nd.glowing &&
    pd.edgeType === nd.edgeType &&
    pd.label === nd.label &&
    pd.state === nd.state &&
    pd.burstCount === nd.burstCount &&
    prev.sourceX === next.sourceX &&
    prev.sourceY === next.sourceY &&
    prev.targetX === next.targetX &&
    prev.targetY === next.targetY
  )
}

export const TopologyEdge = memo(function TopologyEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  markerEnd,
}: EdgeProps) {
  const { glowing, edgeType, label, state, burstCount } = (data ?? {}) as TopologyEdgeData
  const isPipe = edgeType === "pipe"
  const isDlq = edgeType === "dlq"
  const isDashed = EDGE_THEME[edgeType ?? ""]?.dash ?? false
  const isStopped = isPipe && state === "STOPPED"
  const color = isStopped
    ? DEFAULT_EDGE_COLOR
    : (EDGE_THEME[edgeType ?? ""]?.color ?? DEFAULT_EDGE_COLOR)

  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  })

  return (
    <>
      {/* Glow layer — rendered behind the main stroke when active */}
      {glowing && !isStopped && (
        <path
          d={edgePath}
          fill="none"
          stroke={color}
          strokeWidth={6}
          strokeOpacity={0.35}
          className="pointer-events-none"
          style={{ filter: `drop-shadow(0 0 4px ${color})` }}
        />
      )}

      <BaseEdge
        id={id}
        path={edgePath}
        markerEnd={markerEnd}
        style={{
          stroke: color,
          strokeWidth: glowing && !isStopped ? 2 : 1.5,
          strokeDasharray: isDashed ? "6 3" : undefined,
          opacity: isStopped ? 0.4 : 1,
          transition: "stroke-width 0.15s, opacity 0.2s",
        }}
      />

      {/* Travelling particle */}
      {glowing && !isStopped && (
        <circle r={4} fill={color} style={{ filter: `drop-shadow(0 0 3px ${color})` }}>
          <animateMotion dur="0.8s" repeatCount="1" path={edgePath} />
        </circle>
      )}

      {/* Edge label (pipes, DLQ, and other labeled edges) */}
      {isDashed && label && (
        <EdgeLabelRenderer>
          <div
            className={cn(
              "nodrag nopan pointer-events-none absolute rounded border px-1.5 py-0.5",
              "text-[9px] leading-tight font-medium",
              isStopped
                ? "border-transparent bg-bg-muted text-fg-subtle"
                : isDlq
                  ? "border-red-400/25 bg-bg-elevated text-red-300"
                  : "border-sky-400/25 bg-bg-elevated text-sky-300",
            )}
            style={{
              transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)`,
              zIndex: 10,
            }}
          >
            {label}
            {isStopped && " (stopped)"}
          </div>
        </EdgeLabelRenderer>
      )}

      {/* Burst counter badge — shown when > 1 to avoid interfering with type labels */}
      {(burstCount ?? 0) > 1 && !isStopped && (
        <EdgeLabelRenderer>
          <div
            className="nodrag nopan pointer-events-none absolute flex items-center gap-0.5 rounded-full px-1.5 py-0.5 text-[9px] font-bold tabular-nums"
            style={{
              transform: `translate(-50%, calc(-50% - ${(isPipe || isDlq) && label ? 16 : 0}px)) translate(${labelX}px,${labelY}px)`,
              zIndex: 11,
              background: `color-mix(in srgb, ${color} 20%, transparent)`,
              border: `1px solid color-mix(in srgb, ${color} 40%, transparent)`,
              color,
            }}
          >
            ×{burstCount}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
}, areEdgePropsEqual)
