/**
 * Secrets Manager query/mutation definitions.
 *
 * Key factory:
 *   smKeys.all()                       -> [baseUrl, region, "secretsmanager"]
 *   smKeys.secrets()                   -> [...all(), "secrets"]
 *   smKeys.secret(id)                  -> [...all(), "secret", id]
 *   smKeys.secretValue(secretId)       -> [...all(), "value", secretId]
 *   smKeys.rotation(secretId)          -> [...all(), "rotation", secretId]
 *   smKeys.resourcePolicy(secretId)    -> [...all(), "policy", secretId]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { secretsmanager as sm } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const smKeys = {
  all: () => [...endpointStore.getKeys(), "secretsmanager"] as const,
  secrets: () => [...smKeys.all(), "secrets"] as const,
  secret: (secretId: string) => [...smKeys.all(), "secret", secretId] as const,
  secretValue: (secretId: string) => [...smKeys.all(), "value", secretId] as const,
  rotation: (secretId: string) => [...smKeys.all(), "rotation", secretId] as const,
  resourcePolicy: (secretId: string) => [...smKeys.all(), "policy", secretId] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function secretsListQueryOptions() {
  return queryOptions({
    queryKey: smKeys.secrets(),
    queryFn: () => sm.listSecrets(),
  })
}

export function secretDetailQueryOptions(secretId: string) {
  return queryOptions({
    queryKey: smKeys.secret(secretId),
    queryFn: () => sm.describeSecret(secretId),
    enabled: !!secretId,
  })
}

export function secretValueQueryOptions(secretId: string) {
  return queryOptions({
    queryKey: smKeys.secretValue(secretId),
    queryFn: () => sm.getSecretValue(secretId),
    enabled: !!secretId,
  })
}

export function secretRotationQueryOptions(secretId: string) {
  return queryOptions({
    queryKey: smKeys.rotation(secretId),
    queryFn: () => sm.getRotationStatus(secretId),
    enabled: !!secretId,
  })
}

export function secretResourcePolicyQueryOptions(secretId: string) {
  return queryOptions({
    queryKey: smKeys.resourcePolicy(secretId),
    queryFn: () => sm.getResourcePolicy(secretId),
    enabled: !!secretId,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createSecretMutationOptions() {
  return mutationOptions({
    mutationKey: [...smKeys.secrets(), "create"] as const,
    mutationFn: (params: { Name: string; SecretString?: string; Description?: string }) =>
      sm.createSecret(params),
  })
}

export function updateSecretValueMutationOptions() {
  return mutationOptions({
    mutationKey: [...smKeys.secrets(), "update"] as const,
    mutationFn: (params: { secretId: string; secretString: string }) =>
      sm.updateSecretValue(params.secretId, params.secretString),
  })
}

export function deleteSecretMutationOptions() {
  return mutationOptions({
    mutationKey: [...smKeys.secrets(), "delete"] as const,
    mutationFn: (secretId: string) => sm.deleteSecret(secretId),
  })
}
