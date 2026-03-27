import { useState } from "react"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import {
  MessagesSquare,
  Plus,
  Trash2,
  RefreshCw,
  Send,
  Eye,
  Flame,
  ChevronDown,
  ChevronRight,
} from "lucide-react"
import {
  sqsQueries,
  sqsKeys,
  deleteQueueMutationOptions,
  purgeQueueMutationOptions,
  deleteMessageMutationOptions,
} from "@/features/sqs/data"
import { useEndpoint } from "@/hooks/use-endpoint"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Badge } from "@/components/ui/badge"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { PageHeader, Spinner, EmptyState, CodeBlock } from "@/components/ui/primitives"
import { useToast } from "@/components/ui/toast"
import { SendMessageDialog } from "./send-message"
import type { SQSMessage } from "@/services/api"

// How long messages stay invisible when peeked from the UI (seconds).
// A short value means messages reappear quickly — the console is non-destructive.
const PEEK_VISIBILITY_TIMEOUT = 30

interface Props {
  queueName: string
}

export function QueueDetail({ queueName }: Props) {
  const { endpoint } = useEndpoint()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [showSend, setShowSend] = useState(false)
  const [deleteQueueConfirm, setDeleteQueueConfirm] = useState(false)
  const [purgeConfirm, setPurgeConfirm] = useState(false)
  const [expandedMsg, setExpandedMsg] = useState<string>()
  const [deleteTarget, setDeleteTarget] = useState<SQSMessage>()

  const {
    data: queue,
    isLoading: qLoading,
    isFetching: qFetching,
    refetch: refetchQueue,
  } = useQuery(sqsQueries.queue(endpoint.baseUrl, queueName))

  const {
    data: messages = [],
    isLoading: mLoading,
    isFetching: mFetching,
    refetch: refetchMessages,
  } = useQuery(sqsQueries.messages(endpoint.baseUrl, queueName, PEEK_VISIBILITY_TIMEOUT))

  const deleteQueueMut = useMutation({
    ...deleteQueueMutationOptions(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: sqsKeys.queues() })
      setDeleteQueueConfirm(false)
      toast({ title: "Queue deleted", description: queueName })
      navigate({ to: "/sqs" })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  const purge = useMutation({
    ...purgeQueueMutationOptions(queueName),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: sqsKeys.queue(endpoint.baseUrl, queueName) })
      qc.invalidateQueries({ queryKey: sqsKeys.messageList(endpoint.baseUrl, queueName) })
      setPurgeConfirm(false)
      toast({
        title: "Queue purged",
        description: `All messages deleted from ${queueName}`,
        variant: "success",
      })
    },
    onError: (err: Error) =>
      toast({ title: "Purge failed", description: err.message, variant: "danger" }),
  })

  const deleteMsg = useMutation({
    ...deleteMessageMutationOptions(queueName),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: sqsKeys.messageList(endpoint.baseUrl, queueName) })
      qc.invalidateQueries({ queryKey: sqsKeys.queue(endpoint.baseUrl, queueName) })
      setDeleteTarget(undefined)
      toast({ title: "Message deleted" })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  function handleRefresh() {
    refetchQueue()
    refetchMessages()
  }

  if (qLoading) {
    return (
      <div className="flex items-center justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!queue) return null

  const visibleCount = queue.approximateNumberOfMessages
  const inFlightCount = queue.approximateNumberOfMessagesNotVisible

  return (
    <div className="flex w-full max-w-screen-xl flex-col gap-6">
      <PageHeader
        title={queueName}
        description={queue.arn}
        actions={
          <>
            <Button
              size="sm"
              variant="ghost"
              onClick={handleRefresh}
              disabled={qFetching || mFetching}
            >
              <RefreshCw
                className={`mr-1.5 h-3.5 w-3.5 ${qFetching || mFetching ? "animate-spin" : ""}`}
              />
              Refresh
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setShowSend(true)}>
              <Send className="mr-1.5 h-3.5 w-3.5" />
              Send Message
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="text-warning hover:text-warning"
              onClick={() => setPurgeConfirm(true)}
            >
              <Flame className="mr-1.5 h-3.5 w-3.5" />
              Purge
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="text-danger hover:text-danger"
              onClick={() => setDeleteQueueConfirm(true)}
            >
              <Trash2 className="mr-1.5 h-3.5 w-3.5" />
              Delete Queue
            </Button>
          </>
        }
      />

      {/* ── Attributes panel ── */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <StatCard
          label="Visible Messages"
          value={visibleCount}
          variant={visibleCount > 0 ? "accent" : "default"}
        />
        <StatCard label="In-flight" value={inFlightCount} />
        <StatCard label="Visibility Timeout" value={`${queue.visibilityTimeout}s`} />
        <StatCard label="Retention" value={formatRetention(queue.messageRetentionPeriod)} />
      </div>

      <div className="grid grid-cols-2 gap-4 md:grid-cols-3">
        <AttrRow label="ARN" value={queue.arn} mono />
        <AttrRow label="Delay" value={`${queue.delaySeconds}s`} />
        <AttrRow label="Max Message Size" value={formatBytes(queue.maximumMessageSize)} />
        <AttrRow
          label="Long Poll Wait"
          value={
            queue.receiveMessageWaitTimeSeconds > 0
              ? `${queue.receiveMessageWaitTimeSeconds}s`
              : "Disabled"
          }
        />
      </div>

      {/* ── Messages pane ── */}
      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-fg">
            Messages
            <span className="ml-2 text-xs font-normal text-fg-muted">
              (peeked with {PEEK_VISIBILITY_TIMEOUT}s visibility timeout — messages reappear
              automatically)
            </span>
          </h2>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant="ghost"
              onClick={() => refetchMessages()}
              disabled={mFetching}
            >
              <Eye className={`mr-1.5 h-3.5 w-3.5 ${mFetching ? "animate-spin" : ""}`} />
              Peek
            </Button>
          </div>
        </div>

        {mLoading ? (
          <div className="flex justify-center py-8">
            <Spinner />
          </div>
        ) : messages.length === 0 ? (
          <EmptyState
            icon={<MessagesSquare className="h-8 w-8" />}
            title="No visible messages"
            description="The queue is empty or all messages are in-flight."
            action={
              <Button size="sm" onClick={() => setShowSend(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" />
                Send Message
              </Button>
            }
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8" />
                <TableHead>Message ID</TableHead>
                <TableHead>Body</TableHead>
                <TableHead>Receive Count</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {messages.map((msg) => (
                <MessageRow
                  key={msg.receiptHandle}
                  msg={msg}
                  expanded={expandedMsg === msg.messageId}
                  onToggle={() =>
                    setExpandedMsg((prev) => (prev === msg.messageId ? undefined : msg.messageId))
                  }
                  onDelete={() => setDeleteTarget(msg)}
                />
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      {/* ── Send Message dialog ── */}
      <SendMessageDialog queueName={queueName} open={showSend} onClose={() => setShowSend(false)} />

      {/* ── Delete message confirm ── */}
      <Dialog open={!!deleteTarget} onOpenChange={(v) => !v && setDeleteTarget(undefined)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Message</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-fg-muted">
            Permanently delete message{" "}
            <span className="font-mono text-xs">{deleteTarget?.messageId}</span>? This cannot be
            undone.
          </p>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleteTarget(undefined)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              onClick={() => deleteTarget && deleteMsg.mutate(deleteTarget.receiptHandle)}
              disabled={deleteMsg.isPending}
            >
              {deleteMsg.isPending && <Spinner className="mr-2" />}
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── Purge confirm ── */}
      <Dialog open={purgeConfirm} onOpenChange={(v) => !v && setPurgeConfirm(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Purge Queue</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-fg-muted">
            Delete <strong>all messages</strong> from <strong>{queueName}</strong>? This cannot be
            undone.
          </p>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setPurgeConfirm(false)}>
              Cancel
            </Button>
            <Button variant="danger" onClick={() => purge.mutate()} disabled={purge.isPending}>
              {purge.isPending && <Spinner className="mr-2" />}
              Purge Queue
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ── Delete queue confirm ── */}
      <Dialog open={deleteQueueConfirm} onOpenChange={(v) => !v && setDeleteQueueConfirm(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Queue</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-fg-muted">
            Permanently delete queue <strong>{queueName}</strong> and all its messages? This cannot
            be undone.
          </p>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDeleteQueueConfirm(false)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              onClick={() => deleteQueueMut.mutate(queueName)}
              disabled={deleteQueueMut.isPending}
            >
              {deleteQueueMut.isPending && <Spinner className="mr-2" />}
              Delete Queue
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ─── MessageRow ──────────────────────────────────────────────────────────────

function MessageRow({
  msg,
  expanded,
  onToggle,
  onDelete,
}: {
  msg: SQSMessage
  expanded: boolean
  onToggle: () => void
  onDelete: () => void
}) {
  const hasAttrs = Object.keys(msg.messageAttributes).length > 0
  const receiveCount = parseInt(msg.attributes.ApproximateReceiveCount ?? "0", 10)

  return (
    <>
      <TableRow className="cursor-pointer hover:bg-bg-muted/40" onClick={onToggle}>
        <TableCell>
          {expanded ? (
            <ChevronDown className="h-3.5 w-3.5 text-fg-muted" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 text-fg-muted" />
          )}
        </TableCell>
        <TableCell className="w-72 font-mono text-xs">{msg.messageId}</TableCell>
        <TableCell className="max-w-xs truncate text-sm">
          {msg.body.length > 120 ? msg.body.slice(0, 120) + "…" : msg.body}
        </TableCell>
        <TableCell>
          <Badge variant={receiveCount > 1 ? "warning" : "default"}>{receiveCount}</Badge>
        </TableCell>
        <TableCell>
          <Button
            size="icon"
            variant="ghost"
            className="text-fg-muted hover:text-danger"
            onClick={(e) => {
              e.stopPropagation()
              onDelete()
            }}
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </TableCell>
      </TableRow>
      {expanded && (
        <TableRow>
          <TableCell colSpan={5} className="bg-bg-muted/30 px-4 py-3">
            <div className="flex flex-col gap-3">
              <div className="flex flex-col gap-1">
                <span className="text-xs font-medium text-fg-muted">Message ID</span>
                <span className="font-mono text-xs text-fg">{msg.messageId}</span>
              </div>
              <div className="flex flex-col gap-1">
                <span className="text-xs font-medium text-fg-muted">Receipt Handle</span>
                <span className="max-w-full truncate font-mono text-xs text-fg-muted">
                  {msg.receiptHandle}
                </span>
              </div>
              <div className="flex flex-col gap-1">
                <span className="text-xs font-medium text-fg-muted">Body</span>
                <CodeBlock>{msg.body}</CodeBlock>
              </div>
              {msg.attributes.SentTimestamp && (
                <div className="flex flex-col gap-1">
                  <span className="text-xs font-medium text-fg-muted">Sent</span>
                  <span className="text-xs text-fg">
                    {new Date(parseInt(msg.attributes.SentTimestamp, 10)).toISOString()}
                  </span>
                </div>
              )}
              {hasAttrs && (
                <div className="flex flex-col gap-1.5">
                  <span className="text-xs font-medium text-fg-muted">Message Attributes</span>
                  <div className="grid grid-cols-[auto_auto_1fr] gap-x-4 gap-y-1 text-xs">
                    <span className="font-medium text-fg-subtle">Name</span>
                    <span className="font-medium text-fg-subtle">Type</span>
                    <span className="font-medium text-fg-subtle">Value</span>
                    {Object.entries(msg.messageAttributes).map(([k, v]) => (
                      <>
                        <span key={`${k}-name`} className="font-mono text-fg">
                          {k}
                        </span>
                        <span key={`${k}-type`} className="text-fg-muted">
                          {v.dataType}
                        </span>
                        <span key={`${k}-val`} className="text-fg">
                          {v.stringValue}
                        </span>
                      </>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </TableCell>
        </TableRow>
      )}
    </>
  )
}

// ─── Small helpers ────────────────────────────────────────────────────────────

function StatCard({
  label,
  value,
  variant = "default",
}: {
  label: string
  value: string | number
  variant?: "default" | "accent"
}) {
  return (
    <div className="rounded-lg border border-border bg-bg-muted p-4">
      <p className="text-xs font-medium text-fg-muted">{label}</p>
      <p
        className={`mt-1 text-2xl font-semibold tabular-nums ${variant === "accent" ? "text-accent" : "text-fg"}`}
      >
        {value}
      </p>
    </div>
  )
}

function AttrRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-fg-muted">{label}</span>
      <span className={`text-sm text-fg ${mono ? "font-mono" : ""}`}>{value}</span>
    </div>
  )
}

function formatRetention(seconds: number): string {
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`
  return `${Math.round(seconds / 86400)}d`
}

function formatBytes(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(0)} MiB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KiB`
  return `${bytes} B`
}
