import { useQuery } from "@tanstack/react-query"
import { Lock } from "lucide-react"
import { cloudfrontFLEConfigsQueryOptions } from "@/features/cloudfront/data"
import { RefreshAction, ResourceListPage, ResourceName } from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"

interface FLEConfigListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function FLEConfigList({ sort, onSortChange }: FLEConfigListProps) {
  const {
    data: configs = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(cloudfrontFLEConfigsQueryOptions())

  return (
    <ResourceListPage
      title="Field-Level Encryption Configs"
      count={configs.length}
      actions={<RefreshAction isFetching={isFetching} onClick={() => refetch()} />}
    >
      <ResourceTable
        query={{ data: configs, isLoading, error }}
        noun="FLE configs"
        emptyIcon={Lock}
        emptyTitle="No FLE configs"
        emptyDescription="Field-level encryption configurations specify which fields in POST requests should be encrypted."
        errorTitle="Failed to load FLE configs"
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(config) => config.id}
        columns={[
          {
            id: "id",
            header: "ID",
            sortValue: (config) => config.id,
            cell: (config) => <ResourceName icon={Lock} name={config.id} />,
          },
          {
            header: "Comment",
            prose: true,
            cell: (config) => config.comment || "—",
          },
          {
            header: "Unknown content type",
            cell: (config) => config.contentTypeProfileConfig,
          },
          {
            id: "last-modified",
            header: "Last modified",
            cellClassName: "text-fg-muted",
            sortValue: (config) =>
              config.lastModifiedTime ? new Date(config.lastModifiedTime) : undefined,
            cell: (config) =>
              config.lastModifiedTime ? new Date(config.lastModifiedTime).toLocaleString() : "—",
          },
        ]}
      />
    </ResourceListPage>
  )
}
