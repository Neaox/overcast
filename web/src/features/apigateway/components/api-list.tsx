import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Eye, Globe, Trash2 } from "lucide-react"
import {
  restApisQueryOptions,
  httpApisQueryOptions,
  apigwKeys,
  deleteRestApiMutationOptions,
  deleteHttpApiMutationOptions,
} from "@/features/apigateway/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { EmptyState, QueryListState } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
} from "@/components/ui/resource-list-page"
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
  const [deleteRestTarget, setDeleteRestTarget] = useState<{ id: string; name: string }>()
  const [deleteHttpTarget, setDeleteHttpTarget] = useState<{ id: string; name: string }>()
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
        <ResourceListCard>
          {restLoading || restApis.length === 0 ? (
            <QueryListState
              isLoading={restLoading}
              isEmpty={restApis.length === 0}
              error={restError}
              empty={
                <EmptyState
                  icon={<Globe className="h-10 w-10" />}
                  title="No REST APIs"
                  description="Create a REST API to get started."
                  action={
                    <CreateAction onClick={() => setShowCreateRest(true)}>
                      Create REST API
                    </CreateAction>
                  }
                />
              }
              errorTitle="Failed to load REST APIs"
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>ID</TableHead>
                  <TableHead>Protocol</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead className="w-20 text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {restApis.map((api) => (
                  <TableRow
                    key={api.id}
                    onClick={() =>
                      navigate({
                        to: "/apigateway/rest/$apiId",
                        params: { apiId: api.id },
                      })
                    }
                  >
                    <TableCell>
                      <ResourceName icon={Globe} name={api.name} />
                    </TableCell>
                    <TableCell className="font-mono text-xs text-fg-muted">{api.id}</TableCell>
                    <TableCell>
                      <Badge variant="default">REST</Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs text-fg-muted">
                      {formatDate(api.createdDate)}
                    </TableCell>
                    <TableCell onClick={(e) => e.stopPropagation()}>
                      <RowActions>
                        <RowAction
                          label={`View ${api.name}`}
                          onClick={() =>
                            navigate({
                              to: "/apigateway/rest/$apiId",
                              params: { apiId: api.id },
                            })
                          }
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </RowAction>
                        <RowAction
                          label={`Delete ${api.name}`}
                          tone="danger"
                          onClick={() => setDeleteRestTarget({ id: api.id, name: api.name })}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </RowAction>
                      </RowActions>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </ResourceListCard>
      )}

      {/* HTTP APIs tab */}
      {tab === "http" && (
        <ResourceListCard>
          {httpLoading || httpApis.length === 0 ? (
            <QueryListState
              isLoading={httpLoading}
              isEmpty={httpApis.length === 0}
              error={httpError}
              empty={
                <EmptyState
                  icon={<Globe className="h-10 w-10" />}
                  title="No HTTP APIs"
                  description="Create an HTTP API to get started."
                  action={
                    <CreateAction onClick={() => setShowCreateHttp(true)}>
                      Create HTTP API
                    </CreateAction>
                  }
                />
              }
              errorTitle="Failed to load HTTP APIs"
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>API ID</TableHead>
                  <TableHead>Protocol</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead className="w-20 text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {httpApis.map((api) => (
                  <TableRow
                    key={api.apiId}
                    onClick={() =>
                      navigate({
                        to: "/apigateway/http/$apiId",
                        params: { apiId: api.apiId },
                      })
                    }
                  >
                    <TableCell>
                      <ResourceName icon={Globe} name={api.name} />
                    </TableCell>
                    <TableCell className="font-mono text-xs text-fg-muted">{api.apiId}</TableCell>
                    <TableCell>
                      <Badge variant="success">{api.protocolType}</Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs text-fg-muted">
                      {formatDate(api.createdDate)}
                    </TableCell>
                    <TableCell onClick={(e) => e.stopPropagation()}>
                      <RowActions>
                        <RowAction
                          label={`View ${api.name}`}
                          onClick={() =>
                            navigate({
                              to: "/apigateway/http/$apiId",
                              params: { apiId: api.apiId },
                            })
                          }
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </RowAction>
                        <RowAction
                          label={`Delete ${api.name}`}
                          tone="danger"
                          onClick={() => setDeleteHttpTarget({ id: api.apiId, name: api.name })}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </RowAction>
                      </RowActions>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </ResourceListCard>
      )}

      {/* Create dialogs */}
      <CreateRestApiDialog open={showCreateRest} onOpenChange={setShowCreateRest} />
      <CreateHttpApiDialog open={showCreateHttp} onOpenChange={setShowCreateHttp} />

      {/* Delete REST API confirmation */}
      <ConfirmDialog
        open={!!deleteRestTarget}
        onOpenChange={(open) => !open && setDeleteRestTarget(undefined)}
        title="Delete REST API"
        description={
          <>
            Delete <span className="font-mono font-semibold">{deleteRestTarget?.name}</span>? This
            action cannot be undone.
          </>
        }
        isPending={deleteRestMut.isPending}
        onConfirm={() => deleteRestTarget && deleteRestMut.mutate(deleteRestTarget.id)}
      />

      {/* Delete HTTP API confirmation */}
      <ConfirmDialog
        open={!!deleteHttpTarget}
        onOpenChange={(open) => !open && setDeleteHttpTarget(undefined)}
        title="Delete HTTP API"
        description={
          <>
            Delete <span className="font-mono font-semibold">{deleteHttpTarget?.name}</span>? This
            action cannot be undone.
          </>
        }
        isPending={deleteHttpMut.isPending}
        onConfirm={() => deleteHttpTarget && deleteHttpMut.mutate(deleteHttpTarget.id)}
      />
    </ResourceListPage>
  )
}
