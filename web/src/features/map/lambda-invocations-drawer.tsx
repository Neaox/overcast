/**
 * lambda-invocations-drawer — Drawer showing recent invocations for a Lambda instance.
 * Click on an invocation to see timing and other metadata.
 */
import { useEffect, useMemo, useState } from "react"
import * as Dialog from "@radix-ui/react-dialog"
import { useInfiniteQuery } from "@tanstack/react-query"
import { X } from "lucide-react"
import { LogViewer } from "@/components/logs/log-viewer"
import { cn } from "@/lib/utils"
import { logs } from "@/services/api/logs"

export interface Invocation {
  /** Unique key for this invocation (timestampMs) */
  key: string
  /** ISO timestamp when the invocation started */
  acquiredAt: string
  /** ISO timestamp when the invocation finished; undefined if still running */
  releasedAt?: string
  /** Duration in ms; undefined if still running */
  durationMs?: number
  /** Explicit invocation outcome status emitted by backend once finished */
  outcomeStatus?: "succeeded" | "failed"
  /** Optional failure reason emitted by backend */
  outcomeReason?: string
  /** Instance ID that processed this invocation */
  instanceId?: string
  /** Trigger payload captured for this invocation */
  triggerEvent?: string
  /** Log group/stream for this invocation */
  logGroup?: string
  logStream?: string
}

interface LambdaInvocationsDrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  invocations: Invocation[]
  functionName: string
  instanceId?: string
}

function fmtTime(iso: string): string {
  try {
    const d = new Date(iso)
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })
  } catch {
    return "??:??:??"
  }
}

function fmtDuration(ms: number | undefined): string {
  if (ms == null) return "running"
  if (ms < 1000) return `${Math.max(1, Math.round(ms))}ms`
  const s = ms / 1000
  if (s < 10) return `${s.toFixed(1)}s`
  return `${Math.round(s)}s`
}

export function LambdaInvocationsDrawer({
  open,
  onOpenChange,
  invocations,
  functionName,
  instanceId,
}: LambdaInvocationsDrawerProps) {
  const [nowMs, setNowMs] = useState(() => Date.now())
  const [selectedKey, setSelectedKey] = useState<string | null>(
    invocations.length > 0 ? invocations[0].key : null,
  )

  useEffect(() => {
    if (invocations.length === 0) {
      if (selectedKey !== null) setSelectedKey(null)
      return
    }
    if (!selectedKey || !invocations.some((inv) => inv.key === selectedKey)) {
      setSelectedKey(invocations[0].key)
    }
  }, [invocations, selectedKey])

  const selected = invocations.find((inv) => inv.key === selectedKey)
  const selectedLogGroup = selected?.logGroup
  const selectedLogStream = selected?.logStream
  const selectedTriggerEvent = selected?.triggerEvent
  const selectedStartMs = selected ? Date.parse(selected.acquiredAt) : NaN
  const selectedEndMs = selected?.releasedAt ? Date.parse(selected.releasedAt) : undefined
  const queryEndMs = selectedEndMs != null ? selectedEndMs + 1 : undefined
  const selectedRunning = Boolean(selected && selectedEndMs == null)
  const anyRunning = useMemo(() => invocations.some((inv) => !inv.releasedAt), [invocations])

  useEffect(() => {
    if (!open || !anyRunning) return
    const id = setInterval(() => setNowMs(Date.now()), 1000)
    return () => clearInterval(id)
  }, [open, anyRunning])

  const logsQuery = useInfiniteQuery({
    queryKey: [
      "lambda-invocation-logs",
      selectedLogGroup,
      selectedLogStream,
      selected?.key,
      Number.isFinite(selectedStartMs) ? selectedStartMs : "invalid",
      queryEndMs ?? "running",
    ],
    queryFn: ({ pageParam }: { pageParam: string | undefined }) =>
      logs.getEvents(selectedLogGroup!, selectedLogStream!, {
        startTime: selectedStartMs,
        ...(queryEndMs != null ? { endTime: queryEndMs } : {}),
        limit: 200,
        ...(pageParam != null ? { nextToken: pageParam } : { startFromHead: true }),
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage, _allPages, lastPageParam) => {
      const token = lastPage.nextForwardToken
      return !token || token === lastPageParam ? undefined : token
    },
    enabled: Boolean(
      selected &&
      selectedLogGroup &&
      selectedLogStream &&
      Number.isFinite(selectedStartMs) &&
      (queryEndMs == null || Number.isFinite(queryEndMs)) &&
      open,
    ),
    refetchInterval: selectedRunning && open ? 1000 : false,
    refetchIntervalInBackground: false,
    staleTime: 30_000,
  })

  const logEvents = useMemo(() => {
    const events = logsQuery.data?.pages.flatMap((page) => page.events) ?? []
    if (!Number.isFinite(selectedStartMs)) return []
    return events
      .filter((event) => {
        const ts = event.timestamp
        if (ts == null) return false
        if (ts < selectedStartMs) return false
        if (selectedEndMs != null && ts > selectedEndMs) return false
        return true
      })
      .sort((a, b) => (a.timestamp ?? 0) - (b.timestamp ?? 0))
  }, [logsQuery.data, selectedStartMs, selectedEndMs])

  const selectedDurationMs = useMemo(() => {
    if (!Number.isFinite(selectedStartMs)) return undefined
    const end = selectedEndMs ?? nowMs
    if (!Number.isFinite(end) || end < selectedStartMs) return undefined
    return end - selectedStartMs
  }, [selectedStartMs, selectedEndMs, nowMs])

  const selectedStatus = useMemo(() => {
    if (!selected) return "running" as const
    if (!selected.releasedAt) return "running" as const
    if (selected.outcomeStatus === "failed") return "failed" as const
    if (selected.outcomeStatus === "succeeded") return "completed" as const
    return "unknown" as const
  }, [selected])

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay
          className="fixed inset-0 z-40 bg-black/30"
          onClick={() => onOpenChange(false)}
        />
        <Dialog.Content
          className={cn(
            "nodrag nopan pointer-events-auto fixed z-50 h-full max-h-screen w-225",
            "top-0 right-0 flex flex-col rounded-l-lg border-l border-border",
            "bg-bg-elevated shadow-lg",
          )}
          onOpenAutoFocus={(e) => e.preventDefault()}
        >
          {/* Header */}
          <div className="flex items-start justify-between border-b border-border bg-linear-to-r from-bg-elevated to-bg-elevated/80 px-4 py-4">
            <div>
              <h2 className="font-semibold text-fg">{functionName}</h2>
              {instanceId && (
                <p className="mt-1 font-mono text-[10px] text-fg-muted">{instanceId}</p>
              )}
              <p className="mt-2 text-[10px] font-medium tracking-wide text-fg-muted uppercase">
                {invocations.length} {invocations.length === 1 ? "Invocation" : "Invocations"}
              </p>
            </div>
            <Dialog.Close asChild>
              <button
                type="button"
                className="shrink-0 rounded p-1 text-fg-muted hover:bg-fg-muted/10"
                title="Close"
              >
                <X className="h-4 w-4" />
              </button>
            </Dialog.Close>
          </div>

          <div className="flex flex-1 overflow-hidden">
            {/* Invocations list */}
            <div className="w-40 shrink-0 overflow-y-auto border-r border-border">
              {invocations.length === 0 ? (
                <div className="flex h-full items-center justify-center">
                  <p className="text-[11px] text-fg-muted">No invocations yet</p>
                </div>
              ) : (
                <div>
                  {invocations.map((inv, idx) =>
                    (() => {
                      const rowStatus = !inv.releasedAt
                        ? "running"
                        : inv.outcomeStatus === "failed"
                          ? "failed"
                          : inv.outcomeStatus === "succeeded"
                            ? "completed"
                            : "unknown"
                      return (
                        <button
                          key={inv.key}
                          type="button"
                          onClick={() => setSelectedKey(inv.key)}
                          className={cn(
                            "nodrag nopan pointer-events-auto w-full border-b border-border px-3 py-3 text-left transition-colors",
                            "hover:bg-accent/5 active:bg-accent/10",
                            selectedKey === inv.key
                              ? "border-accent/50 bg-accent/15"
                              : idx === 0
                                ? "bg-bg-elevated"
                                : "bg-bg",
                          )}
                        >
                          <div className="flex items-baseline justify-between gap-2">
                            <div className="font-mono text-[10px] font-semibold text-fg-subtle">
                              {fmtTime(inv.acquiredAt)}
                            </div>
                            <div
                              className={cn(
                                "text-[9px] font-medium tracking-wide uppercase",
                                inv.durationMs
                                  ? inv.durationMs > 5000
                                    ? "text-yellow-400"
                                    : "text-emerald-400"
                                  : "text-fg-muted",
                              )}
                            >
                              {fmtDuration(
                                inv.releasedAt
                                  ? inv.durationMs
                                  : Math.max(0, nowMs - Date.parse(inv.acquiredAt)),
                              )}
                            </div>
                          </div>
                          <div className="mt-1 text-[9px] text-fg-muted">
                            {rowStatus === "running"
                              ? "Running"
                              : rowStatus === "failed"
                                ? "Failed"
                                : rowStatus === "completed"
                                  ? "Completed"
                                  : "Unknown"}
                          </div>
                          {inv.instanceId && (
                            <div className="mt-0.5 font-mono text-[8px] text-fg-muted/80">
                              {inv.instanceId.slice(0, 8)}
                            </div>
                          )}
                        </button>
                      )
                    })(),
                  )}
                </div>
              )}
            </div>

            {/* Detail panel */}
            {selected && (
              <div className="flex-1 overflow-y-auto border-l border-border bg-bg px-3 py-2">
                <div className="space-y-3 text-[10px]">
                  <div>
                    <div className="font-semibold text-fg-muted uppercase">Started</div>
                    <div className="mt-1 font-mono text-fg">{fmtTime(selected.acquiredAt)}</div>
                  </div>
                  <div>
                    <div className="font-semibold text-fg-muted uppercase">Ended</div>
                    <div className="mt-1 font-mono text-fg">
                      {selected.releasedAt ? fmtTime(selected.releasedAt) : "-"}
                    </div>
                  </div>
                  <div>
                    <div className="font-semibold text-fg-muted uppercase">Duration</div>
                    <div className="mt-1 font-mono text-fg">{fmtDuration(selectedDurationMs)}</div>
                  </div>
                  <div>
                    <div className="font-semibold text-fg-muted uppercase">State</div>
                    <div
                      className={cn(
                        "mt-1 font-mono",
                        selectedStatus === "running"
                          ? "text-fg"
                          : selectedStatus === "failed"
                            ? "text-red-400"
                            : selectedStatus === "completed"
                              ? "text-emerald-400"
                              : "text-fg-muted",
                      )}
                    >
                      {selectedStatus === "running"
                        ? "Running"
                        : selectedStatus === "failed"
                          ? "Failed"
                          : selectedStatus === "completed"
                            ? "Completed"
                            : "Unknown"}
                    </div>
                  </div>
                  {selectedTriggerEvent && (
                    <div>
                      <div className="font-semibold text-fg-muted uppercase">Trigger Event</div>
                      <div className="mt-1 max-h-96 overflow-auto rounded bg-bg-elevated p-2 font-mono text-[9px] text-fg-muted">
                        {(() => {
                          try {
                            let value = selectedTriggerEvent
                            // Try to decode base64 if it looks encoded
                            if (
                              typeof value === "string" &&
                              /^[A-Za-z0-9+/=]+$/.test(value) &&
                              value.length > 20
                            ) {
                              try {
                                value = atob(value)
                              } catch {
                                // Not base64, continue
                              }
                            }
                            const obj = typeof value === "string" ? JSON.parse(value) : value
                            return (
                              <pre className="wrap-break-word whitespace-pre-wrap">
                                {JSON.stringify(obj, null, 2)}
                              </pre>
                            )
                          } catch {
                            return (
                              <pre className="wrap-break-word whitespace-pre-wrap">
                                {String(selectedTriggerEvent)}
                              </pre>
                            )
                          }
                        })()}
                      </div>
                    </div>
                  )}
                  <div>
                    <div className="font-semibold text-fg-muted uppercase">Logs</div>
                    {Number.isFinite(selectedStartMs) && (
                      <>
                        <div className="mt-1 font-mono text-[9px] text-fg-muted">
                          emitted window: {new Date(selectedStartMs).toISOString()}
                          {selectedEndMs != null
                            ? ` -> ${new Date(selectedEndMs).toISOString()}`
                            : " -> running"}
                        </div>
                        {selected.outcomeStatus === "failed" && selected.outcomeReason && (
                          <div className="mt-0.5 font-mono text-[9px] text-red-400/90">
                            reason: {selected.outcomeReason}
                          </div>
                        )}
                      </>
                    )}
                    <div className="mt-1 max-h-96">
                      {selectedLogGroup && selectedLogStream ? (
                        <LogViewer
                          events={logEvents}
                          loading={logsQuery.isLoading}
                          error={
                            logsQuery.isError
                              ? "Failed to load logs for this emitted timestamp window"
                              : !Number.isFinite(selectedStartMs)
                                ? "Invalid invocation start timestamp"
                                : null
                          }
                          emptyMessage={
                            selectedStatus === "running"
                              ? "No logs emitted yet for this running invocation"
                              : "No logs found in emitted timestamp window"
                          }
                          hasMore={Boolean(logsQuery.hasNextPage)}
                          isFetchingMore={logsQuery.isFetchingNextPage}
                          onLoadMore={() => void logsQuery.fetchNextPage()}
                          defaultMode="plain"
                        />
                      ) : (
                        <div className="rounded bg-bg-elevated p-3 text-[10px] text-fg-muted">
                          No log stream is attached to this invocation.
                        </div>
                      )}
                    </div>
                  </div>
                </div>
              </div>
            )}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
