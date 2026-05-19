/**
 * StackGroupNode — React Flow group node that wraps resources belonging to a
 * single CloudFormation stack within a region.
 *
 * Renders a rounded container with the stack name as a compact header badge.
 * pointer-events are disabled so child nodes remain interactive.
 */

import { memo, useCallback } from "react"
import { type NodeProps } from "@xyflow/react"
import { useNavigate } from "@tanstack/react-router"
import { Layers } from "lucide-react"

export interface StackGroupData extends Record<string, unknown> {
  stackName: string
}

export const StackGroupNode = memo(function StackGroupNode({ data, width, height }: NodeProps) {
  const { stackName = "" } = data as StackGroupData
  const w = width ?? 0
  const h = height ?? 0
  const navigate = useNavigate()
  const handleClick = useCallback(() => {
    if (stackName) {
      void navigate({
        to: "/cloudformation/$stackName",
        params: { stackName },
      })
    }
  }, [navigate, stackName])

  return (
    <div
      className="pointer-events-none relative rounded-lg border border-indigo-400/30 bg-indigo-400/5"
      style={{ width: w, height: h }}
    >
      {/* Stack name badge — top-left inside the box */}
      <div
        role="button"
        tabIndex={0}
        onClick={handleClick}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") handleClick()
        }}
        className="pointer-events-auto absolute -top-3 left-3 z-10 flex cursor-pointer items-center gap-1 rounded-full border border-indigo-400/30 bg-bg-elevated px-2 py-0.5 text-[10px] font-semibold tracking-wide text-indigo-400 shadow-sm transition-colors hover:border-indigo-400/60 hover:bg-indigo-400/10"
      >
        <Layers className="h-3 w-3" />
        {stackName}
      </div>
    </div>
  )
})
