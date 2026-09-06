import { useQuery } from "@tanstack/react-query"
import { Activity } from "lucide-react"
import { cloudfrontRealtimeLogConfigsQueryOptions } from "@/features/cloudfront/data"
import { RefreshAction, ResourceListPage, ResourceName } from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"
import { ArnText } from "@/components/ui/arn-link"

interface RealtimeLogConfigListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function RealtimeLogConfigList({ sort, onSortChange }: RealtimeLogConfigListProps) {
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
      <ResourceTable
        query={{ data: configs, isLoading, error }}
        noun="realtime log configs"
        emptyIcon={Activity}
        emptyTitle="No realtime log configs"
        emptyDescription="Realtime log configurations let you stream CloudFront access logs in near-real-time."
        errorTitle="Failed to load realtime log configs"
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(config) => config.arn}
        columns={[
          {
            id: "name",
            header: "Name",
            sortValue: (config) => config.name,
            cell: (config) => <ResourceName icon={Activity} name={config.name} />,
          },
          {
            header: "ARN",
            cellClassName: "text-fg-muted",
            cell: (config) => <ArnText arn={config.arn} />,
          },
          {
            id: "sampling-rate",
            header: "Sampling rate",
            sortValue: (config) => config.samplingRate,
            cell: (config) => `${config.samplingRate}%`,
          },
        ]}
      />
    </ResourceListPage>
  )
}
