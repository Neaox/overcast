/**
 * MapMinimap — compact, interactive topology snapshot for the dashboard.
 *
 * Uses purpose-built MiniNode components (~140×32px) instead of full-size
 * ServiceNodes so the topology stays legible even for large, multi-region,
 * multi-stack setups.
 *
 * Features:
 *  - Summary stats header (regions, service breakdown, connections, stacks)
 *  - Adaptive canvas height based on node count
 *  - Light interaction (pan + zoom on hover)
 *  - Smooth expand to /map via View Transitions API
 */

import { useMemo, useState, useCallback } from "react"
import { useNavigate } from "@tanstack/react-router"
import {
  ReactFlow,
  Background,
  BackgroundVariant,
  MarkerType,
  ReactFlowProvider,
  type Edge,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { Network, ArrowRight, Maximize2 } from "lucide-react"
import { cn } from "@/lib/utils"
import { Spinner } from "@/components/ui/primitives"
import { useTopology } from "./use-topology"
import { useEventAnimations } from "./use-event-animations"
import { buildCompactLayoutNodes } from "./map-layout"
import { MiniNode } from "./mini-node"
import { TopologyEdge as TopologyEdgeComponent } from "./topology-edges"
import { SERVICE_THEME, EDGE_THEME, DEFAULT_EDGE_COLOR } from "./map-theme"
import type { TopologyNode, TopologyEdge } from "@/types"

const NODE_TYPES = { miniNode: MiniNode }
const EDGE_TYPES = { topologyEdge: TopologyEdgeComponent }

/** Choose canvas height class based on resource count. */
function canvasHeightClass(nodeCount: number): string {
  if (nodeCount <= 5) return "h-48" // 192px
  if (nodeCount <= 15) return "h-64" // 256px
  return "h-80" // 320px
}

/** Build a compact service-breakdown summary from topology nodes. */
function serviceBreakdown(nodes: TopologyNode[]) {
  const counts = new Map<string, number>()
  for (const n of nodes) {
    counts.set(n.service, (counts.get(n.service) ?? 0) + 1)
  }
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1]) // most frequent first
    .map(([service, count]) => ({
      service,
      count,
      theme: SERVICE_THEME[service],
    }))
}

/** Determine the most-centered region based on node positions and viewport. */
function detectCenteredRegion(nodes: TopologyNode[]): string | undefined {
  if (nodes.length === 0) return undefined
  // Count nodes per region — pick the region with the most resources as
  // the initial focus. This is a simple heuristic that works well when the
  // user hasn't panned.
  const counts = new Map<string, number>()
  for (const n of nodes) {
    counts.set(n.region, (counts.get(n.region) ?? 0) + 1)
  }
  let best = ""
  let bestCount = 0
  for (const [r, c] of counts) {
    if (c > bestCount) {
      best = r
      bestCount = c
    }
  }
  return best || undefined
}

// ─── Inner canvas (needs ReactFlowProvider wrapping) ─────────────────────────

function MinimapCanvas({
  topologyNodes,
  topologyEdges,
  nodeCounts,
  glowingEdges,
  onExpand,
}: {
  topologyNodes: TopologyNode[]
  topologyEdges: TopologyEdge[]
  nodeCounts: Record<string, number>
  glowingEdges: Set<string>
  onExpand: () => void
}) {
  const [hovered, setHovered] = useState(false)

  const rfNodes = useMemo(() => {
    const positioned = buildCompactLayoutNodes(topologyNodes, topologyEdges)
    return positioned.map((n) => ({
      ...n,
      data: {
        ...n.data,
        eventCount: nodeCounts[n.id] ?? 0,
      },
    }))
  }, [topologyNodes, topologyEdges, nodeCounts])

  const rfEdges: Edge[] = useMemo(() => {
    const nodeIdSet = new Set(rfNodes.map((n) => n.id))
    return topologyEdges
      .filter((e) => nodeIdSet.has(e.source) && nodeIdSet.has(e.target))
      .map((e) => {
        const edgeColor = EDGE_THEME[e.type]?.color ?? DEFAULT_EDGE_COLOR
        return {
          id: e.id,
          source: e.source,
          target: e.target,
          type: "topologyEdge",
          data: {
            glowing: glowingEdges.has(e.id),
            edgeType: e.type,
            label: e.label,
            state: e.state,
          },
          markerEnd: {
            type: MarkerType.ArrowClosed,
            width: 12,
            height: 12,
            color: edgeColor,
          },
        }
      })
  }, [rfNodes, topologyEdges, glowingEdges])

  return (
    <div
      className="relative h-full w-full"
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <ReactFlow
        nodes={rfNodes}
        edges={rfEdges}
        nodeTypes={NODE_TYPES}
        edgeTypes={EDGE_TYPES}
        onInit={async (instance) => {
          await instance.fitView({ padding: 0.15, maxZoom: 1.2 })
          const vp = instance.getViewport()
          void instance.setViewport({ ...vp, zoom: Math.round(vp.zoom * 20) / 20 }, { duration: 0 })
        }}
        nodesDraggable={false}
        nodesConnectable={false}
        nodesFocusable={false}
        edgesFocusable={false}
        panOnDrag
        zoomOnScroll={hovered}
        zoomOnPinch
        zoomOnDoubleClick={false}
        minZoom={0.2}
        maxZoom={1.5}
        preventScrolling={hovered}
        className="bg-bg"
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1} className="opacity-10" />
      </ReactFlow>

      {/* Expand button — bottom right of the canvas */}
      <button
        type="button"
        onClick={onExpand}
        className="absolute right-2 bottom-2 z-10 flex items-center gap-1 rounded-md border border-border bg-bg-elevated/90 px-2 py-1 text-[11px] font-medium text-fg-muted shadow-sm backdrop-blur-sm transition-colors hover:bg-bg-muted hover:text-fg"
        title="Open full system map"
      >
        <Maximize2 className="h-3 w-3" />
        Expand
      </button>
    </div>
  )
}

// ─── Exported component ──────────────────────────────────────────────────────

export function MapMinimap() {
  const { data, isLoading } = useTopology()
  const navigate = useNavigate()

  const topologyNodes = useMemo(() => data?.nodes ?? [], [data])
  const topologyEdges = useMemo(() => data?.edges ?? [], [data])
  const regions = data?.regions ?? []

  const { glowingEdges, nodeCounts } = useEventAnimations(topologyNodes, topologyEdges)

  const breakdown = useMemo(() => serviceBreakdown(topologyNodes), [topologyNodes])
  const stackCount = useMemo(() => {
    const stacks = new Set<string>()
    for (const n of topologyNodes) {
      if (n.stackName) stacks.add(n.stackName)
    }
    return stacks.size
  }, [topologyNodes])

  const heightClass = useMemo(() => canvasHeightClass(topologyNodes.length), [topologyNodes.length])

  const navigateToMap = useCallback(() => {
    const focusRegion = detectCenteredRegion(topologyNodes)
    const search = focusRegion ? { focusRegion } : {}
    const doNav = () => navigate({ to: "/map", search })

    if (typeof document !== "undefined" && "startViewTransition" in document) {
      ;(
        document as Document & { startViewTransition: (cb: () => void) => void }
      ).startViewTransition(doNav)
    } else {
      void doNav()
    }
  }, [navigate, topologyNodes])

  return (
    <div
      className="rounded-xl border border-border bg-bg-elevated shadow-sm"
      style={{ viewTransitionName: "system-map" }}
    >
      {/* Header with stats */}
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <div className="flex items-center gap-2">
          <Network className="h-4 w-4 text-fg-muted" />
          <span className="text-sm font-semibold text-fg">System Map</span>
        </div>
        <button
          type="button"
          onClick={navigateToMap}
          className="flex items-center gap-1 text-xs text-fg-muted transition-colors hover:text-fg"
        >
          Full view
          <ArrowRight className="h-3 w-3" />
        </button>
      </div>

      {/* Stats bar — visible when there are resources */}
      {topologyNodes.length > 0 && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-border px-4 py-2 text-[11px] text-fg-muted">
          {/* Region info */}
          {regions.length > 0 && (
            <span>{regions.length <= 3 ? regions.join(", ") : `${regions.length} regions`}</span>
          )}

          {/* Service breakdown */}
          {breakdown.map(({ service, count, theme }) => (
            <span key={service} className="flex items-center gap-1">
              <span
                className="inline-block h-2 w-2 rounded-full"
                style={{ backgroundColor: theme?.hex ?? "#6b7280" }}
              />
              <span>
                {count} {theme?.letter ?? service}
              </span>
            </span>
          ))}

          {/* Connections */}
          {topologyEdges.length > 0 && (
            <span className="rounded-full bg-accent/20 px-1.5 py-0.5 text-[10px] font-medium text-accent">
              {topologyEdges.length} connection{topologyEdges.length !== 1 ? "s" : ""}
            </span>
          )}

          {/* Stacks */}
          {stackCount > 0 && (
            <span>
              {stackCount} stack{stackCount !== 1 ? "s" : ""}
            </span>
          )}
        </div>
      )}

      {/* Canvas */}
      <div className={cn("w-full overflow-hidden rounded-b-xl", heightClass)}>
        {isLoading && (
          <div className="flex h-full items-center justify-center">
            <Spinner className="h-5 w-5" />
          </div>
        )}

        {!isLoading && topologyNodes.length === 0 && (
          <div className="flex h-full items-center justify-center text-xs text-fg-subtle">
            No resources yet
          </div>
        )}

        {!isLoading && topologyNodes.length > 0 && (
          <ReactFlowProvider>
            <MinimapCanvas
              topologyNodes={topologyNodes}
              topologyEdges={topologyEdges}
              nodeCounts={nodeCounts}
              glowingEdges={glowingEdges}
              onExpand={navigateToMap}
            />
          </ReactFlowProvider>
        )}
      </div>
    </div>
  )
}
