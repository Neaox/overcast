import { useQuery } from "@tanstack/react-query"
import { Activity, RefreshCw } from "lucide-react"
import { cloudfrontRealtimeLogConfigsQueryOptions } from "@/features/cloudfront/data"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"
import { ArnText } from "@/components/ui/arn-link"

export function RealtimeLogConfigList() {
  const {
    data: configs = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(cloudfrontRealtimeLogConfigsQueryOptions())

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Realtime Log Configs"
        description={`${configs.length} config${configs.length !== 1 ? "s" : ""}`}
        actions={
          <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
            <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
            Refresh
          </Button>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : configs.length === 0 ? (
        <EmptyState
          icon={<Activity className="h-10 w-10" />}
          title="No realtime log configs"
          description="Realtime log configurations let you stream CloudFront access logs in near-real-time."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>ARN</TableHead>
              <TableHead>Sampling Rate</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {configs.map((c) => (
              <TableRow key={c.arn}>
                <TableCell className="font-medium">{c.name}</TableCell>
                <TableCell className="text-fg-muted">
                  <ArnText arn={c.arn} />
                </TableCell>
                <TableCell>{c.samplingRate}%</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
