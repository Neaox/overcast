import { useQuery } from "@tanstack/react-query"
import { RefreshCw } from "lucide-react"
import {
  applicationQueryOptions,
  applicationResourcesQueryOptions,
} from "@/features/applications/data"
import { Button } from "@/components/ui/button"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { ResourceTable } from "@/components/ui/resource-table"
import { Definition, DefinitionCard } from "@/components/ui/definition-card"
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
       * A `DefinitionCard`, not a `ResourceTable`: this is the application's
       * attribute list — one fixed pair per field — not a list of resources, so
       * it has nothing to sort, hide or page (CONTRIBUTING § Tables).
       */}
      <DefinitionCard>
        <Definition label="ID" value={app.id} copyable />
        <Definition label="ARN" value={app.arn} copyable full />
        <Definition label="Description" value={app.description} variant="prose" />
        <Definition label="awsApplication" value={app.applicationTag?.awsApplication} />
      </DefinitionCard>

      <h2 className="font-mono text-lg font-medium">Associated resources ({resources.length})</h2>
      <ResourceTable
        variant="embedded"
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
