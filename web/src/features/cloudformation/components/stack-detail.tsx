import { useState, useEffect, useRef } from "react"
import { useQuery, useInfiniteQuery } from "@tanstack/react-query"
import { useScrollTrigger } from "@/hooks/use-scroll-trigger"
import { useNavigate, Link } from "@tanstack/react-router"
import { RefreshCw, Trash2, Edit2, Loader2, AlertCircle } from "lucide-react"
import {
  cfnStackQueryOptions,
  cfnResourcesQueryOptions,
  cfnEventsInfiniteQueryOptions,
  cfnTemplateQueryOptions,
  cfnDiagnosticsQueryOptions,
  cfnKeys,
  deleteStackMutationOptions,
} from "@/features/cloudformation/data"
import {
  stackStatusVariant,
  formatStatus,
  resourceStatusVariant,
  canUpdateStack,
  canDeleteStack,
  failedResource,
  isStackInProgress,
  isStackFailed,
  isStackRollingBack,
  stackStatusExplanation,
} from "@/features/cloudformation/utils"
import { UpdateStackDialog } from "./update-stack-dialog"
import { StackDiagnosticsPanel } from "./stack-diagnostics"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { PageHeader, Spinner, EmptyState, CodeBlock } from "@/components/ui/primitives"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import {
  Table,
  TableBody,
  TableCell,
  TableCellProse,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Definition, DefinitionCard } from "@/components/ui/definition-card"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { cn } from "@/lib/utils"

interface Props {
  stackName: string
}

// Diagnostics sits between Events and Template because it is what someone
// reaches for immediately after the events failed to explain anything.
type TabKey = "overview" | "resources" | "events" | "diagnostics" | "template"

export function StackDetail({ stackName }: Props) {
  const navigate = useNavigate()

  const [selectedTab, setTab] = useState<TabKey>("overview")
  const [showUpdate, setShowUpdate] = useState(false)
  const [showDelete, setShowDelete] = useState(false)
  const eventsScrollRef = useRef<HTMLDivElement>(null)

  // ─── Queries ────────────────────────────────────────────────────────────────

  const {
    data: stack,
    isLoading: stackLoading,
    isFetching: stackFetching,
    refetch: refetchStack,
  } = useQuery(cfnStackQueryOptions(stackName))

  const stackStatus = stack?.StackStatus ?? ""

  // The one query that cannot be gated on its own tab: whether the Diagnostics
  // tab exists at all is the answer to this request, so waiting for the tab to
  // be selected would mean it never is.
  //
  // It is not gated on the stack's status either, deliberately. The server
  // writes a journal only for a deploy that did not land and deletes it once one
  // does, so "a journal exists" already means "the most recent deploy of this
  // stack failed" — reading that off the status here would be a second, weaker
  // copy of the same rule, and it would hide the answer in the cases where the
  // two disagree: a stack redeploying after a failure reads IN_PROGRESS while
  // still holding the last completed deploy's diagnosis, which is exactly what
  // its reader is looking for.
  const {
    data: diagnostics,
    isFetching: diagnosticsFetching,
    refetch: refetchDiagnostics,
  } = useQuery(cfnDiagnosticsQueryOptions(stackName))

  const hasDiagnostics = Boolean(diagnostics)

  // A successful redeploy clears the journal, so the diagnostics can vanish
  // under a reader who is looking at them, taking their tab with them. Derived
  // rather than corrected in an effect — an effect would render the tabless,
  // panel-less page once before fixing it, and that flash is exactly what reads
  // as a broken console rather than as "that record is gone".
  const tab: TabKey = selectedTab === "diagnostics" && !hasDiagnostics ? "overview" : selectedTab

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
  let eventHistoryStatus = "All events loaded."
  if (isFetchingMoreEvents) {
    eventHistoryStatus = "Loading older events…"
  } else if (hasMoreEvents) {
    eventHistoryStatus = "Scroll for older events."
  }

  const eventsSentinelRef = useScrollTrigger({
    onTrigger: () => void fetchNextEventsPage(),
    enabled: hasMoreEvents && !isFetchingMoreEvents,
    rootRef: eventsScrollRef,
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
    if (tab === "diagnostics") void refetchDiagnostics()
    if (tab === "template") void refetchTemplate()
  }

  const isFetching =
    stackFetching ||
    (tab === "resources" && resourcesFetching) ||
    (tab === "events" && eventsFetching) ||
    (tab === "diagnostics" && diagnosticsFetching) ||
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
        <EmptyState title="Stack not found" description={`No stack named "${stackName}" exists.`} />
      </div>
    )
  }

  const statusExplanation = stackStatusExplanation(stackStatus)
  const rootCause = failedResource(resources)

  return (
    <div className="flex w-full flex-col gap-4">
      {/* Header */}
      <PageHeader
        title={stackName}
        actions={
          <div className="flex items-center gap-2">
            <Badge variant={stackStatusVariant(stackStatus)}>{formatStatus(stackStatus)}</Badge>
            <Button
              variant="ghost"
              size="sm"
              onClick={handleRefresh}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            {canUpdateStack(stackStatus) && (
              <Button size="sm" variant="secondary" onClick={() => setShowUpdate(true)}>
                <Edit2 className="mr-1.5 h-3.5 w-3.5" />
                Update
              </Button>
            )}
            {canDeleteStack(stackStatus) && (
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
      {(isStackFailed(stackStatus) || isStackRollingBack(stackStatus)) && (
        <div className="flex items-start gap-3 rounded-md border border-danger/30 bg-danger-muted p-3 text-sm">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="font-medium text-danger">{formatStatus(stackStatus)}</span>
            {statusExplanation && <span className="text-fg-muted">{statusExplanation}</span>}
            {stack.StackStatusReason && (
              <span className="text-fg-muted">{stack.StackStatusReason}</span>
            )}
            {/* A rollback that reached its terminal state clears
                StackStatusReason, so the only surviving answer to "why" is on
                the resource that failed. */}
            {!stack.StackStatusReason && rootCause && (
              <span className="text-fg-muted">
                <span className="font-medium">{rootCause.logicalId}</span>: {rootCause.reason}
              </span>
            )}
          </div>
          <Button
            size="sm"
            variant="ghost"
            className="shrink-0 text-xs"
            onClick={() => setTab("events")}
          >
            View events
          </Button>
          {/* Offered only when there is an answer behind it. The events are a
              mirror of DescribeStackEvents and often carry nothing more useful
              than "is unable to consistently start tasks successfully", so this
              is the better of the two actions whenever it is here at all — but
              a button that leads to an empty tab would be worse than none. */}
          {hasDiagnostics && (
            <Button
              size="sm"
              variant="ghost"
              className="shrink-0 text-xs"
              onClick={() => setTab("diagnostics")}
            >
              Why did this fail?
            </Button>
          )}
        </div>
      )}

      {/* Tabs */}
      <Tabs selectedKey={tab} onSelectionChange={(key) => setTab(key as TabKey)}>
        <TabList>
          <Tab id="overview">Overview</Tab>
          <Tab id="resources">
            Resources
            {resources.length > 0 && (
              <span className="ml-1.5 rounded-full bg-bg-muted px-1.5 py-0.5 font-mono text-xs text-fg-muted">
                {resources.length}
              </span>
            )}
          </Tab>
          <Tab id="events">Events</Tab>
          {/* Absent, not empty: a healthy stack has no journal, and an always-
              present tab that usually says "nothing here" trains people to
              ignore the one time it does not. */}
          {hasDiagnostics && <Tab id="diagnostics">Diagnostics</Tab>}
          <Tab id="template">Template</Tab>
        </TabList>

        {/* ── Overview ──────────────────────────────────────────────────── */}
        <TabPanel id="overview" className="mt-4 flex flex-col gap-4">
          {/* Stack details card */}
          <DefinitionCard>
            <Definition label="Created" value={stack.CreationTime?.toLocaleString()} />
            {stack.LastUpdatedTime && (
              <Definition label="Last updated" value={stack.LastUpdatedTime.toLocaleString()} />
            )}
            {stack.ParentId && (
              <Definition
                label="Parent stack"
                value={
                  <Link
                    to="/cloudformation/$stackName"
                    params={{ stackName: parentStackName(stack.ParentId) }}
                    className="text-accent hover:underline"
                  >
                    {parentStackName(stack.ParentId)}
                  </Link>
                }
              />
            )}
            {(stack.Capabilities ?? []).length > 0 && (
              <Definition label="Capabilities" value={(stack.Capabilities ?? []).join(", ")} />
            )}
            <Definition label="Stack ID" value={stack.StackId} full />
          </DefinitionCard>

          {/* Parameters */}
          {(stack.Parameters ?? []).length > 0 && (
            <section className="flex flex-col gap-2">
              <h2 className="font-mono text-sm font-medium text-fg">Parameters</h2>
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
                        <TableCell>{p.ParameterKey}</TableCell>
                        <TableCell>{p.ParameterValue}</TableCell>
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
              <h2 className="font-mono text-sm font-medium text-fg">Outputs</h2>
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
                        <TableCell className="font-medium">{o.OutputKey}</TableCell>
                        <TableCell>{o.OutputValue}</TableCell>
                        <TableCellProse>{o.Description ?? "—"}</TableCellProse>
                        <TableCell className="text-fg-muted">{o.ExportName ?? "—"}</TableCell>
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
              <h2 className="font-mono text-sm font-medium text-fg">Tags</h2>
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
                        <TableCell>{t.Key}</TableCell>
                        <TableCell>{t.Value}</TableCell>
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
                <h2 className="font-mono text-sm font-medium text-fg">Resources</h2>
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
                        <TableCell className="font-medium">
                          <ResourceLink
                            logicalId={r.LogicalResourceId ?? ""}
                            resourceType={r.ResourceType ?? ""}
                            physicalId={r.PhysicalResourceId}
                          />
                        </TableCell>
                        <TableCell className="text-fg-muted">{r.ResourceType}</TableCell>
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
                            <p className="mt-0.5 font-sans text-[13px] text-danger">
                              {r.ResourceStatusReason}
                            </p>
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
                      <TableCell className="font-medium">
                        <ResourceLink
                          logicalId={r.LogicalResourceId ?? ""}
                          resourceType={r.ResourceType ?? ""}
                          physicalId={r.PhysicalResourceId}
                        />
                      </TableCell>
                      <TableCell className="max-w-xs truncate text-fg-muted">
                        {r.PhysicalResourceId ?? "—"}
                      </TableCell>
                      <TableCell className="text-fg-muted">{r.ResourceType}</TableCell>
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
                            <span className="font-sans text-[13px] text-danger">
                              {r.ResourceStatusReason}
                            </span>
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="text-fg-muted">
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
              <div
                ref={eventsScrollRef}
                role="region"
                aria-label="CloudFormation stack events"
                tabIndex={0}
                className="max-h-150 overflow-y-auto"
              >
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
                      // Not just *_FAILED: the rollback events are where a
                      // failed deploy explains itself, and the tone of the
                      // reason should match the tone of the badge beside it.
                      const isFailed = resourceStatusVariant(e.ResourceStatus ?? "") === "danger"
                      return (
                        <TableRow key={e.EventId}>
                          <TableCell className="w-40 whitespace-nowrap text-fg-muted">
                            {e.Timestamp ? e.Timestamp.toLocaleString() : ""}
                          </TableCell>
                          <TableCell className="font-medium">
                            <ResourceLink
                              logicalId={e.LogicalResourceId ?? ""}
                              resourceType={e.ResourceType ?? ""}
                              physicalId={e.PhysicalResourceId}
                            />
                          </TableCell>
                          <TableCell className="text-fg-muted">{e.ResourceType}</TableCell>
                          <TableCell>
                            <Badge variant={resourceStatusVariant(e.ResourceStatus ?? "")}>
                              {formatStatus(e.ResourceStatus ?? "")}
                            </Badge>
                          </TableCell>
                          <TableCellProse className={cn("max-w-sm", isFailed && "text-danger")}>
                            {e.ResourceStatusReason ?? ""}
                          </TableCellProse>
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
                <div ref={eventsSentinelRef} className="h-px" />
              </div>
              <div
                role="status"
                aria-live="polite"
                className="border-t border-border px-3 py-2 text-xs text-fg-muted"
              >
                Showing {events.length} events. {eventHistoryStatus}
              </div>
            </div>
          )}
        </TabPanel>

        {/* ── Diagnostics ───────────────────────────────────────────────── */}
        <TabPanel id="diagnostics" className="mt-4">
          {diagnostics && <StackDiagnosticsPanel diagnostics={diagnostics} />}
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
