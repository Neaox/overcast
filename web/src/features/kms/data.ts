/**
 * KMS query/mutation definitions.
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { kms } from "@/services/api/kms"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const kmsKeys = {
  all: () => [...endpointStore.getKeys(), "kms"] as const,
  keys: () => [...kmsKeys.all(), "keys"] as const,
  key: (keyId: string) => [...kmsKeys.all(), "key", keyId] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function kmsKeysQueryOptions() {
  return queryOptions({
    queryKey: kmsKeys.keys(),
    queryFn: () => kms.listKeys(),
  })
}

export function kmsKeyDetailQueryOptions(keyId: string) {
  return queryOptions({
    queryKey: kmsKeys.key(keyId),
    queryFn: () => kms.describeKey(keyId),
    enabled: !!keyId,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createKeyMutationOptions() {
  return mutationOptions({
    mutationKey: [...kmsKeys.keys(), "create"] as const,
    mutationFn: (description?: string) => kms.createKey(description),
  })
}

export function enableKeyMutationOptions() {
  return mutationOptions({
    mutationKey: [...kmsKeys.keys(), "enable"] as const,
    mutationFn: (keyId: string) => kms.enableKey(keyId),
  })
}

export function disableKeyMutationOptions() {
  return mutationOptions({
    mutationKey: [...kmsKeys.keys(), "disable"] as const,
    mutationFn: (keyId: string) => kms.disableKey(keyId),
  })
}

export function scheduleKeyDeletionMutationOptions() {
  return mutationOptions({
    mutationKey: [...kmsKeys.keys(), "delete"] as const,
    mutationFn: (keyId: string) => kms.scheduleKeyDeletion(keyId),
  })
}
