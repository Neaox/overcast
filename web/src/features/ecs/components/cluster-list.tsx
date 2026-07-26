import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { Boxes, Eye, Trash2 } from "lucide-react"
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
import { QueryListState, Spinner, EmptyState } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
} from "@/components/ui/resource-list-page"
import { Badge } from "@/components/ui/badge"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"

export function ClusterList() {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
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
      <ResourceListCard>
        {isLoading || clusters.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={clusters.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<Boxes className="h-10 w-10" />}
                title="No clusters yet"
                description="Create a cluster to start running containers."
                action={
                  <CreateAction onClick={() => setShowCreate(true)}>Create cluster</CreateAction>
                }
              />
            }
            errorTitle="Failed to load clusters"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Cluster name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Running tasks</TableHead>
                <TableHead>Active services</TableHead>
                <TableHead>Pending tasks</TableHead>
                <TableHead>Container instances</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {clusters.map((c) => (
                <TableRow
                  key={c.clusterArn}
                  onClick={() =>
                    navigate({ to: "/ecs/$cluster", params: { cluster: c.clusterName } })
                  }
                >
                  <TableCell>
                    <ResourceName icon={Boxes} name={c.clusterName} />
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={c.status} />
                  </TableCell>
                  <TableCell className="font-mono text-xs">{c.runningTasksCount}</TableCell>
                  <TableCell className="font-mono text-xs">{c.activeServicesCount}</TableCell>
                  <TableCell className="font-mono text-xs">{c.pendingTasksCount}</TableCell>
                  <TableCell className="font-mono text-xs">
                    {c.registeredContainerInstancesCount}
                  </TableCell>
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <RowActions>
                      <RowAction
                        label={`View ${c.clusterName}`}
                        onClick={() =>
                          navigate({ to: "/ecs/$cluster", params: { cluster: c.clusterName } })
                        }
                      >
                        <Eye className="h-3.5 w-3.5" />
                      </RowAction>
                      <RowAction
                        label={`Delete ${c.clusterName}`}
                        tone="danger"
                        onClick={() => setDeleteTarget(c.clusterName)}
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

      <CreateClusterDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(name) => createMut.mutate(name)}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete Cluster"
        description={
          <>
            Permanently delete cluster <strong>{deleteTarget}</strong>?
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
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
