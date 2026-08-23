import { useQuery } from "@tanstack/react-query"
import { RefreshCw } from "lucide-react"
import {
  applicationQueryOptions,
  applicationResourcesQueryOptions,
} from "@/features/applications/data"
import { Button } from "@/components/ui/button"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { ResourceTable } from "@/components/ui/resource-table"
import { Card, CardContent } from "@/components/ui/card"
import { cn } from "@/lib/utils"

interface Props {
  applicationId: string
}

export function ApplicationDetail({ applicationId }: Props) {
  const {
    data: app,
    isLoading,
    isFetching,
    refetch,
  } = useQuery(applicationQueryOptions(applicationId))
  const { data: resources = [] } = useQuery(applicationResourcesQueryOptions(applicationId))

  if (isLoading || !app) {
    return <Spinner />
  }

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={app.name}
        actions={
          <Button
            variant="ghost"
            size="sm"
            onClick={() => refetch()}
            disabled={isFetching}
            title="Refresh"
          >
            <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
          </Button>
        }
      />

      {/*
       * Stays a hand-built grid, not a `ResourceTable`: this is the
       * application's attribute list — one fixed row per field — not a list of
       * resources, so it has nothing to sort, hide or page (CONTRIBUTING §
       * Tables). It is not a `<Table>` either.
       */}
      <Card>
        <CardContent className="flex flex-col gap-2 text-sm">
          <div className="flex gap-2">
            <span className="w-32 text-fg-muted">ID</span>
            <span className="font-mono text-xs">{app.id}</span>
          </div>
          <div className="flex gap-2">
            <span className="w-32 text-fg-muted">ARN</span>
            <span className="font-mono text-xs">{app.arn}</span>
          </div>
          {app.description && (
            <div className="flex gap-2">
              <span className="w-32 text-fg-muted">Description</span>
              <span>{app.description}</span>
            </div>
          )}
          <div className="flex gap-2">
            <span className="w-32 text-fg-muted">awsApplication</span>
            <span className="font-mono text-xs">{app.applicationTag?.awsApplication ?? "—"}</span>
          </div>
        </CardContent>
      </Card>

      <h2 className="font-mono text-lg font-medium">Associated resources ({resources.length})</h2>
      <ResourceTable
        variant="embedded"
        columnToggle={false}
        query={{ data: resources, isLoading: false }}
        noun="associated resources"
        emptyTitle="No associated resources"
        emptyDescription="Stacks and resources become associated when CDK's Application construct tags them, or when a CloudFormation ResourceAssociation is declared."
        rowKey={(r) => r.arn}
        columns={[
          { header: "Type", sortValue: (r) => r.resourceType, cell: (r) => r.resourceType },
          { header: "ARN", sortValue: (r) => r.arn, cell: (r) => r.arn },
        ]}
      />
    </div>
  )
}
