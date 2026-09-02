/**
 * CollapsedStackNode — compact chip rendered when a nested CloudFormation
 * stack is too small to show its children at the current zoom level.
 *
 * Shows the stack name + a resource count badge. As the user zooms in and
 * the zoom threshold is met, this chip is replaced by a full StackGroupNode
 * containing the stack's resources.
 */

import { memo, useCallback } from "react"
import { type NodeProps } from "@xyflow/react"
import { useNavigate } from "@tanstack/react-router"
import { Layers } from "lucide-react"

export interface CollapsedStackData extends Record<string, unknown> {
  stackName: string
  resourceCount: number
}

export const CollapsedStackNode = memo(function CollapsedStackNode({
  data,
  width,
  height,
}: NodeProps) {
  const { stackName = "", resourceCount = 0 } = data as CollapsedStackData
  const w = width ?? 200
  const h = height ?? 56
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
      role="button"
      tabIndex={0}
      onClick={handleClick}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") handleClick()
      }}
      className="flex cursor-pointer items-center gap-2 rounded-lg border border-cat-6/30 bg-cat-6/5 px-3 transition-colors hover:border-cat-6/60 hover:bg-cat-6/10"
      style={{ width: w, height: h }}
    >
      <Layers className="h-4 w-4 shrink-0 text-cat-6" />
      <span className="min-w-0 truncate text-xs font-semibold text-cat-6">{stackName}</span>
      {resourceCount > 0 && (
        <span className="ml-auto shrink-0 rounded-full border border-cat-6/40 bg-bg-elevated px-1.5 py-0.5 font-mono text-2xs font-medium text-cat-6 tabular-nums">
          {resourceCount}
        </span>
      )}
    </div>
  )
})
