import { useQuery } from "@tanstack/react-query"
import { GitBranch } from "lucide-react"
import { cloudfrontContinuousDeploymentPoliciesQueryOptions } from "@/features/cloudfront/data"
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
import { Badge } from "@/components/ui/badge"

export function ContinuousDeploymentPolicyList() {
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
      <ResourceListCard>
        {isLoading || policies.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={policies.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<GitBranch className="h-10 w-10" />}
                title="No continuous deployment policies"
                description="Continuous deployment policies let you test CloudFront configuration changes on a staging distribution."
              />
            }
            errorTitle="Failed to load continuous deployment policies"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>Enabled</TableHead>
                <TableHead>Last modified</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {policies.map((p) => (
                <TableRow key={p.id}>
                  <TableCell>
                    <ResourceName icon={GitBranch} name={p.id} />
                  </TableCell>
                  <TableCell>
                    <Badge variant={p.enabled ? "success" : "default"}>
                      {p.enabled ? "Enabled" : "Disabled"}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-fg-muted">
                    {p.lastModifiedTime ? new Date(p.lastModifiedTime).toLocaleString() : "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>
    </ResourceListPage>
  )
}
