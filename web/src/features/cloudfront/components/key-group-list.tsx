import { useQuery } from "@tanstack/react-query"
import { Key } from "lucide-react"
import { cloudfrontKeyGroupsQueryOptions } from "@/features/cloudfront/data"
import { RefreshAction, ResourceListPage, ResourceName } from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"

interface KeyGroupListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function KeyGroupList({ sort, onSortChange }: KeyGroupListProps) {
  const {
    data: keyGroups = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(cloudfrontKeyGroupsQueryOptions())

  return (
    <ResourceListPage
      title="Key Groups"
      count={keyGroups.length}
      actions={<RefreshAction isFetching={isFetching} onClick={() => refetch()} />}
    >
      <ResourceTable
        query={{ data: keyGroups, isLoading, error }}
        noun="key groups"
        emptyIcon={Key}
        emptyTitle="No key groups"
        emptyDescription="Key groups contain public keys used to verify signed URLs and signed cookies."
        errorTitle="Failed to load key groups"
        sort={sort}
        onSortChange={onSortChange}
        // Four columns, none of them noise on a page this small.
        columnToggle={false}
        rowKey={(keyGroup) => keyGroup.id}
        columns={[
          {
            id: "name",
            header: "Name",
            sortValue: (keyGroup) => keyGroup.name,
            cell: (keyGroup) => <ResourceName icon={Key} name={keyGroup.name} />,
          },
          {
            header: "ID",
            cellClassName: "text-fg-muted",
            cell: (keyGroup) => keyGroup.id,
          },
          {
            header: "Comment",
            prose: true,
            cell: (keyGroup) => keyGroup.comment || "—",
          },
          {
            id: "last-modified",
            header: "Last modified",
            cellClassName: "text-fg-muted",
            sortValue: (keyGroup) =>
              keyGroup.lastModifiedTime ? new Date(keyGroup.lastModifiedTime) : undefined,
            cell: (keyGroup) =>
              keyGroup.lastModifiedTime
                ? new Date(keyGroup.lastModifiedTime).toLocaleString()
                : "—",
          },
        ]}
      />
    </ResourceListPage>
  )
}
