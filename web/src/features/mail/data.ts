/**
 * Inbox query/mutation definitions for the captured message inbox.
 *
 * Key factory:
 *   inboxKeys.all()              -> [baseUrl, region, "inbox"]
 *   inboxKeys.messages()         -> [...all(), "messages"]
 *   inboxKeys.message(id)        -> [...all(), "message", id]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { inbox } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const inboxKeys = {
  all: () => [...endpointStore.getKeys(), "inbox"] as const,
  messages: () => [...inboxKeys.all(), "messages"] as const,
  message: (id: string) => [...inboxKeys.all(), "message", id] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function inboxMessagesQueryOptions() {
  return queryOptions({
    queryKey: inboxKeys.messages(),
    queryFn: () => inbox.list(),
  })
}

export function inboxMessageQueryOptions(id: string) {
  return queryOptions({
    queryKey: inboxKeys.message(id),
    queryFn: () => inbox.get(id),
    enabled: !!id,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function clearInboxMutationOptions() {
  return mutationOptions({
    mutationKey: [...inboxKeys.all(), "clear"] as const,
    mutationFn: () => inbox.clear(),
  })
}

export function deleteInboxMessageMutationOptions() {
  return mutationOptions({
    mutationKey: [...inboxKeys.all(), "delete"] as const,
    mutationFn: (id: string) => inbox.delete(id),
  })
}
