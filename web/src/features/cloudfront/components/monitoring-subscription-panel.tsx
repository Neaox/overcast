import { useQuery } from "@tanstack/react-query"
import { cloudfrontMonitoringSubscriptionQueryOptions } from "@/features/cloudfront/data"
import { Definition, DefinitionList } from "@/components/ui/definition-card"
import { Spinner } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"

export function MonitoringSubscriptionPanel({ distributionId }: { distributionId: string }) {
  const { data, isLoading } = useQuery(cloudfrontMonitoringSubscriptionQueryOptions(distributionId))

  if (isLoading) {
    return (
      <div className="flex justify-center py-8">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }

  if (!data) return null

  const status = data.realtimeMetricsSubscriptionStatus
  const isEnabled = status === "Enabled"

  // Neither table fits, because there is no list here: GetMonitoringSubscription
  // returns a single setting, and this renders it as one fixed label/value pair.
  // Nothing to sort, hide, click or delete. See CONTRIBUTING § Tables.
  return (
    // Stacked rather than the default: the one label here is three words long,
    // and a 7rem label column would wrap it beside a one-word badge.
    <DefinitionList layout="stacked" className="rounded-md border border-border p-4">
      <Definition
        label="Realtime Metrics Subscription"
        value={<Badge variant={isEnabled ? "success" : "default"}>{status}</Badge>}
      />
    </DefinitionList>
  )
}
