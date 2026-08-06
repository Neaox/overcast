import { useMemo } from "react"
import {
  ReactFlow,
  Background,
  Controls,
  MarkerType,
  type Node,
  type Edge,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import type { TraceEntry } from "@/types"
import { mapTheme } from "@/features/map/map-theme"

interface FlowMapProps {
  trace: TraceEntry
  aggregateThreshold?: number
}

/**
 * Interactive ReactFlow graph showing a request's journey through internal
 * services. Each hop becomes a node with the target service's color; edges
 * show the hop sequence. Noisy hops (10+) are aggregated into a single node
 * with a count badge.
 */
export function FlowMap({ trace, aggregateThreshold = 5 }: FlowMapProps) {
  const { nodes, edges } = useMemo(() => buildGraph(trace, aggregateThreshold), [trace, aggregateThreshold])

  return (
    <div className="h-[500px] w-full rounded-lg border border-border">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        fitView
        fitViewOptions={{ padding: 0.3 }}
        nodesDraggable={false}
        nodesConnectable={false}
        proOptions={{ hideAttribution: true }}
      >
        <Background gap={20} color="var(--color-border)" />
        <Controls className="!bg-bg-elevated !border-border !text-fg" />
      </ReactFlow>
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
}

function buildGraph(
  trace: TraceEntry,
  threshold: number,
): { nodes: Node<FlowNodeData>[]; edges: Edge[] } {
  const nodes: Node<FlowNodeData>[] = []
  const edges: Edge[] = []

  const rootId = "entry"
  nodes.push({
    id: rootId,
    type: "default",
    position: { x: 0, y: 0 },
    data: {
      label: trace.service + (trace.operation ? "." + trace.operation : ""),
      service: trace.service,
      isEntry: true,
      aggregateCount: 0,
      responseStatus: trace.statusCode,
      duration: trace.duration,
    },
    style: {
      background: "var(--color-bg-elevated)",
      border: `2px solid ${serviceColor(trace.service)}`,
      borderRadius: "8px",
      padding: "8px 16px",
      fontSize: "12px",
      color: "var(--color-fg)",
    },
  })

  let x = 200
  let y = 0

  const aggregated = aggregateHops(trace.hops ?? [], threshold)

  for (const hop of aggregated.kept) {
    nodes.push({
      id: hop.id,
      type: "default",
      position: { x, y },
      data: {
        label: hop.service + "." + hop.operation,
        service: hop.service,
        isEntry: false,
        aggregateCount: 0,
        responseStatus: hop.responseStatus,
        duration: hop.duration,
      },
      style: {
        background: "var(--color-bg-elevated)",
        border: `2px solid ${serviceColor(hop.service)}`,
        borderRadius: "8px",
        padding: "8px 16px",
        fontSize: "12px",
        color: "var(--color-fg)",
      },
    })
    edges.push({
      id: `${rootId}→${hop.id}`,
      source: rootId,
      target: hop.id,
      label: hop.operation,
      style: { stroke: serviceColor(hop.service) },
      markerEnd: { type: MarkerType.ArrowClosed, color: serviceColor(hop.service) },
    })
    y += 80
  }

  for (const agg of aggregated.aggregates) {
    nodes.push({
      id: agg.id,
      type: "default",
      position: { x, y },
      data: {
        label: `${agg.service}.${agg.operation} ×${agg.count}`,
        service: agg.service,
        isEntry: false,
        aggregateCount: agg.count,
        responseStatus: 0,
        duration: 0,
      },
      style: {
        background: "var(--color-bg-elevated)",
        border: `2px dashed ${serviceColor(agg.service)}`,
        borderRadius: "8px",
        padding: "8px 16px",
        fontSize: "12px",
        color: "var(--color-fg-muted)",
      },
    })
    edges.push({
      id: `${rootId}→${agg.id}`,
      source: rootId,
      target: agg.id,
      label: `${agg.operation} ×${agg.count}`,
      style: { stroke: serviceColor(agg.service), strokeDasharray: "4 2" },
      markerEnd: { type: MarkerType.ArrowClosed, color: serviceColor(agg.service) },
    })
    y += 80
  }

  return { nodes, edges }
}

interface AggregatedHop {
  id: string
  service: string
  operation: string
  count: number
}

interface AggregationResult {
  kept: TraceEntry["hops"] extends (infer H)[] | undefined ? (H & { id: string; service: string; operation: string })[] : never
  aggregates: AggregatedHop[]
}

function aggregateHops(
  hops: NonNullable<TraceEntry["hops"]>,
  threshold: number,
): { kept: typeof hops; aggregates: AggregatedHop[] } {
  const groups = new Map<string, typeof hops>()
  const kept: typeof hops = []

  for (const h of hops) {
    if (h.noisy) {
      const key = `${h.callerService}/${h.service}/${h.operation}`
      const group = groups.get(key) ?? []
      group.push(h)
      groups.set(key, group)
    } else {
      kept.push(h)
    }
  }

  const aggregates: AggregatedHop[] = []
  for (const [, group] of groups) {
    if (group.length <= threshold) {
      kept.push(...group)
    } else {
      aggregates.push({
        id: `agg-${group[0].service}-${group[0].operation}`,
        service: group[0].service,
        operation: group[0].operation,
        count: group.length,
      })
    }
  }

  return { kept, aggregates }
}

function serviceColor(service: string): string {
  const colors: Record<string, string> = {
    cloudformation: "#f59e0b",
    lambda: "#f97316",
    sqs: "#8b5cf6",
    sns: "#ec4899",
    s3: "#22c55e",
    dynamodb: "#3b82f6",
    iam: "#ef4444",
    ecs: "#06b6d4",
    ec2: "#f97316",
    kms: "#a855f7",
    ssm: "#6366f1",
    logs: "#14b8a6",
    stepfunctions: "#84cc16",
    events: "#e11d48",
    secretsmanager: "#d946ef",
    apigateway: "#0ea5e9",
    appsync: "#f43f5e",
    cognito: "#8b5cf6",
    waf: "#e11d48",
    cloudfront: "#06b6d4",
    pipes: "#f59e0b",
    kinesis: "#3b82f6",
  }
  return colors[service] ?? "var(--color-accent)"
}
