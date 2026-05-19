import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Radio, Plus, Trash2, RefreshCw } from "lucide-react"
import {
  kinesisStreamsQueryOptions,
  kinesisKeys,
  deleteStreamMutationOptions,
} from "@/features/kinesis/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { CreateStreamDialog } from "./create-stream-dialog"
import { cn } from "@/lib/utils"

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

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Kinesis Data Streams"
        description={`${streams.length} stream${streams.length !== 1 ? "s" : ""}`}
        actions={
          <>
            <ServiceDocsButton
              service="kinesis"
              label="Kinesis"
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
              Create Stream
            </Button>
          </>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : streams.length === 0 ? (
        <EmptyState
          icon={<Radio className="h-6 w-6" />}
          title="No streams"
          description="Create a Kinesis data stream to get started."
          action={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Stream
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Shards</TableHead>
              <TableHead>Retention</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {streams.map((stream) => (
              <TableRow
                key={stream.name}
                className="hover:bg-muted/50 cursor-pointer"
                onClick={() =>
                  navigate({ to: "/kinesis/$streamName", params: { streamName: stream.name } })
                }
              >
                <TableCell className="font-mono text-sm">{stream.name}</TableCell>
                <TableCell>
                  <Badge variant={statusVariant(stream.status)}>{stream.status}</Badge>
                </TableCell>
                <TableCell>{stream.shardCount}</TableCell>
                <TableCell>{stream.retentionHours}h</TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={(e) => {
                      e.stopPropagation()
                      setDeleteTarget(stream.name)
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
    </div>
  )
}
