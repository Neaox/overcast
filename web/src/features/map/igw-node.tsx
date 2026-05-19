/**
 * IgwNode — custom React Flow node for an Internet Gateway on the system map.
 *
 * Styled to evoke "connection to the internet" — a circular-ish node with
 * a globe icon, radiating signal rings, and a distinctive blue glow.
 * Shows the IGW ID and the attached VPC (if any).
 */

import { memo } from "react"
import { Handle, Position, type NodeProps } from "@xyflow/react"
import { Globe } from "lucide-react"
import { cn } from "@/lib/utils"

export interface IgwNodeData extends Record<string, unknown> {
  label: string
  attachedVpcId?: string
  eventCount?: number
  hasTarget?: boolean
  hasSource?: boolean
}

function areIgwPropsEqual(prev: NodeProps, next: NodeProps): boolean {
  if (prev.selected !== next.selected) return false
  const pd = prev.data as IgwNodeData
  const nd = next.data as IgwNodeData
  return (
    pd.label === nd.label &&
    pd.attachedVpcId === nd.attachedVpcId &&
    pd.eventCount === nd.eventCount &&
    pd.hasTarget === nd.hasTarget &&
    pd.hasSource === nd.hasSource
  )
}

export const IgwNode = memo(function IgwNode({ data }: NodeProps) {
  const { label, attachedVpcId, eventCount, hasTarget, hasSource } = data as IgwNodeData

  const isAttached = !!attachedVpcId

  return (
    <div
      className={cn(
        "relative flex items-center gap-2.5 rounded-full border-2 px-3 py-2 shadow-md transition-colors",
        "bg-bg-elevated text-fg",
        isAttached
          ? "border-blue-400/50 shadow-blue-400/15"
          : "border-blue-400/20 shadow-blue-400/5",
      )}
    >
      {/* Ambient glow ring for "internet" feel */}
      {isAttached && (
        <div className="pointer-events-none absolute -inset-1 rounded-full border border-blue-400/10" />
      )}

      {/* Left target handle */}
      {hasTarget && (
        <Handle
          type="target"
          position={Position.Left}
          className="size-2! rounded-full! border-0! bg-blue-400/50!"
        />
      )}

      {/* Globe icon with signal effect */}
      <div
        className={cn(
          "relative flex h-8 w-8 shrink-0 items-center justify-center rounded-full",
          isAttached ? "bg-blue-400/15" : "bg-fg-muted/10",
        )}
      >
        <Globe className={cn("h-4.5 w-4.5", isAttached ? "text-blue-400" : "text-fg-muted/50")} />
        {/* Subtle signal arcs */}
        {isAttached && (
          <>
            <span className="pointer-events-none absolute -inset-1 rounded-full border border-blue-400/15" />
            <span className="pointer-events-none absolute -inset-2.5 rounded-full border border-blue-400/8" />
          </>
        )}
      </div>

      {/* Label + status */}
      <div className="min-w-0 pr-1">
        <p className="truncate text-xs leading-tight font-semibold">{label}</p>
        <p
          className={cn(
            "text-[10px] leading-tight",
            isAttached ? "text-blue-400" : "text-fg-muted",
          )}
        >
          {isAttached ? "attached" : "detached"}
        </p>
      </div>

      {/* Event counter badge */}
      {(eventCount ?? 0) > 0 && (
        <span className="absolute -top-1.5 -right-1.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-accent px-1 text-[9px] font-bold text-fg-on-accent tabular-nums">
          {(eventCount ?? 0) > 99 ? "99+" : eventCount}
        </span>
      )}

      {/* Right source handle */}
      {hasSource && (
        <Handle
          type="source"
          position={Position.Right}
          className="size-2! rounded-full! border-0! bg-blue-400/50!"
        />
      )}
    </div>
  )
}, areIgwPropsEqual)
