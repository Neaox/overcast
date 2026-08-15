import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Eye, MessagesSquare, Trash2 } from "lucide-react"
import {
  sqsQueuesQueryOptions,
  sqsKeys,
  createQueueMutationOptions,
  deleteQueueMutationOptions,
} from "@/features/sqs/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { sqs } from "@/services/api"
import { useToast } from "@/components/ui/toast"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, FormRow, fieldError } from "@/components/ui/form"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { EmptyState, QueryListState, Spinner } from "@/components/ui/primitives"
import { RegionElsewhereNotice } from "@/features/preflight/components/region-elsewhere-notice"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
  SelectCheckbox,
} from "@/components/ui/resource-list-page"
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { RawStateLink } from "@/features/debug/raw-state-link"
import { cn } from "@/lib/utils"

export function QueueList() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [selectedQueues, setSelectedQueues] = useState<Set<string>>(new Set())
  const [showBulkDelete, setShowBulkDelete] = useState(false)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: queues = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(sqsQueuesQueryOptions())

  const createMut = useResourceMutation({
    options: createQueueMutationOptions(),
    invalidateKeys: [sqsKeys.queues()],
    successTitle: "Queue created",
    successDescription: ({ name }) => name,
    onSuccess: () => setShowCreate(false),
  })

  const deleteMut = useResourceMutation({
    options: deleteQueueMutationOptions(),
    invalidateKeys: [sqsKeys.queues()],
    successTitle: "Queue deleted",
    successDescription: (name) => name,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const bulkDeleteMut = useMutation({
    mutationFn: async (names: string[]) => {
      await Promise.all(names.map((n) => sqs.deleteQueue(n)))
    },
    onSuccess: (_, names) => {
      void qc.invalidateQueries({ queryKey: sqsKeys.queues() })
      setSelectedQueues(new Set())
      setShowBulkDelete(false)
      toast({
        title: `${names.length} queue${names.length !== 1 ? "s" : ""} deleted`,
        variant: "success",
      })
    },
    onError: (err: Error) =>
      toast({ title: "Bulk delete failed", description: err.message, variant: "danger" }),
  })

  function openCreate() {
    setShowCreate(true)
  }

  const visibleMessages = queues.reduce((n, q) => n + q.approximateNumberOfMessages, 0)
  const inFlightMessages = queues.reduce((n, q) => n + q.approximateNumberOfMessagesNotVisible, 0)

  return (
    <ResourceListPage
      title="SQS Queues"
      count={queues.length}
      meta={
        queues.length > 0 ? `${visibleMessages} visible · ${inFlightMessages} in-flight` : undefined
      }
      actions={
        <>
          <ServiceDocsButton
            service="sqs"
            label="SQS"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RawStateLink service="sqs" />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={openCreate}>Create queue</CreateAction>
        </>
      }
    >
      {selectedQueues.size > 0 && (
        <div className="flex items-center gap-3 rounded-card border border-border bg-bg-muted px-3 py-2">
          <span className="text-sm font-medium">
            {selectedQueues.size} queue{selectedQueues.size !== 1 ? "s" : ""} selected
          </span>
          <Button size="sm" variant="danger" onClick={() => setShowBulkDelete(true)}>
            <Trash2 className="h-3.5 w-3.5" />
            Delete selected
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setSelectedQueues(new Set())}>
            Clear
          </Button>
        </div>
      )}

      <ResourceListCard>
        {isLoading || queues.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={queues.length === 0}
            error={error}
            empty={
              <>
                <EmptyState
                  icon={<MessagesSquare className="h-10 w-10" />}
                  title="No queues yet"
                  description="Create a queue to start sending and receiving messages."
                  action={<CreateAction onClick={openCreate}>Create queue</CreateAction>}
                />
                <RegionElsewhereNotice kind="sqs-queues" noun="queues" />
              </>
            }
            errorTitle="Failed to load queues"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10 pr-0">
                  <SelectCheckbox
                    label="Select all queues"
                    checked={selectedQueues.size === queues.length}
                    indeterminate={selectedQueues.size > 0 && selectedQueues.size < queues.length}
                    onCheckedChange={(checked) =>
                      setSelectedQueues(checked ? new Set(queues.map((q) => q.name)) : new Set())
                    }
                  />
                </TableHead>
                <TableHead>Queue name</TableHead>
                <TableHead>Visible</TableHead>
                <TableHead>In-flight</TableHead>
                <TableHead>Visibility timeout</TableHead>
                <TableHead>ARN</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {queues.map((q) => (
                <TableRow
                  key={q.name}
                  onClick={() => navigate({ to: "/sqs/$queue", params: { queue: q.name } })}
                >
                  <TableCell className="pr-0" onClick={(e) => e.stopPropagation()}>
                    <SelectCheckbox
                      label={`Select ${q.name}`}
                      checked={selectedQueues.has(q.name)}
                      onCheckedChange={(checked) => {
                        const next = new Set(selectedQueues)
                        if (checked) next.add(q.name)
                        else next.delete(q.name)
                        setSelectedQueues(next)
                      }}
                    />
                  </TableCell>
                  <TableCell>
                    <ResourceName icon={MessagesSquare} name={q.name}>
                      {q.name.endsWith(".fifo") && <Badge variant="info">FIFO</Badge>}
                    </ResourceName>
                  </TableCell>
                  <TableCell>
                    <Badge variant={q.approximateNumberOfMessages > 0 ? "accent" : "default"}>
                      {q.approximateNumberOfMessages}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Badge
                      variant={q.approximateNumberOfMessagesNotVisible > 0 ? "warning" : "default"}
                    >
                      {q.approximateNumberOfMessagesNotVisible}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-fg-muted">{q.visibilityTimeout}s</TableCell>
                  <TableCell className="max-w-xs truncate text-fg-muted">{q.arn}</TableCell>
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <RowActions>
                      <RowAction
                        label={`View ${q.name}`}
                        onClick={() => navigate({ to: "/sqs/$queue", params: { queue: q.name } })}
                      >
                        <Eye className="h-3.5 w-3.5" />
                      </RowAction>
                      <RowAction
                        label={`Delete ${q.name}`}
                        tone="danger"
                        onClick={() => setDeleteTarget(q.name)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </RowAction>
                    </RowActions>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>

      {/* ── Create queue dialog ── */}
      <CreateQueueDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(values) => createMut.mutate(values)}
      />

      {/* ── Delete confirm dialog ── */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete Queue"
        description={
          <>
            Permanently delete <strong>{deleteTarget}</strong> and all its messages?
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />

      {/* ── Bulk delete confirm dialog ── */}
      <Dialog open={showBulkDelete} onOpenChange={(v) => !v && setShowBulkDelete(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              Delete {selectedQueues.size} Queue{selectedQueues.size !== 1 ? "s" : ""}
            </DialogTitle>
          </DialogHeader>
          <DialogBody>
            <p className="text-sm text-fg-muted">
              Permanently delete these queues and all their messages?
            </p>
            <ul className="mt-3 max-h-40 overflow-y-auto rounded border border-border bg-bg-muted p-2 font-mono text-xs">
              {[...selectedQueues].map((name) => (
                <li key={name} className="truncate py-0.5">
                  {name}
                </li>
              ))}
            </ul>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setShowBulkDelete(false)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              onClick={() => bulkDeleteMut.mutate([...selectedQueues])}
              disabled={bulkDeleteMut.isPending}
            >
              {bulkDeleteMut.isPending && <Spinner className="mr-2" />}
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </ResourceListPage>
  )
}

// ─── CreateQueueDialog ────────────────────────────────────────────────────────

const createQueueSchema = z.object({
  name: z
    .string()
    .min(1, "Name is required")
    .regex(/^[a-zA-Z0-9_-]+$/, "Only letters, numbers, hyphens, and underscores"),
  fifo: z.boolean(),
  contentBasedDeduplication: z.boolean(),
  visibilityTimeout: z.number().int().min(0, "Min 0").max(43200, "Max 43200"),
  messageRetentionPeriod: z.number().int().min(60, "Min 60").max(1209600, "Max 1209600"),
})

type CreateQueueValues = z.infer<typeof createQueueSchema>

function CreateQueueDialog({
  open,
  onClose,
  onSubmit,
  isPending,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (values: CreateQueueValues) => void
  isPending: boolean
}) {
  const form = useForm({
    validators: { onChange: createQueueSchema },
    defaultValues: {
      name: "",
      fifo: false,
      contentBasedDeduplication: false,
      visibilityTimeout: 30,
      messageRetentionPeriod: 345600,
    },
    onSubmit: ({ value }) => {
      const finalName = value.fifo ? `${value.name}.fifo` : value.name
      onSubmit({ ...value, name: finalName })
    },
  })

  function handleClose() {
    onClose()
    setTimeout(() => form.reset(), 150)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create queue</DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            e.stopPropagation()
            void form.handleSubmit()
          }}
        >
          {/* Queue type selector */}
          <form.Field name="fifo">
            {(field) => (
              <FormField label="Queue Type">
                <div className="flex gap-2">
                  <button
                    type="button"
                    className={cn(
                      "flex-1 rounded-md border px-3 py-2 text-sm font-medium transition-colors",
                      !field.state.value
                        ? "border-accent bg-accent/10 text-accent"
                        : "border-border text-fg-muted hover:border-fg-muted",
                    )}
                    onClick={() => field.handleChange(false)}
                  >
                    Standard
                  </button>
                  <button
                    type="button"
                    className={cn(
                      "flex-1 rounded-md border px-3 py-2 text-sm font-medium transition-colors",
                      field.state.value
                        ? "border-accent bg-accent/10 text-accent"
                        : "border-border text-fg-muted hover:border-fg-muted",
                    )}
                    onClick={() => field.handleChange(true)}
                  >
                    FIFO
                  </button>
                </div>
              </FormField>
            )}
          </form.Field>

          <form.Field name="name" validators={{ onChange: createQueueSchema.shape.name }}>
            {(field) => (
              <form.Subscribe selector={(s) => s.values.fifo}>
                {(isFifo) => (
                  <FormField
                    label="Queue Name"
                    required
                    error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                  >
                    <div className="relative">
                      <Input
                        placeholder={isFifo ? "my-queue" : "my-queue"}
                        value={field.state.value}
                        onChange={(e) => field.handleChange(e.target.value)}
                        onBlur={field.handleBlur}
                        className={cn(isFifo && "pr-14")}
                        autoFocus
                      />
                      {isFifo && (
                        <span className="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 rounded bg-bg-muted px-1.5 py-0.5 font-mono text-xs text-fg-muted">
                          .fifo
                        </span>
                      )}
                    </div>
                  </FormField>
                )}
              </form.Subscribe>
            )}
          </form.Field>

          {/* Content-based deduplication (FIFO only) */}
          <form.Subscribe selector={(s) => s.values.fifo}>
            {(isFifo) =>
              isFifo ? (
                <form.Field name="contentBasedDeduplication">
                  {(field) => (
                    <label className="flex items-center gap-2 text-xs">
                      <input
                        type="checkbox"
                        checked={field.state.value}
                        onChange={(e) => field.handleChange(e.target.checked)}
                        className="rounded border-border"
                      />
                      <span>Content-based deduplication</span>
                      <span className="font-sans text-fg-muted">(uses message body MD5)</span>
                    </label>
                  )}
                </form.Field>
              ) : null
            }
          </form.Subscribe>

          <FormRow>
            <form.Field
              name="visibilityTimeout"
              validators={{ onChange: createQueueSchema.shape.visibilityTimeout }}
            >
              {(field) => (
                <FormField
                  label="Visibility Timeout (s)"
                  hint="0–43200"
                  error={
                    field.state.meta.isTouched ? field.state.meta.errors[0]?.message : undefined
                  }
                >
                  <Input
                    type="number"
                    min={0}
                    max={43200}
                    value={field.state.value}
                    onChange={(e) => field.handleChange(Number(e.target.value))}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
            <form.Field
              name="messageRetentionPeriod"
              validators={{ onChange: createQueueSchema.shape.messageRetentionPeriod }}
            >
              {(field) => (
                <FormField
                  label="Message Retention (s)"
                  hint="60–1209600"
                  error={
                    field.state.meta.isTouched ? field.state.meta.errors[0]?.message : undefined
                  }
                >
                  <Input
                    type="number"
                    min={60}
                    max={1209600}
                    value={field.state.value}
                    onChange={(e) => field.handleChange(Number(e.target.value))}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
          </FormRow>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <form.Subscribe selector={(s) => [s.canSubmit, s.isSubmitting]}>
              {([canSubmit, isSubmitting]) => (
                <Button type="submit" disabled={!canSubmit || isPending}>
                  {(isSubmitting || isPending) && <Spinner className="mr-2" />}
                  Create queue
                </Button>
              )}
            </form.Subscribe>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
