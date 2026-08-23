import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Boxes, Eye } from "lucide-react"
import { applicationsQueryOptions } from "@/features/applications/data"
import {
  RefreshAction,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
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
      <ResourceTable
        columnToggle={false}
        query={{ data: apps, isLoading, error }}
        noun="applications"
        emptyIcon={Boxes}
        emptyTitle="No applications"
        emptyDescription="Applications are created by CloudFormation stacks that use the AWS::ServiceCatalogAppRegistry::Application resource, typically via CDK's Application construct."
        errorTitle="Failed to load applications"
        rowKey={(app) => app.id}
        onRowClick={(app) =>
          navigate({ to: "/applications/$applicationId", params: { applicationId: app.id } })
        }
        columns={[
          {
            header: "Name",
            sortValue: (app) => app.name,
            cell: (app) => <ResourceName icon={Boxes} name={app.name} />,
          },
          { header: "Description", prose: true, cell: (app) => app.description ?? "—" },
          { header: "ID", cellClassName: "text-fg-muted", cell: (app) => app.id },
        ]}
        rowActions={(app) => (
          <RowAction
            label={`View ${app.name}`}
            onClick={() =>
              navigate({ to: "/applications/$applicationId", params: { applicationId: app.id } })
            }
          >
            <Eye className="h-3.5 w-3.5" />
          </RowAction>
        )}
      />
    </ResourceListPage>
  )
}
