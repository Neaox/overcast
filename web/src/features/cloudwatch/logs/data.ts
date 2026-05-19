/**
 * CloudWatch Logs query/mutation definitions.
 *
 * Key factory:
 *   logsKeys.all()                          -> ["logs"]
 *   logsKeys.groups()                       -> ["logs", "groups"]
 *   logsKeys.streams(groupName)             -> ["logs", "streams", groupName]
 *   logsKeys.filter(groupName)              -> ["logs", "filter", groupName]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { logs } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const logsKeys = {
  all: () => [...endpointStore.getKeys(), "logs"] as const,
  groups: () => [...logsKeys.all(), "groups"] as const,
  streams: (groupName: string) => [...logsKeys.all(), "streams", groupName] as const,
  filter: (groupName: string) => [...logsKeys.all(), "filter", groupName] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function logsGroupsQueryOptions() {
  return queryOptions({
    queryKey: logsKeys.groups(),
    queryFn: () => logs.listGroups(),
  })
}

export function logsStreamsQueryOptions(groupName: string) {
  return queryOptions({
    queryKey: logsKeys.streams(groupName),
    queryFn: () => logs.listStreams(groupName),
  })
}

export function logsFilterQueryOptions(
  groupName: string,
  opts: {
    filterPattern?: string
    startTime?: number
    endTime?: number
    logStreamNames?: string[]
    logStreamNamePrefix?: string
  } = {},
) {
  return queryOptions({
    queryKey: [...logsKeys.filter(groupName), opts] as const,
    queryFn: () => logs.filterEvents(groupName, opts),
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createLogGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...logsKeys.groups(), "create"] as const,
    mutationFn: (name: string) => logs.createGroup(name),
  })
}

export function deleteLogGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...logsKeys.groups(), "delete"] as const,
    mutationFn: (name: string) => logs.deleteGroup(name),
  })
}

export function createLogStreamMutationOptions(groupName: string) {
  return mutationOptions({
    mutationKey: [...logsKeys.streams(groupName), "create"] as const,
    mutationFn: (name: string) => logs.createStream(groupName, name),
  })
}

export function deleteLogStreamMutationOptions(groupName: string) {
  return mutationOptions({
    mutationKey: [...logsKeys.streams(groupName), "delete"] as const,
    mutationFn: (streamName: string) => logs.deleteStream(groupName, streamName),
  })
}
