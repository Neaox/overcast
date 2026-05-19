/**
 * Cognito query/mutation definitions.
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { cognito } from "@/services/api/cognito"
import type {
  PasswordPolicy,
  ManagedLoginBranding,
  AdminCreateUserConfig,
  EmailConfig,
  VerificationMessageTemplate,
} from "@/services/api/cognito"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const cognitoKeys = {
  all: () => [...endpointStore.getKeys(), "cognito"] as const,
  pools: () => [...cognitoKeys.all(), "pools"] as const,
  pool: (poolId: string) => [...cognitoKeys.all(), "pool", poolId] as const,
  users: (poolId: string) => [...cognitoKeys.all(), "users", poolId] as const,
  groups: (poolId: string) => [...cognitoKeys.all(), "groups", poolId] as const,
  groupMembers: (poolId: string, groupName: string) =>
    [...cognitoKeys.all(), "groupMembers", poolId, groupName] as const,
  user: (poolId: string, username: string) =>
    [...cognitoKeys.all(), "user", poolId, username] as const,
  clients: (poolId: string) => [...cognitoKeys.all(), "clients", poolId] as const,
  client: (poolId: string, clientId: string) =>
    [...cognitoKeys.all(), "client", poolId, clientId] as const,
  branding: (poolId: string) => [...cognitoKeys.all(), "branding", poolId] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function cognitoPoolsQueryOptions() {
  return queryOptions({
    queryKey: cognitoKeys.pools(),
    queryFn: () => cognito.listPools(),
  })
}

export function cognitoPoolDetailQueryOptions(poolId: string) {
  return queryOptions({
    queryKey: cognitoKeys.pool(poolId),
    queryFn: () => cognito.describePool(poolId),
    enabled: !!poolId,
  })
}

export function cognitoUsersQueryOptions(poolId: string) {
  return queryOptions({
    queryKey: cognitoKeys.users(poolId),
    queryFn: () => cognito.listUsers(poolId),
    enabled: !!poolId,
  })
}

export function cognitoGroupsQueryOptions(poolId: string) {
  return queryOptions({
    queryKey: cognitoKeys.groups(poolId),
    queryFn: () => cognito.listGroups(poolId),
    enabled: !!poolId,
  })
}

export function cognitoGroupMembersQueryOptions(poolId: string, groupName: string) {
  return queryOptions({
    queryKey: cognitoKeys.groupMembers(poolId, groupName),
    queryFn: () => cognito.listUsersInGroup(poolId, groupName),
    enabled: !!poolId && !!groupName,
  })
}

export function cognitoUserDetailQueryOptions(poolId: string, username: string) {
  return queryOptions({
    queryKey: cognitoKeys.user(poolId, username),
    queryFn: () => cognito.getUser(poolId, username),
    enabled: !!poolId && !!username,
  })
}

export function cognitoClientsQueryOptions(poolId: string) {
  return queryOptions({
    queryKey: cognitoKeys.clients(poolId),
    queryFn: () => cognito.listClients(poolId),
    enabled: !!poolId,
  })
}

export function cognitoClientDetailQueryOptions(poolId: string, clientId: string) {
  return queryOptions({
    queryKey: cognitoKeys.client(poolId, clientId),
    queryFn: () => cognito.describeClient(poolId, clientId),
    enabled: !!poolId && !!clientId,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createPoolMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.pools(), "create"] as const,
    mutationFn: (opts: {
      name: string
      usernameAttributes?: string[]
      adminOnly?: boolean
      passwordPolicy?: PasswordPolicy
    }) => cognito.createPool(opts),
  })
}

export function deletePoolMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.pools(), "delete"] as const,
    mutationFn: (poolId: string) => cognito.deletePool(poolId),
  })
}

export function createUserMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "createUser"] as const,
    mutationFn: ({
      poolId,
      username,
      email,
      phoneNumber,
      temporaryPassword,
      messageAction,
    }: {
      poolId: string
      username: string
      email?: string
      phoneNumber?: string
      temporaryPassword?: string
      messageAction?: "SUPPRESS" | "RESEND"
    }) =>
      cognito.createUser(poolId, username, {
        email,
        phoneNumber,
        temporaryPassword,
        messageAction,
      }),
  })
}

export function deleteUserMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "deleteUser"] as const,
    mutationFn: ({ poolId, username }: { poolId: string; username: string }) =>
      cognito.deleteUser(poolId, username),
  })
}

export function disableUserMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "disableUser"] as const,
    mutationFn: ({ poolId, username }: { poolId: string; username: string }) =>
      cognito.disableUser(poolId, username),
  })
}

export function enableUserMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "enableUser"] as const,
    mutationFn: ({ poolId, username }: { poolId: string; username: string }) =>
      cognito.enableUser(poolId, username),
  })
}

export function setUserPasswordMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "setUserPassword"] as const,
    mutationFn: ({
      poolId,
      username,
      password,
      permanent,
    }: {
      poolId: string
      username: string
      password: string
      permanent: boolean
    }) => cognito.setUserPassword(poolId, username, password, permanent),
  })
}

export function createGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "createGroup"] as const,
    mutationFn: ({
      poolId,
      name,
      description,
    }: {
      poolId: string
      name: string
      description?: string
    }) => cognito.createGroup(poolId, name, description),
  })
}

export function deleteGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "deleteGroup"] as const,
    mutationFn: ({ poolId, groupName }: { poolId: string; groupName: string }) =>
      cognito.deleteGroup(poolId, groupName),
  })
}

export function addUserToGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "addUserToGroup"] as const,
    mutationFn: ({
      poolId,
      username,
      groupName,
    }: {
      poolId: string
      username: string
      groupName: string
    }) => cognito.addUserToGroup(poolId, username, groupName),
  })
}

export function removeUserFromGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "removeUserFromGroup"] as const,
    mutationFn: ({
      poolId,
      username,
      groupName,
    }: {
      poolId: string
      username: string
      groupName: string
    }) => cognito.removeUserFromGroup(poolId, username, groupName),
  })
}

export function createClientMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "createClient"] as const,
    mutationFn: ({
      poolId,
      name,
      generateSecret,
    }: {
      poolId: string
      name: string
      generateSecret: boolean
    }) => cognito.createClient(poolId, name, generateSecret),
  })
}

export function deleteClientMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "deleteClient"] as const,
    mutationFn: ({ poolId, clientId }: { poolId: string; clientId: string }) =>
      cognito.deleteClient(poolId, clientId),
  })
}

export function updateUserAttributesMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "updateUserAttributes"] as const,
    mutationFn: ({
      poolId,
      username,
      attributes,
    }: {
      poolId: string
      username: string
      attributes: { name: string; value: string }[]
    }) => cognito.updateUserAttributes(poolId, username, attributes),
  })
}

export function deleteUserAttributesMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "deleteUserAttributes"] as const,
    mutationFn: ({
      poolId,
      username,
      attributeNames,
    }: {
      poolId: string
      username: string
      attributeNames: string[]
    }) => cognito.deleteUserAttributes(poolId, username, attributeNames),
  })
}

export function updateClientMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "updateClient"] as const,
    mutationFn: ({
      poolId,
      clientId,
      updates,
    }: {
      poolId: string
      clientId: string
      updates: {
        accessTokenValidity?: number
        idTokenValidity?: number
        refreshTokenValidity?: number
        tokenValidityUnits?: {
          accessToken?: string
          idToken?: string
          refreshToken?: string
        }
        callbackUrls?: string[]
        logoutUrls?: string[]
        allowedOAuthFlows?: string[]
        allowedOAuthScopes?: string[]
        allowedOAuthFlowsUserPoolClient?: boolean
        supportedIdentityProviders?: string[]
      }
    }) => cognito.updateClient(poolId, clientId, updates),
  })
}

export function createDomainMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "createDomain"] as const,
    mutationFn: ({ poolId, domain }: { poolId: string; domain: string }) =>
      cognito.createDomain(poolId, domain),
  })
}

export function deleteDomainMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "deleteDomain"] as const,
    mutationFn: ({ poolId, domain }: { poolId: string; domain: string }) =>
      cognito.deleteDomain(poolId, domain),
  })
}

export function updatePoolPasswordPolicyMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "updatePoolPasswordPolicy"] as const,
    mutationFn: ({ poolId, policy }: { poolId: string; policy: PasswordPolicy }) =>
      cognito.updatePoolPasswordPolicy(poolId, policy),
  })
}

export function cognitoBrandingQueryOptions(poolId: string) {
  return queryOptions({
    queryKey: cognitoKeys.branding(poolId),
    queryFn: () => cognito.getBranding(poolId),
    enabled: !!poolId,
  })
}

export function setBrandingMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "setBranding"] as const,
    mutationFn: ({ poolId, branding }: { poolId: string; branding: ManagedLoginBranding }) =>
      cognito.setBranding(poolId, branding),
  })
}

export function updatePoolSelfRegistrationMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "updatePoolSelfRegistration"] as const,
    mutationFn: ({ poolId, config }: { poolId: string; config: AdminCreateUserConfig }) =>
      cognito.updatePoolSelfRegistration(poolId, config),
  })
}

export function updatePoolEmailConfigMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "updatePoolEmailConfig"] as const,
    mutationFn: ({ poolId, config }: { poolId: string; config: EmailConfig }) =>
      cognito.updatePoolEmailConfig(poolId, config),
  })
}

export function updatePoolVerificationMessagesMutationOptions() {
  return mutationOptions({
    mutationKey: [...cognitoKeys.all(), "updatePoolVerificationMessages"] as const,
    mutationFn: ({
      poolId,
      template,
    }: {
      poolId: string
      template: VerificationMessageTemplate
    }) => cognito.updatePoolVerificationMessages(poolId, template),
  })
}
