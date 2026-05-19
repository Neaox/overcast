import { useQuery } from "@tanstack/react-query"
import { GitBranch, RefreshCw } from "lucide-react"
import { cloudfrontContinuousDeploymentPoliciesQueryOptions } from "@/features/cloudfront/data"
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
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

export function ContinuousDeploymentPolicyList() {
  const {
    data: policies = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(cloudfrontContinuousDeploymentPoliciesQueryOptions())

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Continuous Deployment Policies"
        description={`${policies.length} polic${policies.length !== 1 ? "ies" : "y"}`}
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
      ) : policies.length === 0 ? (
        <EmptyState
          icon={<GitBranch className="h-10 w-10" />}
          title="No continuous deployment policies"
          description="Continuous deployment policies let you test CloudFront configuration changes on a staging distribution."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Enabled</TableHead>
              <TableHead>Last Modified</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {policies.map((p) => (
              <TableRow key={p.id}>
                <TableCell className="font-mono text-xs">{p.id}</TableCell>
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
    </div>
  )
}
