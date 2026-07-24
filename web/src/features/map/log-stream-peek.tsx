/**
 * LogStreamPeek — right-side slide-in panel for peering into a Lambda
 * instance's log stream and trigger event.
 *
 * - Logs tab: loads existing events via REST then subscribes to the global
 *   SSE stream and appends any new `logs:LogEventsWritten` events that match
 *   the instance's log group + stream.
 * - Trigger Event tab: pretty-prints the JSON payload that triggered the
 *   invocation (as recorded by the instance tracker).
 */

import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import * as Dialog from "@radix-ui/react-dialog"
import { infiniteQueryOptions, useInfiniteQuery } from "@tanstack/react-query"
import { X, FileText, Zap } from "lucide-react"
import { cn } from "@/lib/utils"
import { logs } from "@/services/api"
import type { LogEvent } from "@/types"
import { useScrollTrigger } from "@/hooks/use-scroll-trigger"
import { tailLogEvents } from "@/features/cloudwatch/logs/tail"
import { TriggerEventViewer } from "./trigger-event-viewer"

type Tab = "logs" | "trigger"

/**
 * A log stream to display in the peek panel.
 * Lambda instances produce one by converting their fields;
 * log group nodes produce one from the selected stream.
 */
export interface LogStreamTarget {
  /** Primary display label — function name, group name, etc. */
  title: string
  /** Secondary label — instance short ID, stream name, etc. */
  subtitle: string
  logGroup: string
  logStream: string
  /** Optional JSON trigger event (Lambda only). */
  triggerEvent?: string
}

function logStreamPeekQueryOptions(target: LogStreamTarget | null, enabled: boolean) {
  return infiniteQueryOptions({
    queryKey: ["logs", target?.logGroup, target?.logStream],
    queryFn: ({ pageParam }: { pageParam: string | undefined }) =>
      logs.getEvents(target!.logGroup, target!.logStream, {
        nextToken: pageParam,
        limit: 200,
        ...(pageParam == null ? { startFromHead: false } : {}),
      }),
    initialPageParam: undefined as string | undefined,
    // Each page gives us a backward token to fetch the page before it.
    getNextPageParam: (lastPage, _allPages, lastPageParam) => {
      if (lastPage.events.length === 0) return undefined
      const token = lastPage.nextBackwardToken
      return !token || token === lastPageParam ? undefined : token
    },
    enabled,
    // Disable stale refetching — SSE handles live tail updates.
    staleTime: Infinity,
  })
}

function fmtTimestamp(ms: number): string {
  const d = new Date(ms)
  const hh = String(d.getHours()).padStart(2, "0")
  const mm = String(d.getMinutes()).padStart(2, "0")
  const ss = String(d.getSeconds()).padStart(2, "0")
  const ms3 = String(d.getMilliseconds()).padStart(3, "0")
  return `${hh}:${mm}:${ss}.${ms3}`
}

interface LogStreamPeekProps {
  target: LogStreamTarget | null
  onClose: () => void
}

export const LogStreamPeek = memo(function LogStreamPeek({ target, onClose }: LogStreamPeekProps) {
  const visible = target !== null
  const [activeTab, setActiveTab] = useState<Tab>("logs")
  const [appendedEvents, setAppendedEvents] = useState<LogEvent[]>([])

  // Reset appended events and active tab when the target stream changes.
  const prevTargetKey = useRef<string | null>(null)
  const targetKey = target ? `${target.logGroup}::${target.logStream}` : null
  if (targetKey !== prevTargetKey.current) {
    prevTargetKey.current = targetKey
    if (appendedEvents.length > 0) setAppendedEvents([])
    if (!target && activeTab !== "logs") setActiveTab("logs")
  }

  // Infinite query — first page fetches the latest events (startFromHead: false);
  // subsequent pages fetch older events via nextBackwardToken.
  const logQuery = useInfiniteQuery(
    logStreamPeekQueryOptions(
      target,
      !!target && activeTab === "logs" && !!target.logGroup && !!target.logStream,
    ),
  )

  // Append new log events for the active stream. The generator unsubscribes
  // from the shared event stream when the target changes or the panel closes.
  useEffect(() => {
    if (!target || !target.logGroup || !target.logStream || activeTab !== "logs") return

    const controller = new AbortController()
    void (async () => {
      for await (const event of tailLogEvents({
        groupIdentifier: target.logGroup,
        streamName: target.logStream,
        signal: controller.signal,
      })) {
        setAppendedEvents((prev) => [
          ...prev,
          {
            timestamp: event.timestamp,
            message: event.message,
            ingestionTime: event.ingestionTime,
          },
        ])
      }
    })()

    return () => controller.abort()
  }, [target, activeTab])

  // All historical events: pages are in reverse order (newest first page),
  // so reverse them to get chronological order, then append live events.
  const historicalEvents = useMemo(
    () => [...(logQuery.data?.pages ?? [])].reverse().flatMap((p) => p.events),
    [logQuery.data],
  )
  const logEvents = useMemo(
    () => [...historicalEvents, ...appendedEvents],
    [historicalEvents, appendedEvents],
  )

  return (
    <Dialog.Root
      open={visible}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <Dialog.Portal>
        {/* Backdrop — pointer-events-none so clicking another peek-able node on the
            canvas (e.g. a different log stream) reaches that node instead of being
            swallowed here. Outside-click-to-close is still handled below via
            onInteractOutside. */}
        <Dialog.Overlay className="fixed inset-0 z-60 pointer-events-none" />

        {/* Slide-in panel */}
        <Dialog.Content
          aria-describedby={undefined}
          onEscapeKeyDown={onClose}
          onInteractOutside={(e) => {
            // Clicking another peek trigger (a lambda instance or log stream row)
            // should switch this panel to the new target, not close it — that
            // trigger's own onClick already calls onPeek with the new target.
            const target = e.detail.originalEvent.target as HTMLElement | null
            if (target?.closest("[data-peek-trigger]")) {
              e.preventDefault()
              return
            }
            onClose()
          }}
          className={cn(
            "fixed inset-y-0 right-0 z-70 flex w-120 flex-col border-l border-border bg-bg-elevated shadow-2xl",
            "transition-transform duration-300",
            "data-[state=closed]:translate-x-full data-[state=open]:translate-x-0",
          )}
        >
          <Dialog.Title className="sr-only">{target?.title ?? "Log stream"}</Dialog.Title>
          {target && (
            <>
              {/* Header */}
              <div className="flex shrink-0 items-start justify-between gap-3 border-b border-border px-4 py-3">
                <div className="min-w-0">
                  <p className="truncate text-sm font-semibold">{target.title}</p>
                  <p className="truncate font-mono text-xs text-fg-muted">{target.subtitle}</p>
                </div>
                <Dialog.Close asChild>
                  <button
                    type="button"
                    className="mt-0.5 shrink-0 rounded p-1 text-fg-muted hover:bg-fg-muted/15 hover:text-fg"
                    aria-label="Close"
                  >
                    <X className="h-4 w-4" />
                  </button>
                </Dialog.Close>
              </div>

              {/* Tabs */}
              <div className="flex shrink-0 gap-0 border-b border-border">
                <TabButton
                  active={activeTab === "logs"}
                  onClick={() => setActiveTab("logs")}
                  icon={<FileText className="h-3.5 w-3.5" />}
                  label="Logs"
                  disabled={!target.logGroup || !target.logStream}
                />
                {target.triggerEvent && (
                  <TabButton
                    active={activeTab === "trigger"}
                    onClick={() => setActiveTab("trigger")}
                    icon={<Zap className="h-3.5 w-3.5" />}
                    label="Trigger Event"
                  />
                )}
              </div>

              {/* Body */}
              <div className="min-h-0 flex-1 overflow-hidden">
                {activeTab === "logs" && (
                  <LogsPane
                    key={`${target.logGroup}::${target.logStream}`}
                    logEvents={logEvents}
                    loading={logQuery.isLoading}
                    hasStream={Boolean(target.logGroup && target.logStream)}
                    hasMore={logQuery.hasNextPage}
                    loadingMore={logQuery.isFetchingNextPage}
                    onLoadMore={() => logQuery.fetchNextPage()}
                  />
                )}
                {activeTab === "trigger" && <TriggerPane triggerEvent={target.triggerEvent} />}
              </div>
            </>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
})

function TabButton({
  active,
  onClick,
  icon,
  label,
  disabled,
}: {
  active: boolean
  onClick: () => void
  icon: React.ReactNode
  label: string
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "flex items-center gap-1.5 px-4 py-2 text-xs font-medium transition-colors",
        "-mb-px border-b-2",
        active
          ? "border-purple-400 text-purple-400"
          : "border-transparent text-fg-muted hover:text-fg",
        disabled && "cursor-not-allowed opacity-40",
      )}
    >
      {icon}
      {label}
    </button>
  )
}

function LogsPane({
  logEvents,
  loading,
  hasStream,
  hasMore,
  loadingMore,
  onLoadMore,
}: {
  logEvents: LogEvent[]
  loading: boolean
  hasStream: boolean
  hasMore: boolean
  loadingMore: boolean
  onLoadMore: () => void
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const pinnedRef = useRef(true)
  const [hasUnread, setHasUnread] = useState(false)
  const initializedRef = useRef(false)
  const prevLenRef = useRef(0)
  const prependSnapshotRef = useRef<{
    scrollHeight: number
    scrollTop: number
    itemCount: number
  } | null>(null)
  const skipUnreadRef = useRef(false)

  const scrollToBottom = useCallback((behavior: ScrollBehavior = "auto") => {
    const el = scrollRef.current
    if (!el) return
    el.scrollTo({ top: el.scrollHeight, behavior })
    pinnedRef.current = true
    setHasUnread(false)
  }, [])

  const handleLoadMore = useCallback(() => {
    const el = scrollRef.current
    if (!el || !hasMore || loadingMore) return
    prependSnapshotRef.current = {
      scrollHeight: el.scrollHeight,
      scrollTop: el.scrollTop,
      itemCount: logEvents.length,
    }
    onLoadMore()
  }, [hasMore, loadingMore, logEvents.length, onLoadMore])

  // Load older logs when the top sentinel enters view
  const topSentinelRef = useScrollTrigger({
    onTrigger: handleLoadMore,
    enabled: hasMore && !loadingMore,
    direction: "up",
    rootMargin: "120px",
  })

  const onScroll = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 8
    pinnedRef.current = atBottom
    if (atBottom) setHasUnread(false)
  }, [])

  useLayoutEffect(() => {
    const el = scrollRef.current
    if (!el || logEvents.length === 0) return

    if (!initializedRef.current) {
      initializedRef.current = true
      prevLenRef.current = logEvents.length
      el.scrollTo({ top: el.scrollHeight, behavior: "instant" })
      pinnedRef.current = true
      return
    }

    if (prependSnapshotRef.current && !loadingMore) {
      const snapshot = prependSnapshotRef.current
      prependSnapshotRef.current = null
      if (logEvents.length > snapshot.itemCount) {
        const addedHeight = el.scrollHeight - snapshot.scrollHeight
        el.scrollTop = snapshot.scrollTop + addedHeight
        skipUnreadRef.current = true
      }
      prevLenRef.current = logEvents.length
      return
    }

    if (logEvents.length <= prevLenRef.current) return

    prevLenRef.current = logEvents.length
    if (skipUnreadRef.current) {
      skipUnreadRef.current = false
      return
    }

    if (pinnedRef.current) {
      el.scrollTo({ top: el.scrollHeight, behavior: "auto" })
      pinnedRef.current = true
    } else {
      const unreadTimer = window.setTimeout(() => setHasUnread(true), 0)
      return () => window.clearTimeout(unreadTimer)
    }
  }, [logEvents.length, loadingMore])

  if (!hasStream) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-fg-muted">
        No log stream attached to this instance yet.
      </div>
    )
  }
  if (loading && logEvents.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-fg-muted">
        Loading logs…
      </div>
    )
  }
  if (!loading && logEvents.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-fg-muted">
        No log events yet.
      </div>
    )
  }
  return (
    <div className="relative flex h-full flex-col overflow-hidden">
      <div
        ref={scrollRef}
        onScroll={onScroll}
        className="min-h-0 flex-1 overflow-y-auto p-2 font-mono text-[11px] leading-relaxed"
      >
        {/* Top sentinel — triggers loading older pages when scrolled into view */}
        <div ref={topSentinelRef} />
        {loadingMore && (
          <div className="py-2 text-center text-[11px] text-fg-muted">Loading older logs…</div>
        )}
        {!loadingMore && !hasMore && (
          <div className="py-2 text-center text-[11px] text-fg-muted">No earlier logs</div>
        )}
        {logEvents.map((e, i) => (
          <div
            key={`${e.timestamp ?? i}-${(e.message ?? "").slice(0, 16)}-${i}`}
            className="flex gap-2 hover:bg-fg-muted/5"
          >
            <span className="shrink-0 text-fg-muted tabular-nums">
              {fmtTimestamp(e.timestamp ?? 0)}
            </span>
            <span className="min-w-0 wrap-break-word text-fg">{e.message}</span>
          </div>
        ))}
      </div>

      {/* "New logs" pill — visible when scrolled up and new events arrive */}
      {hasUnread && (
        <button
          type="button"
          onClick={() => scrollToBottom("smooth")}
          className="absolute bottom-3 left-1/2 -translate-x-1/2 rounded-full bg-purple-500 px-3 py-1 text-[11px] font-medium text-white shadow-lg hover:bg-purple-400"
        >
          ↓ New logs
        </button>
      )}
    </div>
  )
}

function TriggerPane({ triggerEvent }: { triggerEvent: unknown }) {
  if (!triggerEvent) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-fg-muted">
        No trigger event recorded.
      </div>
    )
  }
  return (
    <div className="m-0 p-4 font-mono text-[11px] leading-relaxed text-fg">
      <TriggerEventViewer event={triggerEvent} />
    </div>
  )
}
