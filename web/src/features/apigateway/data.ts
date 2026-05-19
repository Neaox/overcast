/**
 * API Gateway query/mutation definitions.
 *
 * Key factory:
 *   apigwKeys.all()                          -> [...ep, "apigateway"]
 *   apigwKeys.restApis()                     -> [..., "rest-apis"]
 *   apigwKeys.restApi(id)                    -> [..., "rest-api", id]
 *   apigwKeys.resources(apiId)               -> [..., "rest-api", apiId, "resources"]
 *   apigwKeys.stages(apiId)                  -> [..., "rest-api", apiId, "stages"]
 *   apigwKeys.deployments(apiId)             -> [..., "rest-api", apiId, "deployments"]
 *   apigwKeys.authorizers(apiId)             -> [..., "rest-api", apiId, "authorizers"]
 *   apigwKeys.httpApis()                     -> [..., "http-apis"]
 *   apigwKeys.httpApi(id)                    -> [..., "http-api", id]
 *   apigwKeys.routes(apiId)                  -> [..., "http-api", apiId, "routes"]
 *   apigwKeys.integrations(apiId)            -> [..., "http-api", apiId, "integrations"]
 *   apigwKeys.httpStages(apiId)              -> [..., "http-api", apiId, "stages"]
 *   apigwKeys.v2Authorizers(apiId)           -> [..., "http-api", apiId, "authorizers"]
 *   apigwKeys.apiKeys()                      -> [..., "api-keys"]
 *   apigwKeys.usagePlans()                   -> [..., "usage-plans"]
 *   apigwKeys.usagePlanKeys(planId)          -> [..., "usage-plan", planId, "keys"]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { apigateway } from "@/services/api"
import type { Authorizer, ApiKey, UsagePlan, UsagePlanKey, AuthorizerV2 } from "@/services/api/apigateway"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const apigwKeys = {
  all: () => [...endpointStore.getKeys(), "apigateway"] as const,

  // REST API v1
  restApis: () => [...apigwKeys.all(), "rest-apis"] as const,
  restApi: (id: string) => [...apigwKeys.all(), "rest-api", id] as const,
  resources: (apiId: string) => [...apigwKeys.restApi(apiId), "resources"] as const,
  stages: (apiId: string) => [...apigwKeys.restApi(apiId), "stages"] as const,
  deployments: (apiId: string) => [...apigwKeys.restApi(apiId), "deployments"] as const,
  authorizers: (apiId: string) => [...apigwKeys.restApi(apiId), "authorizers"] as const,

  // HTTP API v2
  httpApis: () => [...apigwKeys.all(), "http-apis"] as const,
  httpApi: (id: string) => [...apigwKeys.all(), "http-api", id] as const,
  routes: (apiId: string) => [...apigwKeys.httpApi(apiId), "routes"] as const,
  integrations: (apiId: string) => [...apigwKeys.httpApi(apiId), "integrations"] as const,
  httpStages: (apiId: string) => [...apigwKeys.httpApi(apiId), "stages"] as const,
  v2Authorizers: (apiId: string) => [...apigwKeys.httpApi(apiId), "authorizers"] as const,

  // Global resources
  apiKeys: () => [...apigwKeys.all(), "api-keys"] as const,
  usagePlans: () => [...apigwKeys.all(), "usage-plans"] as const,
  usagePlanKeys: (planId: string) =>
    [...apigwKeys.all(), "usage-plan", planId, "keys"] as const,
}

// ─── REST API queries ──────────────────────────────────────────────────────

export function restApisQueryOptions() {
  return queryOptions({
    queryKey: apigwKeys.restApis(),
    queryFn: () => apigateway.listRestApis(),
  })
}

export function restApiQueryOptions(id: string) {
  return queryOptions({
    queryKey: apigwKeys.restApi(id),
    queryFn: () => apigateway.getRestApi(id),
  })
}

export function resourcesQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: apigwKeys.resources(apiId),
    queryFn: () => apigateway.getResources(apiId),
  })
}

export function stagesQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: apigwKeys.stages(apiId),
    queryFn: () => apigateway.getStages(apiId),
  })
}

export function deploymentsQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: apigwKeys.deployments(apiId),
    queryFn: () => apigateway.getDeployments(apiId),
  })
}

// ─── REST API mutations ────────────────────────────────────────────────────

export function createRestApiMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.restApis(), "create"] as const,
    mutationFn: (opts: { name: string; description?: string }) => apigateway.createRestApi(opts),
  })
}

export function deleteRestApiMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.restApis(), "delete"] as const,
    mutationFn: (id: string) => apigateway.deleteRestApi(id),
  })
}

export function createResourceMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "create-resource"] as const,
    mutationFn: (opts: { apiId: string; parentId: string; pathPart: string }) =>
      apigateway.createResource(opts.apiId, opts.parentId, opts.pathPart),
  })
}

export function deleteResourceMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "delete-resource"] as const,
    mutationFn: (opts: { apiId: string; resourceId: string }) =>
      apigateway.deleteResource(opts.apiId, opts.resourceId),
  })
}

export function putMethodMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "put-method"] as const,
    mutationFn: (opts: {
      apiId: string
      resourceId: string
      httpMethod: string
      authorizationType?: string
    }) =>
      apigateway.putMethod(opts.apiId, opts.resourceId, opts.httpMethod, {
        authorizationType: opts.authorizationType,
      }),
  })
}

export function putIntegrationMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "put-integration"] as const,
    mutationFn: (opts: {
      apiId: string
      resourceId: string
      httpMethod: string
      type: string
      uri?: string
      integrationHttpMethod?: string
    }) =>
      apigateway.putIntegration(opts.apiId, opts.resourceId, opts.httpMethod, {
        type: opts.type,
        uri: opts.uri,
        integrationHttpMethod: opts.integrationHttpMethod,
      }),
  })
}

export function createStageMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "create-stage"] as const,
    mutationFn: (opts: { apiId: string; stageName: string; deploymentId: string }) =>
      apigateway.createStage(opts.apiId, opts),
  })
}

export function createDeploymentMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "create-deployment"] as const,
    mutationFn: (opts: { apiId: string; description?: string }) =>
      apigateway.createDeployment(opts.apiId, opts.description),
  })
}

// ─── HTTP API queries ──────────────────────────────────────────────────────

export function httpApisQueryOptions() {
  return queryOptions({
    queryKey: apigwKeys.httpApis(),
    queryFn: () => apigateway.listHttpApis(),
  })
}

export function httpApiQueryOptions(id: string) {
  return queryOptions({
    queryKey: apigwKeys.httpApi(id),
    queryFn: () => apigateway.getHttpApi(id),
  })
}

export function routesQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: apigwKeys.routes(apiId),
    queryFn: () => apigateway.getRoutes(apiId),
  })
}

export function httpIntegrationsQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: apigwKeys.integrations(apiId),
    queryFn: () => apigateway.getIntegrations(apiId),
  })
}

export function httpStagesQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: apigwKeys.httpStages(apiId),
    queryFn: () => apigateway.getHttpStages(apiId),
  })
}

// ─── HTTP API mutations ────────────────────────────────────────────────────

export function createHttpApiMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.httpApis(), "create"] as const,
    mutationFn: (opts: { name: string; protocolType: string; description?: string }) =>
      apigateway.createHttpApi(opts),
  })
}

export function deleteHttpApiMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.httpApis(), "delete"] as const,
    mutationFn: (id: string) => apigateway.deleteHttpApi(id),
  })
}

export function createRouteMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "create-route"] as const,
    mutationFn: (opts: { apiId: string; routeKey: string; target?: string }) =>
      apigateway.createRoute(opts.apiId, opts.routeKey, opts.target),
  })
}

export function deleteRouteMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "delete-route"] as const,
    mutationFn: (opts: { apiId: string; routeId: string }) =>
      apigateway.deleteRoute(opts.apiId, opts.routeId),
  })
}

export function createHttpIntegrationMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "create-http-integration"] as const,
    mutationFn: (opts: {
      apiId: string
      integrationType: string
      integrationUri?: string
      payloadFormatVersion?: string
    }) => apigateway.createIntegration(opts.apiId, opts),
  })
}

export function createHttpStageMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "create-http-stage"] as const,
    mutationFn: (opts: { apiId: string; stageName: string }) =>
      apigateway.createHttpStage(opts.apiId, opts.stageName),
  })
}

export function deleteHttpStageMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "delete-http-stage"] as const,
    mutationFn: (opts: { apiId: string; stageName: string }) =>
      apigateway.deleteHttpStage(opts.apiId, opts.stageName),
  })
}

// ─── REST v1 Authorizer queries & mutations ────────────────────────────────

export function authorizersQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: apigwKeys.authorizers(apiId),
    queryFn: () => apigateway.listAuthorizers(apiId),
  })
}

export function createAuthorizerMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "create-authorizer"] as const,
    mutationFn: (opts: {
      apiId: string
      name: string
      type: string
      authorizerUri?: string
      identitySource?: string
    }) =>
      apigateway.createAuthorizer(opts.apiId, {
        name: opts.name,
        type: opts.type,
        authorizerUri: opts.authorizerUri,
        identitySource: opts.identitySource,
      }),
  })
}

export function deleteAuthorizerMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "delete-authorizer"] as const,
    mutationFn: (opts: { apiId: string; authorizerId: string }) =>
      apigateway.deleteAuthorizer(opts.apiId, opts.authorizerId),
  })
}

// ─── API Key queries & mutations ───────────────────────────────────────────

export function apiKeysQueryOptions() {
  return queryOptions({
    queryKey: apigwKeys.apiKeys(),
    queryFn: () => apigateway.listApiKeys(),
  })
}

export function createApiKeyMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.apiKeys(), "create"] as const,
    mutationFn: (opts: { name: string; enabled?: boolean }) => apigateway.createApiKey(opts),
  })
}

export function deleteApiKeyMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.apiKeys(), "delete"] as const,
    mutationFn: (id: string) => apigateway.deleteApiKey(id),
  })
}

// ─── Usage Plan queries & mutations ───────────────────────────────────────

export function usagePlansQueryOptions() {
  return queryOptions({
    queryKey: apigwKeys.usagePlans(),
    queryFn: () => apigateway.listUsagePlans(),
  })
}

export function createUsagePlanMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.usagePlans(), "create"] as const,
    mutationFn: (opts: { name: string; description?: string }) =>
      apigateway.createUsagePlan(opts),
  })
}

export function deleteUsagePlanMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.usagePlans(), "delete"] as const,
    mutationFn: (id: string) => apigateway.deleteUsagePlan(id),
  })
}

export function usagePlanKeysQueryOptions(planId: string) {
  return queryOptions({
    queryKey: apigwKeys.usagePlanKeys(planId),
    queryFn: () => apigateway.listUsagePlanKeys(planId),
    enabled: !!planId,
  })
}

export function addUsagePlanKeyMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "add-usage-plan-key"] as const,
    mutationFn: (opts: { planId: string; keyId: string }) =>
      apigateway.addUsagePlanKey(opts.planId, opts.keyId),
  })
}

export function removeUsagePlanKeyMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "remove-usage-plan-key"] as const,
    mutationFn: (opts: { planId: string; keyId: string }) =>
      apigateway.removeUsagePlanKey(opts.planId, opts.keyId),
  })
}

// ─── HTTP v2 Authorizer queries & mutations ────────────────────────────────

export function v2AuthorizersQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: apigwKeys.v2Authorizers(apiId),
    queryFn: () => apigateway.listV2Authorizers(apiId),
  })
}

export function createV2AuthorizerMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "create-v2-authorizer"] as const,
    mutationFn: (opts: {
      apiId: string
      name: string
      authorizerType: string
      identitySource?: string
      jwtConfiguration?: { audience: string[]; issuer: string }
    }) =>
      apigateway.createV2Authorizer(opts.apiId, {
        name: opts.name,
        authorizerType: opts.authorizerType,
        identitySource: opts.identitySource,
        jwtConfiguration: opts.jwtConfiguration,
      }),
  })
}

export function deleteV2AuthorizerMutationOptions() {
  return mutationOptions({
    mutationKey: [...apigwKeys.all(), "delete-v2-authorizer"] as const,
    mutationFn: (opts: { apiId: string; authorizerId: string }) =>
      apigateway.deleteV2Authorizer(opts.apiId, opts.authorizerId),
  })
}

// Re-export types for consumers
export type { Authorizer, ApiKey, UsagePlan, UsagePlanKey, AuthorizerV2 }
