import { useQuery } from "@tanstack/react-query"
import { ShieldCheck } from "lucide-react"
import { cloudfrontFLEProfilesQueryOptions } from "@/features/cloudfront/data"
import { RefreshAction, ResourceListPage, ResourceName } from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"

interface FLEProfileListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function FLEProfileList({ sort, onSortChange }: FLEProfileListProps) {
  const {
    data: profiles = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(cloudfrontFLEProfilesQueryOptions())

  return (
    <ResourceListPage
      title="Field-Level Encryption Profiles"
      count={profiles.length}
      actions={<RefreshAction isFetching={isFetching} onClick={() => refetch()} />}
    >
      <ResourceTable
        query={{ data: profiles, isLoading, error }}
        noun="FLE profiles"
        emptyIcon={ShieldCheck}
        emptyTitle="No FLE profiles"
        emptyDescription="Field-level encryption profiles define which public keys are used to encrypt specific fields."
        errorTitle="Failed to load FLE profiles"
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(profile) => profile.id}
        columns={[
          {
            id: "name",
            header: "Name",
            sortValue: (profile) => profile.name,
            cell: (profile) => <ResourceName icon={ShieldCheck} name={profile.name} />,
          },
          {
            header: "ID",
            cellClassName: "text-fg-muted",
            cell: (profile) => profile.id,
          },
          {
            header: "Comment",
            prose: true,
            cell: (profile) => profile.comment || "—",
          },
          {
            id: "last-modified",
            header: "Last modified",
            cellClassName: "text-fg-muted",
            sortValue: (profile) =>
              profile.lastModifiedTime ? new Date(profile.lastModifiedTime) : undefined,
            cell: (profile) =>
              profile.lastModifiedTime ? new Date(profile.lastModifiedTime).toLocaleString() : "—",
          },
        ]}
      />
    </ResourceListPage>
  )
}
