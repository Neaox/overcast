/**
 * RegionGroupNode — React Flow group node that wraps all resources in a region.
 *
 * Renders a subtle dashed border with the region name as a badge that stays
 * pinned to the visible bottom-centre of the box as the user pans/zooms.
 * pointer-events are disabled on the container so child nodes remain interactive.
 */

import { memo, useMemo, useState, useEffect } from "react"
import { createPortal } from "react-dom"
import { useViewport, useStore, type NodeProps } from "@xyflow/react"
import { cn } from "@/lib/utils"

export interface RegionGroupData extends Record<string, unknown> {
  region: string
  empty?: boolean
  active?: boolean
}

function clamp(v: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, v))
}

export const RegionGroupNode = memo(function RegionGroupNode({
  data,
  width,
  height,
  positionAbsoluteX,
  positionAbsoluteY,
}: NodeProps) {
  const { region = "", empty = false, active = false } = data as RegionGroupData
  const w = width ?? 0
  const h = height ?? 0

  const viewport = useViewport()
  const containerW = useStore((s) => s.width)
  const containerH = useStore((s) => s.height)

  // Compute badge position clamped to the visible portion of this group box.
  // The badge is kept fully inside the viewport with a margin so it never
  // clips off-screen.
  const badgeStyle = useMemo(() => {
    const zoom = viewport.zoom
    // Approximate badge half-dimensions in flow-coordinate units.
    // The badge lives in the node's local CSS space which maps 1:1 to
    // flow coordinates, so these are constant regardless of zoom.
    const BADGE_HALF_W = 52
    const BADGE_HALF_H = 12
    const MARGIN = 16 // breathing room from viewport edge (in flow coords)

    // Visible viewport bounds in flow coordinates
    const vpLeft = -viewport.x / zoom
    const vpTop = -viewport.y / zoom
    const vpRight = vpLeft + containerW / zoom
    const vpBottom = vpTop + containerH / zoom

    // Group box bounds in flow coordinates
    const boxLeft = positionAbsoluteX
    const boxRight = positionAbsoluteX + w
    const boxBottom = positionAbsoluteY + h

    // Badge X: centre of the visible horizontal overlap, clamped so the
    // pill doesn't overflow the viewport or the box.
    const visLeft = Math.max(boxLeft, vpLeft)
    const visRight = Math.min(boxRight, vpRight)
    const rawX = (visLeft + visRight) / 2 - boxLeft
    const vpMinX = vpLeft + BADGE_HALF_W + MARGIN - boxLeft
    const vpMaxX = vpRight - BADGE_HALF_W - MARGIN - boxLeft
    // Box bounds are the hard constraint — badge must stay inside the box.
    const badgeX = clamp(clamp(rawX, vpMinX, vpMaxX), 0, w)

    // Badge Y: bottom of visible vertical overlap, clamped inside both
    // the viewport and the box.
    const visBottom = Math.min(boxBottom, vpBottom)
    const rawY = visBottom - positionAbsoluteY
    const vpMinY = vpTop + BADGE_HALF_H + MARGIN - positionAbsoluteY
    const vpMaxY = vpBottom - BADGE_HALF_H - MARGIN - positionAbsoluteY
    // Box bounds are the hard constraint.
    const badgeY = clamp(clamp(rawY, vpMinY, vpMaxY), 0, h)

    // Badge opacity — fully opaque when hugging an edge, fading to 0.55
    // as it floats toward the centre of the box.
    const distFromLeft = badgeX
    const distFromRight = w - badgeX
    const distFromTop = badgeY
    const distFromBottom = h - badgeY
    const minEdgeDist = Math.min(distFromLeft, distFromRight, distFromTop, distFromBottom)
    // Start fading after 30px from edge, fully transparent-ish at 120px+
    const opacity = 1 - clamp(minEdgeDist / 120, 0, 1) * 0.45

    // Absolute flow-coordinate position for the portal-rendered badge.
    const absX = positionAbsoluteX + badgeX
    const absY = positionAbsoluteY + badgeY

    return { badgeX, badgeY, absX, absY, opacity } as const
  }, [viewport, containerW, containerH, positionAbsoluteX, positionAbsoluteY, w, h])

  // Resolve the viewport DOM element for the portal. useEffect ensures
  // we pick it up after React Flow has mounted the element.
  const [viewportEl, setViewportEl] = useState<Element | null>(null)
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setViewportEl(document.querySelector(".react-flow__viewport"))
  }, [])

  return (
    <div
      className={cn(
        "pointer-events-none relative rounded-xl border-2 border-dashed bg-bg-elevated/30",
        active ? "border-accent/40" : "border-border/50",
      )}
      style={{ width: w, height: h }}
    >
      {/* Empty region placeholder */}
      {empty && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
          <p className="text-xs text-fg-muted/60">No resources in this region</p>
        </div>
      )}

      {/* Region badge — portalled above all nodes */}
      {viewportEl &&
        createPortal(
          <div
            className="pointer-events-auto absolute rounded-full border border-border bg-bg-elevated px-3 py-0.5 text-[10px] font-semibold tracking-wide whitespace-nowrap text-fg-muted uppercase shadow-sm transition-opacity duration-200"
            style={{
              left: badgeStyle.absX,
              top: badgeStyle.absY,
              opacity: badgeStyle.opacity,
              transform: "translate(-50%, -50%)",
              zIndex: 10000,
            }}
          >
            {region}
          </div>,
          viewportEl,
        )}
    </div>
  )
})
