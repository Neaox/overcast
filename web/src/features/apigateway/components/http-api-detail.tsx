import { useState } from "react"
import { useQuery, useMutation } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Trash2, RefreshCw, Plus } from "lucide-react"
import {
  httpApiQueryOptions,
  routesQueryOptions,
  httpIntegrationsQueryOptions,
  httpStagesQueryOptions,
  v2AuthorizersQueryOptions,
  apigwKeys,
  deleteHttpApiMutationOptions,
  createRouteMutationOptions,
  createHttpIntegrationMutationOptions,
  createHttpStageMutationOptions,
  deleteRouteMutationOptions,
  deleteHttpStageMutationOptions,
  createV2AuthorizerMutationOptions,
  deleteV2AuthorizerMutationOptions,
} from "@/features/apigateway/data"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Badge } from "@/components/ui/badge"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { useToast } from "@/components/ui/toast"
import { formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"

interface Props {
  apiId: string
}

type Tab = "routes" | "integrations" | "stages" | "authorizers"

export function HttpApiDetail({ apiId }: Props) {
  const navigate = useNavigate()
  const { toast } = useToast()

  const [tab, setTab] = useState<Tab>("routes")
  const [showDelete, setShowDelete] = useState(false)
  const [showCreateRoute, setShowCreateRoute] = useState(false)
  const [showCreateIntegration, setShowCreateIntegration] = useState(false)
  const [showCreateStage, setShowCreateStage] = useState(false)
  const [deleteRouteTarget, setDeleteRouteTarget] = useState<{
    routeId: string
    routeKey: string
  }>()
  const [deleteStageTarget, setDeleteStageTarget] = useState<string>()
  const [showCreateV2Authorizer, setShowCreateV2Authorizer] = useState(false)
  const [deleteV2AuthorizerTarget, setDeleteV2AuthorizerTarget] = useState<{
    authorizerId: string
    name: string
  }>()

  // Form state
  const [newRouteKey, setNewRouteKey] = useState("")
  const [newIntType, setNewIntType] = useState("AWS_PROXY")
  const [newIntUri, setNewIntUri] = useState("")
  const [newStageName, setNewStageName] = useState("")
  const [newV2AuthName, setNewV2AuthName] = useState("")
  const [newV2AuthType, setNewV2AuthType] = useState("JWT")
  const [newV2AuthIdentitySource, setNewV2AuthIdentitySource] = useState("")
  const [newV2AuthJwtIssuer, setNewV2AuthJwtIssuer] = useState("")
  const [newV2AuthJwtAudience, setNewV2AuthJwtAudience] = useState("")

  const {
    data: api,
    isLoading: apiLoading,
    isFetching: apiFetching,
    refetch: refetchApi,
  } = useQuery(httpApiQueryOptions(apiId))

  const {
    data: routes = [],
    isLoading: routesLoading,
    refetch: refetchRoutes,
  } = useQuery(routesQueryOptions(apiId))

  const { data: integrations = [], refetch: refetchIntegrations } = useQuery(
    httpIntegrationsQueryOptions(apiId),
  )

  const { data: stages = [], refetch: refetchStages } = useQuery(httpStagesQueryOptions(apiId))
  const { data: v2Authorizers = [], refetch: refetchV2Authorizers } = useQuery(
    v2AuthorizersQueryOptions(apiId),
  )

  const deleteMut = useMutation({
    ...deleteHttpApiMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.httpApis() })
      void navigate({ to: "/apigateway" })
      toast({ title: "HTTP API deleted", description: api?.name })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  const createRouteMut = useMutation({
    ...createRouteMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.routes(apiId) })
      setShowCreateRoute(false)
      setNewRouteKey("")
      toast({ title: "Route created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create route failed", description: err.message, variant: "danger" }),
  })

  const deleteRouteMut = useMutation({
    ...deleteRouteMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.routes(apiId) })
      setDeleteRouteTarget(undefined)
      toast({ title: "Route deleted" })
    },
    onError: (err: Error) =>
      toast({ title: "Delete route failed", description: err.message, variant: "danger" }),
  })

  const createIntMut = useMutation({
    ...createHttpIntegrationMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.integrations(apiId) })
      setShowCreateIntegration(false)
      setNewIntUri("")
      toast({ title: "Integration created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create integration failed", description: err.message, variant: "danger" }),
  })

  const createStageMut = useMutation({
    ...createHttpStageMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.httpStages(apiId) })
      setShowCreateStage(false)
      setNewStageName("")
      toast({ title: "Stage created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create stage failed", description: err.message, variant: "danger" }),
  })

  const deleteStageMut = useMutation({
    ...deleteHttpStageMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.httpStages(apiId) })
      setDeleteStageTarget(undefined)
      toast({ title: "Stage deleted" })
    },
    onError: (err: Error) =>
      toast({ title: "Delete stage failed", description: err.message, variant: "danger" }),
  })

  const createV2AuthorizerMut = useMutation({
    ...createV2AuthorizerMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.v2Authorizers(apiId) })
      setShowCreateV2Authorizer(false)
      setNewV2AuthName("")
      setNewV2AuthType("JWT")
      setNewV2AuthIdentitySource("")
      setNewV2AuthJwtIssuer("")
      setNewV2AuthJwtAudience("")
      toast({ title: "Authorizer created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create authorizer failed", description: err.message, variant: "danger" }),
  })

  const deleteV2AuthorizerMut = useMutation({
    ...deleteV2AuthorizerMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.v2Authorizers(apiId) })
      setDeleteV2AuthorizerTarget(undefined)
      toast({ title: "Authorizer deleted" })
    },
    onError: (err: Error) =>
      toast({ title: "Delete authorizer failed", description: err.message, variant: "danger" }),
  })

  function refetchAll() {
    void refetchApi()
    void refetchRoutes()
    void refetchIntegrations()
    void refetchStages()
    void refetchV2Authorizers()
  }

  if (apiLoading || routesLoading) {
    return (
      <div className="flex justify-center py-16">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!api) return null

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={api.name}
        description={api.description || api.apiId}
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={refetchAll} disabled={apiFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", apiFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" variant="danger" onClick={() => setShowDelete(true)}>
              <Trash2 className="mr-1.5 h-3.5 w-3.5" />
              Delete
            </Button>
          </>
        }
      />

      <ApplicationOwnershipBanner candidates={[api.apiId, api.name]} />

      {/* Summary cards */}
      <div className="grid grid-cols-4 gap-3">
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="text-xs text-fg-muted">API ID</div>
          <div className="mt-1 font-mono text-sm">{api.apiId}</div>
        </div>
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="text-xs text-fg-muted">Protocol</div>
          <div className="mt-1">
            <Badge variant="success">{api.protocolType}</Badge>
          </div>
        </div>
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="text-xs text-fg-muted">Routes</div>
          <div className="mt-1 text-2xl font-semibold">{routes.length}</div>
        </div>
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="text-xs text-fg-muted">Created</div>
          <div className="mt-1 text-sm">{formatDate(api.createdDate)}</div>
        </div>
      </div>

      {/* CORS info if configured */}
      {api.corsConfiguration && (
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="mb-2 text-xs font-medium text-fg-muted">CORS Configuration</div>
          <div className="flex gap-6 text-sm">
            {api.corsConfiguration.allowOrigins && (
              <div>
                <span className="text-fg-muted">Origins: </span>
                <span className="font-mono">{api.corsConfiguration.allowOrigins.join(", ")}</span>
              </div>
            )}
            {api.corsConfiguration.allowMethods && (
              <div>
                <span className="text-fg-muted">Methods: </span>
                <span className="font-mono">{api.corsConfiguration.allowMethods.join(", ")}</span>
              </div>
            )}
          </div>
        </div>
      )}

      {/* Tabs */}
      <div className="flex gap-1 border-b">
        {(["routes", "integrations", "stages", "authorizers"] as Tab[]).map((t) => (
          <button
            key={t}
            className={cn(
              "px-4 py-2 text-sm capitalize transition-colors",
              tab === t
                ? "border-b-2 border-accent font-medium text-fg"
                : "text-fg-muted hover:text-fg",
            )}
            onClick={() => setTab(t)}
          >
            {t}
          </button>
        ))}
      </div>

      {/* Routes tab */}
      {tab === "routes" && (
        <div className="flex flex-col gap-3">
          <div className="flex justify-end">
            <Button size="sm" onClick={() => setShowCreateRoute(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Route
            </Button>
          </div>
          {routes.length === 0 ? (
            <p className="py-8 text-center text-sm text-fg-muted">No routes defined.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Route Key</TableHead>
                  <TableHead>Route ID</TableHead>
                  <TableHead>Target</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {routes.map((route) => (
                  <TableRow key={route.routeId}>
                    <TableCell className="font-mono text-sm font-medium">
                      {route.routeKey}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-fg-muted">
                      {route.routeId}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-fg-muted">
                      {route.target || "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-danger hover:text-danger"
                        onClick={() =>
                          setDeleteRouteTarget({
                            routeId: route.routeId,
                            routeKey: route.routeKey,
                          })
                        }
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      )}

      {/* Integrations tab */}
      {tab === "integrations" && (
        <div className="flex flex-col gap-3">
          <div className="flex justify-end">
            <Button size="sm" onClick={() => setShowCreateIntegration(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Integration
            </Button>
          </div>
          {integrations.length === 0 ? (
            <p className="py-8 text-center text-sm text-fg-muted">No integrations defined.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Integration ID</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>URI</TableHead>
                  <TableHead>Payload Format</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {integrations.map((int) => (
                  <TableRow key={int.integrationId}>
                    <TableCell className="font-mono text-xs">{int.integrationId}</TableCell>
                    <TableCell>
                      <Badge variant="default">{int.integrationType}</Badge>
                    </TableCell>
                    <TableCell className="max-w-60 truncate font-mono text-xs text-fg-muted">
                      {int.integrationUri || "—"}
                    </TableCell>
                    <TableCell className="text-sm text-fg-muted">
                      {int.payloadFormatVersion || "1.0"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      )}

      {/* Stages tab */}
      {tab === "stages" && (
        <div className="flex flex-col gap-3">
          <div className="flex justify-end">
            <Button size="sm" onClick={() => setShowCreateStage(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Stage
            </Button>
          </div>
          {stages.length === 0 ? (
            <p className="py-8 text-center text-sm text-fg-muted">No stages.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Stage Name</TableHead>
                  <TableHead>Auto Deploy</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {stages.map((stage) => (
                  <TableRow key={stage.stageName}>
                    <TableCell className="font-medium">{stage.stageName}</TableCell>
                    <TableCell>
                      <Badge variant={stage.autoDeploy ? "success" : "default"}>
                        {stage.autoDeploy ? "Yes" : "No"}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-sm text-fg-muted">
                      {formatDate(stage.createdDate)}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-danger hover:text-danger"
                        onClick={() => setDeleteStageTarget(stage.stageName)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      )}

      {/* Authorizers tab */}
      {tab === "authorizers" && (
        <div className="flex flex-col gap-3">
          <div className="flex justify-end">
            <Button size="sm" onClick={() => setShowCreateV2Authorizer(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Add Authorizer
            </Button>
          </div>
          {v2Authorizers.length === 0 ? (
            <p className="py-8 text-center text-sm text-fg-muted">No authorizers defined.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Identity Source</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {v2Authorizers.map((auth) => (
                  <TableRow key={auth.authorizerId}>
                    <TableCell className="font-medium">{auth.name}</TableCell>
                    <TableCell>
                      <Badge variant="default">{auth.authorizerType}</Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs text-fg-muted">
                      {auth.identitySource || "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-danger hover:text-danger"
                        onClick={() =>
                          setDeleteV2AuthorizerTarget({
                            authorizerId: auth.authorizerId,
                            name: auth.name,
                          })
                        }
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      )}

      {/* Delete API confirmation */}
      <ConfirmDialog
        open={showDelete}
        onOpenChange={setShowDelete}
        title="Delete HTTP API"
        description={
          <>
            Delete <span className="font-mono font-semibold">{api.name}</span>? All routes,
            integrations, and stages will be removed.
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteMut.mutate(apiId)}
      />

      {/* Delete route confirmation */}
      <ConfirmDialog
        open={!!deleteRouteTarget}
        onOpenChange={(open) => !open && setDeleteRouteTarget(undefined)}
        title="Delete Route"
        description={
          <>
            Delete route{" "}
            <span className="font-mono font-semibold">{deleteRouteTarget?.routeKey}</span>?
          </>
        }
        isPending={deleteRouteMut.isPending}
        onConfirm={() =>
          deleteRouteTarget && deleteRouteMut.mutate({ apiId, routeId: deleteRouteTarget.routeId })
        }
      />

      {/* Delete stage confirmation */}
      <ConfirmDialog
        open={!!deleteStageTarget}
        onOpenChange={(open) => !open && setDeleteStageTarget(undefined)}
        title="Delete Stage"
        description={
          <>
            Delete stage <span className="font-mono font-semibold">{deleteStageTarget}</span>?
          </>
        }
        isPending={deleteStageMut.isPending}
        onConfirm={() =>
          deleteStageTarget && deleteStageMut.mutate({ apiId, stageName: deleteStageTarget })
        }
      />

      {/* Create route dialog */}
      <Dialog
        open={showCreateRoute}
        onOpenChange={(v) => {
          if (!v) {
            setShowCreateRoute(false)
            setNewRouteKey("")
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create Route</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              createRouteMut.mutate({ apiId, routeKey: newRouteKey })
            }}
            className="flex flex-col gap-4"
          >
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="route-key">
                Route Key
              </label>
              <input
                id="route-key"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newRouteKey}
                onChange={(e) => setNewRouteKey(e.target.value)}
                placeholder="GET /items"
                autoFocus
              />
              <p className="mt-1 text-xs text-fg-muted">
                Format: METHOD /path (e.g. GET /users, POST /items, $default)
              </p>
            </div>
            <DialogFooter>
              <Button variant="ghost" type="button" onClick={() => setShowCreateRoute(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={!newRouteKey || createRouteMut.isPending}>
                {createRouteMut.isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Create integration dialog */}
      <Dialog
        open={showCreateIntegration}
        onOpenChange={(v) => {
          if (!v) {
            setShowCreateIntegration(false)
            setNewIntUri("")
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create Integration</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              createIntMut.mutate({
                apiId,
                integrationType: newIntType,
                integrationUri: newIntUri || undefined,
              })
            }}
            className="flex flex-col gap-4"
          >
            <div>
              <label className="mb-1 block text-sm font-medium">Integration Type</label>
              <select
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newIntType}
                onChange={(e) => setNewIntType(e.target.value)}
              >
                {["AWS_PROXY", "HTTP_PROXY", "MOCK"].map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="int-uri">
                Integration URI
              </label>
              <input
                id="int-uri"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newIntUri}
                onChange={(e) => setNewIntUri(e.target.value)}
                placeholder="arn:aws:lambda:..."
              />
            </div>
            <DialogFooter>
              <Button variant="ghost" type="button" onClick={() => setShowCreateIntegration(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={createIntMut.isPending}>
                {createIntMut.isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Create stage dialog */}
      <Dialog
        open={showCreateStage}
        onOpenChange={(v) => {
          if (!v) {
            setShowCreateStage(false)
            setNewStageName("")
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create Stage</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              createStageMut.mutate({ apiId, stageName: newStageName })
            }}
            className="flex flex-col gap-4"
          >
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="http-stage-name">
                Stage Name
              </label>
              <input
                id="http-stage-name"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newStageName}
                onChange={(e) => setNewStageName(e.target.value)}
                placeholder="$default"
                autoFocus
              />
            </div>
            <DialogFooter>
              <Button variant="ghost" type="button" onClick={() => setShowCreateStage(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={!newStageName || createStageMut.isPending}>
                {createStageMut.isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Delete v2 authorizer confirmation */}
      <ConfirmDialog
        open={!!deleteV2AuthorizerTarget}
        onOpenChange={(open) => !open && setDeleteV2AuthorizerTarget(undefined)}
        title="Delete Authorizer"
        description={
          <>
            Delete authorizer{" "}
            <span className="font-mono font-semibold">{deleteV2AuthorizerTarget?.name}</span>?
          </>
        }
        isPending={deleteV2AuthorizerMut.isPending}
        onConfirm={() =>
          deleteV2AuthorizerTarget &&
          deleteV2AuthorizerMut.mutate({
            apiId,
            authorizerId: deleteV2AuthorizerTarget.authorizerId,
          })
        }
      />

      {/* Create v2 authorizer dialog */}
      <Dialog
        open={showCreateV2Authorizer}
        onOpenChange={(v) => {
          if (!v) {
            setShowCreateV2Authorizer(false)
            setNewV2AuthName("")
            setNewV2AuthType("JWT")
            setNewV2AuthIdentitySource("")
            setNewV2AuthJwtIssuer("")
            setNewV2AuthJwtAudience("")
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add Authorizer</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              const audience = newV2AuthJwtAudience
                .split(",")
                .map((s) => s.trim())
                .filter(Boolean)
              createV2AuthorizerMut.mutate({
                apiId,
                name: newV2AuthName,
                authorizerType: newV2AuthType,
                identitySource: newV2AuthIdentitySource || undefined,
                jwtConfiguration:
                  newV2AuthType === "JWT" && newV2AuthJwtIssuer
                    ? { issuer: newV2AuthJwtIssuer, audience }
                    : undefined,
              })
            }}
            className="flex flex-col gap-4"
          >
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="v2-auth-name">
                Name <span className="text-danger">*</span>
              </label>
              <input
                id="v2-auth-name"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newV2AuthName}
                onChange={(e) => setNewV2AuthName(e.target.value)}
                placeholder="MyAuthorizer"
                autoFocus
                required
              />
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium">Type</label>
              <select
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newV2AuthType}
                onChange={(e) => setNewV2AuthType(e.target.value)}
              >
                {["JWT", "REQUEST"].map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="v2-auth-identity-source">
                Identity Source
              </label>
              <input
                id="v2-auth-identity-source"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newV2AuthIdentitySource}
                onChange={(e) => setNewV2AuthIdentitySource(e.target.value)}
                placeholder="$request.header.Authorization"
              />
            </div>
            {newV2AuthType === "JWT" && (
              <>
                <div>
                  <label className="mb-1 block text-sm font-medium" htmlFor="v2-auth-jwt-issuer">
                    JWT Issuer
                  </label>
                  <input
                    id="v2-auth-jwt-issuer"
                    className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                    value={newV2AuthJwtIssuer}
                    onChange={(e) => setNewV2AuthJwtIssuer(e.target.value)}
                    placeholder="https://cognito-idp.us-east-1.amazonaws.com/..."
                  />
                </div>
                <div>
                  <label
                    className="mb-1 block text-sm font-medium"
                    htmlFor="v2-auth-jwt-audience"
                  >
                    Audience (comma-separated)
                  </label>
                  <input
                    id="v2-auth-jwt-audience"
                    className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                    value={newV2AuthJwtAudience}
                    onChange={(e) => setNewV2AuthJwtAudience(e.target.value)}
                    placeholder="my-client-id, another-client-id"
                  />
                </div>
              </>
            )}
            <DialogFooter>
              <Button
                variant="ghost"
                type="button"
                onClick={() => setShowCreateV2Authorizer(false)}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={!newV2AuthName || createV2AuthorizerMut.isPending}>
                {createV2AuthorizerMut.isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
