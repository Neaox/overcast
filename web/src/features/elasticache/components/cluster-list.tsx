import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { DatabaseZap } from "lucide-react"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { DockerBanner } from "@/components/docker-banner"
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
  Dialog,
  DialogBody,
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
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"
import { Combobox } from "@/components/ui/combobox"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"

interface ClusterListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function ClusterList({ sort, onSortChange }: ClusterListProps = {}) {
  const [showCreate, setShowCreate] = useState(false)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: clusters = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(elasticacheClustersQueryOptions())

  const [deleteTarget, setDeleteTarget] = useState<(typeof clusters)[number]>()

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
    <ResourceListPage
      title="ElastiCache Clusters"
      count={clusters.length}
      actions={
        <>
          <ServiceDocsButton
            service="elasticache"
            label="ElastiCache"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create cluster</CreateAction>
        </>
      }
    >
      <DockerBanner forService="elasticache" />

      <ResourceTable
        query={{ data: clusters, isLoading, error }}
        noun="cache clusters"
        emptyIcon={DatabaseZap}
        emptyTitle="No cache clusters"
        emptyDescription="Create a cache cluster to get started."
        emptyAction={
          <CreateAction onClick={() => setShowCreate(true)}>Create cluster</CreateAction>
        }
        errorTitle="Failed to load cache clusters"
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(c) => c.CacheClusterId ?? ""}
        columns={[
          {
            id: "cluster",
            header: "Cluster ID",
            sortValue: (c) => c.CacheClusterId,
            cell: (c) => <ResourceName icon={DatabaseZap} name={c.CacheClusterId} />,
          },
          { header: "Engine", cellClassName: "capitalize", cell: (c) => c.Engine },
          {
            header: "Version",
            cellClassName: "text-fg-muted",
            cell: (c) => c.EngineVersion ?? "—",
          },
          {
            header: "Status",
            cell: (c) => <ClusterStatusBadge status={c.CacheClusterStatus ?? ""} />,
          },
          { header: "Node type", cellClassName: "text-fg-muted", cell: (c) => c.CacheNodeType },
          {
            id: "nodes",
            header: "Nodes",
            cellClassName: "text-fg-muted",
            sortValue: (c) => c.NumCacheNodes,
            cell: (c) => c.NumCacheNodes,
          },
        ]}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (v) => !v && setDeleteTarget(undefined),
          mutation: deleteMut,
          getVars: (c) => c.CacheClusterId ?? "",
          label: (c) => c.CacheClusterId ?? "",
          noun: "cache cluster",
          title: "Delete Cache Cluster",
          actionLabel: (c) => `Delete ${c.CacheClusterId ?? "cluster"}`,
          description: (c) => (
            <>
              Permanently delete <strong>{c.CacheClusterId}</strong>? This action cannot be undone.
            </>
          ),
        }}
      />

      <CreateClusterDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(opts) => createMut.mutate(opts)}
      />
    </ResourceListPage>
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
          <DialogTitle>Create cache cluster</DialogTitle>
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
