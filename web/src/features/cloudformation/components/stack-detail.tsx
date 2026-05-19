import { useState, useEffect } from "react"
import { useQuery, useInfiniteQuery } from "@tanstack/react-query"
import { useScrollTrigger } from "@/hooks/use-scroll-trigger"
import { useNavigate, Link } from "@tanstack/react-router"
import { RefreshCw, Trash2, Edit2, Loader2, AlertCircle } from "lucide-react"
import {
  cfnStackQueryOptions,
  cfnResourcesQueryOptions,
  cfnEventsInfiniteQueryOptions,
  cfnTemplateQueryOptions,
  cfnKeys,
  deleteStackMutationOptions,
} from "@/features/cloudformation/data"
import {
  stackStatusVariant,
  formatStatus,
  resourceStatusVariant,
  canUpdateStack,
  canDeleteStack,
  isStackInProgress,
  isStackFailed,
} from "@/features/cloudformation/utils"
import { UpdateStackDialog } from "./update-stack-dialog"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { PageHeader, Breadcrumb, Spinner, EmptyState, CodeBlock } from "@/components/ui/primitives"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Card, CardContent } from "@/components/ui/card"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { cn } from "@/lib/utils"

interface Props {
  stackName: string
}

type TabKey = "overview" | "resources" | "events" | "template"

export function StackDetail({ stackName }: Props) {
  const navigate = useNavigate()

  const [tab, setTab] = useState<TabKey>("overview")
  const [showUpdate, setShowUpdate] = useState(false)
  const [showDelete, setShowDelete] = useState(false)

  // ─── Queries ────────────────────────────────────────────────────────────────

  const {
    data: stack,
    isLoading: stackLoading,
    isFetching: stackFetching,
    refetch: refetchStack,
  } = useQuery(cfnStackQueryOptions(stackName))

  const {
    data: resources = [],
    isFetching: resourcesFetching,
    refetch: refetchResources,
  } = useQuery({
    ...cfnResourcesQueryOptions(stackName),
    enabled: tab === "resources" || tab === "overview" || tab === "events",
  })

  const {
    data: eventsData,
    isFetching: eventsFetching,
    refetch: refetchEvents,
    fetchNextPage: fetchNextEventsPage,
    hasNextPage: hasMoreEvents,
    isFetchingNextPage: isFetchingMoreEvents,
  } = useInfiniteQuery({
    ...cfnEventsInfiniteQueryOptions(stackName),
    enabled: tab === "events",
  })

  const events = eventsData?.pages.flatMap((p) => p.events) ?? []

  const eventsSentinelRef = useScrollTrigger({
    onTrigger: () => void fetchNextEventsPage(),
    enabled: hasMoreEvents && !isFetchingMoreEvents,
  })

  const {
    data: templateBody = "",
    isFetching: templateFetching,
    refetch: refetchTemplate,
  } = useQuery({
    ...cfnTemplateQueryOptions(stackName),
    enabled: tab === "template",
  })

  // ─── Navigate away when stack is deleted ────────────────────────────────────

  useEffect(() => {
    if (stack?.StackStatus === "DELETE_COMPLETE") {
      void navigate({ to: "/cloudformation" })
    }
  }, [stack?.StackStatus, navigate])

  // ─── Mutations ──────────────────────────────────────────────────────────────

  const deleteMut = useResourceMutation({
    options: deleteStackMutationOptions(),
    invalidateKeys: [cfnKeys.stacks()],
    successTitle: "Stack deletion started",
    successDescription: () => stackName,
    errorTitle: "Delete failed",
    onSuccess: () => {
      setShowDelete(false)
      void navigate({ to: "/cloudformation" })
    },
  })

  // ─── Helpers ────────────────────────────────────────────────────────────────

  function handleRefresh() {
    void refetchStack()
    if (tab === "resources") void refetchResources()
    if (tab === "events") void refetchEvents()
    if (tab === "template") void refetchTemplate()
  }

  const isFetching =
    stackFetching ||
    (tab === "resources" && resourcesFetching) ||
    (tab === "events" && eventsFetching) ||
    (tab === "template" && templateFetching)

  if (stackLoading) {
    return (
      <div className="flex w-full justify-center py-24">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!stack) {
    return (
      <div className="flex w-full flex-col gap-4">
        <Breadcrumb
          items={[
            { label: "CloudFormation", onClick: () => navigate({ to: "/cloudformation" }) },
            { label: stackName },
          ]}
        />
        <EmptyState title="Stack not found" description={`No stack named "${stackName}" exists.`} />
      </div>
    )
  }

  return (
    <div className="flex w-full flex-col gap-4">
      {/* Header */}
      <PageHeader
        title={stackName}
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "CloudFormation", onClick: () => navigate({ to: "/cloudformation" }) },
              { label: stackName },
            ]}
          />
        }
        actions={
          <div className="flex items-center gap-2">
            <Badge variant={stackStatusVariant(stack.StackStatus ?? "")}>
              {formatStatus(stack.StackStatus ?? "")}
            </Badge>
            <Button
              variant="ghost"
              size="sm"
              onClick={handleRefresh}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            {canUpdateStack(stack.StackStatus ?? "") && (
              <Button size="sm" variant="secondary" onClick={() => setShowUpdate(true)}>
                <Edit2 className="mr-1.5 h-3.5 w-3.5" />
                Update
              </Button>
            )}
            {canDeleteStack(stack.StackStatus ?? "") && (
              <Button size="sm" variant="danger" onClick={() => setShowDelete(true)}>
                <Trash2 className="mr-1.5 h-3.5 w-3.5" />
                Delete
              </Button>
            )}
          </div>
        }
      />

      <ApplicationOwnershipBanner candidates={[stack.StackId, stack.StackName, stackName]} />

      {/* Failure / rollback banner */}
      {isStackFailed(stack.StackStatus ?? "") && stack.StackStatusReason && (
        <div className="flex items-start gap-3 rounded-md border border-danger/30 bg-danger-muted p-3 text-sm">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="font-medium text-danger">{formatStatus(stack.StackStatus ?? "")}</span>
            <span className="text-fg-muted">{stack.StackStatusReason}</span>
          </div>
          <Button
            size="sm"
            variant="ghost"
            className="shrink-0 text-xs"
            onClick={() => setTab("events")}
          >
            View events
          </Button>
        </div>
      )}

      {/* Tabs */}
      <Tabs selectedKey={tab} onSelectionChange={(k) => setTab(k as TabKey)}>
        <TabList>
          <Tab id="overview">Overview</Tab>
          <Tab id="resources">
            Resources
            {resources.length > 0 && (
              <span className="ml-1.5 rounded-full bg-bg-muted px-1.5 py-0.5 text-xs text-fg-muted">
                {resources.length}
              </span>
            )}
          </Tab>
          <Tab id="events">Events</Tab>
          <Tab id="template">Template</Tab>
        </TabList>

        {/* ── Overview ──────────────────────────────────────────────────── */}
        <TabPanel id="overview" className="mt-4 flex flex-col gap-4">
          {/* Stack details card */}
          <Card>
            <CardContent className="grid grid-cols-2 gap-x-8 gap-y-3 p-4 text-sm md:grid-cols-3">
              <DetailRow label="Stack ID" value={stack.StackId ?? ""} mono />
              {stack.ParentId && (
                <div className="flex flex-col gap-0.5">
                  <dt className="text-xs text-fg-muted">Parent stack</dt>
                  <dd>
                    <Link
                      to="/cloudformation/$stackName"
                      params={{ stackName: parentStackName(stack.ParentId) }}
                      className="text-sm text-accent hover:underline"
                    >
                      {parentStackName(stack.ParentId)}
                    </Link>
                  </dd>
                </div>
              )}
              <DetailRow label="Created" value={stack.CreationTime?.toLocaleString() ?? "—"} />
              {stack.LastUpdatedTime && (
                <DetailRow label="Last updated" value={stack.LastUpdatedTime.toLocaleString()} />
              )}
              {(stack.Capabilities ?? []).length > 0 && (
                <DetailRow label="Capabilities" value={(stack.Capabilities ?? []).join(", ")} />
              )}
            </CardContent>
          </Card>

          {/* Parameters */}
          {(stack.Parameters ?? []).length > 0 && (
            <section className="flex flex-col gap-2">
              <h2 className="text-sm font-medium text-fg">Parameters</h2>
              <div className="rounded-md border border-border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Key</TableHead>
                      <TableHead>Value</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(stack.Parameters ?? []).map((p) => (
                      <TableRow key={p.ParameterKey}>
                        <TableCell className="font-mono text-xs">{p.ParameterKey}</TableCell>
                        <TableCell className="font-mono text-xs">{p.ParameterValue}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </section>
          )}

          {/* Outputs */}
          {(stack.Outputs ?? []).length > 0 && (
            <section className="flex flex-col gap-2">
              <h2 className="text-sm font-medium text-fg">Outputs</h2>
              <div className="rounded-md border border-border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Key</TableHead>
                      <TableHead>Value</TableHead>
                      <TableHead>Description</TableHead>
                      <TableHead>Export name</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(stack.Outputs ?? []).map((o) => (
                      <TableRow key={o.OutputKey}>
                        <TableCell className="font-mono text-xs font-medium">
                          {o.OutputKey}
                        </TableCell>
                        <TableCell className="font-mono text-xs">{o.OutputValue}</TableCell>
                        <TableCell className="text-sm text-fg-muted">
                          {o.Description ?? "—"}
                        </TableCell>
                        <TableCell className="font-mono text-xs text-fg-muted">
                          {o.ExportName ?? "—"}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </section>
          )}

          {/* Tags */}
          {(stack.Tags ?? []).length > 0 && (
            <section className="flex flex-col gap-2">
              <h2 className="text-sm font-medium text-fg">Tags</h2>
              <div className="rounded-md border border-border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Key</TableHead>
                      <TableHead>Value</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(stack.Tags ?? []).map((t) => (
                      <TableRow key={t.Key}>
                        <TableCell className="font-mono text-xs">{t.Key}</TableCell>
                        <TableCell className="font-mono text-xs">{t.Value}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </section>
          )}

          {/* Resource summary on overview */}
          {resources.length > 0 && (
            <section className="flex flex-col gap-2">
              <div className="flex items-center justify-between">
                <h2 className="text-sm font-medium text-fg">Resources</h2>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => setTab("resources")}
                  className="text-xs"
                >
                  View all
                </Button>
              </div>
              <div className="rounded-md border border-border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Logical ID</TableHead>
                      <TableHead>Type</TableHead>
                      <TableHead>Status</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {resources.slice(0, 5).map((r) => (
                      <TableRow key={r.LogicalResourceId}>
                        <TableCell className="font-mono text-xs font-medium">
                          <ResourceLink
                            logicalId={r.LogicalResourceId ?? ""}
                            resourceType={r.ResourceType ?? ""}
                            physicalId={r.PhysicalResourceId}
                          />
                        </TableCell>
                        <TableCell className="font-mono text-xs text-fg-muted">
                          {r.ResourceType}
                        </TableCell>
                        <TableCell>
                          <span className="flex items-center gap-1.5">
                            {isStackInProgress(r.ResourceStatus ?? "") && (
                              <Loader2 className="h-3 w-3 animate-spin text-fg-muted" />
                            )}
                            <Badge variant={resourceStatusVariant(r.ResourceStatus ?? "")}>
                              {formatStatus(r.ResourceStatus ?? "")}
                            </Badge>
                          </span>
                          {r.ResourceStatusReason && (
                            <p className="mt-0.5 text-xs text-danger">{r.ResourceStatusReason}</p>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </section>
          )}
        </TabPanel>

        {/* ── Resources ─────────────────────────────────────────────────── */}
        <TabPanel id="resources" className="mt-4">
          {resources.length === 0 ? (
            <EmptyState
              title="No resources"
              description="No resources have been provisioned yet."
            />
          ) : (
            <div className="rounded-md border border-border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Logical ID</TableHead>
                    <TableHead>Physical ID</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Last updated</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {resources.map((r) => (
                    <TableRow key={r.LogicalResourceId}>
                      <TableCell className="font-mono text-xs font-medium">
                        <ResourceLink
                          logicalId={r.LogicalResourceId ?? ""}
                          resourceType={r.ResourceType ?? ""}
                          physicalId={r.PhysicalResourceId}
                        />
                      </TableCell>
                      <TableCell className="max-w-xs truncate font-mono text-xs text-fg-muted">
                        {r.PhysicalResourceId ?? "—"}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-fg-muted">
                        {r.ResourceType}
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-col gap-1">
                          <span className="flex items-center gap-1.5">
                            {isStackInProgress(r.ResourceStatus ?? "") && (
                              <Loader2 className="h-3 w-3 animate-spin text-fg-muted" />
                            )}
                            <Badge variant={resourceStatusVariant(r.ResourceStatus ?? "")}>
                              {formatStatus(r.ResourceStatus ?? "")}
                            </Badge>
                          </span>
                          {r.ResourceStatusReason && (
                            <span className="text-xs text-danger">{r.ResourceStatusReason}</span>
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="text-sm text-fg-muted">
                        {r.LastUpdatedTimestamp ? r.LastUpdatedTimestamp.toLocaleString() : "—"}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </TabPanel>

        {/* ── Events ────────────────────────────────────────────────────── */}
        <TabPanel id="events" className="mt-4">
          {events.length === 0 && !eventsFetching ? (
            <EmptyState
              title="No events"
              description="Stack events appear here during deployments."
            />
          ) : (
            <div className="rounded-md border border-border">
              <div className="max-h-150 overflow-y-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Timestamp</TableHead>
                      <TableHead>Logical ID</TableHead>
                      <TableHead>Type</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>Reason</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {events.map((e) => {
                      const isFailed = (e.ResourceStatus ?? "").endsWith("_FAILED")
                      return (
                        <TableRow key={e.EventId}>
                          <TableCell className="w-40 text-xs whitespace-nowrap text-fg-muted">
                            {e.Timestamp ? e.Timestamp.toLocaleString() : ""}
                          </TableCell>
                          <TableCell className="font-mono text-xs font-medium">
                            <ResourceLink
                              logicalId={e.LogicalResourceId ?? ""}
                              resourceType={e.ResourceType ?? ""}
                              physicalId={e.PhysicalResourceId}
                            />
                          </TableCell>
                          <TableCell className="font-mono text-xs text-fg-muted">
                            {e.ResourceType}
                          </TableCell>
                          <TableCell>
                            <Badge variant={resourceStatusVariant(e.ResourceStatus ?? "")}>
                              {formatStatus(e.ResourceStatus ?? "")}
                            </Badge>
                          </TableCell>
                          <TableCell
                            className={cn(
                              "max-w-sm text-xs",
                              isFailed ? "text-danger" : "text-fg-muted",
                            )}
                          >
                            {e.ResourceStatusReason ?? ""}
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
                {isFetchingMoreEvents && (
                  <div className="flex justify-center py-3">
                    <Spinner className="h-4 w-4" />
                  </div>
                )}
                <div ref={eventsSentinelRef} />
              </div>
            </div>
          )}
        </TabPanel>

        {/* ── Template ──────────────────────────────────────────────────── */}
        <TabPanel id="template" className="mt-4">
          {templateFetching && !templateBody ? (
            <div className="flex justify-center py-8">
              <Spinner />
            </div>
          ) : templateBody ? (
            <CodeBlock className="max-h-128 overflow-y-auto text-xs">{templateBody}</CodeBlock>
          ) : (
            <EmptyState title="No template" description="Template body is not available." />
          )}
        </TabPanel>
      </Tabs>

      {/* Dialogs */}
      <UpdateStackDialog
        stackName={stackName}
        currentTemplate={templateBody}
        open={showUpdate}
        onOpenChange={setShowUpdate}
      />

      <ConfirmDialog
        open={showDelete}
        onOpenChange={setShowDelete}
        title="Delete stack"
        description={
          <>
            Permanently delete <strong>{stackName}</strong> and all its resources? This cannot be
            undone.
          </>
        }
        confirmLabel="Delete stack"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteMut.mutate(stackName)}
      />
    </div>
  )
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

/** Extract stack name from a CloudFormation stack ARN. */
function parentStackName(arn: string): string {
  const match = arn.match(/:stack\/([^/]+)/)
  return match?.[1] ?? arn
}

function DetailRow({
  label,
  value,
  mono = false,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-xs text-fg-muted">{label}</dt>
      <dd className={cn("text-sm", mono ? "font-mono text-xs break-all" : "text-fg")}>{value}</dd>
    </div>
  )
}

/**
 * Renders a resource's logical ID as a typed TanStack Router link when the
 * AWS resource type maps to a known UI route; plain text otherwise.
 * The AWS type is the discriminator — each case uses the correct param name.
 */
function ResourceLink({
  logicalId,
  resourceType,
  physicalId,
}: {
  logicalId: string
  resourceType: string
  physicalId: string | undefined
}) {
  const cls = "text-accent hover:underline"
  const stop = (e: React.MouseEvent) => e.stopPropagation()

  if (!physicalId) return <span>{logicalId}</span>

  switch (resourceType) {
    case "AWS::S3::Bucket":
      return (
        <Link to="/s3/$bucket" params={{ bucket: physicalId }} className={cls} onClick={stop}>
          {logicalId}
        </Link>
      )
    case "AWS::SQS::Queue": {
      const queue = physicalId.split("/").pop() ?? physicalId
      return (
        <Link to="/sqs/$queue" params={{ queue }} className={cls} onClick={stop}>
          {logicalId}
        </Link>
      )
    }
    case "AWS::SNS::Topic": {
      const topic = physicalId.split(":").pop() ?? physicalId
      return (
        <Link to="/sns/$topic" params={{ topic }} className={cls} onClick={stop}>
          {logicalId}
        </Link>
      )
    }
    case "AWS::DynamoDB::Table":
      return (
        <Link
          to="/dynamodb/$tableName"
          params={{ tableName: physicalId }}
          className={cls}
          onClick={stop}
        >
          {logicalId}
        </Link>
      )
    case "AWS::Lambda::Function":
      return (
        <Link to="/lambda/$name" params={{ name: physicalId }} className={cls} onClick={stop}>
          {logicalId}
        </Link>
      )
    case "AWS::Kinesis::Stream":
      return (
        <Link
          to="/kinesis/$streamName"
          params={{ streamName: physicalId }}
          className={cls}
          onClick={stop}
        >
          {logicalId}
        </Link>
      )
    case "AWS::Logs::LogGroup":
    case "AWS::CloudWatch::LogGroup":
      return (
        <Link
          to="/cloudwatch/logs/group"
          search={{ groupName: physicalId }}
          className={cls}
          onClick={stop}
        >
          {logicalId}
        </Link>
      )
    case "AWS::SecretsManager::Secret":
      return (
        <Link
          to="/secretsmanager/$secretName"
          params={{ secretName: physicalId }}
          className={cls}
          onClick={stop}
        >
          {logicalId}
        </Link>
      )
    case "AWS::CloudFormation::Stack": {
      // physicalId is the child stack ARN: arn:aws:cloudformation:region:account:stack/name/uuid
      const match = physicalId.match(/:stack\/([^/]+)/)
      const childStackName = match?.[1] ?? physicalId
      return (
        <Link
          to="/cloudformation/$stackName"
          params={{ stackName: childStackName }}
          className={cls}
          onClick={stop}
        >
          {logicalId}
        </Link>
      )
    }
    default:
      return <span>{logicalId}</span>
  }
}
