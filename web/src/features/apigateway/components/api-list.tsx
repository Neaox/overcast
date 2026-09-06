import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Eye, Globe } from "lucide-react"
import {
  restApisQueryOptions,
  httpApisQueryOptions,
  apigwKeys,
  deleteRestApiMutationOptions,
  deleteHttpApiMutationOptions,
} from "@/features/apigateway/data"
import type { HttpApi, RestApi } from "@/types/apigateway"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import {
  CreateAction,
  RefreshAction,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { formatDate } from "@/lib/format"
import { CreateRestApiDialog } from "./create-rest-api-dialog"
import { CreateHttpApiDialog } from "./create-http-api-dialog"
import { cn } from "@/lib/utils"

type Tab = "rest" | "http"

export function ApiGatewayList() {
  const navigate = useNavigate()
  const [tab, setTab] = useState<Tab>("rest")
  const [showCreateRest, setShowCreateRest] = useState(false)
  const [showCreateHttp, setShowCreateHttp] = useState(false)
  const [deleteRestTarget, setDeleteRestTarget] = useState<RestApi>()
  const [deleteHttpTarget, setDeleteHttpTarget] = useState<HttpApi>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: restApis = [],
    isLoading: restLoading,
    isFetching: restFetching,
    refetch: refetchRest,
    error: restError,
  } = useQuery(restApisQueryOptions())

  const {
    data: httpApis = [],
    isLoading: httpLoading,
    isFetching: httpFetching,
    refetch: refetchHttp,
    error: httpError,
  } = useQuery(httpApisQueryOptions())

  const deleteRestMut = useResourceMutation({
    options: deleteRestApiMutationOptions(),
    invalidateKeys: [apigwKeys.restApis()],
    successTitle: "REST API deleted",
    successDescription: (id) => id,
    onSuccess: () => setDeleteRestTarget(undefined),
  })

  const deleteHttpMut = useResourceMutation({
    options: deleteHttpApiMutationOptions(),
    invalidateKeys: [apigwKeys.httpApis()],
    successTitle: "HTTP API deleted",
    successDescription: (id) => id,
    onSuccess: () => setDeleteHttpTarget(undefined),
  })

  const isFetching = tab === "rest" ? restFetching : httpFetching
  const refetch = tab === "rest" ? refetchRest : refetchHttp
  const totalCount = restApis.length + httpApis.length

  return (
    <ResourceListPage
      title="API Gateway"
      count={totalCount}
      meta={`${restApis.length} REST · ${httpApis.length} HTTP`}
      actions={
        <>
          <ServiceDocsButton
            service="apigateway"
            label="API Gateway"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          {tab === "rest" ? (
            <CreateAction onClick={() => setShowCreateRest(true)}>Create REST API</CreateAction>
          ) : (
            <CreateAction onClick={() => setShowCreateHttp(true)}>Create HTTP API</CreateAction>
          )}
        </>
      }
    >
      {/* Tab bar */}
      <div className="flex gap-1 border-b">
        {(["rest", "http"] as Tab[]).map((t) => (
          <button
            key={t}
            className={cn(
              "px-4 py-2 text-sm transition-colors",
              tab === t
                ? "border-b-2 border-accent font-medium text-fg"
                : "text-fg-muted hover:text-fg",
            )}
            onClick={() => setTab(t)}
          >
            {t === "rest" ? `REST APIs (${restApis.length})` : `HTTP APIs (${httpApis.length})`}
          </button>
        ))}
      </div>

      {/* REST APIs tab */}
      {tab === "rest" && (
        <ResourceTable
          query={{ data: restApis, isLoading: restLoading, error: restError }}
          noun="REST APIs"
          emptyIcon={Globe}
          emptyTitle="No REST APIs"
          emptyDescription="Create a REST API to get started."
          emptyAction={
            <CreateAction onClick={() => setShowCreateRest(true)}>Create REST API</CreateAction>
          }
          errorTitle="Failed to load REST APIs"
          rowKey={(api) => api.id}
          onRowClick={(api) =>
            navigate({ to: "/apigateway/rest/$apiId", params: { apiId: api.id } })
          }
          columns={[
            {
              header: "Name",
              sortValue: (api) => api.name,
              cell: (api) => <ResourceName icon={Globe} name={api.name} />,
            },
            {
              header: "ID",
              cellClassName: "text-fg-muted",
              sortValue: (api) => api.id,
              cell: (api) => api.id,
            },
            { header: "Protocol", cell: () => <Badge variant="default">REST</Badge> },
            {
              header: "Created",
              cellClassName: "text-fg-muted",
              sortValue: (api) => api.createdDate,
              cell: (api) => formatDate(api.createdDate),
            },
          ]}
          rowActions={(api) => (
            <RowAction
              label={`View ${api.name}`}
              onClick={() => navigate({ to: "/apigateway/rest/$apiId", params: { apiId: api.id } })}
            >
              <Eye className="h-3.5 w-3.5" />
            </RowAction>
          )}
          onDelete={{
            target: deleteRestTarget,
            onRequest: setDeleteRestTarget,
            onOpenChange: (open) => !open && setDeleteRestTarget(undefined),
            mutation: deleteRestMut,
            getVars: (api) => api.id,
            label: (api) => api.name,
            noun: "REST API",
            title: "Delete REST API",
            description: (api) => (
              <>
                Delete <span className="font-mono font-semibold">{api.name}</span>? This action
                cannot be undone.
              </>
            ),
          }}
        />
      )}

      {/* HTTP APIs tab */}
      {tab === "http" && (
        <ResourceTable
          query={{ data: httpApis, isLoading: httpLoading, error: httpError }}
          noun="HTTP APIs"
          emptyIcon={Globe}
          emptyTitle="No HTTP APIs"
          emptyDescription="Create an HTTP API to get started."
          emptyAction={
            <CreateAction onClick={() => setShowCreateHttp(true)}>Create HTTP API</CreateAction>
          }
          errorTitle="Failed to load HTTP APIs"
          rowKey={(api) => api.apiId}
          onRowClick={(api) =>
            navigate({ to: "/apigateway/http/$apiId", params: { apiId: api.apiId } })
          }
          columns={[
            {
              header: "Name",
              sortValue: (api) => api.name,
              cell: (api) => <ResourceName icon={Globe} name={api.name} />,
            },
            {
              header: "API ID",
              cellClassName: "text-fg-muted",
              sortValue: (api) => api.apiId,
              cell: (api) => api.apiId,
            },
            {
              header: "Protocol",
              cell: (api) => <Badge variant="success">{api.protocolType}</Badge>,
            },
            {
              header: "Created",
              cellClassName: "text-fg-muted",
              sortValue: (api) => api.createdDate,
              cell: (api) => formatDate(api.createdDate),
            },
          ]}
          rowActions={(api) => (
            <RowAction
              label={`View ${api.name}`}
              onClick={() =>
                navigate({ to: "/apigateway/http/$apiId", params: { apiId: api.apiId } })
              }
            >
              <Eye className="h-3.5 w-3.5" />
            </RowAction>
          )}
          onDelete={{
            target: deleteHttpTarget,
            onRequest: setDeleteHttpTarget,
            onOpenChange: (open) => !open && setDeleteHttpTarget(undefined),
            mutation: deleteHttpMut,
            getVars: (api) => api.apiId,
            label: (api) => api.name,
            noun: "HTTP API",
            title: "Delete HTTP API",
            description: (api) => (
              <>
                Delete <span className="font-mono font-semibold">{api.name}</span>? This action
                cannot be undone.
              </>
            ),
          }}
        />
      )}

      {/* Create dialogs */}
      <CreateRestApiDialog open={showCreateRest} onOpenChange={setShowCreateRest} />
      <CreateHttpApiDialog open={showCreateHttp} onOpenChange={setShowCreateHttp} />
    </ResourceListPage>
  )
}
