import { useState, useEffect, useMemo } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Trash2, RefreshCw, Link, Send, CheckCircle2, XCircle, Bell, Activity } from "lucide-react"
import {
  snsTopicsQueryOptions,
  snsSubscriptionsQueryOptions,
  snsMetricsQueryOptions,
  snsKeys,
  subscribeMutationOptions,
  unsubscribeMutationOptions,
  deleteTopicMutationOptions,
} from "@/features/sns/data"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import {
  MonitorPanel,
  type MonitorCardConfig,
} from "@/features/monitoring/components/monitor-panel"
import { DEFAULT_CHART_RANGE, type ChartRangeToken } from "@/features/monitoring/types"
import { useEventStream } from "@/hooks/use-event-stream"
import { EventType } from "@/services/event-types"
import { ArnLink } from "@/components/ui/arn-link"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, FormRow, fieldError } from "@/components/ui/form"
import { ResourceArnCombobox } from "@/components/ui/resource-arn-combobox"
import type { ArnResourceType } from "@/components/ui/resource-arn-combobox"
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
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Card, CardContent } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { EventConsole } from "@/components/ui/event-console"
import { RawStateLink } from "@/features/debug/raw-state-link"
import { useToast } from "@/components/ui/toast"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { PublishMessageDialog } from "@/features/sns/components/publish-dialog"
import type { SNSSubscription, StreamEvent } from "@/types"
import { sectionLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

interface Props {
  topicName: string
}

// The AWS/SNS phase 2 catalogue (metrics_sns.go), grouped into cards the way
// Lambda's Monitor tab groups its own catalogue
// (docs/plans/service-metrics-platform.md phase 4).
const SNS_MONITOR_CARDS: MonitorCardConfig[] = [
  {
    title: "Messages published",
    unit: "Count",
    series: [{ metric: "NumberOfMessagesPublished", statistic: "Sum", label: "Published" }],
  },
  {
    title: "Publish size",
    unit: "Bytes",
    series: [
      { metric: "PublishSize", statistic: "Average", label: "Average" },
      { metric: "PublishSize", statistic: "Maximum", label: "Maximum" },
    ],
  },
  {
    title: "Deliveries",
    unit: "Count",
    series: [
      { metric: "NumberOfNotificationsDelivered", statistic: "Sum", label: "Delivered" },
      { metric: "NumberOfNotificationsFailed", statistic: "Sum", label: "Failed" },
    ],
  },
]

/** Live delivery outcome for one subscription, derived from the event stream. */
interface DeliveryState {
  status: "delivered" | "failed"
  at: string
  /** Why the delivery failed. Only set when status is "failed". */
  reason?: string
}

/**
 * Event types that mean "SNS handed the message to this subscriber". One per
 * protocol, because each delivery path reports its own event.
 *
 * Handed over, not processed: `sqs` means enqueued and `lambda` means the
 * function accepted the event, the same way an `InvocationType=Event` invoke
 * returns 202 before the handler runs. What the subscriber then did with it is
 * that service's to report.
 */
const DELIVERED_EVENT_TYPES: readonly string[] = [
  EventType.sns.Notification,
  EventType.sns.LambdaDelivered,
  EventType.sns.EmailDelivered,
  EventType.sns.SMSDelivered,
  EventType.sns.WebhookDelivered,
  EventType.sns.PushDelivered,
]

/**
 * Reduce the event stream to the latest delivery outcome per subscription ARN.
 *
 * Every SNS delivery payload carries `subscriptionArn`, so a subscription that
 * a publish never reached simply has no entry — which is the point: before this
 * existed, a `lambda` subscription that silently dropped every message looked
 * exactly like one that worked.
 */
function deliveryStateBySubscription(events: StreamEvent[]): Record<string, DeliveryState> {
  const byArn: Record<string, DeliveryState> = {}
  for (const event of events) {
    const payload = event.payload as Record<string, unknown> | undefined
    const arn = payload?.subscriptionArn as string | undefined
    if (!arn) continue
    if (DELIVERED_EVENT_TYPES.includes(event.type)) {
      byArn[arn] = { status: "delivered", at: event.time }
    } else if (event.type === EventType.sns.DeliveryFailed) {
      byArn[arn] = {
        status: "failed",
        at: event.time,
        reason: payload?.reason as string | undefined,
      }
    }
  }
  return byArn
}

export function TopicDetail({ topicName }: Props) {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [showSubscribe, setShowSubscribe] = useState(false)
  const [showPublish, setShowPublish] = useState(false)
  const [deleteSubTarget, setDeleteSubTarget] = useState<SNSSubscription>()
  const [activeTab, setActiveTab] = useState<"subscriptions" | "monitor">("subscriptions")
  const [monitorRange, setMonitorRange] = useState<ChartRangeToken>(DEFAULT_CHART_RANGE)

  // ─── Data ──────────────────────────────────────────────────────────────────
  const {
    data: subscriptions = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(snsSubscriptionsQueryOptions(topicName))

  const metricsQuery = useQuery(snsMetricsQueryOptions(topicName, monitorRange))

  const { data: allTopics = [] } = useQuery(snsTopicsQueryOptions())
  const topic = allTopics.find((t) => t.TopicArn?.split(":").pop() === topicName)

  // ─── Live event stream ─────────────────────────────────────────────────────
  // Subscribe to sns events to keep subscriptions list fresh in real time
  const { events, connected, clear } = useEventStream({ source: "sns" })
  const lastEvent = events.at(-1)

  // Filter events to only those relevant to this topic
  const topicEvents = events.filter((e) => {
    const p = e.payload as Record<string, unknown> | undefined
    return p?.topicName === topicName || (p?.name as string | undefined)?.includes(topicName)
  })

  // Per-subscription delivery indicators, from the same stream.
  const deliveryState = useMemo(() => deliveryStateBySubscription(topicEvents), [topicEvents])

  useEffect(() => {
    if (!lastEvent) return
    const type = lastEvent.type
    const payload = lastEvent.payload as Record<string, unknown> | undefined
    const topicName_ = payload?.name as string | undefined

    if (type === EventType.sns.TopicDeleted && topicName_?.includes(topicName)) {
      void navigate({ to: "/sns" })
      toast({ title: "Topic deleted", description: topicName })
    }
    // TODO: navigate and toast are stable references; topicName is a prop.
    // All three are included here to satisfy exhaustive-deps.
  }, [lastEvent, navigate, toast, topicName])

  // ─── Mutations ─────────────────────────────────────────────────────────────
  const deleteMut = useResourceMutation({
    options: deleteTopicMutationOptions(),
    invalidateKeys: [snsKeys.topics()],
    successTitle: "Topic deleted",
    successDescription: () => topicName,
    errorTitle: "Delete failed",
    onSuccess: () => navigate({ to: "/sns" }),
  })

  const unsubscribeMut = useResourceMutation({
    options: unsubscribeMutationOptions(),
    invalidateKeys: [snsKeys.subscriptionList(topicName)],
    successTitle: "Unsubscribed",
    errorTitle: "Unsubscribe failed",
    onSuccess: () => setDeleteSubTarget(undefined),
  })

  const subscribeMut = useResourceMutation({
    options: subscribeMutationOptions(),
    invalidateKeys: [snsKeys.subscriptionList(topicName)],
    successTitle: "Subscribed",
    errorTitle: "Subscribe failed",
    onSuccess: () => setShowSubscribe(false),
  })

  // ─── Forms ─────────────────────────────────────────────────────────────────
  const subscribeForm = useForm({
    validators: {
      onChange: z.object({
        protocol: z.enum(["sqs", "sms", "http", "https", "email", "email-json", "lambda"]),
        endpoint: z.string().min(1, "Required"),
      }),
    },
    defaultValues: {
      protocol: "sqs" as "sqs" | "sms" | "http" | "https" | "email" | "email-json" | "lambda",
      endpoint: "",
    },
    onSubmit: ({ value }) =>
      subscribeMut.mutate({ topicName, protocol: value.protocol, endpoint: value.endpoint }),
  })

  return (
    <div className="flex w-full flex-col gap-6">
      <PageHeader
        title={topicName}
        description={topic?.TopicArn}
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh subscriptions"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            <RawStateLink namespace="sns:topics" stateKey={topicName} />
            <Button size="sm" variant="secondary" onClick={() => setShowSubscribe(true)}>
              <Link className="mr-1 h-4 w-4" />
              Subscribe
            </Button>
            <Button size="sm" onClick={() => setShowPublish(true)}>
              <Send className="mr-1 h-4 w-4" />
              Publish
            </Button>
            <Button
              size="sm"
              variant="danger"
              onClick={() => deleteMut.mutate(topicName)}
              disabled={deleteMut.isPending}
            >
              <Trash2 className="mr-1 h-4 w-4" />
              Delete topic
            </Button>
          </div>
        }
      />

      <ApplicationOwnershipBanner candidates={[topic?.TopicArn, topicName]} />

      <Tabs
        selectedKey={activeTab}
        onSelectionChange={(k) => setActiveTab(k as "subscriptions" | "monitor")}
      >
        <TabList aria-label="Topic sections">
          <Tab id="subscriptions">
            <Bell className="mr-1.5 h-3.5 w-3.5" />
            Subscriptions
          </Tab>
          <Tab id="monitor">
            <Activity className="mr-1.5 h-3.5 w-3.5" />
            Monitor
          </Tab>
        </TabList>

        <TabPanel id="subscriptions" className="pt-4">
          {/* Subscriptions */}
          <Card>
            <CardContent className="p-0">
              <div className="flex items-center justify-between border-b px-4 py-3">
                <span className="text-sm font-medium">
                  Subscriptions
                  {subscriptions.length > 0 && (
                    <Badge variant="default" className="ml-2">
                      {subscriptions.length}
                    </Badge>
                  )}
                </span>
                {connected && (
                  <span className="flex items-center gap-1 text-xs text-green-500">
                    <span className="inline-block h-1.5 w-1.5 rounded-full bg-green-500" />
                    live
                  </span>
                )}
              </div>

              {isLoading ? (
                <div className="flex justify-center py-12">
                  <Spinner className="h-5 w-5" />
                </div>
              ) : subscriptions.length === 0 ? (
                <div className="py-12">
                  <EmptyState
                    icon={<Link className="h-6 w-6 opacity-40" />}
                    title="No subscriptions"
                    description="Click Subscribe to add one."
                  />
                </div>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Protocol</TableHead>
                      <TableHead>Endpoint</TableHead>
                      <TableHead>Last delivery</TableHead>
                      <TableHead>ARN</TableHead>
                      <TableHead className="w-12" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {subscriptions.map((sub) => (
                      <TableRow key={sub.SubscriptionArn}>
                        <TableCell>
                          <Badge variant="default">{sub.Protocol}</Badge>
                        </TableCell>
                        <TableCell>
                          <ArnLink arn={sub.Endpoint ?? ""} />
                        </TableCell>
                        <TableCell>
                          <DeliveryIndicator state={deliveryState[sub.SubscriptionArn ?? ""]} />
                        </TableCell>
                        <TableCell className="text-fg-muted">
                          <ArnLink arn={sub.SubscriptionArn ?? ""} className="text-fg-muted" />
                        </TableCell>
                        <TableCell>
                          <Button
                            variant="danger-ghost"
                            size="sm"
                            aria-label="Delete subscription"
                            onClick={() => setDeleteSubTarget(sub)}
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </TabPanel>

        <TabPanel id="monitor" className="pt-4">
          <MonitorPanel
            range={monitorRange}
            onRangeChange={setMonitorRange}
            isLoading={metricsQuery.isLoading}
            isFetching={metricsQuery.isFetching}
            error={metricsQuery.error}
            data={metricsQuery.data}
            cards={SNS_MONITOR_CARDS}
            onRefresh={() =>
              void qc.invalidateQueries({ queryKey: snsKeys.metrics(topicName, monitorRange) })
            }
          />
        </TabPanel>
      </Tabs>

      <div>
        <h2 className={cn(sectionLabel, "mb-2 text-fg-muted")}>Event stream</h2>
        <EventConsole events={topicEvents} connected={connected} onClear={clear} />
      </div>

      <Dialog open={showSubscribe} onOpenChange={setShowSubscribe}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add subscription</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void subscribeForm.handleSubmit()
            }}
          >
            <div className="space-y-3">
              <subscribeForm.Field name="protocol">
                {(field) => (
                  <FormRow>
                    <FormField label="Protocol" error={fieldError(field.state.meta.errors)}>
                      <select
                        className="flex h-8 w-full rounded-md border border-border bg-bg px-3 py-1 text-sm text-fg focus-visible:border-accent focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none"
                        value={field.state.value}
                        onChange={(e) => {
                          field.handleChange(
                            e.target.value as
                              | "sqs"
                              | "sms"
                              | "http"
                              | "https"
                              | "email"
                              | "email-json"
                              | "lambda",
                          )
                          // Clear the endpoint when the protocol changes.
                          subscribeForm.setFieldValue("endpoint", "")
                        }}
                      >
                        <optgroup label="Implemented">
                          <option value="sqs">sqs — SQS queue</option>
                          <option value="lambda">lambda — invokes the function</option>
                          <option value="sms">sms — captured in Inbox</option>
                          <option value="email">email — captured in Inbox</option>
                          <option value="email-json">email-json — captured in Inbox</option>
                          <option value="http">http — requires reachable URL</option>
                          <option value="https">https — requires reachable URL</option>
                        </optgroup>
                        <optgroup label="Not yet implemented">
                          <option value="application" disabled>
                            application (mobile push)
                          </option>
                          <option value="firehose" disabled>
                            firehose
                          </option>
                        </optgroup>
                      </select>
                    </FormField>
                  </FormRow>
                )}
              </subscribeForm.Field>
              <subscribeForm.Field name="endpoint">
                {(field) => {
                  const protocol = subscribeForm.getFieldValue("protocol")
                  const arnResourceType: ArnResourceType | null =
                    protocol === "sqs" ? "sqs" : protocol === "lambda" ? "lambda" : null
                  const placeholder =
                    protocol === "email" || protocol === "email-json"
                      ? "you@example.com"
                      : protocol === "sms"
                        ? "+12125551234"
                        : "https://example.com/hook"
                  return (
                    <FormRow>
                      <FormField label="Endpoint" error={fieldError(field.state.meta.errors)}>
                        {arnResourceType ? (
                          <ResourceArnCombobox
                            resourceType={arnResourceType}
                            value={field.state.value}
                            onChange={(v) => field.handleChange(v)}
                          />
                        ) : (
                          <Input
                            value={field.state.value}
                            onChange={(e) => field.handleChange(e.target.value)}
                            placeholder={placeholder}
                            autoFocus
                          />
                        )}
                      </FormField>
                    </FormRow>
                  )
                }}
              </subscribeForm.Field>
            </div>
            <DialogFooter className="mt-4">
              <Button variant="ghost" type="button" onClick={() => setShowSubscribe(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={subscribeMut.isPending}>
                {subscribeMut.isPending ? <Spinner className="h-4 w-4" /> : "Subscribe"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <PublishMessageDialog
        topicName={topicName}
        open={showPublish}
        onOpenChange={setShowPublish}
      />

      {/* Confirm unsubscribe dialog */}
      <Dialog open={!!deleteSubTarget} onOpenChange={() => setDeleteSubTarget(undefined)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Remove subscription?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-fg-muted">
            Unsubscribe <span className="font-mono font-medium">{deleteSubTarget?.Protocol}</span>{" "}
            endpoint <span className="font-mono font-medium">{deleteSubTarget?.Endpoint}</span>?
          </p>
          <DialogFooter className="mt-4">
            <Button variant="ghost" onClick={() => setDeleteSubTarget(undefined)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={unsubscribeMut.isPending}
              onClick={() =>
                deleteSubTarget && unsubscribeMut.mutate(deleteSubTarget.SubscriptionArn ?? "")
              }
            >
              {unsubscribeMut.isPending ? <Spinner className="h-4 w-4" /> : "Unsubscribe"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

/**
 * Shows whether the most recent publish actually reached this subscription.
 *
 * The state comes from the live event stream, so it covers this session only —
 * "No deliveries yet" means nothing has been observed since the page loaded,
 * not that the subscription has never worked.
 *
 * "Accepted" rather than "Delivered": what SNS reports is the hand-off, and for
 * every protocol the subscriber's own processing happens after it. For `lambda`
 * that gap is real work — the function may still be cold-starting, and if the
 * handler then throws, nothing here changes. The Lambda page is where the
 * invocation's outcome lives.
 */
function DeliveryIndicator({ state }: { state?: DeliveryState }) {
  if (!state) {
    return <span className="text-xs text-fg-muted">No deliveries yet</span>
  }
  const when = new Date(state.at).toLocaleTimeString()
  if (state.status === "failed") {
    return (
      <span
        className="flex items-center gap-1.5 text-xs text-red-500"
        title={state.reason ?? "Delivery failed"}
      >
        <XCircle className="h-3.5 w-3.5" aria-hidden />
        Failed at {when}
      </span>
    )
  }
  return (
    <span
      className="flex items-center gap-1.5 text-xs text-green-500"
      title="SNS handed the message to this subscriber. Whether the subscriber finished processing it is reported on that resource's own page."
    >
      <CheckCircle2 className="h-3.5 w-3.5" aria-hidden />
      Accepted at {when}
    </span>
  )
}
