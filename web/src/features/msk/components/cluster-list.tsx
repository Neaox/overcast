import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Radio } from "lucide-react"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { DockerBanner } from "@/components/docker-banner"
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
import { ResourceTable } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"
import { Combobox } from "@/components/ui/combobox"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
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
    error,
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
    <ResourceListPage
      title="MSK Clusters"
      count={clusters.length}
      actions={
        <>
          <ServiceDocsButton
            service="msk"
            label="MSK"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create cluster</CreateAction>
        </>
      }
    >
      <DockerBanner forService="msk" />

      <ResourceTable
        query={{ data: clusters, isLoading, error }}
        noun="clusters"
        emptyIcon={Radio}
        emptyTitle="No Kafka clusters"
        emptyDescription="Create a cluster to get started. Redpanda will be launched automatically."
        emptyAction={
          <CreateAction onClick={() => setShowCreate(true)}>Create cluster</CreateAction>
        }
        errorTitle="Failed to load clusters"
        // ListClusters returns the emulator's storage order; A→Z is what a name
        // column implies.
        defaultSort={{ id: "cluster-name", desc: false }}
        rowKey={(c) => c.ClusterArn ?? ""}
        columns={[
          {
            id: "cluster-name",
            header: "Cluster name",
            sortValue: (c) => c.ClusterName,
            cell: (c) => <ResourceName icon={Radio} name={c.ClusterName} />,
          },
          {
            header: "Kafka version",
            cellClassName: "text-fg-muted",
            cell: (c) => c.CurrentBrokerSoftwareInfo?.KafkaVersion ?? "—",
          },
          {
            header: "Brokers",
            cellClassName: "text-fg-muted",
            sortValue: (c) => c.NumberOfBrokerNodes,
            cell: (c) => c.NumberOfBrokerNodes,
          },
          {
            header: "Status",
            cell: (c) => <ClusterStatusBadge status={c.State ?? ""} />,
          },
        ]}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (v) => !v && setDeleteTarget(undefined),
          mutation: deleteMut,
          getVars: (c) => c.ClusterArn ?? "",
          label: (c) => c.ClusterName ?? "cluster",
          noun: "cluster",
          title: "Delete Kafka Cluster",
          description: (c) => (
            <>
              Permanently delete <strong>{c.ClusterName}</strong>? The Redpanda container will be
              stopped. This action cannot be undone.
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
  onSubmit: (opts: {
    clusterName: string
    kafkaVersion: string
    numberOfBrokerNodes: number
  }) => void
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
          <DialogTitle>Create Kafka cluster</DialogTitle>
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
