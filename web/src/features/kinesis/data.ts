/**
 * Kinesis query/mutation definitions.
 *
 * Key factory:
 *   kinesisKeys.all                                      -> ["kinesis"]
 *   kinesisKeys.streams()                                -> ["kinesis", "streams"]
 *   kinesisKeys.streamList(baseUrl)                      -> ["kinesis", "streams", baseUrl]
 *   kinesisKeys.stream()                                 -> ["kinesis", "stream"]
 *   kinesisKeys.streamDetail(baseUrl, name)              -> ["kinesis", "stream", baseUrl, name]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { kinesis } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const kinesisKeys = {
  all: () => [...endpointStore.getKeys(), "kinesis"] as const,
  streams: () => [...kinesisKeys.all(), "streams"] as const,
  stream: () => [...kinesisKeys.all(), "stream"] as const,
  streamDetail: (name: string) => [...kinesisKeys.stream(), name] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function kinesisStreamsQueryOptions() {
  return queryOptions({
    queryKey: kinesisKeys.streams(),
    queryFn: () => kinesis.listStreams(),
  })
}

export function kinesisStreamQueryOptions(name: string) {
  return queryOptions({
    queryKey: kinesisKeys.streamDetail(name),
    queryFn: () => kinesis.getStream(name),
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createStreamMutationOptions() {
  return mutationOptions({
    mutationKey: [...kinesisKeys.streams(), "create"] as const,
    mutationFn: ({ name, shardCount }: { name: string; shardCount: number }) =>
      kinesis.createStream(name, shardCount),
  })
}

export function deleteStreamMutationOptions() {
  return mutationOptions({
    mutationKey: [...kinesisKeys.streams(), "delete"] as const,
    mutationFn: (name: string) => kinesis.deleteStream(name),
  })
}
