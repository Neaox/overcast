/**
 * map-layout — automatic graph layout via Dagre with region grouping.
 *
 * When multiple regions are present, nodes are grouped by region. Each region
 * runs its own dagre layout (LR), then the region groups are stacked
 * vertically with a gap between them. A React Flow group node is emitted per
 * region to draw a visual container around the region's resources.
 *
 * When only one region is present, the layout is flat (no region group) for
 * visual simplicity.
 */

import dagre from "@dagrejs/dagre"
import type { Node } from "@xyflow/react"
import type { TopologyNode, TopologyEdge } from "@/types"

export const NODE_WIDTH = 260
export const NODE_HEIGHT = 100

// Space between ranks (columns when rankdir is LR) and nodes within a rank.
const RANKSEP = 120
const NODESEP = 60

/** Padding inside a region group box. */
const REGION_PAD_X = 40
const REGION_PAD_TOP = 48 // room for the region badge
const REGION_PAD_BOTTOM = 32

/** Vertical gap between stacked region groups. */
const REGION_GAP = 60

/** Padding inside a CloudFormation stack group box. */
const STACK_PAD_X = 20
const STACK_PAD_TOP = 28 // room for the badge at -top-3
const STACK_PAD_BOTTOM = 16

/** Collapsed nested stack chip dimensions (matches CollapsedStackNode). */
export const COLLAPSED_STACK_WIDTH = 200
export const COLLAPSED_STACK_HEIGHT = 56

/** Padding inside a VPC group box. */
const VPC_PAD_X = 20
const VPC_PAD_TOP = 28
const VPC_PAD_BOTTOM = 16

/**
 * Run dagre on a set of nodes/edges and return positioned results.
 * Coordinates are translated so the top-left is at (0, 0).
 */
function layoutSubgraph(
  nodes: TopologyNode[],
  edges: TopologyEdge[],
  nodeSizeOverrides: Record<string, { width: number; height: number } | undefined>,
): {
  positioned: Array<{ node: TopologyNode; x: number; y: number; w: number; h: number }>
  width: number
  height: number
} {
  if (nodes.length === 0) return { positioned: [], width: 0, height: 0 }

  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: "LR", ranksep: RANKSEP, nodesep: NODESEP })

  for (const n of nodes) {
    const override = nodeSizeOverrides[n.id]
    g.setNode(n.id, {
      width: override?.width ?? NODE_WIDTH,
      height: override?.height ?? NODE_HEIGHT,
    })
  }

  const nodeIds = new Set(nodes.map((n) => n.id))
  for (const e of edges) {
    if (nodeIds.has(e.source) && nodeIds.has(e.target)) {
      g.setEdge(e.source, e.target)
    }
  }

  dagre.layout(g)

  // Find bounding box and normalise to (0, 0).
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity

  const rawPositions = nodes.map((n) => {
    const pos = g.node(n.id)
    const override = nodeSizeOverrides[n.id]
    const w = override?.width ?? NODE_WIDTH
    const h = override?.height ?? NODE_HEIGHT
    const x = pos.x - w / 2
    const y = pos.y - h / 2
    if (x < minX) minX = x
    if (y < minY) minY = y
    if (x + w > maxX) maxX = x + w
    if (y + h > maxY) maxY = y + h
    return { node: n, x, y, w, h }
  })

  const positioned = rawPositions.map((p) => ({
    ...p,
    x: Math.round(p.x - minX),
    y: Math.round(p.y - minY),
  }))

  return { positioned, width: maxX - minX, height: maxY - minY }
}

/** IGW nodes are smaller (pill-shaped). */
export const IGW_NODE_WIDTH = 200
export const IGW_NODE_HEIGHT = 52

/** Map service key to custom React Flow node type. */
function nodeType(service: string): string {
  switch (service) {
    case "vpc":
      return "vpcNetworkNode"
    case "igw":
      return "igwNode"
    default:
      return "serviceNode"
  }
}

/** Create a React Flow node from a positioned topology node. */
function toFlowNode(
  p: { node: TopologyNode; x: number; y: number; w: number; h: number },
  parentId?: string,
): Node {
  return {
    id: p.node.id,
    type: nodeType(p.node.service),
    ...(parentId ? { parentId, extent: "parent" as const } : {}),
    position: { x: p.x, y: p.y },
    width: p.w,
    height: p.h,
    data: {
      service: p.node.service,
      label: p.node.label,
      region: p.node.region,
      streamEnabled: p.node.streamEnabled,
      approximateNumberOfMessages: p.node.approximateNumberOfMessages,
      approximateNumberOfMessagesNotVisible: p.node.approximateNumberOfMessagesNotVisible,
      status: p.node.status,
      cidrBlock: p.node.cidrBlock,
      subnetCount: p.node.subnetCount,
      hasInternetGateway: p.node.hasInternetGateway,
      attachedVpcId: p.node.attachedVpcId,
      protocolType: p.node.protocolType,
      routeCount: p.node.routeCount,
      stageCount: p.node.stageCount,
      authenticationType: p.node.authenticationType,
      dataSourceCount: p.node.dataSourceCount,
      resolverCount: p.node.resolverCount,
      repositoryUri: p.node.repositoryUri,
      esmId: p.node.esmId,
      functionName: p.node.functionName,
      eventSource: p.node.eventSource,
      sourceType: p.node.sourceType,
      filterPatterns: p.node.filterPatterns,
    },
    style: { width: p.w },
  }
}

/**
 * Converts topology nodes and edges into positioned React Flow nodes.
 *
 * Layout hierarchy: region → (optional) CloudFormation stack → resource node.
 * Within each region, nodes that share a `stackName` are grouped into a stack
 * container which dagre treats as a single fat node at the region level.
 *
 * @param nodeSizeOverrides - optional per-node {width, height} overrides, keyed by node ID.
 */
export function buildLayoutNodes(
  topologyNodes: TopologyNode[],
  topologyEdges: TopologyEdge[] = [],
  nodeSizeOverrides: Record<string, { width: number; height: number } | undefined> = {},
  /** When set, guarantees this region has a group box even if it has no resources. */
  activeRegion?: string,
  /** Stack phantom IDs to render as collapsed chips instead of full groups. */
  collapsedStacks: Set<string> = new Set(),
): Node[] {
  // Group nodes by region.
  const byRegion = new Map<string, TopologyNode[]>()
  for (const n of topologyNodes) {
    const r = n.region
    if (!byRegion.has(r)) byRegion.set(r, [])
    byRegion.get(r)!.push(n)
  }

  // Ensure the active region exists even when empty.
  if (activeRegion && !byRegion.has(activeRegion)) {
    byRegion.set(activeRegion, [])
  }

  const regions = [...byRegion.keys()].sort()
  if (regions.length === 0) return []

  // Every region gets a group container with a region badge — even when
  // there is only one region — so the developer always sees which region
  // resources belong to.
  const result: Node[] = []
  let yOffset = 0

  for (const region of regions) {
    const regionNodes = byRegion.get(region)!
    const groupId = `region::${region}`

    if (regionNodes.length === 0) {
      // Empty region — render a placeholder-sized box.
      const emptyW = 340
      const emptyH = 140
      result.push({
        id: groupId,
        type: "regionGroup",
        position: { x: 0, y: yOffset },
        width: emptyW,
        height: emptyH,
        data: { region, empty: true, active: region === activeRegion },
        style: { width: emptyW, height: emptyH },
        draggable: false,
        selectable: false,
      })
      yOffset += emptyH + REGION_GAP
      continue
    }

    // Separate nodes into stacked (belongs to a CFN stack), VPC-grouped, and unstacked.
    const byStack = new Map<string, TopologyNode[]>()
    const byVpc = new Map<string, TopologyNode[]>()
    const unstacked: TopologyNode[] = []
    for (const n of regionNodes) {
      if (n.stackName) {
        if (!byStack.has(n.stackName)) byStack.set(n.stackName, [])
        byStack.get(n.stackName)!.push(n)
      } else if (n.vpcId) {
        if (!byVpc.has(n.vpcId)) byVpc.set(n.vpcId, [])
        byVpc.get(n.vpcId)!.push(n)
      } else {
        unstacked.push(n)
      }
    }

    const regionNodeIds = new Set(regionNodes.map((n) => n.id))
    const regionEdges = topologyEdges.filter(
      (e) => regionNodeIds.has(e.source) && regionNodeIds.has(e.target),
    )

    if (byStack.size === 0 && byVpc.size === 0) {
      // No stacks — simple flat layout (fast path).
      const { positioned, width, height } = layoutSubgraph(
        regionNodes,
        regionEdges,
        nodeSizeOverrides,
      )
      const groupW = Math.round(width + REGION_PAD_X * 2)
      const groupH = Math.round(height + REGION_PAD_TOP + REGION_PAD_BOTTOM)

      result.push({
        id: groupId,
        type: "regionGroup",
        position: { x: 0, y: yOffset },
        width: groupW,
        height: groupH,
        data: { region, active: region === activeRegion },
        style: { width: groupW, height: groupH },
        draggable: false,
        selectable: false,
      })

      for (const p of positioned) {
        result.push(toFlowNode({ ...p, x: p.x + REGION_PAD_X, y: p.y + REGION_PAD_TOP }, groupId))
      }

      yOffset += groupH + REGION_GAP
      continue
    }

    // ── Nested stack layout: region → stack (recursive) / vpc → node ─────

    // Parse nested-stack edges to derive parent→child stack relationships.
    const stackParent = new Map<string, string>()
    const stackChildMap = new Map<string, string[]>()
    for (const e of topologyEdges) {
      if (e.type !== "nested-stack") continue
      const pn = e.source.split("::").pop()!
      const cn = e.target.split("::").pop()!
      if (pn && cn) {
        stackParent.set(cn, pn)
        const arr = stackChildMap.get(pn) ?? []
        arr.push(cn)
        stackChildMap.set(pn, arr)
      }
    }

    // Root stacks: present in byStack and not a child of another stack.
    const rootStacks = [...byStack.keys()].filter((n) => !stackParent.has(n))

    // Total recursive resource count (for collapsed chip badges).
    function totalResources(name: string): number {
      const direct = byStack.get(name)?.length ?? 0
      return direct + (stackChildMap.get(name) ?? []).reduce((s, c) => s + totalResources(c), 0)
    }

    // Collect all descendant resource node IDs for edge remapping.
    function descendantNodeIds(name: string): string[] {
      const ids = (byStack.get(name) ?? []).map((n) => n.id)
      for (const c of stackChildMap.get(name) ?? []) {
        ids.push(...descendantNodeIds(c))
      }
      return ids
    }

    // ── Recursive stack layout (bottom-up) ──────────────────────────────
    // Each stack's direct resources + child stacks are laid out via dagre.
    // Collapsed children become compact chip nodes; expanded children
    // become nested sub-groups.

    interface StackLayoutResult {
      resultNodes: Node[]
      width: number
      height: number
      resourceCount: number
    }

    function layoutStackHierarchy(stackName: string): StackLayoutResult {
      const stackId = `stack::${region}::${stackName}`
      const directNodes = byStack.get(stackName) ?? []
      const children = stackChildMap.get(stackName) ?? []

      // --- Recurse into children first (bottom-up) ---
      const childResults = new Map<string, StackLayoutResult>()
      const childOverrides: Record<string, { width: number; height: number }> = {}

      for (const cn of children) {
        const childId = `stack::${region}::${cn}`
        if (collapsedStacks.has(childId)) {
          childResults.set(cn, {
            resultNodes: [],
            width: COLLAPSED_STACK_WIDTH,
            height: COLLAPSED_STACK_HEIGHT,
            resourceCount: totalResources(cn),
          })
          childOverrides[childId] = {
            width: COLLAPSED_STACK_WIDTH,
            height: COLLAPSED_STACK_HEIGHT,
          }
        } else {
          const cr = layoutStackHierarchy(cn)
          childResults.set(cn, cr)
          childOverrides[childId] = {
            width: Math.round(cr.width + STACK_PAD_X * 2),
            height: Math.round(cr.height + STACK_PAD_TOP + STACK_PAD_BOTTOM),
          }
        }
      }

      // --- Build dagre input: direct resources + child phantoms ---
      const layoutNodes: TopologyNode[] = [
        ...directNodes,
        ...children.map((cn) => ({
          id: `stack::${region}::${cn}`,
          service: "cloudformation",
          label: cn,
          region,
        })),
      ]

      // Remap edges: child descendants → child phantom ID.
      const nodeToChild = new Map<string, string>()
      for (const cn of children) {
        const cid = `stack::${region}::${cn}`
        for (const nid of descendantNodeIds(cn)) {
          nodeToChild.set(nid, cid)
        }
      }
      const layoutIds = new Set(layoutNodes.map((n) => n.id))
      const intraEdges = regionEdges
        .map((e) => ({
          ...e,
          source: nodeToChild.get(e.source) ?? e.source,
          target: nodeToChild.get(e.target) ?? e.target,
        }))
        .filter((e) => layoutIds.has(e.source) && layoutIds.has(e.target))
        .filter((e) => e.source !== e.target)

      const mergedSizes = { ...nodeSizeOverrides, ...childOverrides }
      const { positioned, width, height } = layoutSubgraph(layoutNodes, intraEdges, mergedSizes)

      // --- Emit React Flow nodes ---
      const resultNodes: Node[] = []
      const sw = Math.round(width + STACK_PAD_X * 2)
      const sh = Math.round(height + STACK_PAD_TOP + STACK_PAD_BOTTOM)

      // Stack group container (position set by caller).
      resultNodes.push({
        id: stackId,
        type: "stackGroup",
        position: { x: 0, y: 0 },
        width: sw,
        height: sh,
        data: { stackName },
        style: { width: sw, height: sh },
        draggable: false,
        selectable: false,
      })

      for (const p of positioned) {
        const px = p.x + STACK_PAD_X
        const py = p.y + STACK_PAD_TOP

        if (p.node.id.startsWith("stack::")) {
          // Child stack phantom.
          const cn = p.node.label
          const cid = p.node.id
          const cr = childResults.get(cn)!

          if (collapsedStacks.has(cid)) {
            // Collapsed chip: small clickable node.
            resultNodes.push({
              id: cid,
              type: "collapsedStack",
              parentId: stackId,
              extent: "parent" as const,
              position: { x: px, y: py },
              width: COLLAPSED_STACK_WIDTH,
              height: COLLAPSED_STACK_HEIGHT,
              data: { stackName: cn, resourceCount: cr.resourceCount },
              style: { width: COLLAPSED_STACK_WIDTH },
              draggable: false,
              selectable: false,
            })
          } else {
            // Expanded sub-group: reposition and wire parentId.
            const [groupNode, ...groupChildren] = cr.resultNodes
            resultNodes.push({
              ...groupNode,
              parentId: stackId,
              extent: "parent" as const,
              position: { x: px, y: py },
            })
            resultNodes.push(...groupChildren)
          }
        } else {
          // Regular resource node.
          resultNodes.push(toFlowNode({ ...p, x: px, y: py }, stackId))
        }
      }

      return { resultNodes, width, height, resourceCount: totalResources(stackName) }
    }

    // --- Run recursive layout for root stacks ---
    const stackResults = new Map<string, StackLayoutResult>()
    const stackSizeOverrides: Record<string, { width: number; height: number }> = {}

    for (const sn of rootStacks) {
      const sr = layoutStackHierarchy(sn)
      stackResults.set(sn, sr)
      const pid = `stack::${region}::${sn}`
      stackSizeOverrides[pid] = {
        width: Math.round(sr.width + STACK_PAD_X * 2),
        height: Math.round(sr.height + STACK_PAD_TOP + STACK_PAD_BOTTOM),
      }
    }

    // 1b. Layout each VPC internally.
    const vpcLayouts = new Map<string, ReturnType<typeof layoutSubgraph>>()
    const vpcSizeOverrides: Record<string, { width: number; height: number }> = {}

    for (const [vpcId, vpcNodes] of byVpc) {
      const vpcNodeIds = new Set(vpcNodes.map((n) => n.id))
      const intraEdges = regionEdges.filter(
        (e) => vpcNodeIds.has(e.source) && vpcNodeIds.has(e.target),
      )
      const layout = layoutSubgraph(vpcNodes, intraEdges, nodeSizeOverrides)
      vpcLayouts.set(vpcId, layout)

      const phantomId = `vpc-group-${vpcId}`
      vpcSizeOverrides[phantomId] = {
        width: Math.round(layout.width + VPC_PAD_X * 2),
        height: Math.round(layout.height + VPC_PAD_TOP + VPC_PAD_BOTTOM),
      }
    }

    // 2. Build phantom nodes for root stacks + VPCs + keep unstacked nodes.
    const regionLevelNodes: TopologyNode[] = [
      ...unstacked,
      ...rootStacks.map((name) => ({
        id: `stack::${region}::${name}`,
        service: "cloudformation",
        label: name,
        region,
      })),
      ...[...byVpc.keys()].map((vpcId) => ({
        id: `vpc-group-${vpcId}`,
        service: "vpc",
        label: vpcId,
        region,
      })),
    ]

    // 3. Remap edges: all stacked/VPC node IDs → root phantom IDs.
    const nodeToPhantom = new Map<string, string>()
    for (const sn of rootStacks) {
      const pid = `stack::${region}::${sn}`
      function mapAll(name: string) {
        for (const n of byStack.get(name) ?? []) {
          nodeToPhantom.set(n.id, pid)
        }
        for (const cn of stackChildMap.get(name) ?? []) {
          mapAll(cn)
        }
      }
      mapAll(sn)
    }
    for (const [vpcId, vpcNodes] of byVpc) {
      const phantomId = `vpc-group-${vpcId}`
      for (const n of vpcNodes) {
        nodeToPhantom.set(n.id, phantomId)
      }
    }

    const remappedEdges = regionEdges
      .map((e) => ({
        ...e,
        source: nodeToPhantom.get(e.source) ?? e.source,
        target: nodeToPhantom.get(e.target) ?? e.target,
      }))
      .filter((e) => e.source !== e.target)

    // 4. Run region-level layout with root stacks and VPCs as fat nodes.
    const mergedOverrides = { ...nodeSizeOverrides, ...stackSizeOverrides, ...vpcSizeOverrides }
    const { positioned, width, height } = layoutSubgraph(
      regionLevelNodes,
      remappedEdges,
      mergedOverrides,
    )

    const groupW = Math.round(width + REGION_PAD_X * 2)
    const groupH = Math.round(height + REGION_PAD_TOP + REGION_PAD_BOTTOM)

    // Region group node
    result.push({
      id: groupId,
      type: "regionGroup",
      position: { x: 0, y: yOffset },
      width: groupW,
      height: groupH,
      data: { region, active: region === activeRegion },
      style: { width: groupW, height: groupH },
      draggable: false,
      selectable: false,
    })

    // 5. Emit positioned nodes — root stacks use recursive results,
    //    VPCs expand as before, unstacked nodes are direct children.
    for (const p of positioned) {
      const regionX = p.x + REGION_PAD_X
      const regionY = p.y + REGION_PAD_TOP

      if (p.node.id.startsWith("stack::")) {
        // Root stack → emit all nodes from recursive layout.
        const stackName = p.node.label
        const sr = stackResults.get(stackName)!
        const [groupNode, ...groupChildren] = sr.resultNodes
        result.push({
          ...groupNode,
          parentId: groupId,
          extent: "parent" as const,
          position: { x: regionX, y: regionY },
        })
        result.push(...groupChildren)
      } else if (p.node.id.startsWith("vpc-group-")) {
        // Phantom → emit VPC group container + its children.
        const vpcId = p.node.label
        const vpcGroupId = p.node.id
        const vw = p.w
        const vh = p.h

        result.push({
          id: vpcGroupId,
          type: "vpcGroup",
          parentId: groupId,
          extent: "parent" as const,
          position: { x: regionX, y: regionY },
          width: vw,
          height: vh,
          data: { vpcId },
          style: { width: vw, height: vh },
          draggable: false,
          selectable: false,
        })

        const layout = vpcLayouts.get(vpcId)!
        for (const cp of layout.positioned) {
          result.push(toFlowNode({ ...cp, x: cp.x + VPC_PAD_X, y: cp.y + VPC_PAD_TOP }, vpcGroupId))
        }
      } else {
        // Unstacked node — direct child of region group.
        result.push(toFlowNode({ ...p, x: regionX, y: regionY }, groupId))
      }
    }

    yOffset += groupH + REGION_GAP
  }

  // Cross-region edges still reference the child nodes directly — React Flow
  // handles edges between children of different group nodes automatically.

  return result
}

// ─── Compact layout for dashboard minimap ────────────────────────────────────

/** Compact node dimensions used by the dashboard minimap. */
const MINI_W = 140
const MINI_H = 32
const MINI_RANKSEP = 60
const MINI_NODESEP = 16
const MINI_REGION_GAP = 24

/**
 * Builds a flat, compact layout for the dashboard minimap.
 *
 * Unlike `buildLayoutNodes`, this intentionally **omits** region/stack group
 * container nodes — the minimap header shows a stats summary instead.
 * All nodes are emitted as `type: "miniNode"` with smaller dimensions so the
 * topology fits legibly inside a 320px-tall card.
 */
export function buildCompactLayoutNodes(
  topologyNodes: TopologyNode[],
  topologyEdges: TopologyEdge[] = [],
): Node[] {
  if (topologyNodes.length === 0) return []

  // Group nodes by region and lay out each region separately, then stack
  // them vertically with a small gap.
  const byRegion = new Map<string, TopologyNode[]>()
  for (const n of topologyNodes) {
    if (!byRegion.has(n.region)) byRegion.set(n.region, [])
    byRegion.get(n.region)!.push(n)
  }

  const regions = [...byRegion.keys()].sort()
  const result: Node[] = []
  let yOffset = 0

  for (const region of regions) {
    const regionNodes = byRegion.get(region)!
    const regionNodeIds = new Set(regionNodes.map((n) => n.id))
    const regionEdges = topologyEdges.filter(
      (e) => regionNodeIds.has(e.source) && regionNodeIds.has(e.target),
    )

    const { positioned, height } = layoutCompactSubgraph(regionNodes, regionEdges)

    for (const p of positioned) {
      result.push({
        id: p.node.id,
        type: "miniNode",
        position: { x: p.x, y: p.y + yOffset },
        width: p.w,
        height: p.h,
        data: {
          service: p.node.service,
          label: p.node.label,
        },
        style: { width: p.w },
      })
    }

    yOffset += height + MINI_REGION_GAP
  }

  return result
}

/** Dagre layout with compact dimensions. Mirrors `layoutSubgraph` but with smaller params. */
function layoutCompactSubgraph(
  nodes: TopologyNode[],
  edges: TopologyEdge[],
): {
  positioned: Array<{ node: TopologyNode; x: number; y: number; w: number; h: number }>
  width: number
  height: number
} {
  if (nodes.length === 0) return { positioned: [], width: 0, height: 0 }

  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: "LR", ranksep: MINI_RANKSEP, nodesep: MINI_NODESEP })

  for (const n of nodes) {
    g.setNode(n.id, { width: MINI_W, height: MINI_H })
  }

  const nodeIds = new Set(nodes.map((n) => n.id))
  for (const e of edges) {
    if (nodeIds.has(e.source) && nodeIds.has(e.target)) {
      g.setEdge(e.source, e.target)
    }
  }

  dagre.layout(g)

  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity

  const rawPositions = nodes.map((n) => {
    const pos = g.node(n.id)
    const x = pos.x - MINI_W / 2
    const y = pos.y - MINI_H / 2
    if (x < minX) minX = x
    if (y < minY) minY = y
    if (x + MINI_W > maxX) maxX = x + MINI_W
    if (y + MINI_H > maxY) maxY = y + MINI_H
    return { node: n, x, y, w: MINI_W, h: MINI_H }
  })

  const positioned = rawPositions.map((p) => ({
    ...p,
    x: Math.round(p.x - minX),
    y: Math.round(p.y - minY),
  }))

  return { positioned, width: maxX - minX, height: maxY - minY }
}

// ─── Module-level async layout worker ────────────────────────────────────

let _layoutWorker: Worker | null = null
let _layoutId = 0
const _layoutListeners = new Set<() => void>()
let _layoutResult = { nodes: [] as Node[] }
let _layoutLoading = false
let _lastLayoutHash = ''

function getWorker(): Worker {
  if (!_layoutWorker) {
    _layoutWorker = new Worker(
      new URL('./map-layout.worker.ts', import.meta.url),
      { type: 'module' },
    )
    _layoutWorker.onmessage = (e: MessageEvent) => {
      if (e.data.id !== _layoutId) return
      if (e.data.error) {
        console.error('Layout worker error:', e.data.error)
        _layoutLoading = false
        return
      }
      _layoutResult = { nodes: e.data.nodes ?? [] }
      _layoutLoading = false
      _layoutListeners.forEach((cb) => cb())
    }
  }
  return _layoutWorker
}

function layoutInputHash(
  topologyNodes: TopologyNode[],
  topologyEdges: TopologyEdge[],
  nodeSizeOverrides: Record<string, { width: number; height: number } | undefined>,
  activeRegion?: string,
  collapsedStacks?: Set<string>,
): string {
  const overridesStr = Object.entries(nodeSizeOverrides)
    .filter(([, v]) => v)
    .map(([k, v]) => `${k}:${v!.width}x${v!.height}`)
    .join(';')
  return `${topologyNodes.map((n) => n.id).join(',')}|${topologyEdges.length}|${overridesStr}|${activeRegion ?? ''}|${collapsedStacks?.size ?? 0}`
}

/**
 * Request async layout computation.
 * Deduplicates — skips if inputs are identical to the previous request.
 */
export function requestLayoutAsync(
  topologyNodes: TopologyNode[],
  topologyEdges: TopologyEdge[],
  nodeSizeOverrides: Record<string, { width: number; height: number } | undefined>,
  activeRegion?: string,
  collapsedStacks?: Set<string>,
): void {
  if (topologyNodes.length === 0) {
    _layoutResult = { nodes: [] }
    _layoutLoading = false
    _layoutListeners.forEach((cb) => cb())
    return
  }

  const hash = layoutInputHash(topologyNodes, topologyEdges, nodeSizeOverrides, activeRegion, collapsedStacks)
  if (hash === _lastLayoutHash && !_layoutLoading) return
  _lastLayoutHash = hash

  _layoutLoading = true
  const id = ++_layoutId
  const worker = getWorker()
  worker.postMessage({
    id,
    topologyNodes,
    topologyEdges,
    nodeSizeOverrides,
    activeRegion,
    collapsedStacks: collapsedStacks ? [...collapsedStacks] : [],
  })
}

/** useSyncExternalStore subscribe function — returns unsubscribe. */
export function subscribeToLayout(cb: () => void): () => void {
  _layoutListeners.add(cb)
  return () => {
    _layoutListeners.delete(cb)
  }
}

/** useSyncExternalStore snapshot function. */
export function getLayoutSnapshot(): Node[] {
  return _layoutResult.nodes
}

/** Whether a layout computation is in-flight. */
export function isLayoutLoading(): boolean {
  return _layoutLoading
}
