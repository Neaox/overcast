import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Eye, Radio } from "lucide-react"
import {
  kinesisStreamsQueryOptions,
  kinesisKeys,
  deleteStreamMutationOptions,
} from "@/features/kinesis/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import {
  CreateAction,
  RefreshAction,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
import { RegionElsewhereNotice } from "@/features/preflight/components/region-elsewhere-notice"
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { CreateStreamDialog } from "./create-stream-dialog"

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

export function StreamList() {
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: streams = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(kinesisStreamsQueryOptions())

  const [deleteTarget, setDeleteTarget] = useState<(typeof streams)[number]>()

  const deleteMut = useResourceMutation({
    options: deleteStreamMutationOptions(),
    invalidateKeys: [kinesisKeys.streams()],
    successTitle: "Stream deleted",
    successDescription: (name) => name,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const totalShards = streams.reduce((n, s) => n + s.shardCount, 0)

  return (
    <ResourceListPage
      title="Kinesis Data Streams"
      count={streams.length}
      meta={streams.length > 0 ? `${totalShards} shards` : undefined}
      actions={
        <>
          <ServiceDocsButton
            service="kinesis"
            label="Kinesis"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create stream</CreateAction>
        </>
      }
    >
      <ResourceTable
        query={{ data: streams, isLoading, error }}
        noun="streams"
        emptyIcon={Radio}
        emptyTitle="No streams yet"
        emptyDescription="Create a Kinesis data stream to get started."
        emptyAction={<CreateAction onClick={() => setShowCreate(true)}>Create stream</CreateAction>}
        emptyExtra={<RegionElsewhereNotice kind="kinesis-streams" noun="streams" />}
        errorTitle="Failed to load streams"
        // ListStreams returns the emulator's storage order; A→Z is what a name
        // column implies, and `stream-2` sorts before `stream-10`.
        defaultSort={{ id: "name", desc: false }}
        rowKey={(stream) => stream.name}
        onRowClick={(stream) =>
          navigate({ to: "/kinesis/$streamName", params: { streamName: stream.name } })
        }
        columns={[
          {
            id: "name",
            header: "Name",
            sortValue: (stream) => stream.name,
            cell: (stream) => <ResourceName icon={Radio} name={stream.name} />,
          },
          {
            header: "Status",
            cell: (stream) => <Badge variant={statusVariant(stream.status)}>{stream.status}</Badge>,
          },
          {
            header: "Shards",
            sortValue: (stream) => stream.shardCount,
            cell: (stream) => stream.shardCount,
          },
          {
            header: "Retention",
            sortValue: (stream) => stream.retentionHours,
            cell: (stream) => `${stream.retentionHours}h`,
          },
        ]}
        rowActions={(stream) => (
          <RowAction
            label={`View ${stream.name}`}
            onClick={() =>
              navigate({ to: "/kinesis/$streamName", params: { streamName: stream.name } })
            }
          >
            <Eye className="h-3.5 w-3.5" />
          </RowAction>
        )}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getVars: (stream) => stream.name,
          label: (stream) => stream.name,
          noun: "stream",
          title: "Delete Stream",
          description: (stream) => (
            <>
              Delete <span className="font-mono font-semibold">{stream.name}</span>? This action
              cannot be undone.
            </>
          ),
        }}
      />

      <CreateStreamDialog open={showCreate} onOpenChange={setShowCreate} />
    </ResourceListPage>
  )
}
