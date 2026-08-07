import { useMemo, useState } from "react"
import {
  ReactFlow,
  Background,
  Controls,
  MarkerType,
  type Node,
  type Edge,
  type NodeMouseHandler,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import type { TraceEntry } from "@/types"
import { nsToHuman } from "@/features/debug-traces/utils"

interface FlowMapProps {
  trace: TraceEntry
  aggregateThreshold?: number
  onSelectHop?: (hopId: string | null) => void
  selectedHopId?: string | null
}

export function FlowMap({ trace, aggregateThreshold = 5, onSelectHop, selectedHopId }: FlowMapProps) {
  const [hideNoisy, setHideNoisy] = useState(true)
  const [errorsOnly, setErrorsOnly] = useState(false)
  const [minDurationMs, setMinDurationMs] = useState(0)

  const { nodes, edges, hops } = useMemo(() => {
    const h = trace.hops ?? []
    const result = buildGraph(trace, aggregateThreshold)
    let filteredNodes = result.nodes
    let filteredEdges = result.edges

    // Collect matching IDs from active filters
    const noisyIds = new Set(
      h.filter((hop) => hop.noisy).map((hop) => hop.id),
    )
    result.nodes.forEach((n) => {
      if (n.data.aggregateCount > 0) noisyIds.add(n.id)
    })
    const errorIds = new Set(
      result.nodes.filter((n) => n.data.responseStatus >= 400 && !n.data.isEntry).map((n) => n.id),
    )
    const durNs = minDurationMs * 1_000_000
    const slowIds = new Set(
      result.nodes.filter((n) => n.data.duration >= durNs && !n.data.isEntry).map((n) => n.id),
    )

    // Build filtered sets as union of IDs excluded by active filters
    const exclude = new Set<string>()
    if (hideNoisy) for (const id of noisyIds) exclude.add(id)
    if (errorsOnly && errorIds.size > 0) {
      // Keep only: entry + node in errorIds
      filteredNodes = result.nodes.filter((n) => n.data.isEntry || errorIds.has(n.id))
      filteredEdges = result.edges.filter((e) => e.source === "entry" && errorIds.has(e.target))
    }
    if (minDurationMs > 0 && slowIds.size > 0) {
      // Intersect with slow: keep nodes that are in the current set AND also slow
      const currentIds = new Set(filteredNodes.map((n) => n.id))
      filteredNodes = filteredNodes.filter((n) => n.data.isEntry || (slowIds.has(n.id) && currentIds.has(n.id)))
      filteredEdges = filteredEdges.filter((e) => e.source === "entry" && slowIds.has(e.target))
    }

    // Apply hideNoisy last — remove noisy nodes from whatever remains
    if (hideNoisy) {
      filteredNodes = filteredNodes.filter((n) => !noisyIds.has(n.id))
      filteredEdges = filteredEdges.filter((e) => !noisyIds.has(e.target))
    }
    // Style selected node
    if (selectedHopId) {
      filteredNodes = filteredNodes.map((n) => {
        if (n.id === selectedHopId) {
          return { ...n, style: { ...n.style, border: "2px solid var(--color-accent)", boxShadow: "0 0 8px var(--color-accent)" } }
        }
        return { ...n, style: { ...n.style, opacity: 0.5 } }
      })
    }
    return { nodes: filteredNodes, edges: filteredEdges, hops: h }
  }, [trace, aggregateThreshold, hideNoisy, errorsOnly, minDurationMs, selectedHopId])

  const handleNodeClick: NodeMouseHandler = (_event, node) => {
    if (node.id === "entry") {
      onSelectHop?.(null)
    } else if (!node.data.aggregateCount) {
      onSelectHop?.(node.id === selectedHopId ? null : node.id)
    }
  }

  const noisyCount = hops.filter((h) => h.noisy).length
  const errorCount = hops.filter((h) => h.responseStatus >= 400 || !!h.error).length
  const slowCount = hops.filter((h) => h.duration > 100_000_000).length

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap gap-3 text-xs">
        {noisyCount > 0 && (
          <label className="flex items-center gap-1 cursor-pointer select-none text-fg-muted">
            <input type="checkbox" checked={!hideNoisy} onChange={(e) => setHideNoisy(!e.target.checked)} className="rounded" />
            Show {noisyCount} noisy
          </label>
        )}
        {errorCount > 0 && (
          <label className="flex items-center gap-1 cursor-pointer select-none text-fg-muted">
            <input type="checkbox" checked={errorsOnly} onChange={(e) => setErrorsOnly(e.target.checked)} className="rounded" />
            Errors only ({errorCount})
          </label>
        )}
        {slowCount > 0 && (
          <label className="flex items-center gap-1 cursor-pointer select-none text-fg-muted">
            <input type="checkbox" checked={minDurationMs >= 100} onChange={(e) => setMinDurationMs(e.target.checked ? 100 : 0)} className="rounded" />
            ≥100ms ({slowCount})
          </label>
        )}
        {selectedHopId && (
          <button
            onClick={() => onSelectHop?.(null)}
            className="text-accent hover:underline ml-auto"
          >
            Clear selection
          </button>
        )}
      </div>
      <div className="h-[500px] w-full rounded-lg border border-border">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          fitView
          fitViewOptions={{ padding: 0.3 }}
          nodesDraggable={false}
          nodesConnectable={false}
          proOptions={{ hideAttribution: true }}
          onNodeClick={handleNodeClick}
        >
          <Background gap={20} color="var(--color-border)" />
          <Controls className="!bg-bg-elevated !border-border !text-fg" />
        </ReactFlow>
      </div>
    </div>
  )
}

interface FlowNodeData extends Record<string, unknown> {
  label: string
  service: string
  isEntry: boolean
  aggregateCount: number
  responseStatus: number
  duration: number
  tooltip: string
}

function buildGraph(trace: TraceEntry, threshold: number): { nodes: Node<FlowNodeData>[]; edges: Edge[] } {
  const nodes: Node<FlowNodeData>[] = []
  const edges: Edge[] = []
  const rootId = "entry"

  nodes.push({
    id: rootId, type: "default", position: { x: 0, y: 0 },
    data: {
      label: trace.service + (trace.operation ? "." + trace.operation : ""),
      service: trace.service, isEntry: true, aggregateCount: 0,
      responseStatus: trace.statusCode, duration: trace.duration,
      tooltip: `${trace.service}.${trace.operation ?? "request"}\nStatus: ${trace.statusCode}\nDuration: ${nsToHuman(trace.duration)}`,
    },
    style: { background: "var(--color-bg-elevated)", border: `2px solid ${serviceColor(trace.service)}`, borderRadius: "8px", padding: "8px 16px", fontSize: "12px", color: "var(--color-fg)" },
  })

  let y = 0
  const aggregated = aggregateHops(trace.hops ?? [], threshold)

  for (const hop of aggregated.kept) {
    const statusLabel = hop.error ? "Error" : hop.responseStatus
    nodes.push({
      id: hop.id, type: "default", position: { x: 200, y },
      data: {
        label: hop.service + "." + hop.operation,
        service: hop.service, isEntry: false, aggregateCount: 0,
        responseStatus: hop.responseStatus, duration: hop.duration,
        tooltip: `${hop.callerService} → ${hop.service}.${hop.operation}\n${hop.targetUri ?? ""}\nStatus: ${statusLabel}\nDuration: ${nsToHuman(hop.duration)}`,
      },
      style: {
        background: "var(--color-bg-elevated)",
        border: `2px solid ${serviceColor(hop.service)}`,
        borderRadius: "8px", padding: "8px 16px", fontSize: "12px", color: "var(--color-fg)",
        cursor: "pointer",
      },
    })
    edges.push({ id: `${rootId}→${hop.id}`, source: rootId, target: hop.id, label: hop.operation, style: { stroke: serviceColor(hop.service) }, markerEnd: { type: MarkerType.ArrowClosed, color: serviceColor(hop.service) } })
    y += 80
  }
  for (const agg of aggregated.aggregates) {
    nodes.push({
      id: agg.id, type: "default", position: { x: 200, y },
      data: { label: `${agg.service}.${agg.operation} ×${agg.count}`, service: agg.service, isEntry: false, aggregateCount: agg.count, responseStatus: 0, duration: 0, tooltip: `${agg.count} hops: ${agg.service}.${agg.operation}` },
      style: { background: "var(--color-bg-elevated)", border: `2px dashed ${serviceColor(agg.service)}`, borderRadius: "8px", padding: "8px 16px", fontSize: "12px", color: "var(--color-fg-muted)", cursor: "default" },
    })
    edges.push({ id: `${rootId}→${agg.id}`, source: rootId, target: agg.id, label: `${agg.operation} ×${agg.count}`, style: { stroke: serviceColor(agg.service), strokeDasharray: "4 2" }, markerEnd: { type: MarkerType.ArrowClosed, color: serviceColor(agg.service) } })
    y += 80
  }
  return { nodes, edges }
}

interface AggregatedHop { id: string; service: string; operation: string; count: number }

function aggregateHops(hops: NonNullable<TraceEntry["hops"]>, threshold: number): { kept: typeof hops; aggregates: AggregatedHop[] } {
  const groups = new Map<string, typeof hops>()
  const kept: typeof hops = []
  for (const h of hops) {
    if (h.noisy) {
      const key = `${h.callerService}/${h.service}/${h.operation}`
      const group = groups.get(key) ?? []
      group.push(h)
      groups.set(key, group)
    } else { kept.push(h) }
  }
  const aggregates: AggregatedHop[] = []
  for (const [, group] of groups) {
    if (group.length <= threshold) { kept.push(...group) }
    else aggregates.push({ id: `agg-${group[0].service}-${group[0].operation}`, service: group[0].service, operation: group[0].operation, count: group.length })
  }
  return { kept, aggregates }
}

function serviceColor(service: string): string {
  const colors: Record<string, string> = { cloudformation: "#f59e0b", lambda: "#f97316", sqs: "#8b5cf6", sns: "#ec4899", s3: "#22c55e", dynamodb: "#3b82f6", iam: "#ef4444", ecs: "#06b6d4", ec2: "#f97316", kms: "#a855f7", ssm: "#6366f1", logs: "#14b8a6", stepfunctions: "#84cc16", events: "#e11d48", secretsmanager: "#d946ef", apigateway: "#0ea5e9", appsync: "#f43f5e", cognito: "#8b5cf6", waf: "#e11d48", cloudfront: "#06b6d4", pipes: "#f59e0b", kinesis: "#3b82f6", elasticache: "#eab308", rds: "#3b82f6", elbv2: "#06b6d4", autoscaling: "#f97316", route53: "#8b5cf6", acm: "#22c55e", sts: "#ef4444", transfer: "#a855f7", shield: "#e11d48", backup: "#14b8a6", firehose: "#f59e0b", athena: "#3b82f6", glue: "#84cc16", msk: "#ec4899", eks: "#f97316", efs: "#06b6d4", opensearch: "#8b5cf6", appconfig: "#22c55e", bedrock: "#f43f5e", scheduler: "#a855f7", cloudtrail: "#f59e0b", organizations: "#ef4444", ses: "#6366f1" }
  return colors[service] ?? "var(--color-accent)"
}
