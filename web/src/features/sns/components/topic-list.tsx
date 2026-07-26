import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Bell, Eye, Trash2 } from "lucide-react"
import { snsTopicsQueryOptions, snsKeys, deleteTopicMutationOptions } from "@/features/sns/data"
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
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { RawStateLink } from "@/features/debug/raw-state-link"
import { CreateTopicDialog } from "./create-topic-dialog"
import { ArnText } from "@/components/ui/arn-link"

export function TopicList() {
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: topics = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(snsTopicsQueryOptions())

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
    <ResourceListPage
      title="SNS Topics"
      count={topics.length}
      actions={
        <>
          <ServiceDocsButton
            service="sns"
            label="SNS"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RawStateLink service="sns" />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create topic</CreateAction>
        </>
      }
    >
      <ResourceListCard>
        {isLoading || topics.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={topics.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<Bell className="h-10 w-10" />}
                title="No topics yet"
                description="Create a topic to get started."
                action={
                  <CreateAction onClick={() => setShowCreate(true)}>Create topic</CreateAction>
                }
              />
            }
            errorTitle="Failed to load topics"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>ARN</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {topics.map((topic) => {
                const topicName = topic.TopicArn?.split(":").pop() ?? ""
                return (
                  <TableRow
                    key={topic.TopicArn}
                    onClick={() => navigate({ to: "/sns/$topic", params: { topic: topicName } })}
                  >
                    <TableCell>
                      <ResourceName icon={Bell} name={topicName} />
                    </TableCell>
                    <TableCell className="text-fg-muted">
                      <ArnText arn={topic.TopicArn ?? ""} />
                    </TableCell>
                    <TableCell onClick={(e) => e.stopPropagation()}>
                      <RowActions>
                        <RowAction
                          label={`View ${topicName}`}
                          onClick={() =>
                            navigate({ to: "/sns/$topic", params: { topic: topicName } })
                          }
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </RowAction>
                        <RowAction
                          label={`Delete ${topicName}`}
                          tone="danger"
                          onClick={() => setDeleteTarget(topicName)}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </RowAction>
                      </RowActions>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>

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
    </ResourceListPage>
  )
}
