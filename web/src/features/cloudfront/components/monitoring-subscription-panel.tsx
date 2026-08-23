import { useQuery } from "@tanstack/react-query"
import { cloudfrontMonitoringSubscriptionQueryOptions } from "@/features/cloudfront/data"
import { Table, TableBody, TableCell, TableRow } from "@/components/ui/table"
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

  // ResourceTable didn't fit because there is no list here: GetMonitoringSubscription
  // returns a single setting, and this renders it as one fixed label/value row.
  // Nothing to sort, hide, click or delete. See CONTRIBUTING § Tables.
  return (
    <div className="rounded-md border border-border">
      <Table>
        <TableBody>
          <TableRow>
            <TableCell className="w-64 font-medium">Realtime Metrics Subscription</TableCell>
            <TableCell>
              <Badge variant={isEnabled ? "success" : "default"}>{status}</Badge>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
  )
}
