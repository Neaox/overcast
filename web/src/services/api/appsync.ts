import { awsClients } from "../aws-clients"
import {
  ListGraphqlApisCommand,
  CreateGraphqlApiCommand,
  DeleteGraphqlApiCommand,
  GetGraphqlApiCommand,
  ListDataSourcesCommand,
  ListResolversCommand,
  ListFunctionsCommand,
  ListApiKeysCommand,
  GetSchemaCreationStatusCommand,
  ListTypesCommand,
  CreateApiKeyCommand,
  DeleteApiKeyCommand,
  StartSchemaCreationCommand,
} from "@aws-sdk/client-appsync"
import type {
  AppSyncApi,
  AppSyncDataSource,
  AppSyncResolver,
  AppSyncFunction,
  AppSyncApiKey,
  AppSyncType,
} from "@/types"

export const appsync = {
  listApis: async (): Promise<AppSyncApi[]> => {
    const res = await awsClients.appsync().send(new ListGraphqlApisCommand({}))
    return res.graphqlApis ?? []
  },

  getApi: async (apiId: string): Promise<AppSyncApi> => {
    const res = await awsClients.appsync().send(new GetGraphqlApiCommand({ apiId }))
    return res.graphqlApi!
  },

  createApi: async (name: string) => {
    await awsClients.appsync().send(
      new CreateGraphqlApiCommand({
        name,
        authenticationType: "API_KEY",
      }),
    )
  },

  deleteApi: async (apiId: string) => {
    await awsClients.appsync().send(new DeleteGraphqlApiCommand({ apiId }))
  },

  listDataSources: async (apiId: string): Promise<AppSyncDataSource[]> => {
    const res = await awsClients.appsync().send(new ListDataSourcesCommand({ apiId }))
    return res.dataSources ?? []
  },

  listResolvers: async (apiId: string, typeName: string): Promise<AppSyncResolver[]> => {
    const res = await awsClients.appsync().send(new ListResolversCommand({ apiId, typeName }))
    return res.resolvers ?? []
  },

  listFunctions: async (apiId: string): Promise<AppSyncFunction[]> => {
    const res = await awsClients.appsync().send(new ListFunctionsCommand({ apiId }))
    return res.functions ?? []
  },

  listApiKeys: async (apiId: string): Promise<AppSyncApiKey[]> => {
    const res = await awsClients.appsync().send(new ListApiKeysCommand({ apiId }))
    return res.apiKeys ?? []
  },

  createApiKey: async (apiId: string, description?: string) => {
    await awsClients.appsync().send(new CreateApiKeyCommand({ apiId, description }))
  },

  deleteApiKey: async (apiId: string, id: string) => {
    await awsClients.appsync().send(new DeleteApiKeyCommand({ apiId, id }))
  },

  getSchemaStatus: async (apiId: string): Promise<{ status: string; details: string }> => {
    try {
      const res = await awsClients.appsync().send(new GetSchemaCreationStatusCommand({ apiId }))
      return {
        status: res.status ?? "NOT_APPLICABLE",
        details: res.details ?? "",
      }
    } catch {
      return { status: "NOT_APPLICABLE", details: "" }
    }
  },

  startSchemaCreation: async (apiId: string, definition: string) => {
    await awsClients.appsync().send(
      new StartSchemaCreationCommand({
        apiId,
        definition: new TextEncoder().encode(definition),
      }),
    )
  },

  listTypes: async (apiId: string): Promise<AppSyncType[]> => {
    const res = await awsClients.appsync().send(new ListTypesCommand({ apiId, format: "SDL" }))
    return res.types ?? []
  },
}
