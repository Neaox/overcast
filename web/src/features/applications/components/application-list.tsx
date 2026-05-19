import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Boxes, RefreshCw } from "lucide-react"
import { applicationsQueryOptions } from "@/features/applications/data"
import { Button } from "@/components/ui/button"
import { PageHeader, EmptyState, QueryListState } from "@/components/ui/primitives"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { cn } from "@/lib/utils"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"

export function ApplicationList() {
  const navigate = useNavigate()
  const {
    data: apps = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(applicationsQueryOptions())
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Applications"
        description="Service Catalog AppRegistry — groups of resources managed together"
        actions={
          <>
            <ServiceDocsButton
              service="appregistry"
              label="AppRegistry"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
          </>
        }
      />

      {isLoading || apps.length === 0 ? (
        <QueryListState
          isLoading={isLoading}
          isEmpty={apps.length === 0}
          error={error}
          empty={
            <EmptyState
              icon={<Boxes />}
              title="No applications"
              description="Applications are created by CloudFormation stacks that use the AWS::ServiceCatalogAppRegistry::Application resource, typically via CDK's Application construct."
            />
          }
          errorTitle="Failed to load applications"
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Description</TableHead>
              <TableHead>ID</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {apps.map((app) => (
              <TableRow
                key={app.id}
                onClick={() =>
                  navigate({
                    to: "/applications/$applicationId",
                    params: { applicationId: app.id },
                  })
                }
                className="cursor-pointer"
              >
                <TableCell className="font-medium">{app.name}</TableCell>
                <TableCell className="text-fg-muted">{app.description ?? "—"}</TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">{app.id}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
