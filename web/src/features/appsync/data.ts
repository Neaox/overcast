import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { appsync } from "@/services/api/appsync"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const appsyncKeys = {
  all: () => [...endpointStore.getKeys(), "appsync"] as const,
  apis: () => [...appsyncKeys.all(), "apis"] as const,
  api: (apiId: string) => [...appsyncKeys.all(), "api", apiId] as const,
  dataSources: (apiId: string) => [...appsyncKeys.api(apiId), "dataSources"] as const,
  resolvers: (apiId: string, typeName: string) =>
    [...appsyncKeys.api(apiId), "resolvers", typeName] as const,
  functions: (apiId: string) => [...appsyncKeys.api(apiId), "functions"] as const,
  apiKeys: (apiId: string) => [...appsyncKeys.api(apiId), "apiKeys"] as const,
  schemaStatus: (apiId: string) => [...appsyncKeys.api(apiId), "schemaStatus"] as const,
  types: (apiId: string) => [...appsyncKeys.api(apiId), "types"] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function appsyncApisQueryOptions() {
  return queryOptions({
    queryKey: appsyncKeys.apis(),
    queryFn: () => appsync.listApis(),
  })
}

export function appsyncApiQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: appsyncKeys.api(apiId),
    queryFn: () => appsync.getApi(apiId),
  })
}

export function appsyncDataSourcesQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: appsyncKeys.dataSources(apiId),
    queryFn: () => appsync.listDataSources(apiId),
  })
}

export function appsyncResolversQueryOptions(apiId: string, typeName: string) {
  return queryOptions({
    queryKey: appsyncKeys.resolvers(apiId, typeName),
    queryFn: () => appsync.listResolvers(apiId, typeName),
    enabled: !!typeName,
  })
}

export function appsyncFunctionsQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: appsyncKeys.functions(apiId),
    queryFn: () => appsync.listFunctions(apiId),
  })
}

export function appsyncApiKeysQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: appsyncKeys.apiKeys(apiId),
    queryFn: () => appsync.listApiKeys(apiId),
  })
}

export function appsyncSchemaStatusQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: appsyncKeys.schemaStatus(apiId),
    queryFn: () => appsync.getSchemaStatus(apiId),
  })
}

export function appsyncTypesQueryOptions(apiId: string) {
  return queryOptions({
    queryKey: appsyncKeys.types(apiId),
    queryFn: () => appsync.listTypes(apiId),
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createApiMutationOptions() {
  return mutationOptions({
    mutationKey: [...appsyncKeys.apis(), "create"] as const,
    mutationFn: (name: string) => appsync.createApi(name),
  })
}

export function deleteApiMutationOptions() {
  return mutationOptions({
    mutationKey: [...appsyncKeys.apis(), "delete"] as const,
    mutationFn: (apiId: string) => appsync.deleteApi(apiId),
  })
}

export function createApiKeyMutationOptions(apiId: string) {
  return mutationOptions({
    mutationKey: [...appsyncKeys.apiKeys(apiId), "create"] as const,
    mutationFn: (description?: string) => appsync.createApiKey(apiId, description),
  })
}

export function deleteApiKeyMutationOptions(apiId: string) {
  return mutationOptions({
    mutationKey: [...appsyncKeys.apiKeys(apiId), "delete"] as const,
    mutationFn: (keyId: string) => appsync.deleteApiKey(apiId, keyId),
  })
}

export function startSchemaCreationMutationOptions(apiId: string) {
  return mutationOptions({
    mutationKey: [...appsyncKeys.schemaStatus(apiId), "create"] as const,
    mutationFn: (definition: string) => appsync.startSchemaCreation(apiId, definition),
  })
}
