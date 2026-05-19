/**
 * VpcGroupNode — React Flow group node that wraps resources belonging to a
 * single VPC within a region.
 *
 * Renders a dashed-border container with a light teal tint and the VPC ID
 * as a compact header badge. pointer-events are disabled so child nodes
 * remain interactive.
 */

import { memo } from "react"
import { type NodeProps } from "@xyflow/react"
import { Globe } from "lucide-react"

export interface VpcGroupData extends Record<string, unknown> {
  vpcId: string
}

export const VpcGroupNode = memo(function VpcGroupNode({ data, width, height }: NodeProps) {
  const { vpcId = "" } = data as VpcGroupData
  const w = width ?? 0
  const h = height ?? 0

  return (
    <div
      className="pointer-events-none relative rounded-lg border border-dashed border-teal-400/30 bg-teal-400/5"
      style={{ width: w, height: h }}
    >
      {/* VPC ID badge — top-left inside the box */}
      <div className="pointer-events-auto absolute -top-3 left-3 z-10 flex items-center gap-1 rounded-full border border-dashed border-teal-400/30 bg-bg-elevated px-2 py-0.5 text-[10px] font-semibold tracking-wide text-teal-400 shadow-sm">
        <Globe className="h-3 w-3" />
        {vpcId}
      </div>
    </div>
  )
})
