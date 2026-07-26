import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Boxes, Eye } from "lucide-react"
import { applicationsQueryOptions } from "@/features/applications/data"
import { EmptyState, QueryListState } from "@/components/ui/primitives"
import {
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
} from "@/components/ui/resource-list-page"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
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
    <ResourceListPage
      title="Applications"
      count={apps.length}
      actions={
        <>
          <ServiceDocsButton
            service="appregistry"
            label="AppRegistry"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
        </>
      }
    >
      <ResourceListCard>
        {isLoading || apps.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={apps.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<Boxes className="h-10 w-10" />}
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
                <TableHead className="w-20 text-right">Actions</TableHead>
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
                >
                  <TableCell>
                    <ResourceName icon={Boxes} name={app.name} />
                  </TableCell>
                  <TableCell className="text-fg-muted">{app.description ?? "—"}</TableCell>
                  <TableCell className="font-mono text-xs text-fg-muted">{app.id}</TableCell>
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <RowActions>
                      <RowAction
                        label={`View ${app.name}`}
                        onClick={() =>
                          navigate({
                            to: "/applications/$applicationId",
                            params: { applicationId: app.id },
                          })
                        }
                      >
                        <Eye className="h-3.5 w-3.5" />
                      </RowAction>
                    </RowActions>
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
