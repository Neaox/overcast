/**
 * VpcNetworkNode — custom React Flow node for a VPC resource on the system map.
 *
 * Styled as a distinctive network node with a hexagonal-feel shape (rounded
 * rectangle with a subtle inner border pattern). Shows:
 *   - VPC ID
 *   - CIDR block
 *   - Subnet count pill
 *   - Internet gateway indicator (globe icon that lights up when an IGW is attached)
 *
 * Navigates to the VPC detail page on click.
 */

import { memo, useCallback } from "react"
import { Handle, Position, type NodeProps } from "@xyflow/react"
import { useNavigate } from "@tanstack/react-router"
import { Network, Globe } from "lucide-react"
import { cn } from "@/lib/utils"

export interface VpcNetworkNodeData extends Record<string, unknown> {
  label: string
  cidrBlock?: string
  subnetCount?: number
  hasInternetGateway?: boolean
  status?: string
  eventCount?: number
  hasTarget?: boolean
  hasSource?: boolean
}

function areVpcPropsEqual(prev: NodeProps, next: NodeProps): boolean {
  if (prev.selected !== next.selected) return false
  const pd = prev.data as VpcNetworkNodeData
  const nd = next.data as VpcNetworkNodeData
  return (
    pd.label === nd.label &&
    pd.cidrBlock === nd.cidrBlock &&
    pd.subnetCount === nd.subnetCount &&
    pd.hasInternetGateway === nd.hasInternetGateway &&
    pd.status === nd.status &&
    pd.eventCount === nd.eventCount &&
    pd.hasTarget === nd.hasTarget &&
    pd.hasSource === nd.hasSource
  )
}

export const VpcNetworkNode = memo(function VpcNetworkNode({ data }: NodeProps) {
  const {
    label,
    cidrBlock,
    subnetCount = 0,
    hasInternetGateway = false,
    status,
    eventCount,
    hasTarget,
    hasSource,
  } = data as VpcNetworkNodeData

  const navigate = useNavigate()
  const handleClick = useCallback(() => {
    void navigate({ to: "/ec2/vpc/$vpcId", params: { vpcId: label } })
  }, [navigate, label])

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={handleClick}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") handleClick()
      }}
      className={cn(
        "relative flex flex-col rounded-xl border-2 px-3 py-2.5 shadow-md transition-colors",
        "cursor-pointer hover:brightness-110",
        "bg-bg-elevated text-fg",
        hasInternetGateway ? "border-cat-5/50 shadow-cat-5/10" : "border-cat-5/25 shadow-cat-5/5",
      )}
    >
      {/* Subtle inner glow for network feel */}
      <div
        className={cn(
          "pointer-events-none absolute inset-0 rounded-xl bg-linear-to-br to-transparent",
          hasInternetGateway ? "from-cat-5/8" : "from-cat-5/4",
        )}
      />

      {/* Left target handle */}
      {hasTarget && (
        <Handle
          type="target"
          position={Position.Left}
          className="size-2! rounded-full! border-0! bg-cat-5/50!"
        />
      )}

      {/* Header row: network icon + VPC ID + IGW indicator */}
      <div className="relative flex items-center gap-2.5">
        <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-cat-5/10">
          <Network className="h-5 w-5 text-cat-5" />
        </div>

        <div className="min-w-0 flex-1">
          <p className="truncate text-sm leading-tight font-semibold">{label}</p>
          {cidrBlock && (
            <p className="font-mono text-xs leading-tight text-fg-subtle">{cidrBlock}</p>
          )}
        </div>

        {/* Internet gateway indicator */}
        <div
          className={cn(
            "flex h-7 w-7 shrink-0 items-center justify-center rounded-full transition-colors",
            hasInternetGateway ? "bg-cat-7/15 text-cat-7" : "bg-fg-muted/10 text-fg-muted/40",
          )}
          title={hasInternetGateway ? "Internet gateway attached" : "No internet gateway"}
        >
          <Globe className="h-3.5 w-3.5" />
        </div>
      </div>

      {/* Info row: subnet count + status */}
      <div className="relative mt-1.5 flex items-center gap-2 text-xs">
        <span
          className={cn(
            "rounded px-1.5 py-0.5 font-mono font-medium tabular-nums",
            subnetCount > 0 ? "bg-cat-5/10 text-cat-5" : "bg-fg-muted/10 text-fg-muted",
          )}
        >
          {subnetCount} subnet{subnetCount !== 1 ? "s" : ""}
        </span>
        {status && status !== "available" && (
          <span className="rounded bg-warning/10 px-1.5 py-0.5 font-medium text-warning">
            {status}
          </span>
        )}
      </div>

      {/* Event counter badge */}
      {(eventCount ?? 0) > 0 && (
        <span className="absolute -top-1.5 -right-1.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-accent px-1 font-mono text-[9px] font-bold text-fg-on-accent tabular-nums">
          {(eventCount ?? 0) > 99 ? "99+" : eventCount}
        </span>
      )}

      {/* Right source handle */}
      {hasSource && (
        <Handle
          type="source"
          position={Position.Right}
          className="size-2! rounded-full! border-0! bg-cat-5/50!"
        />
      )}
    </div>
  )
}, areVpcPropsEqual)
