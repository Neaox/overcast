import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Radio, Plus, Trash2, RefreshCw } from "lucide-react"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import {
  mskClustersQueryOptions,
  mskKeys,
  createMSKClusterMutationOptions,
  deleteMSKClusterMutationOptions,
} from "@/features/msk/data"
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
import type { ClusterInfo } from "@/services/api/msk"

export function ClusterList() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ClusterInfo>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: clusters = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(mskClustersQueryOptions())

  const createMut = useResourceMutation({
    options: createMSKClusterMutationOptions(),
    invalidateKeys: [mskKeys.clusters()],
    successTitle: "Kafka cluster created",
    successDescription: (opts) => opts.clusterName,
    onSuccess: () => setShowCreate(false),
  })

  const deleteMut = useResourceMutation({
    options: deleteMSKClusterMutationOptions(),
    invalidateKeys: [mskKeys.clusters()],
    successTitle: "Kafka cluster deleted",
    successDescription: (arn) => arn,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="MSK Clusters"
        description={`${clusters.length} cluster${clusters.length !== 1 ? "s" : ""}`}
        actions={
          <div className="flex gap-2">
            <ServiceDocsButton
              service="msk"
              label="MSK"
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
          icon={<Radio className="h-10 w-10" />}
          title="No Kafka clusters"
          description="Create a cluster to get started. Redpanda will be launched automatically."
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
              <TableHead>Kafka Version</TableHead>
              <TableHead>Brokers</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {clusters.map((c) => (
              <TableRow key={c.ClusterArn}>
                <TableCell className="font-medium">{c.ClusterName}</TableCell>
                <TableCell className="text-sm text-fg-muted">{c.CurrentBrokerSoftwareInfo?.KafkaVersion ?? "—"}</TableCell>
                <TableCell className="text-sm text-fg-muted">{c.NumberOfBrokerNodes}</TableCell>
                <TableCell>
                  <ClusterStatusBadge status={c.State ?? ""} />
                </TableCell>
                <TableCell>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="text-fg-muted hover:text-danger"
                    title="Delete"
                    onClick={() => setDeleteTarget(c)}
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
        title="Delete Kafka Cluster"
        description={
          <>
            Permanently delete <strong>{deleteTarget?.ClusterName}</strong>? The Redpanda container
            will be stopped. This action cannot be undone.
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget?.ClusterArn && deleteMut.mutate(deleteTarget.ClusterArn)}
      />
    </div>
  )
}

// ─── Status badge ─────────────────────────────────────────────────────────

function ClusterStatusBadge({ status }: { status: string }) {
  const variant =
    status === "ACTIVE"
      ? "success"
      : status === "CREATING"
        ? "warning"
        : status === "DELETING"
          ? "warning"
          : status === "FAILED" || status === "stopped"
            ? "danger"
            : "default"
  return <Badge variant={variant}>{status}</Badge>
}

// ─── Create Cluster Dialog ────────────────────────────────────────────────

const createSchema = z.object({
  clusterName: z
    .string()
    .min(1, "Cluster name is required")
    .regex(/^[a-zA-Z0-9-]+$/, "Alphanumeric characters and hyphens only"),
  kafkaVersion: z.string().min(1, "Kafka version is required"),
  numberOfBrokerNodes: z.number().int().min(1, "Min 1 broker").max(15, "Max 15 brokers"),
})

const KAFKA_VERSIONS = [
  { value: "3.6.0" },
  { value: "3.5.1" },
  { value: "3.4.0" },
  { value: "2.8.1" },
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
  onSubmit: (opts: { clusterName: string; kafkaVersion: string; numberOfBrokerNodes: number }) => void
}) {
  const form = useForm({
    validators: { onChange: createSchema },
    defaultValues: {
      clusterName: "",
      kafkaVersion: "3.6.0",
      numberOfBrokerNodes: 3,
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
          <DialogTitle>Create Kafka Cluster</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void form.handleSubmit()
          }}
        >
          <DialogBody className="space-y-4">
            <form.Field name="clusterName">
              {(field) => (
                <FormField
                  label="Cluster Name"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    placeholder="my-kafka-cluster"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
            <div className="grid grid-cols-2 gap-4">
              <form.Field name="kafkaVersion">
                {(field) => (
                  <FormField
                    label="Kafka Version"
                    error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                  >
                    <Combobox<{ value: string }>
                      value={field.state.value}
                      onChange={(v) => field.handleChange(v)}
                      items={KAFKA_VERSIONS}
                      filterFn={(item, q) => item.value.includes(q)}
                      getItemValue={(item) => item.value}
                      renderItem={(item) => item.value}
                      placeholder="Select version…"
                    />
                  </FormField>
                )}
              </form.Field>
              <form.Field name="numberOfBrokerNodes">
                {(field) => (
                  <FormField
                    label="Broker Nodes"
                    error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                  >
                    <Input
                      type="number"
                      min={1}
                      max={15}
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
