import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { RefreshCw } from "lucide-react"
import {
  applicationQueryOptions,
  applicationResourcesQueryOptions,
} from "@/features/applications/data"
import { Button } from "@/components/ui/button"
import { PageHeader, Breadcrumb, Spinner, EmptyState } from "@/components/ui/primitives"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Card, CardContent } from "@/components/ui/card"
import { cn } from "@/lib/utils"

interface Props {
  applicationId: string
}

export function ApplicationDetail({ applicationId }: Props) {
  const navigate = useNavigate()

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
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "Applications", onClick: () => navigate({ to: "/applications" }) },
              { label: app.name },
            ]}
          />
        }
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

      <h2 className="text-lg font-medium">Associated resources ({resources.length})</h2>
      {resources.length === 0 ? (
        <EmptyState
          title="No associated resources"
          description="Stacks and resources become associated when CDK's Application construct tags them, or when a CloudFormation ResourceAssociation is declared."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Type</TableHead>
              <TableHead>ARN</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {resources.map((r) => (
              <TableRow key={r.arn}>
                <TableCell>{r.resourceType}</TableCell>
                <TableCell className="font-mono text-xs">{r.arn}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
