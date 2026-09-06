import { useQuery } from "@tanstack/react-query"
import { GitBranch } from "lucide-react"
import { cloudfrontContinuousDeploymentPoliciesQueryOptions } from "@/features/cloudfront/data"
import { RefreshAction, ResourceListPage, ResourceName } from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"

interface ContinuousDeploymentPolicyListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function ContinuousDeploymentPolicyList({
  sort,
  onSortChange,
}: ContinuousDeploymentPolicyListProps) {
  const {
    data: policies = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(cloudfrontContinuousDeploymentPoliciesQueryOptions())

  return (
    <ResourceListPage
      title="Continuous Deployment Policies"
      count={policies.length}
      actions={<RefreshAction isFetching={isFetching} onClick={() => refetch()} />}
    >
      <ResourceTable
        query={{ data: policies, isLoading, error }}
        noun="continuous deployment policies"
        emptyIcon={GitBranch}
        emptyTitle="No continuous deployment policies"
        emptyDescription="Continuous deployment policies let you test CloudFront configuration changes on a staging distribution."
        errorTitle="Failed to load continuous deployment policies"
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(policy) => policy.id}
        columns={[
          {
            id: "id",
            header: "ID",
            sortValue: (policy) => policy.id,
            cell: (policy) => <ResourceName icon={GitBranch} name={policy.id} />,
          },
          {
            header: "Enabled",
            cell: (policy) => (
              <Badge variant={policy.enabled ? "success" : "default"}>
                {policy.enabled ? "Enabled" : "Disabled"}
              </Badge>
            ),
          },
          {
            id: "last-modified",
            header: "Last modified",
            cellClassName: "text-fg-muted",
            sortValue: (policy) =>
              policy.lastModifiedTime ? new Date(policy.lastModifiedTime) : undefined,
            cell: (policy) =>
              policy.lastModifiedTime ? new Date(policy.lastModifiedTime).toLocaleString() : "—",
          },
        ]}
      />
    </ResourceListPage>
  )
}
