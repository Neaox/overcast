import { useState, useEffect } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { GitBranch, Plus, Trash2, RefreshCw, ArrowRight, Check, AlertCircle } from "lucide-react"
import { SERVICES } from "@/lib/service-registry"
import {
  pipeListQueryOptions,
  pipeKeys,
  createPipeMutationOptions,
  deletePipeMutationOptions,
} from "@/features/pipes/data"
import { dynamoTablesQueryOptions } from "@/features/dynamodb/data"
import { sqsQueuesQueryOptions } from "@/features/sqs/data"
import { useLocation, useNavigate } from "@tanstack/react-router"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, fieldError } from "@/components/ui/form"
import { Combobox } from "@/components/ui/combobox"
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
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { useToast } from "@/components/ui/toast"
import type { DynamoTable, SQSQueue } from "@/types"
import { cn } from "@/lib/utils"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"

// ─── Service type definitions ─────────────────────────────────────────────────

interface ServiceDef {
  id: string
  label: string
  icon: React.ElementType
  color: string
  bgColor: string
}

const SOURCE_SERVICES: ServiceDef[] = [
  {
    id: "dynamodb-streams",
    label: "DynamoDB Streams",
    icon: SERVICES.dynamodb.icon,
    color: SERVICES.dynamodb.color,
    bgColor: SERVICES.dynamodb.bg,
  },
]

const TARGET_SERVICES: ServiceDef[] = [
  {
    id: "sqs",
    label: "SQS Queue",
    icon: SERVICES.sqs.icon,
    color: SERVICES.sqs.color,
    bgColor: SERVICES.sqs.bg,
  },
]

// ─── ServiceTypeSelector ──────────────────────────────────────────────────────

function ServiceTypeSelector({
  services,
  selected,
  onSelect,
}: {
  services: ServiceDef[]
  selected: string
  onSelect: (id: string) => void
}) {
  return (
    <div className="flex flex-wrap gap-2">
      {services.map((svc) => {
        const Icon = svc.icon
        const isSelected = svc.id === selected
        return (
          <button
            key={svc.id}
            type="button"
            onClick={() => onSelect(svc.id)}
            className={cn(
              "flex items-center gap-2 rounded-lg border px-3 py-2 text-sm font-medium transition-all",
              isSelected
                ? "border-accent bg-accent/10 text-fg shadow-sm"
                : "border-border bg-bg text-fg-muted hover:border-border-muted hover:text-fg",
            )}
          >
            <span className={cn("flex h-5 w-5 items-center justify-center rounded", svc.bgColor)}>
              <Icon className={cn("h-3 w-3", svc.color)} />
            </span>
            {svc.label}
            {isSelected && <Check className="h-3.5 w-3.5 text-accent" />}
          </button>
        )
      })}
    </div>
  )
}

// ─── ARN display helpers ──────────────────────────────────────────────────────

function arnToDisplayLabel(arn: string, tables: DynamoTable[], queues: SQSQueue[]): string {
  if (!arn) return ""
  const t = tables.find((t) => t.latestStreamArn === arn || t.tableArn === arn)
  if (t) return t.tableName
  const q = queues.find((q) => q.arn === arn)
  if (q) return q.name
  return arn
}

// ─── CreatePipeDialog ─────────────────────────────────────────────────────────

interface CreatePipeDialogProps {
  open: boolean
  onClose: () => void
  onCreated: (name: string) => void
  /** Pre-seed the source ARN (e.g. a DynamoDB stream ARN) when opening from another page. */
  initialSourceArn?: string
}

// Zod schema for the pipe creation form
const pipeSchema = z.object({
  name: z
    .string()
    .min(1, "Name is required")
    .regex(/^[a-zA-Z0-9_-]+$/, "Only letters, numbers, hyphens, and underscores"),
  sourceArn: z.string().min(1, "Select a source table"),
  targetArn: z.string().min(1, "Select a target queue"),
})

export function CreatePipeDialog({
  open,
  onClose,
  onCreated,
  initialSourceArn,
}: CreatePipeDialogProps) {
  // sourceService / targetService are UI-only selectors — not part of the submitted value
  const [sourceService, setSourceService] = useState("dynamodb-streams")
  const [targetService, setTargetService] = useState("sqs")

  const { data: tables = [], isLoading: tablesLoading } = useQuery({
    ...dynamoTablesQueryOptions(),
    enabled: open,
  })

  const { data: queues = [], isLoading: queuesLoading } = useQuery({
    ...sqsQueuesQueryOptions(),
    enabled: open,
  })

  const createMut = useResourceMutation({
    options: createPipeMutationOptions(),
    invalidateKeys: [pipeKeys.list()],
    successDescription: ({ name: n }) => n,
    errorTitle: "Create failed",
    onSuccess: (_, { name: n }) => {
      onCreated(n)
      handleClose()
    },
  })

  const form = useForm({
    validators: { onChange: pipeSchema },
    defaultValues: {
      name: "",
      sourceArn: initialSourceArn ?? "",
      targetArn: "",
    },
    onSubmit: ({ value }) => {
      createMut.mutate({ name: value.name, sourceArn: value.sourceArn, targetArn: value.targetArn })
    },
  })

  // Keep sourceArn in sync when a stream ARN is injected after mount
  useEffect(() => {
    if (open && initialSourceArn) {
      form.setFieldValue("sourceArn", initialSourceArn)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- form is a stable TanStack Form instance
  }, [open, initialSourceArn])

  function handleClose() {
    onClose()
    setTimeout(() => {
      form.reset()
      setSourceService("dynamodb-streams")
      setTargetService("sqs")
    }, 150)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Create Pipe</DialogTitle>
        </DialogHeader>

        <form
          className="flex flex-col gap-5"
          onSubmit={(e) => {
            e.preventDefault()
            e.stopPropagation()
            void form.handleSubmit()
          }}
        >
          {/* Pipe name */}
          <form.Field name="name" validators={{ onChange: pipeSchema.shape.name }}>
            {(field) => (
              <FormField
                label="Name"
                required
                error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
              >
                <Input
                  id="pipe-name"
                  placeholder="my-pipe"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                />
              </FormField>
            )}
          </form.Field>

          {/* Source */}
          <div className="flex flex-col gap-3 rounded-lg border border-border bg-bg-subtle p-4">
            <p className="text-xs font-semibold tracking-wider text-fg-muted uppercase">Source</p>

            <FormField label="Service">
              <ServiceTypeSelector
                services={SOURCE_SERVICES}
                selected={sourceService}
                onSelect={(id) => {
                  setSourceService(id)
                  form.setFieldValue("sourceArn", "")
                }}
              />
            </FormField>

            {sourceService === "dynamodb-streams" && (
              <form.Field name="sourceArn" validators={{ onChange: pipeSchema.shape.sourceArn }}>
                {(field) => (
                  <FormField
                    label="Table"
                    required
                    hint={
                      tables.length === 0
                        ? undefined
                        : "Search by name or paste a stream ARN directly."
                    }
                    error={
                      field.state.meta.isTouched ? field.state.meta.errors[0]?.message : undefined
                    }
                  >
                    {tables.length === 0 && !tablesLoading ? (
                      <div className="flex items-center gap-2 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-fg-muted">
                        <AlertCircle className="h-4 w-4 shrink-0 text-warning" />
                        No DynamoDB tables found. Create a table with streaming enabled first.
                      </div>
                    ) : (
                      <Combobox
                        value={arnToDisplayLabel(field.state.value, tables, queues)}
                        onChange={(v) => {
                          field.handleChange(v)
                          field.handleBlur()
                        }}
                        items={tables}
                        filterFn={(t, q) =>
                          t.tableName.toLowerCase().includes(q.toLowerCase()) ||
                          (t.latestStreamArn ?? "").toLowerCase().includes(q.toLowerCase())
                        }
                        getItemValue={(t) => t.latestStreamArn ?? t.tableArn}
                        isItemDisabled={(t) =>
                          !t.streamSpecification?.streamEnabled || !t.latestStreamArn
                            ? "Streams not enabled — enable DynamoDB Streams on this table first"
                            : undefined
                        }
                        renderItem={(t, { selected, disabled, disabledReason }) => (
                          <span className="flex flex-col gap-0.5">
                            <span className="flex items-center justify-between gap-2">
                              <span className="flex items-center gap-2">
                                <SERVICES.dynamodb.icon
                                  className={cn("h-3.5 w-3.5 shrink-0", SERVICES.dynamodb.color)}
                                />
                                <span>{t.tableName}</span>
                              </span>
                              {selected && !disabled && <Check className="h-3 w-3 text-accent" />}
                            </span>
                            {disabled && disabledReason && (
                              <span className="pl-5 text-xs text-fg-muted">{disabledReason}</span>
                            )}
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
                        placeholder={
                          tablesLoading ? "Loading tables…" : "Search tables or paste ARN…"
                        }
                        popoverWidth="w-full"
                      />
                    )}
                  </FormField>
                )}
              </form.Field>
            )}
          </div>

          {/* Flow arrow */}
          <div className="flex items-center gap-2 text-fg-subtle">
            <div className="h-px flex-1 bg-border" />
            <ArrowRight className="h-4 w-4" />
            <div className="h-px flex-1 bg-border" />
          </div>

          {/* Target */}
          <div className="flex flex-col gap-3 rounded-lg border border-border bg-bg-subtle p-4">
            <p className="text-xs font-semibold tracking-wider text-fg-muted uppercase">Target</p>

            <FormField label="Service">
              <ServiceTypeSelector
                services={TARGET_SERVICES}
                selected={targetService}
                onSelect={(id) => {
                  setTargetService(id)
                  form.setFieldValue("targetArn", "")
                }}
              />
            </FormField>

            {targetService === "sqs" && (
              <form.Field name="targetArn" validators={{ onChange: pipeSchema.shape.targetArn }}>
                {(field) => (
                  <FormField
                    label="Queue"
                    required
                    hint="Search by name or paste a queue ARN directly."
                    error={
                      field.state.meta.isTouched ? field.state.meta.errors[0]?.message : undefined
                    }
                  >
                    <Combobox
                      value={arnToDisplayLabel(field.state.value, tables, queues)}
                      onChange={(v) => {
                        field.handleChange(v)
                        field.handleBlur()
                      }}
                      items={queues}
                      filterFn={(q, query) =>
                        q.name.toLowerCase().includes(query.toLowerCase()) ||
                        q.arn.toLowerCase().includes(query.toLowerCase())
                      }
                      getItemValue={(q) => q.arn}
                      renderItem={(q, { selected }) => (
                        <span className="flex items-center justify-between gap-2">
                          <span className="flex items-center gap-2">
                            <SERVICES.sqs.icon
                              className={cn("h-3.5 w-3.5 shrink-0", SERVICES.sqs.color)}
                            />
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
                      placeholder={
                        queuesLoading ? "Loading queues…" : "Search queues or paste ARN…"
                      }
                      popoverWidth="w-full"
                    />
                  </FormField>
                )}
              </form.Field>
            )}
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={handleClose}>
              Cancel
            </Button>
            <form.Subscribe selector={(s) => [s.canSubmit, s.isSubmitting]}>
              {([canSubmit, isSubmitting]) => (
                <Button type="submit" disabled={!canSubmit || createMut.isPending}>
                  {isSubmitting || createMut.isPending ? (
                    <Spinner className="mr-1.5 h-3.5 w-3.5" />
                  ) : null}
                  Create Pipe
                </Button>
              )}
            </form.Subscribe>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── State badge variant ──────────────────────────────────────────────────────

function stateVariant(state: string): "default" | "success" | "warning" | "danger" | "accent" {
  switch (state) {
    case "RUNNING":
      return "success"
    case "CREATING":
    case "UPDATING":
      return "accent"
    case "DELETING":
    case "STOPPING":
      return "warning"
    case "STOPPED":
    case "CREATE_FAILED":
    case "UPDATE_FAILED":
    case "START_FAILED":
    case "STOP_FAILED":
      return "danger"
    default:
      return "default"
  }
}

// ─── PipeList ─────────────────────────────────────────────────────────────────

export function PipeList() {
  const qc = useQueryClient()
  const { toast } = useToast()
  const location = useLocation()
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [initialSourceArn, setInitialSourceArn] = useState("")
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  // React to hash changes via the router location — covers both initial load
  // and navigating here from another page (e.g. the DynamoDB schema tab).
  // Uses "adjust state during render" pattern to avoid setState in an effect.
  const [prevHash, setPrevHash] = useState<string | null>(null)
  if (location.hash !== prevHash) {
    setPrevHash(location.hash)
    const [name, qs = ""] = location.hash.split("?")
    if (name === "create") {
      const params = new URLSearchParams(qs)
      setInitialSourceArn(params.get("source") ?? "")
      setShowCreate(true)
    }
  }

  function openCreate() {
    setInitialSourceArn("")
    setShowCreate(true)
  }

  function closeCreate() {
    setShowCreate(false)
    setInitialSourceArn("")
    // Remove the hash via the router so refreshing doesn't re-open the dialog.
    if (location.hash.startsWith("create")) {
      void navigate({ hash: "", replace: true })
    }
  }

  const { data: pipes = [], isLoading, isFetching, refetch } = useQuery(pipeListQueryOptions())

  const deleteMut = useResourceMutation({
    options: deletePipeMutationOptions(),
    invalidateKeys: [pipeKeys.list()],
    successTitle: "Pipe deleted",
    successDescription: (name) => name,
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="EventBridge Pipes"
        description={`${pipes.length} pipe${pipes.length !== 1 ? "s" : ""}`}
        actions={
          <>
            <ServiceDocsButton
              service="pipes"
              label="Pipes"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={openCreate}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Pipe
            </Button>
          </>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : pipes.length === 0 ? (
        <EmptyState
          icon={<GitBranch className="h-10 w-10" />}
          title="No pipes yet"
          description="Create a pipe to route DynamoDB stream events to an SQS queue."
          action={
            <Button onClick={openCreate}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Pipe
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Source → Target</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Created</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {pipes.map((p) => (
              <TableRow key={p.Name}>
                <TableCell className="font-medium">{p.Name}</TableCell>
                <TableCell>
                  <span className="flex items-center gap-1.5 font-mono text-xs">
                    <span className="text-fg-default">{p.Source}</span>
                    <ArrowRight className="h-3 w-3 text-fg-muted" />
                    <span className="text-fg-default">{p.Target}</span>
                  </span>
                </TableCell>
                <TableCell>
                  <Badge variant={stateVariant(p.CurrentState ?? "")}>{p.CurrentState}</Badge>
                </TableCell>
                <TableCell className="text-sm text-fg-muted">
                  {p.CreationTime?.toLocaleString()}
                </TableCell>
                <TableCell>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="text-fg-muted hover:text-danger"
                    onClick={() => setDeleteTarget(p.Name)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {/* Create pipe dialog */}
      <CreatePipeDialog
        open={showCreate}
        onClose={closeCreate}
        initialSourceArn={initialSourceArn}
        onCreated={(name) => {
          void qc.invalidateQueries({ queryKey: pipeKeys.list() })
          toast({ title: "Pipe created", description: name, variant: "success" })
        }}
      />

      {/* Delete confirmation dialog */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete Pipe"
        description={`Are you sure you want to delete ${deleteTarget}? In-flight events will not be delivered.`}
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}
