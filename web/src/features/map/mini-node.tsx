/**
 * MiniNode — compact React Flow node for the dashboard minimap.
 *
 * ~140×32px pill showing: colored service dot + truncated label.
 * Designed for high-density topology snapshots where the full ServiceNode
 * (260×100px) is too large to be legible.
 */

import { memo } from "react"
import { Handle, Position, type NodeProps } from "@xyflow/react"
import { cn } from "@/lib/utils"
import { SERVICE_THEME } from "./map-theme"

export interface MiniNodeData extends Record<string, unknown> {
  service: string
  label: string
  eventCount?: number
}

export const MINI_NODE_W = 140
export const MINI_NODE_H = 32

export const MiniNode = memo(function MiniNode({ data }: NodeProps) {
  const { service, label, eventCount } = data as MiniNodeData

  const meta = SERVICE_THEME[service] ?? {
    hex: "#6b7280",
    color: "text-fg-muted",
    bg: "bg-fg-muted/10",
    border: "border-fg-muted/30",
    letter: "?",
  }

  return (
    <div
      className={cn(
        "flex items-center gap-1.5 rounded-md border px-2 py-1 shadow-sm",
        "bg-bg-elevated text-fg",
        meta.border,
      )}
      style={{ width: MINI_NODE_W, height: MINI_NODE_H }}
    >
      {/* Service dot with letter */}
      <span
        className={cn(
          "flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[9px] font-bold",
          meta.bg,
          meta.color,
        )}
      >
        {meta.letter}
      </span>

      {/* Label — truncated */}
      <span className="min-w-0 flex-1 truncate text-[10px] leading-tight font-medium text-fg">
        {label}
      </span>

      {/* Event activity dot */}
      {(eventCount ?? 0) > 0 && (
        <span
          className="h-1.5 w-1.5 shrink-0 animate-pulse rounded-full"
          style={{ backgroundColor: meta.hex }}
        />
      )}

      {/* Hidden handles for edge connectivity */}
      <Handle
        type="target"
        position={Position.Left}
        className="h-px! w-px! border-0! bg-transparent!"
      />
      <Handle
        type="source"
        position={Position.Right}
        className="h-px! w-px! border-0! bg-transparent!"
      />
    </div>
  )
})
