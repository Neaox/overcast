import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { Boxes, Plus, Trash2, RefreshCw } from "lucide-react"
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
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { cn } from "@/lib/utils"
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
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="ECS Clusters"
        description={`${clusters.length} cluster${clusters.length !== 1 ? "s" : ""}`}
        actions={
          <>
            <ServiceDocsButton
              service="ecs"
              label="ECS"
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
          </>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : clusters.length === 0 ? (
        <EmptyState
          icon={<Boxes className="h-10 w-10" />}
          title="No clusters yet"
          description="Create a cluster to start running containers."
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
              <TableHead>Cluster Name</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Running Tasks</TableHead>
              <TableHead>Active Services</TableHead>
              <TableHead>Pending Tasks</TableHead>
              <TableHead>Container Instances</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {clusters.map((c) => (
              <TableRow
                key={c.clusterArn}
                className="cursor-pointer"
                onClick={() =>
                  navigate({ to: "/ecs/$cluster", params: { cluster: c.clusterName } })
                }
              >
                <TableCell className="font-medium">{c.clusterName}</TableCell>
                <TableCell>
                  <StatusBadge status={c.status} />
                </TableCell>
                <TableCell>{c.runningTasksCount}</TableCell>
                <TableCell>{c.activeServicesCount}</TableCell>
                <TableCell>{c.pendingTasksCount}</TableCell>
                <TableCell>{c.registeredContainerInstancesCount}</TableCell>
                <TableCell>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="text-fg-muted hover:text-danger"
                    onClick={(e) => {
                      e.stopPropagation()
                      setDeleteTarget(c.clusterName)
                    }}
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
    </div>
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
          <DialogTitle>Create Cluster</DialogTitle>
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
