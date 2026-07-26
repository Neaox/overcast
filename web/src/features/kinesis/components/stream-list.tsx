import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Eye, Radio, Trash2 } from "lucide-react"
import {
  kinesisStreamsQueryOptions,
  kinesisKeys,
  deleteStreamMutationOptions,
} from "@/features/kinesis/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { QueryListState, EmptyState } from "@/components/ui/primitives"
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
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: streams = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(kinesisStreamsQueryOptions())

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
      <ResourceListCard>
        {isLoading || streams.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={streams.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<Radio className="h-10 w-10" />}
                title="No streams yet"
                description="Create a Kinesis data stream to get started."
                action={
                  <CreateAction onClick={() => setShowCreate(true)}>Create stream</CreateAction>
                }
              />
            }
            errorTitle="Failed to load streams"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Shards</TableHead>
                <TableHead>Retention</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {streams.map((stream) => (
                <TableRow
                  key={stream.name}
                  onClick={() =>
                    navigate({ to: "/kinesis/$streamName", params: { streamName: stream.name } })
                  }
                >
                  <TableCell>
                    <ResourceName icon={Radio} name={stream.name} />
                  </TableCell>
                  <TableCell>
                    <Badge variant={statusVariant(stream.status)}>{stream.status}</Badge>
                  </TableCell>
                  <TableCell>{stream.shardCount}</TableCell>
                  <TableCell>{stream.retentionHours}h</TableCell>
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <RowActions>
                      <RowAction
                        label={`View ${stream.name}`}
                        onClick={() =>
                          navigate({
                            to: "/kinesis/$streamName",
                            params: { streamName: stream.name },
                          })
                        }
                      >
                        <Eye className="h-3.5 w-3.5" />
                      </RowAction>
                      <RowAction
                        label={`Delete ${stream.name}`}
                        tone="danger"
                        onClick={() => setDeleteTarget(stream.name)}
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

      <CreateStreamDialog open={showCreate} onOpenChange={setShowCreate} />

      {/* Delete confirmation dialog */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete Stream"
        description={
          <>
            Delete <span className="font-mono font-semibold">{deleteTarget}</span>? This action
            cannot be undone.
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </ResourceListPage>
  )
}
