/**
 * BucketConfig — Config tab for /s3/$bucket/config
 *
 * Shows bucket event notification configurations and lets the user add new
 * SQS queue notification rules or delete existing ones.
 */
import { useState, useEffect } from "react"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import {
  Bell,
  BellOff,
  BellPlus,
  ChevronRight,
  MessagesSquare,
  Pencil,
  Trash2,
  Check,
  Plus,
} from "lucide-react"
import { useNavigate } from "@tanstack/react-router"
import { Route } from "@/routes/s3/$bucket/config"
import {
  s3BucketNotificationQueryOptions,
  s3Keys,
  putBucketNotificationMutationOptions,
} from "@/features/s3/data"
import { sqsQueuesQueryOptions } from "@/features/sqs/data"
import { Badge } from "@/components/ui/badge"
import { ArnLink } from "@/components/ui/arn-link"
import { PageHeader, Breadcrumb, Spinner, EmptyState } from "@/components/ui/primitives"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField } from "@/components/ui/form"
import { Combobox } from "@/components/ui/combobox"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { useToast } from "@/components/ui/toast"
import { BucketTabs } from "./bucket-tabs"
import type {
  QueueNotificationConfig,
  TopicNotificationConfig,
  LambdaNotificationConfig,
  NotificationFilterRule,
  BucketNotificationConfig,
  SQSQueue,
} from "@/types"
import { cn } from "@/lib/utils"

// ─── S3 event options ─────────────────────────────────────────────────────────

const S3_EVENTS = [
  { value: "s3:ObjectCreated:*", label: "ObjectCreated:* — any upload" },
  { value: "s3:ObjectCreated:Put", label: "ObjectCreated:Put" },
  { value: "s3:ObjectCreated:Post", label: "ObjectCreated:Post" },
  { value: "s3:ObjectCreated:Copy", label: "ObjectCreated:Copy" },
  {
    value: "s3:ObjectCreated:CompleteMultipartUpload",
    label: "ObjectCreated:CompleteMultipartUpload",
  },
  { value: "s3:ObjectRemoved:*", label: "ObjectRemoved:* — any delete" },
  { value: "s3:ObjectRemoved:Delete", label: "ObjectRemoved:Delete" },
  { value: "s3:ObjectRemoved:DeleteMarkerCreated", label: "ObjectRemoved:DeleteMarkerCreated" },
]

// ─── AddNotificationDialog ────────────────────────────────────────────────────

interface AddNotificationDialogProps {
  open: boolean
  onClose: () => void
  existing: BucketNotificationConfig
  bucket: string
  /** When set, the dialog opens in edit mode pre-populated with this config. */
  editing?: QueueNotificationConfig
}

function AddNotificationDialog({
  open,
  onClose,
  existing,
  bucket,
  editing,
}: AddNotificationDialogProps) {
  const qc = useQueryClient()
  const { toast } = useToast()

  const isEdit = !!editing

  const [id, setId] = useState("")
  const [queueArn, setQueueArn] = useState("")
  const [selectedEvents, setSelectedEvents] = useState<string[]>(["s3:ObjectCreated:*"])
  const [prefix, setPrefix] = useState("")
  const [suffix, setSuffix] = useState("")

  // Reset / pre-populate whenever the dialog opens or the editing target changes
  useEffect(() => {
    if (!open) return
    if (editing) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setId(editing.id)
      setQueueArn(editing.queueArn)
      setSelectedEvents(editing.events)
      setPrefix(editing.filterRules.find((r) => r.name === "prefix")?.value ?? "")
      setSuffix(editing.filterRules.find((r) => r.name === "suffix")?.value ?? "")
    } else {
      setId("")
      setQueueArn("")
      setSelectedEvents(["s3:ObjectCreated:*"])
      setPrefix("")
      setSuffix("")
    }
  }, [open, editing])

  const { data: queues = [], isLoading: queuesLoading } = useQuery({
    ...sqsQueuesQueryOptions(),
    enabled: open,
  })

  const mut = useMutation({
    ...putBucketNotificationMutationOptions(bucket),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: s3Keys.bucketNotification(bucket) })
      toast({ title: isEdit ? "Notification updated" : "Notification added", variant: "success" })
      handleClose()
    },
    onError: (err: Error) =>
      toast({ title: "Save failed", description: err.message, variant: "danger" }),
  })

  function handleClose() {
    onClose()
  }

  function toggleEvent(ev: string) {
    setSelectedEvents((prev) => (prev.includes(ev) ? prev.filter((e) => e !== ev) : [...prev, ev]))
  }

  function arnToQueueName(arn: string, qs: SQSQueue[]) {
    const q = qs.find((q) => q.arn === arn)
    return q ? q.name : arn
  }

  function handleSave() {
    if (!queueArn || selectedEvents.length === 0) return
    const filterRules: NotificationFilterRule[] = []
    if (prefix) filterRules.push({ name: "prefix", value: prefix })
    if (suffix) filterRules.push({ name: "suffix", value: suffix })

    const entry: QueueNotificationConfig = {
      id: id.trim() || editing?.id || `notify-${Date.now()}`,
      queueArn,
      events: selectedEvents,
      filterRules,
    }

    let queueConfigurations: QueueNotificationConfig[]
    if (editing) {
      // Replace the edited entry (match by original ARN)
      queueConfigurations = existing.queueConfigurations.map((q) =>
        q.queueArn === editing.queueArn ? entry : q,
      )
    } else {
      queueConfigurations = [...existing.queueConfigurations, entry]
    }

    mut.mutate({
      queueConfigurations,
      topicConfigurations: existing.topicConfigurations,
      lambdaConfigurations: existing.lambdaConfigurations,
    })
  }

  const canSave = !!(queueArn && selectedEvents.length > 0 && !mut.isPending)

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{isEdit ? "Edit Event Notification" : "Add Event Notification"}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-5">
          {/* Destination */}
          <div className="flex flex-col gap-3 rounded-lg border border-border bg-bg-subtle p-4">
            <p className="text-xs font-semibold tracking-wider text-fg-muted uppercase">
              Destination
            </p>

            {/* Destination type — SQS only for now */}
            <div className="flex items-center gap-2 rounded-lg border border-accent bg-accent/10 px-3 py-2 text-sm font-medium text-fg">
              <span className="flex h-5 w-5 items-center justify-center rounded bg-yellow-400/10">
                <MessagesSquare className="h-3 w-3 text-yellow-400" />
              </span>
              SQS Queue
              <Check className="ml-auto h-3.5 w-3.5 text-accent" />
            </div>

            <FormField label="Queue" required hint="Search by name or paste an ARN directly.">
              <Combobox
                value={arnToQueueName(queueArn, queues)}
                onChange={setQueueArn}
                items={queues}
                filterFn={(q, query) =>
                  q.name.toLowerCase().includes(query.toLowerCase()) ||
                  q.arn.toLowerCase().includes(query.toLowerCase())
                }
                getItemValue={(q) => q.arn}
                renderItem={(q, { selected }) => (
                  <span className="flex items-center justify-between gap-2">
                    <span className="flex items-center gap-2">
                      <MessagesSquare className="h-3.5 w-3.5 shrink-0 text-yellow-400" />
                      <span>{q.name}</span>
                    </span>
                    {selected && <Check className="h-3 w-3 text-accent" />}
                  </span>
                )}
                allowCustom
                renderCustomFooter={(query, select) => (
                  <div className="border-t border-border px-3 py-2">
                    <button
                      type="button"
                      className="w-full rounded px-2 py-1.5 text-left text-sm text-fg-muted hover:bg-bg-muted"
                      onMouseDown={(e) => e.preventDefault()}
                      onClick={() => select(query)}
                    >
                      Use <span className="font-mono text-fg">{query}</span> as ARN
                    </button>
                  </div>
                )}
                placeholder={queuesLoading ? "Loading queues…" : "Search queues or paste ARN…"}
                popoverWidth="w-full"
              />
            </FormField>
          </div>

          {/* Events */}
          <FormField
            label="Events"
            required
            hint="Select all event types that should trigger a notification."
          >
            <div className="flex flex-col gap-1">
              {S3_EVENTS.map((ev) => {
                const checked = selectedEvents.includes(ev.value)
                return (
                  <label
                    key={ev.value}
                    className={cn(
                      "flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm hover:bg-bg-muted",
                      checked ? "text-fg" : "text-fg-muted",
                    )}
                  >
                    <input
                      type="checkbox"
                      className="accent-accent"
                      checked={checked}
                      onChange={() => toggleEvent(ev.value)}
                    />
                    <span className="font-mono text-xs">{ev.value}</span>
                    <span className="text-xs text-fg-subtle">— {ev.label.split("— ")[1]}</span>
                  </label>
                )
              })}
            </div>
          </FormField>

          {/* Filters (optional) */}
          <div className="flex flex-col gap-3 rounded-lg border border-border bg-bg-subtle p-4">
            <p className="text-xs font-semibold tracking-wider text-fg-muted uppercase">
              Filters <span className="font-normal text-fg-subtle normal-case">(optional)</span>
            </p>
            <div className="grid grid-cols-2 gap-3">
              <FormField label="Key prefix">
                <Input
                  placeholder="e.g. uploads/"
                  value={prefix}
                  onChange={(e) => setPrefix(e.target.value)}
                />
              </FormField>
              <FormField label="Key suffix">
                <Input
                  placeholder="e.g. .jpg"
                  value={suffix}
                  onChange={(e) => setSuffix(e.target.value)}
                />
              </FormField>
            </div>
          </div>

          {/* Optional ID */}
          <FormField label="Configuration ID" hint="Optional — auto-generated if left blank.">
            <Input
              placeholder="my-notification-id"
              value={id}
              onChange={(e) => setId(e.target.value)}
            />
          </FormField>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={handleClose}>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={!canSave}>
            {mut.isPending ? (
              <Spinner className="mr-1.5 h-3.5 w-3.5" />
            ) : (
              <BellPlus className="mr-1.5 h-3.5 w-3.5" />
            )}
            {isEdit ? "Save Changes" : "Add Notification"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── BucketConfig ─────────────────────────────────────────────────────────────

export function BucketConfig() {
  const { bucket } = Route.useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [showAdd, setShowAdd] = useState(false)
  const [editingQueue, setEditingQueue] = useState<QueueNotificationConfig | undefined>(undefined)

  const { data, isLoading } = useQuery(s3BucketNotificationQueryOptions(bucket))

  const deleteMut = useMutation({
    ...putBucketNotificationMutationOptions(bucket),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: s3Keys.bucketNotification(bucket) })
      toast({ title: "Notification removed" })
    },
    onError: (err: Error) =>
      toast({ title: "Remove failed", description: err.message, variant: "danger" }),
  })

  function removeQueue(arn: string) {
    if (!data) return
    deleteMut.mutate({
      queueConfigurations: data.queueConfigurations.filter((q) => q.queueArn !== arn),
      topicConfigurations: data.topicConfigurations,
      lambdaConfigurations: data.lambdaConfigurations,
    })
  }

  const hasConfig =
    (data?.queueConfigurations.length ?? 0) > 0 ||
    (data?.topicConfigurations.length ?? 0) > 0 ||
    (data?.lambdaConfigurations.length ?? 0) > 0

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={bucket}
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "S3", onClick: () => navigate({ to: "/s3" }) },
              { label: bucket, onClick: () => navigate({ to: "/s3/$bucket", params: { bucket } }) },
              { label: "Configuration" },
            ]}
          />
        }
        actions={
          <>
            <Button variant="secondary" size="md" onClick={() => navigate({ to: "/s3" })}>
              Buckets
            </Button>
            <Button size="md" onClick={() => setShowAdd(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Add Notification
            </Button>
          </>
        }
      />

      <BucketTabs bucket={bucket} active="config" />

      {isLoading ? (
        <div className="flex items-center justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : !hasConfig ? (
        <EmptyState
          icon={<BellOff className="h-8 w-8" />}
          title="No event notifications"
          description="Add a notification rule to route S3 events to an SQS queue."
          action={
            <Button onClick={() => setShowAdd(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Add Notification
            </Button>
          }
        />
      ) : (
        <div className="flex flex-col gap-6">
          {(data?.queueConfigurations.length ?? 0) > 0 && (
            <ConfigSection
              title="SQS Queue Destinations"
              icon={<Bell className="h-4 w-4 text-yellow-400" />}
            >
              {data!.queueConfigurations.map((q) => (
                <QueueRow
                  key={q.id || q.queueArn}
                  config={q}
                  onDelete={() => removeQueue(q.queueArn)}
                  onEdit={() => {
                    setEditingQueue(q)
                    setShowAdd(true)
                  }}
                  isDeleting={deleteMut.isPending}
                />
              ))}
            </ConfigSection>
          )}

          {(data?.topicConfigurations.length ?? 0) > 0 && (
            <ConfigSection
              title="SNS Topic Destinations"
              icon={<Bell className="h-4 w-4 text-pink-400" />}
            >
              {data!.topicConfigurations.map((t) => (
                <TopicRow key={t.id || t.topicArn} config={t} />
              ))}
            </ConfigSection>
          )}

          {(data?.lambdaConfigurations.length ?? 0) > 0 && (
            <ConfigSection
              title="Lambda Destinations"
              icon={<Bell className="h-4 w-4 text-purple-400" />}
            >
              {data!.lambdaConfigurations.map((l) => (
                <LambdaRow key={l.id || l.functionArn} config={l} />
              ))}
            </ConfigSection>
          )}
        </div>
      )}

      <AddNotificationDialog
        open={showAdd}
        onClose={() => {
          setShowAdd(false)
          setEditingQueue(undefined)
        }}
        existing={
          data ?? { queueConfigurations: [], topicConfigurations: [], lambdaConfigurations: [] }
        }
        bucket={bucket}
        editing={editingQueue}
      />
    </div>
  )
}

// ─── Sub-components ───────────────────────────────────────────────────────────

function ConfigSection({
  title,
  icon,
  children,
}: {
  title: string
  icon: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        {icon}
        <h2 className="text-sm font-medium text-fg">{title}</h2>
      </div>
      <div className="flex flex-col divide-y divide-border overflow-hidden rounded-lg border border-border bg-bg-elevated">
        {children}
      </div>
    </div>
  )
}

function EventList({ events }: { events: string[] }) {
  return (
    <div className="flex flex-wrap gap-1">
      {events.map((ev) => (
        <Badge key={ev} variant="accent" className="font-mono text-[10px]">
          {ev}
        </Badge>
      ))}
    </div>
  )
}

function FilterList({ rules }: { rules: NotificationFilterRule[] }) {
  if (rules.length === 0) return <span className="text-xs text-fg-subtle">none</span>
  return (
    <div className="flex flex-wrap gap-1">
      {rules.map((r, i) => (
        <Badge key={i} variant="default" className="font-mono text-[10px]">
          {r.name}={r.value}
        </Badge>
      ))}
    </div>
  )
}

function QueueRow({
  config: q,
  onDelete,
  onEdit,
  isDeleting,
}: {
  config: QueueNotificationConfig
  onDelete: () => void
  onEdit: () => void
  isDeleting?: boolean
}) {
  return (
    <div className="flex flex-col gap-3 px-4 py-3">
      <div className="flex items-center gap-2">
        <ChevronRight className="h-3 w-3 shrink-0 text-fg-subtle" />
        <ArnLink arn={q.queueArn} />
        {q.id && (
          <Badge variant="default" className="ml-auto shrink-0 text-[10px]">
            {q.id}
          </Badge>
        )}
        <Button
          size="icon"
          variant="ghost"
          className="ml-1 h-6 w-6 shrink-0 text-fg-muted hover:text-fg"
          onClick={onEdit}
          disabled={isDeleting}
          title="Edit"
        >
          <Pencil className="h-3 w-3" />
        </Button>
        <Button
          size="icon"
          variant="ghost"
          className="h-6 w-6 shrink-0 text-fg-muted hover:text-danger"
          onClick={onDelete}
          disabled={isDeleting}
          title="Delete"
        >
          <Trash2 className="h-3 w-3" />
        </Button>
      </div>
      <ConfigRow label="Events">
        <EventList events={q.events} />
      </ConfigRow>
      <ConfigRow label="Filters">
        <FilterList rules={q.filterRules} />
      </ConfigRow>
    </div>
  )
}

function TopicRow({ config: t }: { config: TopicNotificationConfig }) {
  return (
    <div className="flex flex-col gap-3 px-4 py-3">
      <div className="flex items-center gap-2">
        <ChevronRight className="h-3 w-3 shrink-0 text-fg-subtle" />
        <ArnLink arn={t.topicArn} />
        {t.id && (
          <Badge variant="default" className="ml-auto shrink-0 text-[10px]">
            {t.id}
          </Badge>
        )}
      </div>
      <ConfigRow label="Events">
        <EventList events={t.events} />
      </ConfigRow>
      <ConfigRow label="Filters">
        <FilterList rules={t.filterRules} />
      </ConfigRow>
    </div>
  )
}

function LambdaRow({ config: l }: { config: LambdaNotificationConfig }) {
  return (
    <div className="flex flex-col gap-3 px-4 py-3">
      <div className="flex items-center gap-2">
        <ChevronRight className="h-3 w-3 shrink-0 text-fg-subtle" />
        <ArnLink arn={l.functionArn} />
        {l.id && (
          <Badge variant="default" className="ml-auto shrink-0 text-[10px]">
            {l.id}
          </Badge>
        )}
      </div>
      <ConfigRow label="Events">
        <EventList events={l.events} />
      </ConfigRow>
      <ConfigRow label="Filters">
        <FilterList rules={l.filterRules} />
      </ConfigRow>
    </div>
  )
}

function ConfigRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start gap-4 pl-5">
      <span className="w-16 shrink-0 text-xs text-fg-muted">{label}</span>
      <div className="flex-1">{children}</div>
    </div>
  )
}
