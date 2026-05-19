/**
 * API Gateway API client.
 *
 * Uses the AWS SDK v3 clients (@aws-sdk/client-api-gateway for REST v1,
 * @aws-sdk/client-apigatewayv2 for HTTP v2) talking directly to the
 * Overcast emulator — same as every other service client in this directory.
 */

import {
  GetRestApisCommand,
  GetRestApiCommand,
  CreateRestApiCommand,
  DeleteRestApiCommand,
  GetResourcesCommand,
  CreateResourceCommand,
  DeleteResourceCommand,
  PutMethodCommand,
  PutIntegrationCommand,
  GetStagesCommand,
  CreateStageCommand,
  DeleteStageCommand,
  GetDeploymentsCommand,
  CreateDeploymentCommand,
  CreateAuthorizerCommand,
  GetAuthorizersCommand,
  DeleteAuthorizerCommand,
  CreateApiKeyCommand,
  GetApiKeysCommand,
  DeleteApiKeyCommand,
  CreateUsagePlanCommand,
  GetUsagePlansCommand,
  DeleteUsagePlanCommand,
  CreateUsagePlanKeyCommand,
  GetUsagePlanKeysCommand,
  DeleteUsagePlanKeyCommand,
  type EndpointType,
  type IntegrationType as RestIntegrationType,
  type AuthorizerType as RestAuthorizerType,
} from "@aws-sdk/client-api-gateway"
import {
  GetApisCommand,
  GetApiCommand,
  CreateApiCommand,
  DeleteApiCommand,
  GetRoutesCommand,
  CreateRouteCommand,
  DeleteRouteCommand,
  GetIntegrationsCommand,
  CreateIntegrationCommand,
  DeleteIntegrationCommand,
  GetStagesCommand as GetV2StagesCommand,
  CreateStageCommand as CreateV2StageCommand,
  DeleteStageCommand as DeleteV2StageCommand,
  GetDeploymentsCommand as GetV2DeploymentsCommand,
  CreateDeploymentCommand as CreateV2DeploymentCommand,
  CreateAuthorizerCommand as CreateV2AuthorizerCommand,
  GetAuthorizersCommand as GetV2AuthorizersCommand,
  DeleteAuthorizerCommand as DeleteV2AuthorizerCommand,
  type ProtocolType,
  type IntegrationType as V2IntegrationType,
  type AuthorizerType as V2AuthorizerType,
} from "@aws-sdk/client-apigatewayv2"
import { awsClients } from "../aws-clients"
import type {
  RestApi,
  ApiResource,
  ApiStage,
  ApiDeployment,
  HttpApi,
  HttpRoute,
  HttpIntegration,
  HttpStage,
} from "@/types"

// ─── Inline types for new resources ───────────────────────────────────────

export interface Authorizer {
  id: string
  name: string
  type: string // TOKEN, REQUEST, COGNITO_USER_POOLS
  authorizerUri?: string
  identitySource?: string
  authorizerResultTtlInSeconds?: number
  providerARNs?: string[]
}

export interface ApiKey {
  id: string
  name: string
  value?: string
  enabled: boolean
  createdDate: number
  description?: string
}

export interface UsagePlan {
  id: string
  name: string
  description?: string
  apiStages?: { apiId: string; stage: string }[]
}

export interface UsagePlanKey {
  id: string
  name: string
  type: string
  value?: string
}

export interface AuthorizerV2 {
  authorizerId: string
  name: string
  authorizerType: string // JWT, REQUEST
  identitySource?: string
  jwtConfiguration?: { audience: string[]; issuer: string }
}

// ─── REST API v1 ───────────────────────────────────────────────────────────

export const apigateway = {
  listRestApis: async (): Promise<RestApi[]> => {
    const res = await awsClients.apigateway().send(new GetRestApisCommand({}))
    return (res.items ?? []).map((a) => ({
      id: a.id ?? "",
      name: a.name ?? "",
      description: a.description,
      createdDate: a.createdDate?.getTime() ?? 0,
      version: a.version,
      endpointConfiguration: a.endpointConfiguration
        ? { types: a.endpointConfiguration.types ?? [] }
        : undefined,
      tags: a.tags,
      binaryMediaTypes: a.binaryMediaTypes,
      rootResourceId: a.rootResourceId ?? "",
    }))
  },

  getRestApi: async (id: string): Promise<RestApi> => {
    const a = await awsClients.apigateway().send(new GetRestApiCommand({ restApiId: id }))
    return {
      id: a.id ?? "",
      name: a.name ?? "",
      description: a.description,
      createdDate: a.createdDate?.getTime() ?? 0,
      version: a.version,
      endpointConfiguration: a.endpointConfiguration
        ? { types: a.endpointConfiguration.types ?? [] }
        : undefined,
      tags: a.tags,
      binaryMediaTypes: a.binaryMediaTypes,
      rootResourceId: a.rootResourceId ?? "",
    }
  },

  createRestApi: async (opts: {
    name: string
    description?: string
    endpointConfiguration?: { types: string[] }
  }): Promise<RestApi> => {
    const a = await awsClients.apigateway().send(
      new CreateRestApiCommand({
        name: opts.name,
        description: opts.description,
        endpointConfiguration: opts.endpointConfiguration
          ? { types: opts.endpointConfiguration.types as EndpointType[] }
          : undefined,
      }),
    )
    return {
      id: a.id ?? "",
      name: a.name ?? "",
      description: a.description,
      createdDate: a.createdDate?.getTime() ?? 0,
      rootResourceId: a.rootResourceId ?? "",
    }
  },

  deleteRestApi: async (id: string): Promise<void> => {
    await awsClients.apigateway().send(new DeleteRestApiCommand({ restApiId: id }))
  },

  getResources: async (apiId: string): Promise<ApiResource[]> => {
    const res = await awsClients
      .apigateway()
      .send(new GetResourcesCommand({ restApiId: apiId, embed: ["methods"] }))
    return (res.items ?? []).map((r) => ({
      id: r.id ?? "",
      parentId: r.parentId,
      pathPart: r.pathPart ?? "",
      path: r.path ?? "",
      resourceMethods: r.resourceMethods
        ? Object.fromEntries(
            Object.entries(r.resourceMethods).map(([verb, m]) => [
              verb,
              {
                httpMethod: m.httpMethod ?? verb,
                authorizationType: m.authorizationType,
                authorizerId: m.authorizerId,
                apiKeyRequired: m.apiKeyRequired,
                methodIntegration: m.methodIntegration
                  ? {
                      type: m.methodIntegration.type ?? "",
                      integrationHttpMethod: m.methodIntegration.httpMethod,
                      uri: m.methodIntegration.uri,
                    }
                  : undefined,
              },
            ]),
          )
        : undefined,
    }))
  },

  createResource: async (
    apiId: string,
    parentId: string,
    pathPart: string,
  ): Promise<ApiResource> => {
    const r = await awsClients
      .apigateway()
      .send(new CreateResourceCommand({ restApiId: apiId, parentId, pathPart }))
    return {
      id: r.id ?? "",
      parentId: r.parentId,
      pathPart: r.pathPart ?? "",
      path: r.path ?? "",
    }
  },

  deleteResource: async (apiId: string, resourceId: string): Promise<void> => {
    await awsClients.apigateway().send(new DeleteResourceCommand({ restApiId: apiId, resourceId }))
  },

  putMethod: async (
    apiId: string,
    resourceId: string,
    httpMethod: string,
    opts: { authorizationType?: string },
  ): Promise<void> => {
    await awsClients.apigateway().send(
      new PutMethodCommand({
        restApiId: apiId,
        resourceId,
        httpMethod,
        authorizationType: opts.authorizationType ?? "NONE",
      }),
    )
  },

  putIntegration: async (
    apiId: string,
    resourceId: string,
    httpMethod: string,
    opts: { type: string; uri?: string; integrationHttpMethod?: string },
  ): Promise<void> => {
    await awsClients.apigateway().send(
      new PutIntegrationCommand({
        restApiId: apiId,
        resourceId,
        httpMethod,
        type: opts.type as RestIntegrationType,
        uri: opts.uri,
        integrationHttpMethod: opts.integrationHttpMethod,
      }),
    )
  },

  getStages: async (apiId: string): Promise<ApiStage[]> => {
    const res = await awsClients.apigateway().send(new GetStagesCommand({ restApiId: apiId }))
    return (res.item ?? []).map((s) => ({
      stageName: s.stageName ?? "",
      deploymentId: s.deploymentId ?? "",
      description: s.description,
      createdDate: s.createdDate?.getTime(),
    }))
  },

  createStage: async (
    apiId: string,
    opts: { stageName: string; deploymentId: string; description?: string },
  ): Promise<ApiStage> => {
    const s = await awsClients.apigateway().send(
      new CreateStageCommand({
        restApiId: apiId,
        stageName: opts.stageName,
        deploymentId: opts.deploymentId,
        description: opts.description,
      }),
    )
    return {
      stageName: s.stageName ?? "",
      deploymentId: s.deploymentId ?? "",
      description: s.description,
      createdDate: s.createdDate?.getTime(),
    }
  },

  deleteStage: async (apiId: string, stageName: string): Promise<void> => {
    await awsClients.apigateway().send(new DeleteStageCommand({ restApiId: apiId, stageName }))
  },

  getDeployments: async (apiId: string): Promise<ApiDeployment[]> => {
    const res = await awsClients.apigateway().send(new GetDeploymentsCommand({ restApiId: apiId }))
    return (res.items ?? []).map((d) => ({
      id: d.id ?? "",
      description: d.description,
      createdDate: d.createdDate?.getTime(),
    }))
  },

  createDeployment: async (apiId: string, description?: string): Promise<ApiDeployment> => {
    const d = await awsClients
      .apigateway()
      .send(new CreateDeploymentCommand({ restApiId: apiId, description }))
    return {
      id: d.id ?? "",
      description: d.description,
      createdDate: d.createdDate?.getTime(),
    }
  },

  // ─── HTTP API v2 ────────────────────────────────────────────────────────

  listHttpApis: async (): Promise<HttpApi[]> => {
    const res = await awsClients.apigatewayv2().send(new GetApisCommand({}))
    return (res.Items ?? []).map((a) => ({
      apiId: a.ApiId ?? "",
      name: a.Name ?? "",
      protocolType: a.ProtocolType ?? "",
      description: a.Description,
      createdDate: a.CreatedDate?.toISOString(),
      apiEndpoint: a.ApiEndpoint,
      tags: a.Tags,
    }))
  },

  getHttpApi: async (id: string): Promise<HttpApi> => {
    const a = await awsClients.apigatewayv2().send(new GetApiCommand({ ApiId: id }))
    return {
      apiId: a.ApiId ?? "",
      name: a.Name ?? "",
      protocolType: a.ProtocolType ?? "",
      description: a.Description,
      createdDate: a.CreatedDate?.toISOString(),
      apiEndpoint: a.ApiEndpoint,
      tags: a.Tags,
    }
  },

  createHttpApi: async (opts: {
    name: string
    protocolType: string
    description?: string
  }): Promise<HttpApi> => {
    const a = await awsClients.apigatewayv2().send(
      new CreateApiCommand({
        Name: opts.name,
        ProtocolType: opts.protocolType as ProtocolType,
        Description: opts.description,
      }),
    )
    return {
      apiId: a.ApiId ?? "",
      name: a.Name ?? "",
      protocolType: a.ProtocolType ?? "",
      description: a.Description,
      createdDate: a.CreatedDate?.toISOString(),
    }
  },

  deleteHttpApi: async (id: string): Promise<void> => {
    await awsClients.apigatewayv2().send(new DeleteApiCommand({ ApiId: id }))
  },

  getRoutes: async (apiId: string): Promise<HttpRoute[]> => {
    const res = await awsClients.apigatewayv2().send(new GetRoutesCommand({ ApiId: apiId }))
    return (res.Items ?? []).map((r) => ({
      routeId: r.RouteId ?? "",
      routeKey: r.RouteKey ?? "",
      target: r.Target,
    }))
  },

  createRoute: async (apiId: string, routeKey: string, target?: string): Promise<HttpRoute> => {
    const r = await awsClients
      .apigatewayv2()
      .send(new CreateRouteCommand({ ApiId: apiId, RouteKey: routeKey, Target: target }))
    return {
      routeId: r.RouteId ?? "",
      routeKey: r.RouteKey ?? "",
      target: r.Target,
    }
  },

  deleteRoute: async (apiId: string, routeId: string): Promise<void> => {
    await awsClients.apigatewayv2().send(new DeleteRouteCommand({ ApiId: apiId, RouteId: routeId }))
  },

  getIntegrations: async (apiId: string): Promise<HttpIntegration[]> => {
    const res = await awsClients.apigatewayv2().send(new GetIntegrationsCommand({ ApiId: apiId }))
    return (res.Items ?? []).map((i) => ({
      integrationId: i.IntegrationId ?? "",
      integrationType: i.IntegrationType ?? "",
      integrationUri: i.IntegrationUri,
      payloadFormatVersion: i.PayloadFormatVersion,
    }))
  },

  createIntegration: async (
    apiId: string,
    opts: {
      integrationType: string
      integrationUri?: string
      payloadFormatVersion?: string
    },
  ): Promise<HttpIntegration> => {
    const i = await awsClients.apigatewayv2().send(
      new CreateIntegrationCommand({
        ApiId: apiId,
        IntegrationType: opts.integrationType as V2IntegrationType,
        IntegrationUri: opts.integrationUri,
        PayloadFormatVersion: opts.payloadFormatVersion,
      }),
    )
    return {
      integrationId: i.IntegrationId ?? "",
      integrationType: i.IntegrationType ?? "",
      integrationUri: i.IntegrationUri,
      payloadFormatVersion: i.PayloadFormatVersion,
    }
  },

  deleteIntegration: async (apiId: string, integrationId: string): Promise<void> => {
    await awsClients
      .apigatewayv2()
      .send(new DeleteIntegrationCommand({ ApiId: apiId, IntegrationId: integrationId }))
  },

  getHttpStages: async (apiId: string): Promise<HttpStage[]> => {
    const res = await awsClients.apigatewayv2().send(new GetV2StagesCommand({ ApiId: apiId }))
    return (res.Items ?? []).map((s) => ({
      stageName: s.StageName ?? "",
      deploymentId: s.DeploymentId,
      createdDate: s.CreatedDate?.toISOString(),
    }))
  },

  createHttpStage: async (apiId: string, stageName: string): Promise<HttpStage> => {
    const s = await awsClients
      .apigatewayv2()
      .send(new CreateV2StageCommand({ ApiId: apiId, StageName: stageName }))
    return {
      stageName: s.StageName ?? "",
      deploymentId: s.DeploymentId,
      createdDate: s.CreatedDate?.toISOString(),
    }
  },

  deleteHttpStage: async (apiId: string, stageName: string): Promise<void> => {
    await awsClients
      .apigatewayv2()
      .send(new DeleteV2StageCommand({ ApiId: apiId, StageName: stageName }))
  },

  getHttpDeployments: async (apiId: string): Promise<ApiDeployment[]> => {
    const res = await awsClients.apigatewayv2().send(new GetV2DeploymentsCommand({ ApiId: apiId }))
    return (res.Items ?? []).map((d) => ({
      id: d.DeploymentId ?? "",
      description: d.Description,
      createdDate: d.CreatedDate?.getTime(),
    }))
  },

  createHttpDeployment: async (apiId: string): Promise<ApiDeployment> => {
    const d = await awsClients.apigatewayv2().send(new CreateV2DeploymentCommand({ ApiId: apiId }))
    return {
      id: d.DeploymentId ?? "",
      description: d.Description,
      createdDate: d.CreatedDate?.getTime(),
    }
  },

  // ─── REST v1 Authorizers ────────────────────────────────────────────────

  listAuthorizers: async (apiId: string): Promise<Authorizer[]> => {
    const res = await awsClients.apigateway().send(new GetAuthorizersCommand({ restApiId: apiId }))
    return (res.items ?? []).map((a) => ({
      id: a.id ?? "",
      name: a.name ?? "",
      type: a.type ?? "",
      authorizerUri: a.authorizerUri,
      identitySource: a.identitySource,
      authorizerResultTtlInSeconds: a.authorizerResultTtlInSeconds,
      providerARNs: a.providerARNs,
    }))
  },

  createAuthorizer: async (
    apiId: string,
    opts: { name: string; type: string; authorizerUri?: string; identitySource?: string },
  ): Promise<Authorizer> => {
    const a = await awsClients.apigateway().send(
      new CreateAuthorizerCommand({
        restApiId: apiId,
        name: opts.name,
        type: opts.type as RestAuthorizerType,
        authorizerUri: opts.authorizerUri,
        identitySource: opts.identitySource,
      }),
    )
    return {
      id: a.id ?? "",
      name: a.name ?? "",
      type: a.type ?? "",
      authorizerUri: a.authorizerUri,
      identitySource: a.identitySource,
    }
  },

  deleteAuthorizer: async (apiId: string, authorizerId: string): Promise<void> => {
    await awsClients
      .apigateway()
      .send(new DeleteAuthorizerCommand({ restApiId: apiId, authorizerId }))
  },

  // ─── API Keys ───────────────────────────────────────────────────────────

  listApiKeys: async (): Promise<ApiKey[]> => {
    const res = await awsClients.apigateway().send(new GetApiKeysCommand({ includeValues: true }))
    return (res.items ?? []).map((k) => ({
      id: k.id ?? "",
      name: k.name ?? "",
      value: k.value,
      enabled: k.enabled ?? false,
      createdDate: k.createdDate?.getTime() ?? 0,
      description: k.description,
    }))
  },

  createApiKey: async (opts: { name: string; enabled?: boolean }): Promise<ApiKey> => {
    const k = await awsClients.apigateway().send(
      new CreateApiKeyCommand({
        name: opts.name,
        enabled: opts.enabled ?? true,
      }),
    )
    return {
      id: k.id ?? "",
      name: k.name ?? "",
      value: k.value,
      enabled: k.enabled ?? true,
      createdDate: k.createdDate?.getTime() ?? 0,
      description: k.description,
    }
  },

  deleteApiKey: async (id: string): Promise<void> => {
    await awsClients.apigateway().send(new DeleteApiKeyCommand({ apiKey: id }))
  },

  // ─── Usage Plans ────────────────────────────────────────────────────────

  listUsagePlans: async (): Promise<UsagePlan[]> => {
    const res = await awsClients.apigateway().send(new GetUsagePlansCommand({}))
    return (res.items ?? []).map((p) => ({
      id: p.id ?? "",
      name: p.name ?? "",
      description: p.description,
      apiStages: (p.apiStages ?? [])
        .filter((s) => s.apiId && s.stage)
        .map((s) => ({ apiId: s.apiId as string, stage: s.stage as string })),
    }))
  },

  createUsagePlan: async (opts: { name: string; description?: string }): Promise<UsagePlan> => {
    const p = await awsClients
      .apigateway()
      .send(new CreateUsagePlanCommand({ name: opts.name, description: opts.description }))
    return {
      id: p.id ?? "",
      name: p.name ?? "",
      description: p.description,
      apiStages: (p.apiStages ?? [])
        .filter((s) => s.apiId && s.stage)
        .map((s) => ({ apiId: s.apiId as string, stage: s.stage as string })),
    }
  },

  deleteUsagePlan: async (id: string): Promise<void> => {
    await awsClients.apigateway().send(new DeleteUsagePlanCommand({ usagePlanId: id }))
  },

  listUsagePlanKeys: async (planId: string): Promise<UsagePlanKey[]> => {
    const res = await awsClients
      .apigateway()
      .send(new GetUsagePlanKeysCommand({ usagePlanId: planId }))
    return (res.items ?? []).map((k) => ({
      id: k.id ?? "",
      name: k.name ?? "",
      type: k.type ?? "",
      value: k.value,
    }))
  },

  addUsagePlanKey: async (planId: string, keyId: string): Promise<UsagePlanKey> => {
    const k = await awsClients
      .apigateway()
      .send(new CreateUsagePlanKeyCommand({ usagePlanId: planId, keyId, keyType: "API_KEY" }))
    return {
      id: k.id ?? "",
      name: k.name ?? "",
      type: k.type ?? "",
    }
  },

  removeUsagePlanKey: async (planId: string, keyId: string): Promise<void> => {
    await awsClients
      .apigateway()
      .send(new DeleteUsagePlanKeyCommand({ usagePlanId: planId, keyId }))
  },

  // ─── HTTP v2 Authorizers ────────────────────────────────────────────────

  listV2Authorizers: async (apiId: string): Promise<AuthorizerV2[]> => {
    const res = await awsClients.apigatewayv2().send(new GetV2AuthorizersCommand({ ApiId: apiId }))
    return (res.Items ?? []).map((a) => ({
      authorizerId: a.AuthorizerId ?? "",
      name: a.Name ?? "",
      authorizerType: a.AuthorizerType ?? "",
      identitySource: a.IdentitySource?.join(", "),
      jwtConfiguration: a.JwtConfiguration
        ? {
            audience: a.JwtConfiguration.Audience ?? [],
            issuer: a.JwtConfiguration.Issuer ?? "",
          }
        : undefined,
    }))
  },

  createV2Authorizer: async (
    apiId: string,
    opts: {
      name: string
      authorizerType: string
      identitySource?: string
      jwtConfiguration?: { audience: string[]; issuer: string }
    },
  ): Promise<AuthorizerV2> => {
    const a = await awsClients.apigatewayv2().send(
      new CreateV2AuthorizerCommand({
        ApiId: apiId,
        Name: opts.name,
        AuthorizerType: opts.authorizerType as V2AuthorizerType,
        IdentitySource: opts.identitySource
          ? opts.identitySource
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean)
          : undefined,
        JwtConfiguration: opts.jwtConfiguration
          ? {
              Audience: opts.jwtConfiguration.audience,
              Issuer: opts.jwtConfiguration.issuer,
            }
          : undefined,
      }),
    )
    return {
      authorizerId: a.AuthorizerId ?? "",
      name: a.Name ?? "",
      authorizerType: a.AuthorizerType ?? "",
      identitySource: a.IdentitySource?.join(", "),
      jwtConfiguration: a.JwtConfiguration
        ? {
            audience: a.JwtConfiguration.Audience ?? [],
            issuer: a.JwtConfiguration.Issuer ?? "",
          }
        : undefined,
    }
  },

  deleteV2Authorizer: async (apiId: string, authorizerId: string): Promise<void> => {
    await awsClients
      .apigatewayv2()
      .send(new DeleteV2AuthorizerCommand({ ApiId: apiId, AuthorizerId: authorizerId }))
  },
}
