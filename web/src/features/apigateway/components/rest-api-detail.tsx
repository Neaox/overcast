import { Fragment, useState, useCallback, useMemo } from "react"
import { useQuery, useMutation } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import {
  Trash2,
  RefreshCw,
  Plus,
  ChevronRight,
  Rocket,
  Copy,
  Send,
  Check,
  Shield,
  LogIn,
  KeyRound,
} from "lucide-react"
import {
  restApiQueryOptions,
  resourcesQueryOptions,
  stagesQueryOptions,
  deploymentsQueryOptions,
  authorizersQueryOptions,
  apigwKeys,
  deleteRestApiMutationOptions,
  createResourceMutationOptions,
  createDeploymentMutationOptions,
  createStageMutationOptions,
  putMethodMutationOptions,
  putIntegrationMutationOptions,
  createAuthorizerMutationOptions,
  deleteAuthorizerMutationOptions,
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
import { useEndpoint } from "@/hooks/use-endpoint"
import { cognito } from "@/services/api/cognito"
import type { Authorizer } from "@/services/api/apigateway"
import type { ApiResource, ApiMethod, ApiIntegration } from "@/types"

interface Props {
  apiId: string
}

type Tab = "resources" | "stages" | "deployments" | "authorizers"

export function RestApiDetail({ apiId }: Props) {
  const navigate = useNavigate()
  const { toast } = useToast()

  const [tab, setTab] = useState<Tab>("resources")
  const [showDelete, setShowDelete] = useState(false)
  const [showCreateResource, setShowCreateResource] = useState(false)
  const [showCreateStage, setShowCreateStage] = useState(false)
  const [showAddMethod, setShowAddMethod] = useState(false)
  const [selectedResource, setSelectedResource] = useState<ApiResource>()
  const [expandedResources, setExpandedResources] = useState<Set<string>>(new Set())
  const [showCreateAuthorizer, setShowCreateAuthorizer] = useState(false)
  const [deleteAuthorizerTarget, setDeleteAuthorizerTarget] = useState<{
    id: string
    name: string
  }>()
  const [copiedId, setCopiedId] = useState<string>()

  // Test harness state
  const [testTarget, setTestTarget] = useState<{
    resourceId: string
    method: string
    path: string
  }>()
  const [testBody, setTestBody] = useState("")
  const [testHeaders, setTestHeaders] = useState("Content-Type: application/json")
  const [testResponse, setTestResponse] = useState<{
    status: number
    statusText: string
    headers: Record<string, string>
    body: string
    elapsed: number
  }>()
  const [testLoading, setTestLoading] = useState(false)

  // Cognito auth state
  const [cognitoPoolId, setCognitoPoolId] = useState("")
  const [cognitoClientId, setCognitoClientId] = useState("")
  const [cognitoUsername, setCognitoUsername] = useState("")
  const [cognitoPassword, setCognitoPassword] = useState("")
  const [cognitoToken, setCognitoToken] = useState("")
  const [cognitoAuthLoading, setCognitoAuthLoading] = useState(false)
  const [cognitoAuthError, setCognitoAuthError] = useState("")
  const [cognitoUsers, setCognitoUsers] = useState<{ username: string; status: string }[]>([])
  const [cognitoClients, setCognitoClients] = useState<{ id: string; name: string }[]>([])
  const [cognitoPoolsLoaded, setCognitoPoolsLoaded] = useState(false)
  const [localCognitoPools, setLocalCognitoPools] = useState<{ id: string; name: string }[]>([])
  const [localPoolsLoading, setLocalPoolsLoading] = useState(false)

  const endpoint = useEndpoint()

  // Form state
  const [newPathPart, setNewPathPart] = useState("")
  const [parentResourceId, setParentResourceId] = useState("")
  const [newStageName, setNewStageName] = useState("")
  const [selectedDeploymentId, setSelectedDeploymentId] = useState("")
  const [newMethodVerb, setNewMethodVerb] = useState("GET")
  const [newIntegrationType, setNewIntegrationType] = useState("MOCK")
  const [newAuthName, setNewAuthName] = useState("")
  const [newAuthType, setNewAuthType] = useState("TOKEN")
  const [newAuthUri, setNewAuthUri] = useState("")
  const [newAuthIdentitySource, setNewAuthIdentitySource] = useState("")

  const {
    data: api,
    isLoading: apiLoading,
    isFetching: apiFetching,
    refetch: refetchApi,
  } = useQuery(restApiQueryOptions(apiId))

  const {
    data: resources = [],
    isLoading: resourcesLoading,
    refetch: refetchResources,
  } = useQuery(resourcesQueryOptions(apiId))

  const { data: stages = [], refetch: refetchStages } = useQuery(stagesQueryOptions(apiId))
  const { data: deployments = [], refetch: refetchDeployments } = useQuery(
    deploymentsQueryOptions(apiId),
  )
  const { data: authorizers = [], refetch: refetchAuthorizers } = useQuery(
    authorizersQueryOptions(apiId),
  )

  const deleteMut = useMutation({
    ...deleteRestApiMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.restApis() })
      void navigate({ to: "/apigateway" })
      toast({ title: "REST API deleted", description: api?.name })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  const createResourceMut = useMutation({
    ...createResourceMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.resources(apiId) })
      setShowCreateResource(false)
      setNewPathPart("")
      toast({ title: "Resource created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create resource failed", description: err.message, variant: "danger" }),
  })

  const deployMut = useMutation({
    ...createDeploymentMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.deployments(apiId) })
      toast({ title: "Deployment created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Deploy failed", description: err.message, variant: "danger" }),
  })

  const createStageMut = useMutation({
    ...createStageMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.stages(apiId) })
      setShowCreateStage(false)
      setNewStageName("")
      toast({ title: "Stage created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create stage failed", description: err.message, variant: "danger" }),
  })

  const putMethodMut = useMutation({
    ...putMethodMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.resources(apiId) })
      setShowAddMethod(false)
      toast({ title: "Method added", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Add method failed", description: err.message, variant: "danger" }),
  })

  const putIntegrationMut = useMutation({
    ...putIntegrationMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.resources(apiId) })
      toast({ title: "Integration set", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Set integration failed", description: err.message, variant: "danger" }),
  })

  const createAuthorizerMut = useMutation({
    ...createAuthorizerMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.authorizers(apiId) })
      setShowCreateAuthorizer(false)
      setNewAuthName("")
      setNewAuthType("TOKEN")
      setNewAuthUri("")
      setNewAuthIdentitySource("")
      toast({ title: "Authorizer created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create authorizer failed", description: err.message, variant: "danger" }),
  })

  const deleteAuthorizerMut = useMutation({
    ...deleteAuthorizerMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.authorizers(apiId) })
      setDeleteAuthorizerTarget(undefined)
      toast({ title: "Authorizer deleted" })
    },
    onError: (err: Error) =>
      toast({ title: "Delete authorizer failed", description: err.message, variant: "danger" }),
  })

  function refetchAll() {
    void refetchApi()
    void refetchResources()
    void refetchStages()
    void refetchDeployments()
    void refetchAuthorizers()
  }

  function toggleResource(id: string) {
    setExpandedResources((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const firstStage = stages[0]?.stageName

  function buildInvokeUrl(path: string): string {
    const base = endpoint.baseUrl.replace(/\/$/, "")
    if (firstStage) {
      return `${base}/restapis/${apiId}/${firstStage}/_user_request_${path}`
    }
    return `${base}/restapis/${apiId}/_user_request_${path}`
  }

  const copyUrl = useCallback(
    (path: string, id: string) => {
      void navigator.clipboard.writeText(buildInvokeUrl(path))
      setCopiedId(id)
      setTimeout(() => setCopiedId(undefined), 1500)
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [endpoint.baseUrl, apiId, firstStage],
  )

  async function sendTestRequest() {
    if (!testTarget) return
    setTestLoading(true)
    setTestResponse(undefined)

    const url = buildInvokeUrl(testTarget.path)
    const headerMap: Record<string, string> = {}
    for (const line of testHeaders.split("\n")) {
      const idx = line.indexOf(":")
      if (idx > 0) {
        headerMap[line.slice(0, idx).trim()] = line.slice(idx + 1).trim()
      }
    }

    const start = performance.now()
    try {
      const resp = await fetch(url, {
        method: testTarget.method,
        headers: headerMap,
        body: ["GET", "HEAD", "OPTIONS"].includes(testTarget.method)
          ? undefined
          : testBody || undefined,
      })
      const elapsed = Math.round(performance.now() - start)
      const body = await resp.text()
      const respHeaders: Record<string, string> = {}
      resp.headers.forEach((v, k) => {
        respHeaders[k] = v
      })
      setTestResponse({
        status: resp.status,
        statusText: resp.statusText,
        headers: respHeaders,
        body,
        elapsed,
      })
    } catch (err) {
      const elapsed = Math.round(performance.now() - start)
      setTestResponse({
        status: 0,
        statusText: "Network Error",
        headers: {},
        body: err instanceof Error ? err.message : String(err),
        elapsed,
      })
    } finally {
      setTestLoading(false)
    }
  }

  // ─── Cognito authorizer detection ───────────────────────────────────────

  /** Find the COGNITO_USER_POOLS authorizer used by the test target method. */
  const cognitoAuthorizer = useMemo(() => {
    if (!testTarget) return undefined
    const resource = resources.find((r) => r.id === testTarget.resourceId)
    const method = resource?.resourceMethods?.[testTarget.method]
    if (!method?.authorizerId) return undefined
    return authorizers.find((a) => a.id === method.authorizerId && a.type === "COGNITO_USER_POOLS")
  }, [testTarget, resources, authorizers])

  /** Also detect if ANY authorizer on the API is Cognito (for proactive auth). */
  const anyCognitoAuthorizer = useMemo(
    () => authorizers.find((a) => a.type === "COGNITO_USER_POOLS"),
    [authorizers],
  )

  /** Fallback: detect Cognito from the method's authorizationType (works even
   *  when the authorizer object is missing or has no providerARNs). */
  const testMethodUsesCognitoAuth = useMemo(() => {
    if (!testTarget) return false
    const resource = resources.find((r) => r.id === testTarget.resourceId)
    const method = resource?.resourceMethods?.[testTarget.method]
    return method?.authorizationType === "COGNITO_USER_POOLS"
  }, [testTarget, resources])

  /** Extract pool ID from a Cognito provider ARN. */
  function poolIdFromArn(arn: string): string {
    // arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_ABC123
    const parts = arn.split("/")
    return parts[parts.length - 1] ?? ""
  }

  /** Load users and clients for a pool (for the auth picker). */
  async function loadCognitoPoolData(poolId: string) {
    setCognitoPoolId(poolId)
    setCognitoPoolsLoaded(false)
    try {
      const [users, clients] = await Promise.all([
        cognito.listUsers(poolId),
        cognito.listClients(poolId),
      ])
      setCognitoUsers(users.map((u) => ({ username: u.username, status: u.userStatus })))
      setCognitoClients(clients.map((c) => ({ id: c.clientId, name: c.clientName })))
      if (clients.length > 0 && !cognitoClientId) {
        setCognitoClientId(clients[0].clientId)
      }
      if (users.length > 0 && !cognitoUsername) {
        setCognitoUsername(users[0].username)
      }
    } catch {
      setCognitoUsers([])
      setCognitoClients([])
    } finally {
      setCognitoPoolsLoaded(true)
    }
  }

  /** Auto-fill password from emulator (dev convenience). */
  async function autoFillPassword(username: string) {
    if (!cognitoPoolId) return
    const pw = await cognito.getPlaintextPassword(cognitoPoolId, username)
    if (pw) setCognitoPassword(pw)
  }

  /** Browse all local Cognito pools (fallback when providerARNs point to missing pools). */
  async function browseLocalPools() {
    setLocalPoolsLoading(true)
    try {
      const pools = await cognito.listPools()
      setLocalCognitoPools(pools.map((p) => ({ id: p.id, name: p.name })))
    } catch {
      setLocalCognitoPools([])
    } finally {
      setLocalPoolsLoading(false)
    }
  }

  /** Authenticate and inject the token into the test headers. */
  async function cognitoAuthenticate() {
    if (!cognitoClientId || !cognitoUsername || !cognitoPassword) return
    setCognitoAuthLoading(true)
    setCognitoAuthError("")
    try {
      const result = await cognito.initiateAuth({
        clientId: cognitoClientId,
        authFlow: "USER_PASSWORD_AUTH",
        username: cognitoUsername,
        password: cognitoPassword,
      })
      const token = result.idToken ?? result.accessToken ?? ""
      setCognitoToken(token)
      // Inject into test headers — replace or add Authorization header
      setTestHeaders((prev) => {
        const lines = prev.split("\n").filter((l) => !l.toLowerCase().startsWith("authorization:"))
        lines.push(`Authorization: Bearer ${token}`)
        return lines.filter(Boolean).join("\n")
      })
    } catch (err) {
      setCognitoAuthError(err instanceof Error ? err.message : String(err))
    } finally {
      setCognitoAuthLoading(false)
    }
  }

  if (apiLoading || resourcesLoading) {
    return (
      <div className="flex justify-center py-16">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!api) return null

  // Build the resource tree for display
  const rootResource = resources.find((r) => r.path === "/")

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={api.name}
        description={api.description || api.id}
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={refetchAll} disabled={apiFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", apiFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => deployMut.mutate({ apiId })}
              disabled={deployMut.isPending}
            >
              <Rocket className="mr-1.5 h-3.5 w-3.5" />
              Deploy
            </Button>
            <Button size="sm" variant="danger" onClick={() => setShowDelete(true)}>
              <Trash2 className="mr-1.5 h-3.5 w-3.5" />
              Delete
            </Button>
          </>
        }
      />

      <ApplicationOwnershipBanner candidates={[api.id, api.name]} />

      {/* Summary cards */}
      <div className="grid grid-cols-4 gap-3">
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="text-xs text-fg-muted">API ID</div>
          <div className="mt-1 font-mono text-sm">{api.id}</div>
        </div>
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="text-xs text-fg-muted">Resources</div>
          <div className="mt-1 text-2xl font-semibold">{resources.length}</div>
        </div>
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="text-xs text-fg-muted">Stages</div>
          <div className="mt-1 text-2xl font-semibold">{stages.length}</div>
        </div>
        <div className="rounded-lg border bg-bg-elevated p-4">
          <div className="text-xs text-fg-muted">Created</div>
          <div className="mt-1 text-sm">{formatDate(api.createdDate)}</div>
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 border-b">
        {(["resources", "stages", "deployments", "authorizers"] as Tab[]).map((t) => (
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

      {/* Resources tab */}
      {tab === "resources" && (
        <div className="flex flex-col gap-3">
          <div className="flex justify-end gap-2">
            <Button
              size="sm"
              onClick={() => {
                setParentResourceId(rootResource?.id ?? "")
                setShowCreateResource(true)
              }}
            >
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Resource
            </Button>
          </div>

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Path</TableHead>
                <TableHead>Resource ID</TableHead>
                <TableHead>Methods</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {resources.map((resource) => {
                const methods = resource.resourceMethods
                  ? Object.keys(resource.resourceMethods)
                  : []
                const isExpanded = expandedResources.has(resource.id)

                return (
                  <Fragment key={resource.id}>
                    <TableRow
                      className="hover:bg-muted/50 cursor-pointer"
                      onClick={() => toggleResource(resource.id)}
                    >
                      <TableCell className="font-mono text-sm">
                        <span className="inline-flex items-center gap-1">
                          <ChevronRight
                            className={cn(
                              "h-3.5 w-3.5 shrink-0 transition-transform",
                              isExpanded && "rotate-90",
                            )}
                          />
                          {resource.path}
                        </span>
                      </TableCell>
                      <TableCell className="font-mono text-xs text-fg-muted">
                        {resource.id}
                      </TableCell>
                      <TableCell>
                        <div className="flex gap-1">
                          {methods.map((m) => {
                            const requiresKey = Boolean(
                              resource.resourceMethods?.[m]?.apiKeyRequired,
                            )
                            return (
                              <Badge
                                key={m}
                                variant={methodVariant(m)}
                                className={cn(
                                  "inline-flex items-center gap-1",
                                  requiresKey && "ring-1 ring-warning/60",
                                )}
                                title={requiresKey ? "Requires API key" : undefined}
                              >
                                {requiresKey ? <KeyRound className="h-3 w-3" /> : null}
                                {m}
                              </Badge>
                            )
                          })}
                          {methods.length === 0 && <span className="text-xs text-fg-muted">—</span>}
                        </div>
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex items-center justify-end gap-1">
                          <Button
                            size="sm"
                            variant="ghost"
                            title="Copy invoke URL"
                            onClick={(e) => {
                              e.stopPropagation()
                              copyUrl(resource.path, resource.id)
                            }}
                          >
                            {copiedId === resource.id ? (
                              <Check className="h-3.5 w-3.5 text-success" />
                            ) : (
                              <Copy className="h-3.5 w-3.5" />
                            )}
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            title="Add method"
                            onClick={(e) => {
                              e.stopPropagation()
                              setSelectedResource(resource)
                              setShowAddMethod(true)
                            }}
                          >
                            <Plus className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                    {isExpanded && (
                      <MethodDetails
                        key={`${resource.id}-details`}
                        resource={resource}
                        methods={methods}
                        onTest={(method) => {
                          setTestTarget({
                            resourceId: resource.id,
                            method,
                            path: resource.path,
                          })
                          setTestResponse(undefined)
                          setTestBody("")
                          // Auto-load Cognito pool data if this method uses a Cognito authorizer
                          const methodObj = resource.resourceMethods?.[method]
                          const auth = methodObj?.authorizerId
                            ? authorizers.find(
                                (a) =>
                                  a.id === methodObj.authorizerId &&
                                  a.type === "COGNITO_USER_POOLS",
                              )
                            : anyCognitoAuthorizer
                          if (auth?.providerARNs?.length) {
                            const pid = poolIdFromArn(auth.providerARNs[0])
                            if (pid && pid !== cognitoPoolId) {
                              void loadCognitoPoolData(pid)
                            }
                          }
                        }}
                        onCopyUrl={(method) => copyUrl(resource.path, `${resource.id}-${method}`)}
                        copiedId={copiedId}
                        buildInvokeUrl={buildInvokeUrl}
                        resourcePath={resource.path}
                        apiId={apiId}
                      />
                    )}
                  </Fragment>
                )
              })}
            </TableBody>
          </Table>

          {/* Test harness panel */}
          {testTarget && (
            <div className="rounded-lg border border-border bg-bg-elevated">
              <div className="flex items-center justify-between border-b border-border px-4 py-2">
                <div className="flex items-center gap-2">
                  <Badge variant={methodVariant(testTarget.method)}>{testTarget.method}</Badge>
                  <span className="font-mono text-sm text-fg">{testTarget.path}</span>
                  {(cognitoAuthorizer ?? anyCognitoAuthorizer ?? testMethodUsesCognitoAuth) && (
                    <Badge variant="default">
                      <Shield className="mr-1 h-3 w-3" />
                      Cognito
                    </Badge>
                  )}
                </div>
                <button
                  className="text-xs text-fg-muted hover:text-fg"
                  onClick={() => setTestTarget(undefined)}
                >
                  ✕
                </button>
              </div>

              <div className="flex flex-col gap-3 p-4">
                {/* URL bar */}
                <div className="flex items-center gap-2">
                  <div className="flex-1 rounded-md border bg-bg px-3 py-1.5 font-mono text-xs text-fg-muted">
                    {buildInvokeUrl(testTarget.path)}
                  </div>
                  <Button size="sm" onClick={() => void sendTestRequest()} disabled={testLoading}>
                    {testLoading ? (
                      <Spinner className="mr-1.5 h-3.5 w-3.5" />
                    ) : (
                      <Send className="mr-1.5 h-3.5 w-3.5" />
                    )}
                    Send
                  </Button>
                </div>

                {/* Cognito auth panel */}
                <CognitoAuthPanel
                  cognitoAuthorizer={cognitoAuthorizer}
                  anyCognitoAuthorizer={anyCognitoAuthorizer}
                  testMethodUsesCognitoAuth={testMethodUsesCognitoAuth}
                  cognitoPoolId={cognitoPoolId}
                  cognitoClientId={cognitoClientId}
                  cognitoUsername={cognitoUsername}
                  cognitoPassword={cognitoPassword}
                  cognitoToken={cognitoToken}
                  cognitoAuthLoading={cognitoAuthLoading}
                  cognitoAuthError={cognitoAuthError}
                  cognitoUsers={cognitoUsers}
                  cognitoClients={cognitoClients}
                  cognitoPoolsLoaded={cognitoPoolsLoaded}
                  localCognitoPools={localCognitoPools}
                  localPoolsLoading={localPoolsLoading}
                  poolIdFromArn={poolIdFromArn}
                  onLoadPool={loadCognitoPoolData}
                  onBrowseLocalPools={browseLocalPools}
                  onSetClientId={setCognitoClientId}
                  onSetUsername={(u) => {
                    setCognitoUsername(u)
                    void autoFillPassword(u)
                  }}
                  onSetPassword={setCognitoPassword}
                  onAuthenticate={() => void cognitoAuthenticate()}
                />

                {/* Request headers & body */}
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="mb-1 block text-xs font-medium text-fg-muted">Headers</label>
                    <textarea
                      className="w-full rounded-md border bg-bg px-3 py-2 font-mono text-xs text-fg"
                      rows={3}
                      value={testHeaders}
                      onChange={(e) => setTestHeaders(e.target.value)}
                      placeholder={"Content-Type: application/json\nAuthorization: Bearer ..."}
                    />
                  </div>
                  {!["GET", "HEAD", "OPTIONS"].includes(testTarget.method) && (
                    <div>
                      <label className="mb-1 block text-xs font-medium text-fg-muted">Body</label>
                      <textarea
                        className="w-full rounded-md border bg-bg px-3 py-2 font-mono text-xs text-fg"
                        rows={3}
                        value={testBody}
                        onChange={(e) => setTestBody(e.target.value)}
                        placeholder={'{"key": "value"}'}
                      />
                    </div>
                  )}
                </div>

                {/* Response */}
                {testResponse && (
                  <div className="flex flex-col gap-2 border-t border-border pt-3">
                    <div className="flex items-center gap-3">
                      <Badge
                        variant={
                          testResponse.status >= 200 && testResponse.status < 300
                            ? "success"
                            : testResponse.status >= 400
                              ? "danger"
                              : "default"
                        }
                      >
                        {testResponse.status} {testResponse.statusText}
                      </Badge>
                      <span className="text-xs text-fg-muted">{testResponse.elapsed}ms</span>
                    </div>

                    {/* Response headers */}
                    {Object.keys(testResponse.headers).length > 0 && (
                      <details className="text-xs">
                        <summary className="cursor-pointer font-medium text-fg-muted">
                          Response Headers
                        </summary>
                        <pre className="mt-1 max-h-32 overflow-auto rounded-md bg-bg p-2 font-mono text-fg-muted">
                          {Object.entries(testResponse.headers)
                            .map(([k, v]) => `${k}: ${v}`)
                            .join("\n")}
                        </pre>
                      </details>
                    )}

                    {/* Response body */}
                    <div>
                      <div className="mb-1 text-xs font-medium text-fg-muted">Response Body</div>
                      <pre className="max-h-64 overflow-auto rounded-md bg-bg p-3 font-mono text-xs text-fg">
                        {tryFormatJson(testResponse.body)}
                      </pre>
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      )}

      {/* Stages tab */}
      {tab === "stages" && (
        <div className="flex flex-col gap-3">
          <div className="flex justify-end">
            <Button
              size="sm"
              onClick={() => setShowCreateStage(true)}
              disabled={deployments.length === 0}
            >
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Stage
            </Button>
          </div>
          {stages.length === 0 ? (
            <p className="py-8 text-center text-sm text-fg-muted">
              No stages. Create a deployment first, then add a stage.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Stage Name</TableHead>
                  <TableHead>Deployment ID</TableHead>
                  <TableHead>Description</TableHead>
                  <TableHead>Created</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {stages.map((stage) => (
                  <TableRow key={stage.stageName}>
                    <TableCell className="font-medium">{stage.stageName}</TableCell>
                    <TableCell className="font-mono text-xs text-fg-muted">
                      {stage.deploymentId}
                    </TableCell>
                    <TableCell className="text-sm text-fg-muted">
                      {stage.description || "—"}
                    </TableCell>
                    <TableCell className="text-sm text-fg-muted">
                      {formatDate(stage.createdDate)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      )}

      {/* Deployments tab */}
      {tab === "deployments" && (
        <div className="flex flex-col gap-3">
          <div className="flex justify-end">
            <Button
              size="sm"
              onClick={() => deployMut.mutate({ apiId })}
              disabled={deployMut.isPending}
            >
              <Rocket className="mr-1.5 h-3.5 w-3.5" />
              Create Deployment
            </Button>
          </div>
          {deployments.length === 0 ? (
            <p className="py-8 text-center text-sm text-fg-muted">No deployments yet.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Deployment ID</TableHead>
                  <TableHead>Description</TableHead>
                  <TableHead>Created</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {deployments.map((dep) => (
                  <TableRow key={dep.id}>
                    <TableCell className="font-mono text-sm">{dep.id}</TableCell>
                    <TableCell className="text-sm text-fg-muted">
                      {dep.description || "—"}
                    </TableCell>
                    <TableCell className="text-sm text-fg-muted">
                      {formatDate(dep.createdDate)}
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
            <Button size="sm" onClick={() => setShowCreateAuthorizer(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Add Authorizer
            </Button>
          </div>
          {authorizers.length === 0 ? (
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
                {authorizers.map((auth) => (
                  <TableRow key={auth.id}>
                    <TableCell className="font-medium">{auth.name}</TableCell>
                    <TableCell>
                      <Badge variant="default">{auth.type}</Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs text-fg-muted">
                      {auth.identitySource || "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-danger hover:text-danger"
                        onClick={() => setDeleteAuthorizerTarget({ id: auth.id, name: auth.name })}
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
        title="Delete REST API"
        description={
          <>
            Delete <span className="font-mono font-semibold">{api.name}</span>? All resources,
            stages, and deployments will be removed.
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteMut.mutate(apiId)}
      />

      {/* Delete authorizer confirmation */}
      <ConfirmDialog
        open={!!deleteAuthorizerTarget}
        onOpenChange={(open) => !open && setDeleteAuthorizerTarget(undefined)}
        title="Delete Authorizer"
        description={
          <>
            Delete authorizer{" "}
            <span className="font-mono font-semibold">{deleteAuthorizerTarget?.name}</span>?
          </>
        }
        isPending={deleteAuthorizerMut.isPending}
        onConfirm={() =>
          deleteAuthorizerTarget &&
          deleteAuthorizerMut.mutate({ apiId, authorizerId: deleteAuthorizerTarget.id })
        }
      />

      {/* Create authorizer dialog */}
      <Dialog
        open={showCreateAuthorizer}
        onOpenChange={(v) => {
          if (!v) {
            setShowCreateAuthorizer(false)
            setNewAuthName("")
            setNewAuthType("TOKEN")
            setNewAuthUri("")
            setNewAuthIdentitySource("")
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
              createAuthorizerMut.mutate({
                apiId,
                name: newAuthName,
                type: newAuthType,
                authorizerUri: newAuthUri || undefined,
                identitySource: newAuthIdentitySource || undefined,
              })
            }}
            className="flex flex-col gap-4"
          >
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="auth-name">
                Name <span className="text-danger">*</span>
              </label>
              <input
                id="auth-name"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newAuthName}
                onChange={(e) => setNewAuthName(e.target.value)}
                placeholder="MyAuthorizer"
                autoFocus
                required
              />
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium">Type</label>
              <select
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newAuthType}
                onChange={(e) => setNewAuthType(e.target.value)}
              >
                {["TOKEN", "REQUEST", "COGNITO_USER_POOLS"].map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="auth-identity-source">
                Identity Source
              </label>
              <input
                id="auth-identity-source"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newAuthIdentitySource}
                onChange={(e) => setNewAuthIdentitySource(e.target.value)}
                placeholder="method.request.header.Authorization"
              />
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="auth-uri">
                Authorizer URI
              </label>
              <input
                id="auth-uri"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newAuthUri}
                onChange={(e) => setNewAuthUri(e.target.value)}
                placeholder="arn:aws:apigateway:us-east-1:lambda:path/..."
              />
            </div>
            <DialogFooter>
              <Button variant="ghost" type="button" onClick={() => setShowCreateAuthorizer(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={!newAuthName || createAuthorizerMut.isPending}>
                {createAuthorizerMut.isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Create resource dialog */}
      <Dialog
        open={showCreateResource}
        onOpenChange={(v) => {
          if (!v) {
            setShowCreateResource(false)
            setNewPathPart("")
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create Resource</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              createResourceMut.mutate({
                apiId,
                parentId: parentResourceId || rootResource?.id || "",
                pathPart: newPathPart,
              })
            }}
            className="flex flex-col gap-4"
          >
            <div>
              <label className="mb-1 block text-sm font-medium">Parent Resource</label>
              <select
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={parentResourceId}
                onChange={(e) => setParentResourceId(e.target.value)}
              >
                {resources.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.path} ({r.id})
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="path-part">
                Path Part
              </label>
              <input
                id="path-part"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newPathPart}
                onChange={(e) => setNewPathPart(e.target.value)}
                placeholder="users"
                autoFocus
              />
            </div>
            <DialogFooter>
              <Button variant="ghost" type="button" onClick={() => setShowCreateResource(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={!newPathPart || createResourceMut.isPending}>
                {createResourceMut.isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}
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
              createStageMut.mutate({
                apiId,
                stageName: newStageName,
                deploymentId: selectedDeploymentId || deployments[0]?.id || "",
              })
            }}
            className="flex flex-col gap-4"
          >
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="stage-name">
                Stage Name
              </label>
              <input
                id="stage-name"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newStageName}
                onChange={(e) => setNewStageName(e.target.value)}
                placeholder="prod"
                autoFocus
              />
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium">Deployment</label>
              <select
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={selectedDeploymentId}
                onChange={(e) => setSelectedDeploymentId(e.target.value)}
              >
                {deployments.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.id} {d.description && `— ${d.description}`}
                  </option>
                ))}
              </select>
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

      {/* Add method dialog */}
      <Dialog
        open={showAddMethod}
        onOpenChange={(v) => {
          if (!v) setShowAddMethod(false)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add Method to {selectedResource?.path}</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              if (!selectedResource) return
              putMethodMut.mutate(
                {
                  apiId,
                  resourceId: selectedResource.id,
                  httpMethod: newMethodVerb,
                },
                {
                  onSuccess: () => {
                    // Also add integration
                    putIntegrationMut.mutate({
                      apiId,
                      resourceId: selectedResource.id,
                      httpMethod: newMethodVerb,
                      type: newIntegrationType,
                    })
                  },
                },
              )
            }}
            className="flex flex-col gap-4"
          >
            <div>
              <label className="mb-1 block text-sm font-medium">HTTP Method</label>
              <select
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newMethodVerb}
                onChange={(e) => setNewMethodVerb(e.target.value)}
              >
                {["GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD", "ANY"].map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium">Integration Type</label>
              <select
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newIntegrationType}
                onChange={(e) => setNewIntegrationType(e.target.value)}
              >
                {["MOCK", "HTTP", "HTTP_PROXY", "AWS_PROXY", "AWS"].map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
            </div>
            <DialogFooter>
              <Button variant="ghost" type="button" onClick={() => setShowAddMethod(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={putMethodMut.isPending}>
                {putMethodMut.isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}
                Add Method
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ─── Helper components ──────────────────────────────────────────────────────

function methodVariant(method: string): "default" | "success" | "danger" | "warning" {
  switch (method) {
    case "GET":
      return "success"
    case "POST":
      return "warning"
    case "DELETE":
      return "danger"
    default:
      return "default"
  }
}

function MethodDetails({
  resource,
  methods,
  onTest,
  onCopyUrl,
  copiedId,
  buildInvokeUrl: _buildInvokeUrl,
  resourcePath,
  apiId,
}: {
  resource: ApiResource
  methods: string[]
  onTest: (method: string) => void
  onCopyUrl: (method: string) => void
  copiedId?: string
  buildInvokeUrl: (path: string) => string
  resourcePath: string
  apiId: string
}) {
  const navigate = useNavigate()
  if (methods.length === 0) {
    return (
      <TableRow className="bg-bg-elevated/50">
        <TableCell colSpan={4} className="pl-10 text-xs text-fg-muted">
          No methods defined. Click + to add one.
        </TableCell>
      </TableRow>
    )
  }

  return (
    <>
      {methods.map((method) => {
        const methodObj: ApiMethod | undefined = resource.resourceMethods?.[method]
        const integration: ApiIntegration | undefined = methodObj?.methodIntegration

        return (
          <TableRow key={`${resource.id}-${method}`} className="bg-bg-elevated/50">
            <TableCell colSpan={4} className="pl-10">
              <div className="flex items-start justify-between gap-4 py-1">
                <div className="flex flex-col gap-1">
                  <div className="flex items-center gap-2">
                    <Badge variant={methodVariant(method)}>{method}</Badge>
                    <span className="font-mono text-xs text-fg-muted">{resourcePath}</span>
                    <span className="text-xs text-fg-muted">
                      Auth: {methodObj?.authorizationType || "NONE"}
                    </span>
                    {methodObj?.apiKeyRequired ? (
                      <button
                        type="button"
                        title="API key required — click to view the usage plans for this API"
                        onClick={(e) => {
                          e.stopPropagation()
                          void navigate({
                            to: "/apigateway/usage-plans",
                            search: { apiId },
                          })
                        }}
                        className="inline-flex items-center gap-1 rounded border border-warning/40 bg-warning/10 px-1.5 py-0.5 text-xs text-warning hover:bg-warning/20"
                      >
                        <KeyRound className="h-3 w-3" />
                        API key required
                      </button>
                    ) : null}
                  </div>
                  {integration && (
                    <div className="text-xs text-fg-muted">
                      Integration: <Badge variant="default">{integration.type}</Badge>
                      {integration.uri && <span className="ml-2 font-mono">{integration.uri}</span>}
                    </div>
                  )}
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    size="sm"
                    variant="ghost"
                    title={`Copy ${method} ${resourcePath} invoke URL`}
                    onClick={(e) => {
                      e.stopPropagation()
                      onCopyUrl(method)
                    }}
                  >
                    {copiedId === `${resource.id}-${method}` ? (
                      <Check className="h-3 w-3 text-success" />
                    ) : (
                      <Copy className="h-3 w-3" />
                    )}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    title={`Test ${method} ${resourcePath}`}
                    onClick={(e) => {
                      e.stopPropagation()
                      onTest(method)
                    }}
                  >
                    <Send className="h-3 w-3" />
                  </Button>
                </div>
              </div>
            </TableCell>
          </TableRow>
        )
      })}
    </>
  )
}

/** Try to pretty-print JSON; return as-is if not valid JSON. */
function tryFormatJson(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

// ─── Cognito Auth Panel ────────────────────────────────────────────────────

function CognitoAuthPanel({
  cognitoAuthorizer,
  anyCognitoAuthorizer,
  testMethodUsesCognitoAuth,
  cognitoPoolId,
  cognitoClientId,
  cognitoUsername,
  cognitoPassword,
  cognitoToken,
  cognitoAuthLoading,
  cognitoAuthError,
  cognitoUsers,
  cognitoClients,
  cognitoPoolsLoaded,
  localCognitoPools,
  localPoolsLoading,
  poolIdFromArn,
  onLoadPool,
  onBrowseLocalPools,
  onSetClientId,
  onSetUsername,
  onSetPassword,
  onAuthenticate,
}: {
  cognitoAuthorizer?: Authorizer
  anyCognitoAuthorizer?: Authorizer
  testMethodUsesCognitoAuth: boolean
  cognitoPoolId: string
  cognitoClientId: string
  cognitoUsername: string
  cognitoPassword: string
  cognitoToken: string
  cognitoAuthLoading: boolean
  cognitoAuthError: string
  cognitoUsers: { username: string; status: string }[]
  cognitoClients: { id: string; name: string }[]
  cognitoPoolsLoaded: boolean
  localCognitoPools: { id: string; name: string }[]
  localPoolsLoading: boolean
  poolIdFromArn: (arn: string) => string
  onLoadPool: (poolId: string) => void
  onBrowseLocalPools: () => void
  onSetClientId: (id: string) => void
  onSetUsername: (username: string) => void
  onSetPassword: (password: string) => void
  onAuthenticate: () => void
}) {
  const activeAuth = cognitoAuthorizer ?? anyCognitoAuthorizer

  // Show the panel if: an authorizer object exists, OR the method's authorizationType is COGNITO_USER_POOLS
  if (!activeAuth && !testMethodUsesCognitoAuth) return null

  const providerArns = activeAuth?.providerARNs ?? []
  const poolIds = providerArns.map(poolIdFromArn).filter(Boolean)
  const isDirectMatch = !!cognitoAuthorizer || testMethodUsesCognitoAuth

  return (
    <div className="rounded-md border border-border bg-bg p-3">
      <div className="mb-2 flex items-center gap-2">
        <Shield className="h-3.5 w-3.5 text-accent" />
        <span className="text-xs font-medium text-fg">
          Cognito Authentication
          {!isDirectMatch && (
            <span className="ml-1 font-normal text-fg-muted">(available on this API)</span>
          )}
        </span>
        {cognitoToken && (
          <Badge variant="success" className="ml-auto">
            <Check className="mr-1 h-3 w-3" /> Authenticated
          </Badge>
        )}
      </div>

      {/* Pool selection — auto-connect from providerARNs when available */}
      {!cognitoPoolId && poolIds.length > 0 && (
        <div className="flex flex-col gap-2">
          {poolIds.length === 1 ? (
            <Button
              size="sm"
              variant="outline"
              onClick={() => onLoadPool(poolIds[0])}
              className="w-fit"
            >
              <LogIn className="mr-1.5 h-3.5 w-3.5" />
              Connect to pool {poolIds[0]}
            </Button>
          ) : (
            <div className="flex items-center gap-2">
              <span className="text-xs text-fg-muted">User Pool:</span>
              {poolIds.map((pid) => (
                <Button
                  key={pid}
                  size="sm"
                  variant="outline"
                  onClick={() => onLoadPool(pid)}
                  className="font-mono text-xs"
                >
                  {pid}
                </Button>
              ))}
            </div>
          )}
        </div>
      )}

      {/* No providerARNs — browse local pools or enter manually */}
      {!cognitoPoolId && poolIds.length === 0 && (
        <div className="flex flex-col gap-2">
          <p className="text-xs text-fg-muted">
            {activeAuth ? (
              <>
                Authorizer <span className="font-medium">{activeAuth.name}</span> has no provider
                ARNs — the referenced pool may not exist locally.
              </>
            ) : (
              <>This route requires Cognito authentication.</>
            )}{" "}
            Select a local pool or enter a pool ID manually.
          </p>

          {/* Browse local pools */}
          {localCognitoPools.length === 0 && !localPoolsLoading && (
            <Button size="sm" variant="outline" onClick={onBrowseLocalPools} className="w-fit">
              <Shield className="mr-1.5 h-3.5 w-3.5" />
              Browse local pools
            </Button>
          )}

          {localPoolsLoading && (
            <div className="flex items-center gap-2 py-1">
              <Spinner className="h-3.5 w-3.5" />
              <span className="text-xs text-fg-muted">Loading pools…</span>
            </div>
          )}

          {localCognitoPools.length > 0 && (
            <div className="flex flex-wrap items-center gap-2">
              {localCognitoPools.map((pool) => (
                <Button
                  key={pool.id}
                  size="sm"
                  variant="outline"
                  onClick={() => onLoadPool(pool.id)}
                  className="text-xs"
                >
                  <LogIn className="mr-1.5 h-3.5 w-3.5" />
                  {pool.name} <span className="ml-1 font-mono text-fg-muted">({pool.id})</span>
                </Button>
              ))}
            </div>
          )}

          {/* Manual pool ID entry */}
          {localCognitoPools.length === 0 && !localPoolsLoading && (
            <ManualPoolIdInput onLoadPool={onLoadPool} />
          )}
        </div>
      )}

      {/* Auth form — shown after pool is loaded */}
      {cognitoPoolId && cognitoPoolsLoaded && (
        <div className="flex flex-col gap-2">
          <div className="mb-1 flex items-center gap-1 text-xs text-fg-muted">
            Pool: <span className="font-mono">{cognitoPoolId}</span>
          </div>
          <div className="grid grid-cols-2 gap-2">
            {/* Client picker */}
            <div>
              <label className="mb-1 block text-xs text-fg-muted">App Client</label>
              {cognitoClients.length > 0 ? (
                <select
                  className="w-full rounded-md border bg-bg-elevated px-2 py-1.5 text-xs"
                  value={cognitoClientId}
                  onChange={(e) => onSetClientId(e.target.value)}
                >
                  {cognitoClients.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name} ({c.id.slice(0, 8)}…)
                    </option>
                  ))}
                </select>
              ) : (
                <p className="py-1 text-xs text-fg-muted">No app clients found in this pool</p>
              )}
            </div>

            {/* User picker */}
            <div>
              <label className="mb-1 block text-xs text-fg-muted">User</label>
              {cognitoUsers.length > 0 ? (
                <select
                  className="w-full rounded-md border bg-bg-elevated px-2 py-1.5 text-xs"
                  value={cognitoUsername}
                  onChange={(e) => onSetUsername(e.target.value)}
                >
                  {cognitoUsers.map((u) => (
                    <option key={u.username} value={u.username}>
                      {u.username} ({u.status})
                    </option>
                  ))}
                </select>
              ) : (
                <p className="py-1 text-xs text-fg-muted">No users found in this pool</p>
              )}
            </div>
          </div>

          {/* Password + authenticate */}
          <div className="flex items-end gap-2">
            <div className="flex-1">
              <label className="mb-1 block text-xs text-fg-muted">Password</label>
              <input
                type="password"
                className="w-full rounded-md border bg-bg-elevated px-2 py-1.5 text-xs"
                value={cognitoPassword}
                onChange={(e) => onSetPassword(e.target.value)}
                placeholder="Password (auto-filled from emulator)"
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault()
                    onAuthenticate()
                  }
                }}
              />
            </div>
            <Button
              size="sm"
              onClick={onAuthenticate}
              disabled={
                cognitoAuthLoading || !cognitoClientId || !cognitoUsername || !cognitoPassword
              }
            >
              {cognitoAuthLoading ? (
                <Spinner className="mr-1.5 h-3.5 w-3.5" />
              ) : (
                <LogIn className="mr-1.5 h-3.5 w-3.5" />
              )}
              Authenticate
            </Button>
          </div>

          {cognitoAuthError && <p className="text-xs text-danger">{cognitoAuthError}</p>}
        </div>
      )}

      {/* Loading state */}
      {cognitoPoolId && !cognitoPoolsLoaded && (
        <div className="flex items-center gap-2 py-1">
          <Spinner className="h-3.5 w-3.5" />
          <span className="text-xs text-fg-muted">Loading pool data…</span>
        </div>
      )}
    </div>
  )
}

/** Small inline form for entering a pool ID manually. */
function ManualPoolIdInput({ onLoadPool }: { onLoadPool: (poolId: string) => void }) {
  const [manualPoolId, setManualPoolId] = useState("")
  return (
    <div className="flex items-end gap-2">
      <div className="flex-1">
        <label className="mb-1 block text-xs text-fg-muted">Pool ID</label>
        <input
          className="w-full rounded-md border bg-bg-elevated px-2 py-1.5 font-mono text-xs"
          value={manualPoolId}
          onChange={(e) => setManualPoolId(e.target.value)}
          placeholder="us-east-1_ABC123"
          onKeyDown={(e) => {
            if (e.key === "Enter" && manualPoolId) {
              e.preventDefault()
              onLoadPool(manualPoolId)
            }
          }}
        />
      </div>
      <Button
        size="sm"
        variant="outline"
        onClick={() => onLoadPool(manualPoolId)}
        disabled={!manualPoolId}
      >
        Connect
      </Button>
    </div>
  )
}
