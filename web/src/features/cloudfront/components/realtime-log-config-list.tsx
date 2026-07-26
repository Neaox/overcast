import { useQuery } from "@tanstack/react-query"
import { Activity } from "lucide-react"
import { cloudfrontRealtimeLogConfigsQueryOptions } from "@/features/cloudfront/data"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { EmptyState, QueryListState } from "@/components/ui/primitives"
import {
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
} from "@/components/ui/resource-list-page"
import { ArnText } from "@/components/ui/arn-link"

export function RealtimeLogConfigList() {
  const {
    data: configs = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(cloudfrontRealtimeLogConfigsQueryOptions())

  return (
    <ResourceListPage
      title="Realtime Log Configs"
      count={configs.length}
      actions={<RefreshAction isFetching={isFetching} onClick={() => refetch()} />}
    >
      <ResourceListCard>
        {isLoading || configs.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={configs.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<Activity className="h-10 w-10" />}
                title="No realtime log configs"
                description="Realtime log configurations let you stream CloudFront access logs in near-real-time."
              />
            }
            errorTitle="Failed to load realtime log configs"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>ARN</TableHead>
                <TableHead>Sampling rate</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {configs.map((c) => (
                <TableRow key={c.arn}>
                  <TableCell>
                    <ResourceName icon={Activity} name={c.name} />
                  </TableCell>
                  <TableCell className="text-fg-muted">
                    <ArnText arn={c.arn} />
                  </TableCell>
                  <TableCell>{c.samplingRate}%</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>
    </ResourceListPage>
  )
}
