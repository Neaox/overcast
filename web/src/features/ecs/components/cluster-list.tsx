import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { Boxes, Eye } from "lucide-react"
import {
  ecsClustersQueryOptions,
  ecsKeys,
  createClusterMutationOptions,
  deleteClusterMutationOptions,
} from "@/features/ecs/data"
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
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { DockerBanner } from "@/components/docker-banner"
import type { EcsCluster } from "@/types"

export function ClusterList() {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<EcsCluster>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: clusters = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(ecsClustersQueryOptions())

  const createMut = useResourceMutation({
    options: createClusterMutationOptions(),
    invalidateKeys: [ecsKeys.clusters()],
    successTitle: "Cluster created",
    successDescription: (name) => name,
    onSuccess: () => setShowCreate(false),
  })

  const deleteMut = useResourceMutation({
    options: deleteClusterMutationOptions(),
    invalidateKeys: [ecsKeys.clusters()],
    successTitle: "Cluster deleted",
    successDescription: (name) => name,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <ResourceListPage
      title="ECS Clusters"
      count={clusters.length}
      actions={
        <>
          <ServiceDocsButton
            service="ecs"
            label="ECS"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create cluster</CreateAction>
        </>
      }
    >
      <DockerBanner forService="ecs" />

      <ResourceTable
        query={{ data: clusters, isLoading, error }}
        noun="clusters"
        emptyIcon={Boxes}
        emptyTitle="No clusters yet"
        emptyDescription="Create a cluster to start running containers."
        emptyAction={
          <CreateAction onClick={() => setShowCreate(true)}>Create cluster</CreateAction>
        }
        errorTitle="Failed to load clusters"
        rowKey={(c) => c.clusterArn}
        onRowClick={(c) => navigate({ to: "/ecs/$cluster", params: { cluster: c.clusterName } })}
        columns={[
          {
            header: "Cluster name",
            sortValue: (c) => c.clusterName,
            cell: (c) => <ResourceName icon={Boxes} name={c.clusterName} />,
          },
          { header: "Status", cell: (c) => <StatusBadge status={c.status} /> },
          {
            header: "Running tasks",
            sortValue: (c) => c.runningTasksCount,
            cell: (c) => c.runningTasksCount,
          },
          {
            header: "Active services",
            sortValue: (c) => c.activeServicesCount,
            cell: (c) => c.activeServicesCount,
          },
          {
            header: "Pending tasks",
            sortValue: (c) => c.pendingTasksCount,
            cell: (c) => c.pendingTasksCount,
          },
          {
            header: "Container instances",
            sortValue: (c) => c.registeredContainerInstancesCount,
            cell: (c) => c.registeredContainerInstancesCount,
          },
        ]}
        rowActions={(c) => (
          <RowAction
            label={`View ${c.clusterName}`}
            onClick={() => navigate({ to: "/ecs/$cluster", params: { cluster: c.clusterName } })}
          >
            <Eye className="h-3.5 w-3.5" />
          </RowAction>
        )}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getVars: (c) => c.clusterName,
          label: (c) => c.clusterName,
          noun: "cluster",
          title: "Delete Cluster",
          description: (c) => (
            <>
              Permanently delete cluster <strong>{c.clusterName}</strong>?
            </>
          ),
        }}
      />

      <CreateClusterDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(name) => createMut.mutate(name)}
      />
    </ResourceListPage>
  )
}

// ─── Status badge ─────────────────────────────────────────────────────────

function StatusBadge({ status }: { status: string }) {
  const variant =
    status === "ACTIVE"
      ? "success"
      : status === "PROVISIONING"
        ? "warning"
        : status === "INACTIVE"
          ? "default"
          : "default"
  return <Badge variant={variant}>{status}</Badge>
}

// ─── Create cluster dialog ────────────────────────────────────────────────

const createSchema = z.object({
  name: z
    .string()
    .min(1, "Name is required")
    .regex(/^[a-zA-Z0-9_-]+$/, "Only letters, numbers, hyphens, and underscores"),
})

function CreateClusterDialog({
  open,
  onClose,
  onSubmit,
  isPending,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (name: string) => void
  isPending: boolean
}) {
  const form = useForm({
    validators: { onChange: createSchema },
    defaultValues: { name: "" },
    onSubmit: ({ value }) => onSubmit(value.name),
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
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create cluster</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void form.handleSubmit()
          }}
        >
          <DialogBody>
            <form.Field name="name">
              {(field) => (
                <FormField
                  label="Cluster Name"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    placeholder="my-cluster"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
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
