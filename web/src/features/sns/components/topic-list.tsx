import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Bell, Eye } from "lucide-react"
import { snsTopicsQueryOptions, snsKeys, deleteTopicMutationOptions } from "@/features/sns/data"
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
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { RawStateLink } from "@/features/debug/raw-state-link"
import { CreateTopicDialog } from "./create-topic-dialog"
import { ArnText } from "@/components/ui/arn-link"

/** `arn:aws:sns:us-east-1:000000000000:alerts` → `alerts`. */
function topicNameOf(topic: { TopicArn?: string }): string {
  return topic.TopicArn?.split(":").pop() ?? ""
}

export function TopicList() {
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: topics = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(snsTopicsQueryOptions())

  const [deleteTarget, setDeleteTarget] = useState<(typeof topics)[number]>()

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
      <ResourceTable
        query={{ data: topics, isLoading, error }}
        noun="topics"
        emptyIcon={Bell}
        emptyTitle="No topics yet"
        emptyDescription="Create a topic to get started."
        emptyAction={
          <div className="flex flex-col items-center gap-4">
            <CreateAction onClick={() => setShowCreate(true)}>Create topic</CreateAction>
            {/* Same arrangement as the CloudFormation stack list: `ResourceTable`
                owns its empty state, so the cross-region notice rides in the
                action slot, with its sibling margins dropped and the wrapper
                collapsed when the notice has nothing to say. */}
            <div className="empty:hidden [&>div]:mt-0 [&>div]:mb-0">
              <RegionElsewhereNotice kind="sns-topics" noun="topics" />
            </div>
          </div>
        }
        errorTitle="Failed to load topics"
        // ListTopics returns the emulator's own storage order, which is not
        // stable across refetches; A→Z is the order a name column implies.
        defaultSort={{ id: "name", desc: false }}
        rowKey={(topic) => topic.TopicArn ?? ""}
        onRowClick={(topic) =>
          navigate({ to: "/sns/$topic", params: { topic: topicNameOf(topic) } })
        }
        columns={[
          {
            id: "name",
            header: "Name",
            sortValue: (topic) => topicNameOf(topic),
            cell: (topic) => <ResourceName icon={Bell} name={topicNameOf(topic)} />,
          },
          {
            header: "ARN",
            cellClassName: "text-fg-muted",
            cell: (topic) => <ArnText arn={topic.TopicArn ?? ""} />,
          },
        ]}
        rowActions={(topic) => (
          <RowAction
            label={`View ${topicNameOf(topic)}`}
            onClick={() => navigate({ to: "/sns/$topic", params: { topic: topicNameOf(topic) } })}
          >
            <Eye className="h-3.5 w-3.5" />
          </RowAction>
        )}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getId: (topic) => topicNameOf(topic),
          label: (topic) => topicNameOf(topic),
          noun: "topic",
          title: "Delete topic?",
          description: (topic) => (
            <>
              This will permanently delete{" "}
              <span className="font-mono font-medium">{topicNameOf(topic)}</span> and all its
              subscriptions.
            </>
          ),
        }}
      />

      <CreateTopicDialog open={showCreate} onOpenChange={setShowCreate} />
    </ResourceListPage>
  )
}
