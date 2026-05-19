import { useState, useEffect } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Trash2, RefreshCw, Plus, X } from "lucide-react"
import {
  kinesisStreamQueryOptions,
  kinesisKeys,
  deleteStreamMutationOptions,
} from "@/features/kinesis/data"
import { kinesis } from "@/services/api"
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
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Badge } from "@/components/ui/badge"
import { PageHeader, Spinner, CodeBlock } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { useToast } from "@/components/ui/toast"
import { cn } from "@/lib/utils"

interface Props {
  streamName: string
}

function statusVariant(status: string): "default" | "success" | "danger" | "warning" {
  switch (status.toUpperCase()) {
    case "ACTIVE":
      return "success"
    case "CREATING":
    case "UPDATING":
      return "warning"
    case "DELETING":
      return "danger"
    default:
      return "default"
  }
}

type Tab = "shards" | "tags" | "configuration"

export function StreamDetail({ streamName }: Props) {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [tab, setTab] = useState<Tab>("shards")
  const [showDelete, setShowDelete] = useState(false)
  const [showAddTag, setShowAddTag] = useState(false)

  const {
    data: stream,
    isLoading,
    isFetching,
    refetch,
  } = useQuery(kinesisStreamQueryOptions(streamName))

  const deleteMut = useMutation({
    ...deleteStreamMutationOptions(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: kinesisKeys.streams() })
      void navigate({ to: "/kinesis" })
      toast({ title: "Stream deleted", description: streamName })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  const addTagMut = useMutation({
    mutationFn: ({ key, value }: { key: string; value: string }) =>
      kinesis.addTags(streamName, { [key]: value }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: kinesisKeys.streamDetail(streamName) })
      setShowAddTag(false)
      toast({ title: "Tag added", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Add tag failed", description: err.message, variant: "danger" }),
  })

  const removeTagMut = useMutation({
    mutationFn: (key: string) => kinesis.removeTags(streamName, [key]),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: kinesisKeys.streamDetail(streamName) })
      toast({ title: "Tag removed" })
    },
    onError: (err: Error) =>
      toast({ title: "Remove tag failed", description: err.message, variant: "danger" }),
  })

  const retentionForm = useForm({
    defaultValues: { hours: 24 },
    onSubmit: async ({ value }) => {
      await kinesis.setRetention(streamName, value.hours)
      void qc.invalidateQueries({ queryKey: kinesisKeys.streamDetail(streamName) })
      toast({ title: "Retention updated", variant: "success" })
    },
  })

  useEffect(() => {
    if (stream?.retentionHours !== undefined) {
      retentionForm.reset({ hours: stream.retentionHours })
    }
  }, [stream?.retentionHours])

  const addTagForm = useForm({
    defaultValues: { key: "", value: "" },
    onSubmit: ({ value }) => addTagMut.mutate({ key: value.key, value: value.value }),
  })

  if (isLoading) {
    return (
      <div className="flex justify-center py-16">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!stream) return null

  const tags = Object.entries(stream.tags)

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={stream.name}
        description={stream.arn}
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" variant="danger" onClick={() => setShowDelete(true)}>
              <Trash2 className="mr-1.5 h-3.5 w-3.5" />
              Delete
            </Button>
          </>
        }
      />

      <ApplicationOwnershipBanner candidates={[stream.arn, stream.name]} />

      {/* Summary cards */}
      <div className="grid grid-cols-3 gap-3">
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="text-xs text-fg-muted">Status</div>
          <div className="mt-1">
            <Badge variant={statusVariant(stream.status)}>{stream.status}</Badge>
          </div>
        </div>
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="text-xs text-fg-muted">Open Shards</div>
          <div className="mt-1 text-2xl font-semibold">{stream.shardCount}</div>
        </div>
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="text-xs text-fg-muted">Retention</div>
          <div className="mt-1 text-2xl font-semibold">{stream.retentionHours}h</div>
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 border-b">
        {(["shards", "tags", "configuration"] as Tab[]).map((t) => (
          <button
            key={t}
            className={cn(
              "px-4 py-2 text-sm capitalize transition-colors",
              tab === t
                ? "border-b-2 border-accent font-medium text-fg"
                : "text-fg-muted hover:text-fg",
            )}
            onClick={() => setTab(t)}
          >
            {t}
          </button>
        ))}
      </div>

      {tab === "shards" && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Shard ID</TableHead>
              <TableHead>Starting Hash Key</TableHead>
              <TableHead>Ending Hash Key</TableHead>
              <TableHead>Starting Seq No</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {stream.shards.map((shard) => (
              <TableRow key={shard.shardId}>
                <TableCell className="font-mono text-xs">{shard.shardId}</TableCell>
                <TableCell className="max-w-30 truncate font-mono text-xs">
                  {shard.startingHashKey}
                </TableCell>
                <TableCell className="max-w-30 truncate font-mono text-xs">
                  {shard.endingHashKey}
                </TableCell>
                <TableCell className="font-mono text-xs">{shard.startingSeqNo}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {tab === "tags" && (
        <div className="flex flex-col gap-3">
          <div className="flex justify-end">
            <Button size="sm" onClick={() => setShowAddTag(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Add Tag
            </Button>
          </div>
          {tags.length === 0 ? (
            <p className="py-8 text-center text-sm text-fg-muted">No tags</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Key</TableHead>
                  <TableHead>Value</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {tags.map(([key, value]) => (
                  <TableRow key={key}>
                    <TableCell className="font-mono text-sm">{key}</TableCell>
                    <TableCell className="font-mono text-sm">{value}</TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-danger hover:text-danger"
                        onClick={() => removeTagMut.mutate(key)}
                        disabled={removeTagMut.isPending}
                      >
                        <X className="h-3.5 w-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      )}

      {tab === "configuration" && (
        <div className="flex flex-col gap-6">
          <div>
            <h3 className="mb-3 text-sm font-medium">Stream ARN</h3>
            <CodeBlock>{stream.arn}</CodeBlock>
          </div>
          <div>
            <h3 className="mb-3 text-sm font-medium">Retention Period</h3>
            <form
              className="flex items-end gap-3"
              onSubmit={(e) => {
                e.preventDefault()
                e.stopPropagation()
                void retentionForm.handleSubmit()
              }}
            >
              <retentionForm.Field
                name="hours"
                validators={{
                  onChange: z
                    .number()
                    .int()
                    .min(24, "Minimum 24h")
                    .max(8760, "Maximum 8760h (365 days)"),
                }}
              >
                {(field) => (
                  <FormRow>
                    <FormField
                      label="Hours"
                      htmlFor="retention-hours"
                      error={fieldError(field.state.meta.errors)}
                    >
                      <Input
                        id="retention-hours"
                        type="number"
                        min={24}
                        max={8760}
                        value={field.state.value}
                        onChange={(e) => field.handleChange(Number(e.target.value))}
                        className="w-32"
                      />
                    </FormField>
                  </FormRow>
                )}
              </retentionForm.Field>
              <Button type="submit" size="sm">
                Update
              </Button>
            </form>
          </div>
        </div>
      )}

      {/* Delete dialog */}
      <Dialog open={showDelete} onOpenChange={setShowDelete}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Stream</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-fg-muted">
            Delete <span className="font-mono font-semibold">{streamName}</span>? This action cannot
            be undone.
          </p>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setShowDelete(false)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={deleteMut.isPending}
              onClick={() => deleteMut.mutate(streamName)}
            >
              {deleteMut.isPending ? "Deleting…" : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add tag dialog */}
      <Dialog open={showAddTag} onOpenChange={setShowAddTag}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add Tag</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              e.stopPropagation()
              void addTagForm.handleSubmit()
            }}
            className="flex flex-col gap-4"
          >
            <addTagForm.Field
              name="key"
              validators={{ onChange: z.string().min(1, "Key is required") }}
            >
              {(field) => (
                <FormRow>
                  <FormField
                    label="Key"
                    htmlFor="tag-key"
                    error={fieldError(field.state.meta.errors)}
                  >
                    <Input
                      id="tag-key"
                      value={field.state.value}
                      onChange={(e) => field.handleChange(e.target.value)}
                      autoFocus
                    />
                  </FormField>
                </FormRow>
              )}
            </addTagForm.Field>
            <addTagForm.Field name="value">
              {(field) => (
                <FormRow>
                  <FormField label="Value" htmlFor="tag-value">
                    <Input
                      id="tag-value"
                      value={field.state.value}
                      onChange={(e) => field.handleChange(e.target.value)}
                    />
                  </FormField>
                </FormRow>
              )}
            </addTagForm.Field>
            <DialogFooter>
              <Button variant="ghost" type="button" onClick={() => setShowAddTag(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={addTagMut.isPending}>
                {addTagMut.isPending ? "Adding…" : "Add"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
