/**
 * topology-nodes — custom React Flow node for a single AWS resource.
 *
 * Renders service icon + resource label + a small event-count badge that
 * pulses when events flow through the node.
 */

import { memo, useEffect, useRef, useState, useMemo, useCallback } from "react"
import { Handle, Position, type NodeProps, useNodeId } from "@xyflow/react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery, queryOptions } from "@tanstack/react-query"
import { useVirtualizer } from "@tanstack/react-virtual"
import { Box, Zap, Send, Play, Clock, Copy } from "lucide-react"
import { SendMessageDialog } from "@/features/sqs/components/send-message"
import { PublishMessageDialog } from "@/features/sns/components/publish-dialog"
import { LambdaInvokeDialog } from "@/features/lambda/components/lambda-invoke-dialog"
import { LambdaInvocationsDrawer, type Invocation } from "./lambda-invocations-drawer"
import type { LogStreamTarget } from "./log-stream-peek"
import { logsStreamsQueryOptions } from "@/features/cloudwatch/logs/data"
import type { LogStream } from "@/types"
import { cn } from "@/lib/utils"
import { SERVICES } from "@/lib/service-registry"
import { sqs } from "@/services/api"
import type { SQSMessage } from "@/types"
import { useGhostTracker } from "@/hooks/use-ghost-tracker"
import { useEventStream } from "@/hooks/use-event-stream"
import { EventType } from "@/services/event-types"
import { useSqsEventMessages } from "./use-sqs-event-messages"
import {
  computeSqsVisualMessages,
  createSqsVisualMessagesState,
  isInflight,
  sqsVisualMessagesStateEqual,
  type DisplayMessage,
} from "./sqs-visual-messages"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { useEndpoint } from "@/hooks/use-endpoint"
import { endpointStore } from "@/services/endpoint-store"
import { SERVICE_THEME, hexToSweep } from "./map-theme"
import "./map-animations.css"
import type { FileRoutesByTo } from "@/routeTree.gen"
import { Tooltip } from "@/components/ui/tooltip"

interface NodeRoute {
  to: keyof FileRoutesByTo
  params?: Record<string, string>
  search?: Record<string, string>
}

function routeHref(route: NodeRoute, search?: Record<string, string | undefined>): string {
  let href = route.to as string
  for (const [key, value] of Object.entries(route.params ?? {})) {
    href = href.replace(`$${key}`, encodeURIComponent(value))
  }
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries({ ...(route.search ?? {}), ...(search ?? {}) })) {
    if (value) params.set(key, value)
  }
  const query = params.toString()
  return query ? `${href}?${query}` : href
}

function openRouteInNewTab(route: NodeRoute, search?: Record<string, string | undefined>) {
  window.open(routeHref(route, search), "_blank", "noopener,noreferrer")
}

/**
 * Returns the deepest available route for a given service+resource name,
 * or null if there is no per-resource page.
 */
function nodeRoute(
  service: string,
  label: string,
  nodeId?: string,
  protocolType?: string,
): NodeRoute | null {
  switch (service) {
    case "s3":
      return { to: "/s3/$bucket", params: { bucket: label } }
    case "sqs":
      return { to: "/sqs/$queue", params: { queue: label } }
    case "dynamodb":
      return { to: "/dynamodb/$tableName", params: { tableName: label } }
    case "sns":
      return { to: "/sns/$topic", params: { topic: label } }
    case "lambda":
      return { to: "/lambda/$name", params: { name: label } }
    case "logs":
      return { to: "/cloudwatch/logs/group" as const, search: { groupName: label } }
    case "ecs":
      return { to: "/ecs/$cluster", params: { cluster: label } }
    case "ecr":
      return { to: "/ecr/$repositoryName", params: { repositoryName: label } }
    case "ec2":
      return { to: "/ec2/$instanceId", params: { instanceId: label } }
    case "rds":
      return { to: "/rds/$instance", params: { instance: label } }
    case "apigateway": {
      // Node ID format: "region::apigateway::apiId" — extract the API ID.
      const apiId = nodeId?.split("::")[2]
      if (!apiId) return { to: "/apigateway" }
      if (protocolType === "REST") {
        return { to: "/apigateway/rest/$apiId", params: { apiId } }
      }
      return { to: "/apigateway/http/$apiId", params: { apiId } }
    }
    case "appsync": {
      // Node ID format: "region::appsync::apiId" — extract the API ID.
      const apiId = nodeId?.split("::")[2]
      return apiId ? { to: "/appsync/$apiId", params: { apiId } } : { to: "/appsync" }
    }
    case "cognito": {
      // Node ID format: "region::cognito::poolId" — extract the pool ID.
      const poolId = nodeId?.split("::")[2]
      return poolId ? { to: "/cognito/$poolId", params: { poolId } } : { to: "/cognito" }
    }
    case "msk":
      return { to: "/msk" }
    default:
      return null
  }
}

export interface ServiceNodeData extends Record<string, unknown> {
  service: string
  label: string
  /** AWS region this resource belongs to */
  region?: string
  streamEnabled?: boolean
  /** Total events routed through this node since mount — drives badge + ring pulse */
  eventCount?: number
  /** Data-write events (PutObject, PutItem, etc.) — drives the sweep flash */
  writeCount?: number
  /**
   * Draining write-burst count (S3/DynamoDB only) — shown as a badge and drains
   * 1 per ~2 s so rapid writes remain visible after the sweep flash fades.
   */
  writeBurstCount?: number
  /** True only on the first render after a new node appears — triggers pop-in animation */
  isNew?: boolean
  /** SQS only — visible message count */
  approximateNumberOfMessages?: number
  /** SQS only — in-flight message count */
  approximateNumberOfMessagesNotVisible?: number
  /** CloudWatch Logs only — callback fired when user clicks a stream row */
  onPeekStream?: (target: LogStreamTarget) => void
  /** Resource status string (RDS: "available", "stopped", etc.) */
  status?: string
  /** API Gateway only — REST or HTTP */
  protocolType?: string
  /** API Gateway only — number of routes/resources */
  routeCount?: number
  /** API Gateway only — number of deployed stages */
  stageCount?: number
  /** AppSync only — authentication type */
  authenticationType?: string
  /** AppSync only — number of data sources */
  dataSourceCount?: number
  /** AppSync only — number of resolvers */
  resolverCount?: number
  /** ECR only — full push-ready repository URI for copy-to-clipboard action. */
  repositoryUri?: string
  hasTarget?: boolean
  hasSource?: boolean
}

// How long the "pulse" ring stays visible after an event (ms)
const PULSE_TTL = 1500

// How long the inbound sweep flash lasts (ms) — 2 s so rapid writes aren’t invisible
const FLASH_TTL = 2_000

/** Height (px) of the scrollable message list section inside an SQS node. */
const SQS_NODE_LIST_H = 128
/** Estimated row height (px) for the message virtualizer. */
const SQS_MSG_ROW_H = 28
/**
 * Total node height (px) when the message list is visible.
 * Exported so map-page.tsx can pass this as a size override to dagre.
 */
export const SQS_NODE_EXPANDED_H = 242

/** Height (px) of the scrollable stream list inside a CloudWatch Logs node. */
const LOGS_NODE_LIST_H = 112
/** Row height (px) for each stream row. */
const LOGS_STREAM_ROW_H = 28
/**
 * Total height (px) for expanded CloudWatch Logs nodes.
 * Exported so map-page.tsx can pass this as a size override to dagre.
 */
export const LOGS_NODE_EXPANDED_H = 212

/** How recently a stream must have been written to show the active dot (ms). */
const LOGS_ACTIVITY_TTL = 30_000

// How long the message-movement dot stays visible (ms)
const MSG_ANIM_TTL = 900

/**
 * SqsStatsBar — small stats row shown inside SQS queue nodes.
 *
 * Renders "N visible" and "M in-flight" pills and animates a dot
 * when messages move between states:
 *   visible → in-flight  : dot slides →  (message received)
 *   in-flight → visible  : dot slides ←  (visibility timeout elapsed)
 *   in-flight disappears : dot fades     (message deleted)
 */
const SqsStatsBar = memo(function SqsStatsBar({
  visible,
  inFlight,
}: {
  visible: number
  inFlight: number
}) {
  const prevVisible = useRef(visible)
  const prevInFlight = useRef(inFlight)

  // "direction" drives the CSS animation:
  //   "right"  = message moved to in-flight
  //   "left"   = message returned to visible
  //   "delete" = message was deleted
  //   null     = idle
  const [direction, setDirection] = useState<"right" | "left" | "delete" | null>(null)

  useEffect(() => {
    const dv = visible - prevVisible.current
    const df = inFlight - prevInFlight.current
    prevVisible.current = visible
    prevInFlight.current = inFlight

    if (df === 0) return

    let next: "right" | "left" | "delete" | null = null
    if (df > 0) {
      // in-flight increased → received
      next = "right"
    } else if (df < 0 && dv > 0) {
      // in-flight decreased, visible increased → VT expired, returned
      next = "left"
    } else if (df < 0) {
      // in-flight decreased, visible unchanged/decreased → deleted
      next = "delete"
    }

    if (!next) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setDirection(next)
    const t = setTimeout(() => setDirection(null), MSG_ANIM_TTL)
    return () => clearTimeout(t)
  }, [visible, inFlight])

  const dotStyle: React.CSSProperties | undefined = direction
    ? {
        position: "absolute" as const,
        top: "50%",
        marginTop: "-3px",
        width: 6,
        height: 6,
        borderRadius: "50%",
        background: direction === "delete" ? "#f87171" : "#facc15",
        animation: `${
          direction === "right"
            ? "overcastMsgRight"
            : direction === "left"
              ? "overcastMsgLeft"
              : "overcastMsgFade"
        } ${MSG_ANIM_TTL}ms ease-in-out forwards`,
      }
    : undefined

  return (
    <div className="mt-2 flex items-center gap-2 text-xs font-semibold tabular-nums">
      {/* Visible pill */}
      <span
        className={cn(
          "flex items-center gap-0.5 rounded px-1.5 py-0.5",
          visible > 0 ? "bg-emerald-600 text-white" : "bg-fg-muted/15 text-fg-muted",
        )}
        title="Visible messages"
      >
        &#8595;{visible}
      </span>

      {/* Animation lane */}
      <div className="relative flex h-3 flex-1 items-center overflow-visible">
        <div className="h-px w-full bg-fg-muted/20" />
        {dotStyle && <span aria-hidden style={dotStyle} />}
      </div>

      {/* In-flight pill */}
      <span
        className={cn(
          "flex items-center gap-0.5 rounded px-1.5 py-0.5",
          inFlight > 0 ? "bg-orange-600 text-white" : "bg-fg-muted/15 text-fg-muted",
        )}
        title="In-flight messages (received, not yet deleted)"
      >
        &#8599;{inFlight}
      </span>
    </div>
  )
})

// ─── SqsMessageList ──────────────────────────────────────────────────────────

/**
 * Virtualized list of messages stored in an SQS queue, shown inside the node.
 * Uses the peek endpoint — zero state mutation, no visibility timeout applied.
 */

/** How long a deleted message lingers as a ghost before being removed (ms). */
const GHOST_TTL = 60_000
/** Ghost rows start fading this many ms before expiry. */
const GHOST_FADE_START = 30_000
function useSqsVisualMessages(
  queueName: string,
  liveMessages: SQSMessage[],
  sqsEvents: Array<{
    type: string
    time: string
    payload: unknown
  }>,
) {
  const [nowMs, setNowMs] = useState(() => Date.now())
  const ghostSource = [...liveMessages]
  const ghosts = useGhostTracker({
    items: ghostSource,
    getKey: (m) => m.messageId,
    ttl: GHOST_TTL,
  })
  const [visualState, setVisualState] = useState(createSqsVisualMessagesState)
  const visualResult = useMemo(
    () =>
      computeSqsVisualMessages({
        queueName,
        liveMessages,
        ghosts,
        sqsEvents,
        nowMs,
        state: visualState,
      }),
    [queueName, liveMessages, ghosts, sqsEvents, nowMs, visualState],
  )

  if (!sqsVisualMessagesStateEqual(visualState, visualResult.state)) {
    setVisualState(visualResult.state)
  }

  const messages = visualResult.messages

  useEffect(() => {
    if (!visualResult.needsClock) return
    const id = setInterval(() => setNowMs(Date.now()), 250)
    return () => clearInterval(id)
  }, [visualResult.needsClock])

  const visibleCount = useMemo(
    () => messages.filter((m) => !m.isGhost && m.visualPhase === "visible").length,
    [messages],
  )
  const inFlightCount = useMemo(
    () =>
      messages.filter((m) => m.visualPhase === "inflight" || m.visualPhase === "delayed").length,
    [messages],
  )

  return { messages, visibleCount, inFlightCount }
}

const SqsMessageList = memo(function SqsMessageList({
  messages,
  onSelect,
}: {
  messages: DisplayMessage[]
  onSelect: (msg: SQSMessage) => void
}) {
  "use no memo"
  // Track when each inflight window started, keyed by "messageId:visibleAfter".
  // Used to compute the countdown bar width without requiring extra server data.
  const inflightStart = useRef<Map<string, number>>(new Map())

  const containerRef = useRef<HTMLDivElement>(null)

  const virtualizer = useVirtualizer({
    count: messages.length,
    getScrollElement: () => containerRef.current,
    estimateSize: () => SQS_MSG_ROW_H,
    overscan: 3,
  })

  // Prevent ReactFlow panning when the user scrolls the list.
  const stopWheel = useCallback((e: React.WheelEvent) => e.stopPropagation(), [])
  // Prevent the node-level navigation click when clicking inside the list.
  const stopClick = useCallback((e: React.MouseEvent) => e.stopPropagation(), [])

  if (messages.length === 0) return null

  return (
    <div
      ref={containerRef}
      onWheel={stopWheel}
      onClick={stopClick}
      className="mt-2 overflow-y-auto rounded border border-border/40"
      style={{ height: SQS_NODE_LIST_H }}
    >
      <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
        {virtualizer.getVirtualItems().map((row) => {
          const { msg, isGhost, deletedAt, visualPhase } = messages[row.index]
          const isDone = visualPhase === "done"
          // Ghosts fade out linearly over GHOST_FADE_START ms before expiry.
          const ghostAge = isDone && deletedAt ? Date.now() - deletedAt : 0
          const ghostOpacity = isDone
            ? Math.max(
                0.15,
                1 - Math.max(0, ghostAge - (GHOST_TTL - GHOST_FADE_START)) / GHOST_FADE_START,
              )
            : 1
          return (
            <button
              key={msg.messageId}
              type="button"
              onClick={() => !isGhost && onSelect(msg)}
              style={{
                position: "absolute",
                top: row.start,
                left: 0,
                right: 0,
                height: row.size,
                opacity: ghostOpacity,
              }}
              className={cn(
                "flex w-full items-center gap-1.5 overflow-hidden px-2 text-left text-xs transition-colors",
                isDone ? "cursor-default" : "hover:bg-bg-muted/60",
                row.index % 2 === 0 ? "bg-bg/30" : "bg-transparent",
              )}
            >
              {/* Receive count badge — shown first so it's immediately visible */}
              <span
                className={cn(
                  "shrink-0 rounded bg-fg-muted/20 px-1 py-px text-[9px] font-bold text-fg-muted tabular-nums",
                  isDone && "line-through",
                )}
                title={`Received ${msg.approximateReceiveCount} time(s)`}
              >
                &times;{msg.approximateReceiveCount}
              </span>
              {/* Status badge */}
              <span
                className={cn(
                  "inline-flex shrink-0 items-center rounded px-1 py-px text-[9px] leading-none font-bold uppercase",
                  visualPhase === "done"
                    ? "bg-red-700/70 text-white"
                    : visualPhase === "delayed"
                      ? "bg-blue-600 text-white"
                      : visualPhase === "inflight"
                        ? "bg-orange-600 text-white"
                        : "bg-emerald-600 text-white",
                )}
              >
                {visualPhase === "done"
                  ? "done"
                  : visualPhase === "delayed"
                    ? "delay"
                    : visualPhase === "inflight"
                      ? "flight"
                      : "vis"}
              </span>
              <span className={cn("truncate font-mono text-fg-subtle", isDone && "line-through")}>
                {msg.messageId}
              </span>
              {/* Countdown bar — orange for in-flight, blue for delayed (live messages only) */}
              {visualPhase !== "done" &&
                visualPhase !== "visible" &&
                msg.visibleAfter > 0 &&
                (() => {
                  const key = `${msg.messageId}:${msg.visibleAfter}`
                  if (!inflightStart.current.has(key)) {
                    inflightStart.current.set(key, Date.now())
                  }
                  const start = inflightStart.current.get(key)!
                  const total = msg.visibleAfter - start
                  const remaining = msg.visibleAfter - Date.now()
                  const pct = total > 0 ? Math.max(0, Math.min(1, remaining / total)) : 0
                  return (
                    <div
                      className={cn(
                        "pointer-events-none absolute inset-x-0 bottom-0 h-px origin-left",
                        visualPhase === "delayed" ? "bg-blue-500" : "bg-orange-500",
                      )}
                      style={{
                        transform: `scaleX(${pct})`,
                        transition: "transform 1s linear",
                      }}
                    />
                  )
                })()}
            </button>
          )
        })}
      </div>
    </div>
  )
})

// ─── LogStreamList ───────────────────────────────────────────────────────────

/**
 * Scrollable list of log streams for a CloudWatch Logs group node.
 * Streams are sorted most-recently-active first; an emerald dot marks
 * streams that had events written within the last 30 s.
 */
const LogStreamList = memo(function LogStreamList({
  groupName,
  onSelect,
}: {
  groupName: string
  onSelect: (stream: LogStream) => void
}) {
  "use no memo"
  const { data: streams = [] } = useQuery({
    ...logsStreamsQueryOptions(groupName),
    refetchInterval: 10_000,
    staleTime: 0,
  })

  const sorted = useMemo(
    () => [...streams].sort((a, b) => (b.lastIngestionTime ?? 0) - (a.lastIngestionTime ?? 0)),
    [streams],
  )

  const containerRef = useRef<HTMLDivElement>(null)
  const virtualizer = useVirtualizer({
    count: sorted.length,
    getScrollElement: () => containerRef.current,
    estimateSize: () => LOGS_STREAM_ROW_H,
    overscan: 3,
  })

  const stopWheel = useCallback((e: React.WheelEvent) => e.stopPropagation(), [])
  const stopClick = useCallback((e: React.MouseEvent) => e.stopPropagation(), [])

  if (sorted.length === 0) return null

  const now = Date.now()

  return (
    <div
      ref={containerRef}
      onWheel={stopWheel}
      onClick={stopClick}
      className="mt-2 overflow-y-auto rounded border border-border/40"
      style={{ height: LOGS_NODE_LIST_H }}
    >
      <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
        {virtualizer.getVirtualItems().map((row) => {
          const stream = sorted[row.index]
          const isActive = (stream.lastIngestionTime ?? 0) > now - LOGS_ACTIVITY_TTL
          return (
            <button
              key={stream.logStreamName}
              type="button"
              onClick={() => onSelect(stream)}
              style={{
                position: "absolute",
                top: row.start,
                left: 0,
                right: 0,
                height: row.size,
              }}
              className={cn(
                "flex w-full items-center gap-2 overflow-hidden px-2 text-left text-xs transition-colors hover:bg-bg-muted/60",
                row.index % 2 === 0 ? "bg-bg/30" : "bg-transparent",
              )}
            >
              <span
                className={cn(
                  "h-1.5 w-1.5 shrink-0 rounded-full transition-colors",
                  isActive ? "bg-emerald-400" : "bg-fg-muted/30",
                )}
                title={isActive ? "Recently active" : "No recent activity"}
              />
              <span className="truncate font-mono text-fg-subtle">
                {stream.logStreamName ?? ""}
              </span>
            </button>
          )
        })}
      </div>
    </div>
  )
})

// ─── RdsStatusBadge ──────────────────────────────────────────────────────────

/** Small coloured pill showing an RDS instance status. */
const RdsStatusBadge = memo(function RdsStatusBadge({ status }: { status: string }) {
  const colourClass =
    status === "available"
      ? "bg-emerald-600 text-white"
      : status === "stopped"
        ? "bg-fg-muted/20 text-fg-muted"
        : status === "stopping" || status === "starting"
          ? "bg-orange-600 text-white"
          : status === "deleting" || status === "failed"
            ? "bg-red-600 text-white"
            : "bg-blue-600 text-white"
  return (
    <span
      className={cn(
        "mt-0.5 inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase",
        colourClass,
      )}
    >
      {status}
    </span>
  )
})

// ─── SqsMessageModal ─────────────────────────────────────────────────────────

/** Inspect a peeked SQS message — body, attributes, system attributes. Read-only. */
function SqsMessageModal({
  msg,
  queueName,
  open,
  onClose,
}: {
  msg: SQSMessage | null
  queueName: string
  open: boolean
  onClose: () => void
}) {
  const messageId = msg?.messageId
  // Fetch the full message (with body) on-demand when the modal opens.
  const { data: fullMsg, isLoading } = useQuery(
    queryOptions({
      queryKey: ["sqs", "message-detail", queueName, messageId],
      queryFn: async () => {
        if (!messageId) return null
        const messages = await sqs.receiveMessages(queueName)
        return messages.find((m) => m.messageId === messageId) ?? null
      },
      enabled: open && !!messageId,
      staleTime: 0,
      gcTime: 30_000,
    }),
  )
  // Show the event-driven stub while loading, enriched message once loaded.
  // Show the event-driven stub while loading, enriched message once loaded.
  const displayMsg = fullMsg ?? msg
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onClose()
      }}
    >
      <DialogContent
        className="flex max-h-[80vh] max-w-xl flex-col overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <DialogHeader>
          <DialogTitle>SQS Message</DialogTitle>
          <p className="font-mono text-xs break-all text-fg-muted">{displayMsg?.messageId}</p>
        </DialogHeader>
        {displayMsg && (
          <>
            <div className="mb-3 flex flex-wrap gap-2 text-xs">
              <span
                className={cn(
                  "rounded px-1.5 py-0.5 text-[10px] font-bold text-white uppercase",
                  isInflight(displayMsg) ? "bg-orange-600" : "bg-emerald-600",
                )}
              >
                {isInflight(displayMsg) ? "In-flight" : "Visible"}
              </span>
              <span className="rounded bg-fg-muted/15 px-1.5 py-0.5 text-fg-muted">
                received &times;{displayMsg.approximateReceiveCount}
              </span>
              {isInflight(displayMsg) && displayMsg.visibleAfter > 0 && (
                <span className="rounded bg-fg-muted/15 px-1.5 py-0.5 text-fg-muted">
                  visible {new Date(displayMsg.visibleAfter).toLocaleTimeString()}
                </span>
              )}
            </div>
            <div className="flex-1 space-y-4 overflow-y-auto pr-1">
              {isLoading ? (
                <p className="py-4 text-center text-xs text-fg-muted">Loading message…</p>
              ) : (
                <>
                  <section>
                    <p className="mb-1 text-[10px] font-semibold tracking-wider text-fg-muted uppercase">
                      Body
                    </p>
                    <pre className="max-h-48 overflow-x-auto rounded bg-bg p-2 text-xs break-all whitespace-pre-wrap text-fg">
                      {displayMsg.body || "(empty)"}
                    </pre>
                  </section>

                  {Object.keys(displayMsg.messageAttributes).length > 0 && (
                    <section>
                      <p className="mb-1 text-[10px] font-semibold tracking-wider text-fg-muted uppercase">
                        Message Attributes
                      </p>
                      <table className="w-full text-xs">
                        <tbody>
                          {Object.entries(displayMsg.messageAttributes).map(([k, v]) => (
                            <tr key={k} className="border-b border-border/30 last:border-0">
                              <td className="w-2/5 py-1 pr-3 align-top font-mono break-all text-fg-muted">
                                {k}
                              </td>
                              <td className="py-1 font-mono break-all text-fg">{v.stringValue}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </section>
                  )}

                  {Object.keys(displayMsg.attributes).length > 0 && (
                    <section>
                      <p className="mb-1 text-[10px] font-semibold tracking-wider text-fg-muted uppercase">
                        System Attributes
                      </p>
                      <table className="w-full text-xs">
                        <tbody>
                          {Object.entries(displayMsg.attributes).map(([k, v]) => (
                            <tr key={k} className="border-b border-border/30 last:border-0">
                              <td className="w-2/5 py-1 pr-3 align-top font-mono break-all text-fg-muted">
                                {k}
                              </td>
                              <td className="py-1 font-mono break-all text-fg">{v}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </section>
                  )}
                </>
              )}
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

// ─── ServiceNode ─────────────────────────────────────────────────────────────

function areServiceNodePropsEqual(prev: NodeProps, next: NodeProps): boolean {
  if (prev.selected !== next.selected) return false
  const pd = prev.data as ServiceNodeData
  const nd = next.data as ServiceNodeData
  return (
    pd.service === nd.service &&
    pd.label === nd.label &&
    pd.region === nd.region &&
    pd.streamEnabled === nd.streamEnabled &&
    pd.eventCount === nd.eventCount &&
    pd.writeCount === nd.writeCount &&
    pd.writeBurstCount === nd.writeBurstCount &&
    pd.isNew === nd.isNew &&
    pd.approximateNumberOfMessages === nd.approximateNumberOfMessages &&
    pd.approximateNumberOfMessagesNotVisible === nd.approximateNumberOfMessagesNotVisible &&
    pd.status === nd.status &&
    pd.protocolType === nd.protocolType &&
    pd.routeCount === nd.routeCount &&
    pd.stageCount === nd.stageCount &&
    pd.authenticationType === nd.authenticationType &&
    pd.dataSourceCount === nd.dataSourceCount &&
    pd.resolverCount === nd.resolverCount &&
    pd.repositoryUri === nd.repositoryUri &&
    pd.hasTarget === nd.hasTarget &&
    pd.hasSource === nd.hasSource
  )
}

export const ServiceNode = memo(function ServiceNode({ data }: NodeProps) {
  const {
    service,
    label,
    streamEnabled,
    eventCount,
    writeCount,
    writeBurstCount,
    isNew,
    approximateNumberOfMessages,
    approximateNumberOfMessagesNotVisible,
    onPeekStream,
    status,
    protocolType,
    routeCount,
    stageCount,
    authenticationType,
    dataSourceCount,
    resolverCount,
  } = data as ServiceNodeData

  // Capture isNew at mount time only — never re-triggers on subsequent renders
  const [animated] = useState(() => !!isNew)
  // useMemo so the class string is stable after first render
  const enterClass = useMemo(() => (animated ? "overcast-node-enter" : ""), [animated])

  // Selected message for the detail modal (SQS only)
  const [selectedMsg, setSelectedMsg] = useState<SQSMessage | null>(null)

  // Action dialog state
  const [sendOpen, setSendOpen] = useState(false)
  const [publishOpen, setPublishOpen] = useState(false)
  const [testOpen, setTestOpen] = useState(false)

  const sqsMessages = useSqsEventMessages(service === "sqs" ? label : "")
  const { events: sqsEvents } = useEventStream({ source: "sqs" })

  const navigate = useNavigate()
  const endpoint = useEndpoint()
  const nodeId = useNodeId()
  const route = nodeRoute(service, label, nodeId ?? undefined, protocolType)
  const nodeRegion = (data as ServiceNodeData).region
  const handleClick = useCallback(() => {
    if (!route) return
    // Switch region first if this resource lives in a different region.
    if (nodeRegion && nodeRegion !== endpoint.region) {
      endpointStore.set({ ...endpoint, region: nodeRegion })
    }
    void navigate({ to: route.to, params: route.params, search: route.search })
  }, [navigate, route, nodeRegion, endpoint])
  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      if (!route || e.button !== 1) return
      e.preventDefault()
      e.stopPropagation()
      openRouteInNewTab(route, { region: nodeRegion ?? endpoint.region })
    },
    [route, nodeRegion, endpoint.region],
  )

  const { hasTarget, hasSource } = data as ServiceNodeData

  const meta = SERVICE_THEME[service] ?? {
    color: "text-fg-muted",
    bg: "bg-fg-muted/10",
    border: "border-fg-muted/30",
    hex: "#6b7280",
    letter: "?",
  }
  const Icon =
    (SERVICES as Record<string, (typeof SERVICES)[keyof typeof SERVICES] | undefined>)[service]
      ?.icon ?? Box
  const actionButtonClass = {
    sqs: "hover:bg-emerald-400/15 hover:text-emerald-400",
    sns: "hover:bg-orange-400/15 hover:text-orange-400",
    lambda: "hover:bg-purple-400/15 hover:text-purple-400",
  }[service]

  const {
    messages: visualMessages,
    visibleCount: liveVisibleCount,
    inFlightCount: liveInFlightCount,
  } = useSqsVisualMessages(label, sqsMessages, sqsEvents)
  const visibleCount = service === "sqs" ? liveVisibleCount : (approximateNumberOfMessages ?? 0)
  const inFlightCount =
    service === "sqs" ? liveInFlightCount : (approximateNumberOfMessagesNotVisible ?? 0)
  const totalMsgs = service === "sqs" ? visualMessages.length : visibleCount + inFlightCount
  const showMsgList = service === "sqs" && totalMsgs > 0

  return (
    <div
      role={route ? "button" : undefined}
      tabIndex={route ? 0 : undefined}
      onClick={handleClick}
      onMouseDown={handleMouseDown}
      onKeyDown={
        route
          ? (e) => {
              // React portals bubble events through the React tree, so ignore
              // key presses originating from dialog inputs rendered by this node.
              if (e.target !== e.currentTarget) return
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault()
                handleClick()
              }
            }
          : undefined
      }
      className={cn(
        "relative flex flex-col rounded-lg border px-3 py-2 shadow-sm transition-colors",
        "bg-bg-elevated text-fg",
        meta.border,
        route && "cursor-pointer hover:brightness-110",
        enterClass,
      )}
    >
      {/* Ring pulse — keyed by eventCount to re-trigger animation on each event */}
      {(eventCount ?? 0) > 0 && (
        <span
          key={eventCount}
          aria-hidden
          className={cn(
            "pointer-events-none absolute -inset-0.5 rounded-lg ring-2",
            meta.color.replace("text-", "ring-"),
          )}
          style={{ animation: `overcastPulseRing ${PULSE_TTL}ms ease-out forwards` }}
        />
      )}
      {/* Inbound-write sweep flash — keyed by writeCount to re-trigger animation */}
      {(writeCount ?? 0) > 0 && (
        <span
          key={writeCount}
          aria-hidden
          className="pointer-events-none absolute inset-0 rounded-lg"
          style={{
            background: `linear-gradient(90deg, transparent 0%, ${hexToSweep(meta.hex)} 50%, transparent 100%)`,
            animation: `overcastSweep ${FLASH_TTL}ms ease-out forwards`,
          }}
        />
      )}
      {/* Left target handle — only when this node is a connection target */}
      {hasTarget && (
        <Handle
          type="target"
          position={Position.Left}
          className="size-2! rounded-full! border-0! bg-fg-muted/50!"
        />
      )}

      {/* Header row: icon + label + stats + stream indicator */}
      <div className="flex items-center gap-2.5">
        <div
          className={cn("flex h-9 w-9 shrink-0 items-center justify-center rounded-md", meta.bg)}
        >
          <Icon className={cn("h-5 w-5", meta.color)} />
        </div>

        <div className="min-w-0 flex-1">
          <Tooltip content={<span className="break-all">{label}</span>}>
            <p className="truncate text-base leading-tight font-semibold">{label}</p>
          </Tooltip>
          {service === "sqs" ? (
            <SqsStatsBar visible={visibleCount} inFlight={inFlightCount} />
          ) : service === "rds" && status ? (
            <RdsStatusBadge status={status} />
          ) : service === "apigateway" ? (
            <div className="flex items-center gap-1.5">
              <span className="rounded bg-fg-muted/15 px-1.5 py-0.5 text-[10px] font-semibold text-fg-muted uppercase">
                {protocolType ?? "API"}
              </span>
              {routeCount != null && (
                <span className="text-xs text-fg-subtle">
                  {routeCount} {routeCount === 1 ? "route" : "routes"}
                </span>
              )}
              {stageCount != null && stageCount > 0 && (
                <span className="text-xs text-fg-subtle">
                  &middot; {stageCount} {stageCount === 1 ? "stage" : "stages"}
                </span>
              )}
            </div>
          ) : service === "appsync" ? (
            <div className="flex items-center gap-1.5">
              <span className="rounded bg-fg-muted/15 px-1.5 py-0.5 text-[10px] font-semibold text-fg-muted uppercase">
                {authenticationType ?? "GraphQL"}
              </span>
              {dataSourceCount != null && (
                <span className="text-xs text-fg-subtle">
                  {dataSourceCount} {dataSourceCount === 1 ? "source" : "sources"}
                </span>
              )}
              {resolverCount != null && resolverCount > 0 && (
                <span className="text-xs text-fg-subtle">
                  &middot; {resolverCount} {resolverCount === 1 ? "resolver" : "resolvers"}
                </span>
              )}
            </div>
          ) : (
            <p className="text-sm leading-tight text-fg-subtle capitalize">{service}</p>
          )}
        </div>

        {/* Right side: stream indicator + action button */}
        <div className="flex shrink-0 flex-col items-center gap-1">
          {streamEnabled && (
            <div className="h-1.5 w-1.5 rounded-full bg-blue-400" title="Streams enabled" />
          )}
          {(service === "sqs" || service === "sns" || service === "lambda") && (
            <button
              type="button"
              onKeyDown={(e) => e.stopPropagation()}
              onClick={(e) => {
                e.stopPropagation()
                if (service === "sqs") setSendOpen(true)
                else if (service === "sns") setPublishOpen(true)
                else setTestOpen(true)
              }}
              className={cn(
                "flex h-5 w-5 items-center justify-center rounded text-fg-muted transition-colors",
                actionButtonClass,
              )}
              title={
                service === "lambda"
                  ? "Test function"
                  : service === "sns"
                    ? "Publish message"
                    : "Send message"
              }
            >
              {service === "lambda" ? <Play className="h-3 w-3" /> : <Send className="h-3 w-3" />}
            </button>
          )}
          {service === "ecr" && (data as ServiceNodeData).repositoryUri && (
            <button
              type="button"
              onKeyDown={(e) => e.stopPropagation()}
              onClick={(e) => {
                e.stopPropagation()
                void navigator.clipboard.writeText((data as ServiceNodeData).repositoryUri!)
              }}
              className="flex h-5 w-5 items-center justify-center rounded text-fg-muted transition-colors hover:bg-cyan-400/15 hover:text-cyan-400"
              title="Copy repository URI"
            >
              <Copy className="h-3 w-3" />
            </button>
          )}
        </div>
      </div>

      {/* Message list — SQS only, shown when queue has messages */}
      {showMsgList && (
        <SqsMessageList messages={visualMessages} onSelect={(msg) => setSelectedMsg(msg)} />
      )}

      {/* Stream list — CloudWatch Logs only */}
      {service === "logs" && (
        <LogStreamList
          groupName={label}
          onSelect={(stream) =>
            onPeekStream?.({
              title: label,
              subtitle: stream.logStreamName ?? "",
              logGroup: label,
              logStream: stream.logStreamName ?? "",
            })
          }
        />
      )}

      {/* Event counter badge */}
      {(eventCount ?? 0) > 0 && (
        <span
          className={cn(
            "absolute -top-1.5 -right-1.5 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[9px] font-bold tabular-nums",
            "bg-accent text-fg-on-accent",
          )}
        >
          {(eventCount ?? 0) > 99 ? "99+" : eventCount}
        </span>
      )}

      {/* Write-burst badge (S3 / DynamoDB) — drains over time so rapid writes stay visible */}
      {(writeBurstCount ?? 0) > 1 && (
        <span
          className={cn(
            "absolute -right-1.5 -bottom-1.5 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[9px] font-bold tabular-nums",
            meta.color.replace("text-", "bg-").replace("-400", "-600"),
            "text-white",
          )}
          title={`${writeBurstCount} recent writes`}
        >
          ×{writeBurstCount}
        </span>
      )}

      {/* Right source handle — only when this node has outgoing connections */}
      {hasSource && (
        <Handle
          type="source"
          position={Position.Right}
          className="size-2! rounded-full! border-0! bg-fg-muted/50!"
        />
      )}

      {/* Message detail modal — rendered via Radix portal, DOM position doesn't matter */}
      {service === "sqs" && (
        <SqsMessageModal
          msg={selectedMsg}
          queueName={label}
          open={selectedMsg !== null}
          onClose={() => setSelectedMsg(null)}
        />
      )}

      {/* Action dialogs — portaled outside the node DOM tree */}
      {service === "sqs" && (
        <SendMessageDialog
          queueName={label}
          isFifo={label.endsWith(".fifo")}
          open={sendOpen}
          onClose={() => setSendOpen(false)}
        />
      )}
      {service === "sns" && (
        <PublishMessageDialog topicName={label} open={publishOpen} onOpenChange={setPublishOpen} />
      )}
      {service === "lambda" && (
        <LambdaInvokeDialog name={label} open={testOpen} onOpenChange={setTestOpen} />
      )}
    </div>
  )
}, areServiceNodePropsEqual)

// ─── LambdaGroupNode ────────────────────────────────────────────────────────

export const LAMBDA_GROUP_HEADER_H = 56 // px — must match map-page.tsx constant

export interface LambdaGroupNodeData extends Record<string, unknown> {
  label: string
  instanceCount: number
  eventCount?: number
  onPeek?: (target: LogStreamTarget) => void
}

interface LambdaInstanceEventPayload {
  instanceId: string
  functionName: string
  status: "running" | "idle"
  startedAt: number
  lastUsed: number
  expiresAt: number
  logGroup?: string
  logStream?: string
  triggerEvent?: unknown
  lastInvocationStatus?: "succeeded" | "failed"
  lastInvocationError?: string
}

function normalizeTriggerEvent(value: unknown): string | undefined {
  if (value == null) return undefined
  if (typeof value === "string") return value
  try {
    return JSON.stringify(value)
  } catch {
    return undefined
  }
}

/**
 * LambdaGroupNode — container node that hosts LambdaInstanceNode children.
 *
 * React Flow renders child nodes (those with parentId matching this node's id)
 * inside this container automatically.  We just need to provide a visible
 * header and connection handles.
 */
function areLambdaGroupPropsEqual(prev: NodeProps, next: NodeProps): boolean {
  if (prev.selected !== next.selected) return false
  const pd = prev.data as LambdaGroupNodeData
  const nd = next.data as LambdaGroupNodeData
  return (
    pd.label === nd.label &&
    pd.instanceCount === nd.instanceCount &&
    pd.eventCount === nd.eventCount
  )
}

export const LambdaGroupNode = memo(function LambdaGroupNode({ data }: NodeProps) {
  const { label, eventCount } = data as LambdaGroupNodeData
  const navigate = useNavigate()
  const [testOpen, setTestOpen] = useState(false)
  const [showInvocations, setShowInvocations] = useState(false)
  const [invocations, setInvocations] = useState<Invocation[]>([])
  const { events: lambdaEvents } = useEventStream({ source: "lambda" })
  const eventCursorRef = useRef(0)
  const activeByInstanceRef = useRef<Map<string, string[]>>(new Map())
  const invocationsRef = useRef<Map<string, Invocation>>(new Map())
  const route = useMemo<NodeRoute>(
    () => ({ to: "/lambda/$name", params: { name: label } }),
    [label],
  )
  const handleOpenFunction = useCallback(() => {
    void navigate({ to: route.to, params: route.params })
  }, [navigate, route])
  const handleFunctionMouseDown = useCallback(
    (e: React.MouseEvent) => {
      if (e.button !== 1) return
      e.preventDefault()
      e.stopPropagation()
      openRouteInNewTab(route)
    },
    [route],
  )

  useEffect(() => {
    if (lambdaEvents.length < eventCursorRef.current) eventCursorRef.current = 0

    for (let i = eventCursorRef.current; i < lambdaEvents.length; i++) {
      const ev = lambdaEvents[i]
      if (
        ev.type !== EventType.lambda.InstanceAcquired &&
        ev.type !== EventType.lambda.InstanceReleased
      ) {
        continue
      }

      const payload = ev.payload as LambdaInstanceEventPayload | undefined
      if (!payload || payload.functionName !== label) continue

      const t = Date.parse(ev.time)
      if (!Number.isFinite(t)) continue

      if (ev.type === EventType.lambda.InstanceAcquired) {
        const key = `${payload.instanceId}:${t}`
        const inv: Invocation = {
          key,
          acquiredAt: ev.time,
          instanceId: payload.instanceId,
          triggerEvent: normalizeTriggerEvent(payload.triggerEvent),
          logGroup: payload.logGroup,
          logStream: payload.logStream,
        }
        invocationsRef.current.set(key, inv)
        const list = activeByInstanceRef.current.get(payload.instanceId) ?? []
        list.push(key)
        activeByInstanceRef.current.set(payload.instanceId, list)
      } else {
        const list = activeByInstanceRef.current.get(payload.instanceId)
        const key = list?.shift()
        if (list && list.length === 0) activeByInstanceRef.current.delete(payload.instanceId)
        if (!key) continue
        const existing = invocationsRef.current.get(key)
        if (!existing) continue
        invocationsRef.current.set(key, {
          ...existing,
          releasedAt: ev.time,
          durationMs: Math.max(0, t - Date.parse(existing.acquiredAt)),
          outcomeStatus: payload.lastInvocationStatus,
          outcomeReason: payload.lastInvocationError,
          logGroup: payload.logGroup ?? existing.logGroup,
          logStream: payload.logStream ?? existing.logStream,
        })
      }
    }

    eventCursorRef.current = lambdaEvents.length

    const next = Array.from(invocationsRef.current.values()).sort(
      (a, b) => Date.parse(b.acquiredAt) - Date.parse(a.acquiredAt),
    )
    setInvocations(next)
  }, [lambdaEvents, label])

  return (
    <div
      className={cn(
        "relative flex flex-col rounded-lg border shadow-sm",
        "border-purple-400/30 bg-bg-elevated",
      )}
      style={{ width: "100%", height: "100%" }}
    >
      {/* Left target handle */}
      <Handle
        type="target"
        position={Position.Left}
        className="size-2! rounded-full! border-0! bg-fg-muted/50!"
      />

      {/* Header */}
      <div
        className="flex items-center gap-2 rounded-t-lg px-3 py-2"
        style={{ height: LAMBDA_GROUP_HEADER_H }}
      >
        <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-purple-400/10">
          <Zap className="h-5 w-5 text-purple-400" />
        </div>
        <div className="min-w-0 flex-1">
          <Tooltip content={<span className="break-all">{label}</span>}>
            <button
              type="button"
              onClick={handleOpenFunction}
              onMouseDown={handleFunctionMouseDown}
              className="block max-w-full truncate text-left text-base leading-tight font-semibold hover:text-purple-300 hover:underline"
              title={`Open ${label}`}
            >
              {label}
            </button>
          </Tooltip>
          <p className="text-sm leading-tight text-fg-subtle capitalize">lambda</p>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <button
            type="button"
            onKeyDown={(e) => e.stopPropagation()}
            onClick={(e) => {
              e.stopPropagation()
              setTestOpen(true)
            }}
            className="flex h-5 w-5 items-center justify-center rounded text-fg-muted transition-colors hover:bg-purple-400/15 hover:text-purple-400"
            title="Test function"
          >
            <Play className="h-3 w-3" />
          </button>
          <button
            type="button"
            onKeyDown={(e) => e.stopPropagation()}
            onClick={(e) => {
              e.stopPropagation()
              setShowInvocations(true)
            }}
            className="flex h-5 w-5 items-center justify-center rounded text-fg-muted transition-colors hover:bg-blue-400/15 hover:text-blue-400"
            title={`${invocations.length} invocations`}
          >
            <Clock className="h-3 w-3" />
          </button>
        </div>
      </div>

      {/* Divider */}
      <div className="mx-2 border-t border-purple-400/20" />

      {/* Child instance nodes are rendered by React Flow here — no explicit children needed */}
      <div className="flex-1" />

      {/* Event counter badge */}
      {(eventCount ?? 0) > 0 && (
        <span
          className={cn(
            "absolute -top-1.5 -right-1.5 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[9px] font-bold tabular-nums",
            "bg-accent text-fg-on-accent",
          )}
        >
          {(eventCount ?? 0) > 99 ? "99+" : eventCount}
        </span>
      )}

      {/* Right source handle */}
      <Handle
        type="source"
        position={Position.Right}
        className="size-2! rounded-full! border-0! bg-fg-muted/50!"
      />

      <LambdaInvokeDialog name={label} open={testOpen} onOpenChange={setTestOpen} />
      <LambdaInvocationsDrawer
        open={showInvocations}
        onOpenChange={setShowInvocations}
        invocations={invocations}
        functionName={label}
      />
    </div>
  )
}, areLambdaGroupPropsEqual)
