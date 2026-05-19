import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { DatabaseZap, Plus, Trash2, RefreshCw } from "lucide-react"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import {
  elasticacheClustersQueryOptions,
  elasticacheKeys,
  createClusterMutationOptions,
  deleteClusterMutationOptions,
} from "@/features/elasticache/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, fieldError } from "@/components/ui/form"
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
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { Combobox } from "@/components/ui/combobox"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { cn } from "@/lib/utils"

export function ClusterList() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: clusters = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(elasticacheClustersQueryOptions())

  const createMut = useResourceMutation({
    options: createClusterMutationOptions(),
    invalidateKeys: [elasticacheKeys.clusters()],
    successTitle: "Cache cluster created",
    successDescription: (opts) => opts.CacheClusterId,
    onSuccess: () => setShowCreate(false),
  })

  const deleteMut = useResourceMutation({
    options: deleteClusterMutationOptions(),
    invalidateKeys: [elasticacheKeys.clusters()],
    successTitle: "Cache cluster deleted",
    successDescription: (id) => id,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="ElastiCache Clusters"
        description={`${clusters.length} cluster${clusters.length !== 1 ? "s" : ""}`}
        actions={
          <div className="flex gap-2">
            <ServiceDocsButton
              service="elasticache"
              label="ElastiCache"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Cluster
            </Button>
          </div>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : clusters.length === 0 ? (
        <EmptyState
          icon={<DatabaseZap className="h-10 w-10" />}
          title="No cache clusters"
          description="Create a cache cluster to get started."
          action={
            <Button onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Cluster
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Cluster ID</TableHead>
              <TableHead>Engine</TableHead>
              <TableHead>Version</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Node Type</TableHead>
              <TableHead>Nodes</TableHead>
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {clusters.map((c) => (
              <TableRow key={c.CacheClusterId}>
                <TableCell className="font-medium">{c.CacheClusterId}</TableCell>
                <TableCell className="capitalize">{c.Engine}</TableCell>
                <TableCell className="text-sm text-fg-muted">{c.EngineVersion ?? "—"}</TableCell>
                <TableCell>
                  <ClusterStatusBadge status={c.CacheClusterStatus ?? ""} />
                </TableCell>
                <TableCell className="text-sm text-fg-muted">{c.CacheNodeType}</TableCell>
                <TableCell className="text-sm text-fg-muted">{c.NumCacheNodes}</TableCell>
                <TableCell>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="text-fg-muted hover:text-danger"
                    title="Delete"
                    onClick={() => setDeleteTarget(c.CacheClusterId ?? "")}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <CreateClusterDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(opts) => createMut.mutate(opts)}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete Cache Cluster"
        description={
          <>
            Permanently delete <strong>{deleteTarget}</strong>? This action cannot be undone.
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}

// ─── Status badge ─────────────────────────────────────────────────────────

function ClusterStatusBadge({ status }: { status: string }) {
  const variant =
    status === "available"
      ? "success"
      : status === "creating" || status === "modifying"
        ? "warning"
        : status === "deleting" || status === "failed"
          ? "danger"
          : "default"
  return <Badge variant={variant}>{status}</Badge>
}

// ─── Create Cluster Dialog ────────────────────────────────────────────────

const createSchema = z.object({
  CacheClusterId: z
    .string()
    .min(1, "Cluster ID is required")
    .regex(/^[a-zA-Z][a-zA-Z0-9-]*$/, "Must start with a letter, alphanumeric and hyphens only"),
  Engine: z.string().min(1, "Engine is required"),
  CacheNodeType: z.string().min(1, "Node type is required"),
  NumCacheNodes: z.number().int().min(1, "Min 1 node").max(20, "Max 20 nodes"),
})

const NODE_TYPES = [
  { value: "cache.t3.micro" },
  { value: "cache.t3.small" },
  { value: "cache.t3.medium" },
  { value: "cache.m5.large" },
  { value: "cache.m5.xlarge" },
]

function CreateClusterDialog({
  open,
  onClose,
  isPending,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  isPending: boolean
  onSubmit: (opts: {
    CacheClusterId: string
    Engine: string
    CacheNodeType: string
    NumCacheNodes: number
  }) => void
}) {
  const form = useForm({
    validators: { onChange: createSchema },
    defaultValues: {
      CacheClusterId: "",
      Engine: "redis",
      CacheNodeType: "cache.t3.micro",
      NumCacheNodes: 1,
    },
    onSubmit: ({ value }) => onSubmit(value),
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) {
          onClose()
          form.reset()
        }
      }}
    >
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Create Cache Cluster</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void form.handleSubmit()
          }}
        >
          <DialogBody className="space-y-4">
            <form.Field name="CacheClusterId">
              {(field) => (
                <FormField
                  label="Cluster ID"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    placeholder="my-cache-cluster"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
            <form.Field name="Engine">
              {(field) => (
                <FormField
                  label="Engine"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Combobox<{ value: string }>
                    value={field.state.value}
                    onChange={(v) => field.handleChange(v)}
                    items={[{ value: "redis" }, { value: "memcached" }]}
                    filterFn={(item, q) => item.value.toLowerCase().includes(q.toLowerCase())}
                    getItemValue={(item) => item.value}
                    renderItem={(item) => item.value}
                    placeholder="Select engine…"
                  />
                </FormField>
              )}
            </form.Field>
            <div className="grid grid-cols-2 gap-4">
              <form.Field name="CacheNodeType">
                {(field) => (
                  <FormField
                    label="Node Type"
                    error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                  >
                    <Combobox<{ value: string }>
                      value={field.state.value}
                      onChange={(v) => field.handleChange(v)}
                      items={NODE_TYPES}
                      filterFn={(item, q) => item.value.toLowerCase().includes(q.toLowerCase())}
                      getItemValue={(item) => item.value}
                      renderItem={(item) => item.value}
                      allowCustom
                      placeholder="Select node type…"
                    />
                  </FormField>
                )}
              </form.Field>
              <form.Field name="NumCacheNodes">
                {(field) => (
                  <FormField
                    label="Number of Nodes"
                    error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                  >
                    <Input
                      type="number"
                      min={1}
                      max={20}
                      value={field.state.value}
                      onChange={(e) => field.handleChange(parseInt(e.target.value) || 1)}
                      onBlur={field.handleBlur}
                    />
                  </FormField>
                )}
              </form.Field>
            </div>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" type="button" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={isPending}>
              {isPending && <Spinner className="mr-2" />}
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
