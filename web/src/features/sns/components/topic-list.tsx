import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Bell, Plus, Trash2, RefreshCw } from "lucide-react"
import { snsTopicsQueryOptions, snsKeys, deleteTopicMutationOptions } from "@/features/sns/data"
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
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { RawStateLink } from "@/features/debug/raw-state-link"
import { CreateTopicDialog } from "./create-topic-dialog"
import { cn } from "@/lib/utils"
import { ArnText } from "@/components/ui/arn-link"

export function TopicList() {
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const { data: topics = [], isLoading, isFetching, refetch } = useQuery(snsTopicsQueryOptions())

  const deleteMut = useResourceMutation({
    options: deleteTopicMutationOptions(),
    invalidateKeys: [snsKeys.topics()],
    successTitle: "Topic deleted",
    successDescription: (name) => name,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="SNS Topics"
        description="Simple Notification Service — topics and subscriptions"
        actions={
          <div className="flex items-center gap-2">
            <ServiceDocsButton
              service="sns"
              label="SNS"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <RawStateLink service="sns" />
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1 h-4 w-4" />
              Create topic
            </Button>
          </div>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-24">
          <Spinner className="h-6 w-6" />
        </div>
      ) : topics.length === 0 ? (
        <EmptyState
          icon={<Bell className="h-8 w-8 opacity-40" />}
          title="No topics"
          description="Create a topic to get started."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>ARN</TableHead>
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {topics.map((topic) => {
              const topicName = topic.TopicArn?.split(":").pop() ?? ""
              return (
                <TableRow
                  key={topic.TopicArn}
                  className="cursor-pointer"
                  onClick={() => navigate({ to: "/sns/$topic", params: { topic: topicName } })}
                >
                  <TableCell className="font-mono font-medium">{topicName}</TableCell>
                  <TableCell className="text-fg-muted">
                    <ArnText arn={topic.TopicArn ?? ""} />
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-danger hover:text-danger"
                      onClick={(e) => {
                        e.stopPropagation()
                        setDeleteTarget(topicName)
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}

      <CreateTopicDialog open={showCreate} onOpenChange={setShowCreate} />

      {/* Confirm delete dialog */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete topic?"
        description={
          <>
            This will permanently delete{" "}
            <span className="font-mono font-medium">{deleteTarget}</span> and all its subscriptions.
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}
