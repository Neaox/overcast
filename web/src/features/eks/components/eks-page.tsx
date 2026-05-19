import { useState } from "react"
import { useForm } from "@tanstack/react-form"
import { useQuery } from "@tanstack/react-query"
import { RefreshCw, Boxes, Plus } from "lucide-react"
import { z } from "zod"
import {
  createEksClusterMutationOptions,
  eksClustersQueryOptions,
  eksKeys,
} from "@/features/eks/data"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

export function EksPage() {
  const [showCreate, setShowCreate] = useState(false)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()
  const {
    data: clusters = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(eksClustersQueryOptions())

  const createMut = useResourceMutation({
    options: createEksClusterMutationOptions(),
    invalidateKeys: [eksKeys.clusters()],
    successTitle: "Cluster created",
    successDescription: (name) => name,
    onSuccess: () => setShowCreate(false),
  })

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="EKS Clusters"
        description={`${clusters.length} cluster${clusters.length !== 1 ? "s" : ""}`}
        actions={
          <div className="flex gap-2">
            <ServiceDocsButton
              service="eks"
              label="EKS"
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
          icon={<Boxes className="h-6 w-6" />}
          title="No clusters"
          description="Create a cluster to start working with EKS resources."
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
              <TableHead>Name</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Version</TableHead>
              <TableHead>Endpoint</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {clusters.map((c) => (
              <TableRow key={c.arn || c.name}>
                <TableCell className="font-medium">{c.name}</TableCell>
                <TableCell>
                  <Badge
                    variant={
                      c.status === "ACTIVE"
                        ? "success"
                        : c.status === "CREATING" || c.status === "UPDATING"
                          ? "warning"
                          : c.status === "FAILED" || c.status === "DELETING"
                            ? "danger"
                            : "default"
                    }
                  >
                    {c.status}
                  </Badge>
                </TableCell>
                <TableCell className="text-fg-muted">{c.version || "-"}</TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">
                  {c.endpoint || "-"}
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
    </div>
  )
}

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
          <DialogTitle>Create EKS Cluster</DialogTitle>
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
                    placeholder="my-eks-cluster"
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
