import { useState, useEffect } from "react"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Trash2, RefreshCw, Link, Send } from "lucide-react"
import {
  snsTopicsQueryOptions,
  snsSubscriptionsQueryOptions,
  snsKeys,
  subscribeMutationOptions,
  unsubscribeMutationOptions,
  deleteTopicMutationOptions,
} from "@/features/sns/data"
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
import type { SNSSubscription } from "@/types"
import { cn } from "@/lib/utils"

interface Props {
  topicName: string
}

export function TopicDetail({ topicName }: Props) {
  const navigate = useNavigate()
  const { toast } = useToast()

  const [showSubscribe, setShowSubscribe] = useState(false)
  const [showPublish, setShowPublish] = useState(false)
  const [deleteSubTarget, setDeleteSubTarget] = useState<SNSSubscription>()

  // ─── Data ──────────────────────────────────────────────────────────────────
  const {
    data: subscriptions = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(snsSubscriptionsQueryOptions(topicName))

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
                    <TableCell className="font-mono text-xs">
                      <ArnLink arn={sub.Endpoint ?? ""} />
                    </TableCell>
                    <TableCell className="font-mono text-xs text-fg-muted">
                      <ArnLink arn={sub.SubscriptionArn ?? ""} className="text-fg-muted" />
                    </TableCell>
                    <TableCell>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-destructive hover:text-destructive"
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

      <div>
        <h2 className="mb-2 text-sm font-semibold tracking-wide text-fg-muted uppercase">
          Event stream
        </h2>
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
                          <option value="sms">sms — captured in Inbox</option>
                          <option value="email">email — captured in Inbox</option>
                          <option value="email-json">email-json — captured in Inbox</option>
                          <option value="http">http — requires reachable URL</option>
                          <option value="https">https — requires reachable URL</option>
                        </optgroup>
                        <optgroup label="Not yet implemented">
                          <option value="lambda" disabled>
                            lambda
                          </option>
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
