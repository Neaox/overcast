/**
 * BucketConfig — Config tab for /s3/$bucket/config
 *
 * Shows the bucket's event notification configuration (queue, topic, and
 * lambda destinations) in a readable table. Read-only for now; editing
 * directly from the UI is a future enhancement.
 */
import { useQuery } from "@tanstack/react-query"
import { Bell, BellOff, ChevronRight } from "lucide-react"
import { useNavigate } from "@tanstack/react-router"
import { Route } from "@/routes/s3/$bucket/config"
import { s3Queries } from "@/features/s3/data"
import { useEndpoint } from "@/hooks/use-endpoint"
import { Badge } from "@/components/ui/badge"
import { PageHeader, Breadcrumb, Spinner, EmptyState } from "@/components/ui/primitives"
import { Button } from "@/components/ui/button"
import { BucketTabs } from "./bucket-tabs"
import type {
  QueueNotificationConfig,
  TopicNotificationConfig,
  LambdaNotificationConfig,
  NotificationFilterRule,
} from "@/services/api"

export function BucketConfig() {
  const { bucket } = Route.useParams()
  const { endpoint } = useEndpoint()
  const navigate = useNavigate()

  const { data, isLoading } = useQuery(s3Queries.bucketNotification(endpoint.baseUrl, bucket))

  const hasConfig =
    (data?.queueConfigurations?.length ?? 0) > 0 ||
    (data?.topicConfigurations?.length ?? 0) > 0 ||
    (data?.lambdaConfigurations?.length ?? 0) > 0

  return (
    <div className="flex w-full max-w-screen-xl flex-col gap-4">
      <PageHeader
        title={bucket}
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "S3", onClick: () => navigate({ to: "/s3" }) },
              { label: bucket, onClick: () => navigate({ to: "/s3/$bucket", params: { bucket } }) },
              { label: "Configuration" },
            ]}
          />
        }
        actions={
          <Button variant="secondary" size="md" onClick={() => navigate({ to: "/s3" })}>
            Buckets
          </Button>
        }
      />

      <BucketTabs bucket={bucket} active="config" />

      {isLoading ? (
        <div className="flex items-center justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : !hasConfig ? (
        <EmptyState
          icon={<BellOff className="h-8 w-8" />}
          title="No notification configuration"
          description="Use PutBucketNotificationConfiguration to configure event destinations."
        />
      ) : (
        <div className="flex flex-col gap-6">
          {(data?.queueConfigurations?.length ?? 0) > 0 && (
            <ConfigSection
              title="SQS Queue Destinations"
              icon={<Bell className="h-4 w-4 text-yellow-400" />}
            >
              {data!.queueConfigurations.map((q) => (
                <QueueRow key={q.id || q.queueArn} config={q} />
              ))}
            </ConfigSection>
          )}

          {(data?.topicConfigurations?.length ?? 0) > 0 && (
            <ConfigSection
              title="SNS Topic Destinations"
              icon={<Bell className="h-4 w-4 text-pink-400" />}
            >
              {data!.topicConfigurations.map((t) => (
                <TopicRow key={t.id || t.topicArn} config={t} />
              ))}
            </ConfigSection>
          )}

          {(data?.lambdaConfigurations?.length ?? 0) > 0 && (
            <ConfigSection
              title="Lambda Destinations"
              icon={<Bell className="h-4 w-4 text-purple-400" />}
            >
              {data!.lambdaConfigurations.map((l) => (
                <LambdaRow key={l.id || l.functionArn} config={l} />
              ))}
            </ConfigSection>
          )}
        </div>
      )}
    </div>
  )
}

// ─── Sub-components ───────────────────────────────────────────────────────────

function ConfigSection({
  title,
  icon,
  children,
}: {
  title: string
  icon: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        {icon}
        <h2 className="text-sm font-medium text-fg">{title}</h2>
      </div>
      <div className="flex flex-col divide-y divide-border overflow-hidden rounded-lg border border-border bg-bg-elevated">
        {children}
      </div>
    </div>
  )
}

function EventList({ events }: { events: string[] }) {
  return (
    <div className="flex flex-wrap gap-1">
      {events.map((ev) => (
        <Badge key={ev} variant="accent" className="font-mono text-[10px]">
          {ev}
        </Badge>
      ))}
    </div>
  )
}

function FilterList({ rules }: { rules: NotificationFilterRule[] }) {
  if (rules.length === 0) return <span className="text-xs text-fg-subtle">none</span>
  return (
    <div className="flex flex-wrap gap-1">
      {rules.map((r, i) => (
        <Badge key={i} variant="default" className="font-mono text-[10px]">
          {r.name}={r.value}
        </Badge>
      ))}
    </div>
  )
}

function QueueRow({ config: q }: { config: QueueNotificationConfig }) {
  return (
    <div className="flex flex-col gap-3 px-4 py-3">
      <div className="flex items-center gap-2">
        <ChevronRight className="h-3 w-3 shrink-0 text-fg-subtle" />
        <span className="font-mono text-xs break-all text-fg">{q.queueArn}</span>
        {q.id && (
          <Badge variant="default" className="ml-auto shrink-0 text-[10px]">
            {q.id}
          </Badge>
        )}
      </div>
      <ConfigRow label="Events">
        <EventList events={q.events} />
      </ConfigRow>
      <ConfigRow label="Filters">
        <FilterList rules={q.filterRules} />
      </ConfigRow>
    </div>
  )
}

function TopicRow({ config: t }: { config: TopicNotificationConfig }) {
  return (
    <div className="flex flex-col gap-3 px-4 py-3">
      <div className="flex items-center gap-2">
        <ChevronRight className="h-3 w-3 shrink-0 text-fg-subtle" />
        <span className="font-mono text-xs break-all text-fg">{t.topicArn}</span>
        {t.id && (
          <Badge variant="default" className="ml-auto shrink-0 text-[10px]">
            {t.id}
          </Badge>
        )}
      </div>
      <ConfigRow label="Events">
        <EventList events={t.events} />
      </ConfigRow>
      <ConfigRow label="Filters">
        <FilterList rules={t.filterRules} />
      </ConfigRow>
    </div>
  )
}

function LambdaRow({ config: l }: { config: LambdaNotificationConfig }) {
  return (
    <div className="flex flex-col gap-3 px-4 py-3">
      <div className="flex items-center gap-2">
        <ChevronRight className="h-3 w-3 shrink-0 text-fg-subtle" />
        <span className="font-mono text-xs break-all text-fg">{l.functionArn}</span>
        {l.id && (
          <Badge variant="default" className="ml-auto shrink-0 text-[10px]">
            {l.id}
          </Badge>
        )}
      </div>
      <ConfigRow label="Events">
        <EventList events={l.events} />
      </ConfigRow>
      <ConfigRow label="Filters">
        <FilterList rules={l.filterRules} />
      </ConfigRow>
    </div>
  )
}

function ConfigRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start gap-4 pl-5">
      <span className="w-16 shrink-0 text-xs text-fg-muted">{label}</span>
      <div className="flex-1">{children}</div>
    </div>
  )
}
