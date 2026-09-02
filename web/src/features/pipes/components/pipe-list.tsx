import { useState, useEffect, useMemo } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { GitBranch, ArrowRight, Check, AlertCircle } from "lucide-react"
import { SERVICES } from "@/lib/service-registry"
import {
  pipeListQueryOptions,
  pipeWiringQueryOptions,
  pipeKeys,
  createPipeMutationOptions,
  deletePipeMutationOptions,
} from "@/features/pipes/data"
import type { PipeWiring } from "@/services/api/pipes"
import { dynamoTablesQueryOptions } from "@/features/dynamodb/data"
import { sqsQueuesQueryOptions } from "@/features/sqs/data"
import { kinesisStreamsQueryOptions } from "@/features/kinesis/data"
import { lambdaFunctionsQueryOptions } from "@/features/lambda/data"
import { snsTopicsQueryOptions } from "@/features/sns/data"
import { sfnStateMachinesQueryOptions } from "@/features/stepfunctions/data"
import { ebBusesQueryOptions } from "@/features/eventbridge/data"
import { useLocation, useNavigate } from "@tanstack/react-router"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, fieldError } from "@/components/ui/form"
import { Combobox } from "@/components/ui/combobox"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListPage,
  ResourceName,
} from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { useToast } from "@/components/ui/toast"
import type { DynamoTable, SQSQueue } from "@/types"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"

// ─── Candidate resources ──────────────────────────────────────────────────────
//
// Every leg of a pipe is an ARN, so the dialog offers one list of candidate
// resources per leg rather than a service selector plus a per-service picker.
// The emulator refuses a combination it cannot run, so the lists here mirror
// what internal/services/pipes/wiring.go accepts — see docs/services/pipes.md.

interface Candidate {
  arn: string
  label: string
  service: keyof typeof SERVICES
  /** Set when the resource exists but cannot be used, e.g. streams disabled. */
  disabledReason?: string
}

function candidateRow(c: Candidate, selected: boolean, disabledReason?: string) {
  const Icon = SERVICES[c.service].icon
  return (
    <span className="flex flex-col gap-0.5">
      <span className="flex items-center justify-between gap-2">
        <span className="flex items-center gap-2">
          <Icon className={cn("h-3.5 w-3.5 shrink-0", SERVICES[c.service].color)} />
          <span>{c.label}</span>
        </span>
        {selected && !disabledReason && <Check className="h-3 w-3 text-accent" />}
      </span>
      {disabledReason && <span className="pl-5 text-xs text-fg-muted">{disabledReason}</span>}
    </span>
  )
}

/** A combobox over candidate ARNs, with free-text entry for anything else. */
function ArnPicker({
  candidates,
  value,
  loading,
  placeholder,
  onChange,
  onBlur,
}: {
  candidates: Candidate[]
  value: string
  loading: boolean
  placeholder: string
  onChange: (v: string) => void
  onBlur: () => void
}) {
  const label = candidates.find((c) => c.arn === value)?.label ?? value
  return (
    <Combobox
      value={label}
      onChange={(v) => {
        onChange(v)
        onBlur()
      }}
      items={candidates}
      filterFn={(c, q) =>
        c.label.toLowerCase().includes(q.toLowerCase()) ||
        c.arn.toLowerCase().includes(q.toLowerCase())
      }
      getItemValue={(c) => c.arn}
      isItemDisabled={(c) => c.disabledReason}
      renderItem={(c, { selected, disabledReason }) => candidateRow(c, selected, disabledReason)}
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
      placeholder={loading ? "Loading resources…" : placeholder}
      popoverWidth="w-full"
    />
  )
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
  sourceArn: z.string().min(1, "Select a source"),
  targetArn: z.string().min(1, "Select a target"),
  enrichmentArn: z.string(),
})

export function CreatePipeDialog({
  open,
  onClose,
  onCreated,
  initialSourceArn,
}: CreatePipeDialogProps) {
  const { data: tables = [], isLoading: tablesLoading } = useQuery({
    ...dynamoTablesQueryOptions(),
    enabled: open,
  })
  const { data: queues = [], isLoading: queuesLoading } = useQuery({
    ...sqsQueuesQueryOptions(),
    enabled: open,
  })
  const { data: streams = [], isLoading: streamsLoading } = useQuery({
    ...kinesisStreamsQueryOptions(),
    enabled: open,
  })
  const { data: functions = [], isLoading: functionsLoading } = useQuery({
    ...lambdaFunctionsQueryOptions(),
    enabled: open,
  })
  const { data: topics = [] } = useQuery({ ...snsTopicsQueryOptions(), enabled: open })
  const { data: stateMachines = [] } = useQuery({
    ...sfnStateMachinesQueryOptions(),
    enabled: open,
  })
  const { data: buses = [] } = useQuery({ ...ebBusesQueryOptions(), enabled: open })

  const resourcesLoading = tablesLoading || queuesLoading || streamsLoading || functionsLoading

  // Candidate lists mirror what the emulator accepts for each leg.
  const sourceCandidates: Candidate[] = useMemo(
    () => [
      ...tables.map((t: DynamoTable) => ({
        arn: t.latestStreamArn ?? t.tableArn,
        label: t.tableName,
        service: "dynamodb" as const,
        disabledReason:
          !t.streamSpecification?.streamEnabled || !t.latestStreamArn
            ? "Streams not enabled — enable DynamoDB Streams on this table first"
            : undefined,
      })),
      ...streams.map((s) => ({ arn: s.arn, label: s.name, service: "kinesis" as const })),
      ...queues.map((q: SQSQueue) => ({ arn: q.arn, label: q.name, service: "sqs" as const })),
    ],
    [tables, streams, queues],
  )

  const targetCandidates: Candidate[] = useMemo(
    () => [
      ...queues.map((q: SQSQueue) => ({ arn: q.arn, label: q.name, service: "sqs" as const })),
      ...functions.map((f) => ({
        arn: f.FunctionArn ?? "",
        label: f.FunctionName ?? "",
        service: "lambda" as const,
      })),
      ...stateMachines.map((sm) => ({
        arn: sm.stateMachineArn ?? "",
        label: sm.name ?? "",
        service: "stepfunctions" as const,
      })),
      ...topics.map((t) => ({
        arn: t.TopicArn ?? "",
        label: (t.TopicArn ?? "").split(":").pop() ?? "",
        service: "sns" as const,
      })),
      ...streams.map((s) => ({ arn: s.arn, label: s.name, service: "kinesis" as const })),
      ...buses.map((b) => ({
        arn: b.Arn ?? "",
        label: b.Name ?? "",
        service: "eventbridge" as const,
      })),
    ],
    [queues, functions, stateMachines, topics, streams, buses],
  )

  const enrichmentCandidates: Candidate[] = useMemo(
    () =>
      functions.map((f) => ({
        arn: f.FunctionArn ?? "",
        label: f.FunctionName ?? "",
        service: "lambda" as const,
      })),
    [functions],
  )

  const createMut = useResourceMutation({
    options: createPipeMutationOptions(),
    invalidateKeys: [pipeKeys.list(), pipeKeys.wiring()],
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
      enrichmentArn: "",
    },
    onSubmit: ({ value }) => {
      createMut.mutate({
        name: value.name,
        sourceArn: value.sourceArn,
        targetArn: value.targetArn,
        enrichmentArn: value.enrichmentArn,
      })
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
    setTimeout(() => form.reset(), 150)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Create pipe</DialogTitle>
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
            <p className={cn(fieldLabel, "text-fg-muted")}>Source</p>

            <form.Field name="sourceArn" validators={{ onChange: pipeSchema.shape.sourceArn }}>
              {(field) => (
                <FormField
                  label="Resource"
                  required
                  hint="A DynamoDB stream, a Kinesis stream or an SQS queue. Search by name or paste an ARN."
                  error={
                    field.state.meta.isTouched ? field.state.meta.errors[0]?.message : undefined
                  }
                >
                  {sourceCandidates.length === 0 && !resourcesLoading ? (
                    <div className="flex items-center gap-2 rounded-md border border-warning/40 bg-warning-muted px-3 py-2 text-sm text-fg-muted">
                      <AlertCircle className="h-4 w-4 shrink-0 text-warning" />
                      No sources found. Create a DynamoDB table with streams enabled, a Kinesis
                      stream or an SQS queue first.
                    </div>
                  ) : (
                    <ArnPicker
                      candidates={sourceCandidates}
                      value={field.state.value}
                      loading={resourcesLoading}
                      placeholder="Search sources or paste ARN…"
                      onChange={field.handleChange}
                      onBlur={field.handleBlur}
                    />
                  )}
                </FormField>
              )}
            </form.Field>
          </div>

          {/* Flow arrow */}
          <div className="flex items-center gap-2 text-fg-subtle">
            <div className="h-px flex-1 bg-border" />
            <ArrowRight className="h-4 w-4" />
            <div className="h-px flex-1 bg-border" />
          </div>

          {/* Enrichment (optional) */}
          <div className="flex flex-col gap-3 rounded-lg border border-border bg-bg-subtle p-4">
            <p className={cn(fieldLabel, "text-fg-muted")}>Enrichment (optional)</p>

            <form.Field name="enrichmentArn">
              {(field) => (
                <FormField
                  label="Lambda function"
                  hint="Invoked between source and target; its return value replaces the batch."
                >
                  <ArnPicker
                    candidates={enrichmentCandidates}
                    value={field.state.value}
                    loading={functionsLoading}
                    placeholder="None — skip enrichment"
                    onChange={field.handleChange}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
          </div>

          {/* Flow arrow */}
          <div className="flex items-center gap-2 text-fg-subtle">
            <div className="h-px flex-1 bg-border" />
            <ArrowRight className="h-4 w-4" />
            <div className="h-px flex-1 bg-border" />
          </div>

          {/* Target */}
          <div className="flex flex-col gap-3 rounded-lg border border-border bg-bg-subtle p-4">
            <p className={cn(fieldLabel, "text-fg-muted")}>Target</p>

            <form.Field name="targetArn" validators={{ onChange: pipeSchema.shape.targetArn }}>
              {(field) => (
                <FormField
                  label="Resource"
                  required
                  hint="Lambda, SQS, SNS, Step Functions, Kinesis, Firehose or an EventBridge bus."
                  error={
                    field.state.meta.isTouched ? field.state.meta.errors[0]?.message : undefined
                  }
                >
                  <ArnPicker
                    candidates={targetCandidates}
                    value={field.state.value}
                    loading={resourcesLoading}
                    placeholder="Search targets or paste ARN…"
                    onChange={field.handleChange}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
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
                  Create pipe
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

  const {
    data: pipes = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(pipeListQueryOptions())

  const [deleteTarget, setDeleteTarget] = useState<(typeof pipes)[number]>()

  // The wiring feed is what stops an unsupported combination being presented as
  // if it were running: a pipe stored by an older build can read RUNNING while
  // nothing at all flows through it.
  const { data: wiring = [], refetch: refetchWiring } = useQuery(pipeWiringQueryOptions())
  const wiringByName = useMemo(() => {
    const map = new Map<string, PipeWiring>()
    for (const w of wiring) map.set(w.Name, w)
    return map
  }, [wiring])

  const deleteMut = useResourceMutation({
    options: deletePipeMutationOptions(),
    invalidateKeys: [pipeKeys.list(), pipeKeys.wiring()],
    successTitle: "Pipe deleted",
    successDescription: (name) => name,
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <ResourceListPage
      title="EventBridge Pipes"
      count={pipes.length}
      actions={
        <>
          <ServiceDocsButton
            service="pipes"
            label="Pipes"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction
            isFetching={isFetching}
            onClick={() => {
              void refetch()
              void refetchWiring()
            }}
          />
          <CreateAction onClick={openCreate}>Create pipe</CreateAction>
        </>
      }
    >
      <ResourceTable
        query={{ data: pipes, isLoading, error }}
        noun="pipes"
        emptyIcon={GitBranch}
        emptyTitle="No pipes yet"
        emptyDescription="Create a pipe to route DynamoDB, Kinesis or SQS records to a target."
        emptyAction={<CreateAction onClick={openCreate}>Create pipe</CreateAction>}
        errorTitle="Failed to load pipes"
        // ListPipes returns the emulator's storage order; A→Z is what a name
        // column implies. Created is sortable for "what did I just make".
        defaultSort={{ id: "name", desc: false }}
        // Four columns, all primary — a Columns menu here is clutter, not an offer.
        columnToggle={false}
        rowKey={(p) => p.Name ?? ""}
        onRowClick={(p) => navigate({ to: "/pipes/$pipeName", params: { pipeName: p.Name ?? "" } })}
        columns={[
          {
            id: "name",
            header: "Name",
            sortValue: (p) => p.Name,
            cell: (p) => <ResourceName icon={GitBranch} name={p.Name} />,
          },
          {
            header: "Source → target",
            cell: (p) => {
              const w = wiringByName.get(p.Name ?? "")
              return (
                <span className="flex items-center gap-1.5 font-mono text-xs">
                  <span className="text-fg" title={p.Source}>
                    {w?.SourceType ?? p.Source}
                  </span>
                  {w?.Enrichment ? (
                    <>
                      <ArrowRight className="h-3 w-3 text-fg-muted" />
                      <span className="text-fg" title={w.Enrichment}>
                        {w.EnrichmentType}
                      </span>
                    </>
                  ) : null}
                  <ArrowRight className="h-3 w-3 text-fg-muted" />
                  <span className="text-fg" title={p.Target}>
                    {w?.TargetType ?? p.Target}
                  </span>
                </span>
              )
            },
          },
          {
            header: "State",
            cell: (p) => {
              const w = wiringByName.get(p.Name ?? "")
              // A pipe the emulator cannot run must not read RUNNING — that is
              // the failure this view exists to make visible.
              return w && !w.Wired ? (
                <Badge variant="danger" title={w.Reason}>
                  not wired
                </Badge>
              ) : (
                <Badge variant={stateVariant(p.CurrentState ?? "")}>{p.CurrentState}</Badge>
              )
            },
          },
          {
            header: "Created",
            cellClassName: "text-fg-muted",
            sortValue: (p) => p.CreationTime,
            cell: (p) => p.CreationTime?.toLocaleString(),
          },
        ]}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (v) => !v && setDeleteTarget(undefined),
          mutation: deleteMut,
          getId: (p) => p.Name ?? "",
          label: (p) => p.Name ?? "pipe",
          noun: "pipe",
          title: "Delete Pipe",
          description: (p) =>
            `Are you sure you want to delete ${p.Name}? In-flight events will not be delivered.`,
        }}
      />

      {/* Create pipe dialog */}
      <CreatePipeDialog
        open={showCreate}
        onClose={closeCreate}
        initialSourceArn={initialSourceArn}
        onCreated={(name) => {
          void qc.invalidateQueries({ queryKey: pipeKeys.all() })
          toast({ title: "Pipe created", description: name, variant: "success" })
        }}
      />
    </ResourceListPage>
  )
}
