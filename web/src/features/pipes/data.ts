/**
 * EventBridge Pipes query/mutation definitions.
 *
 * Key factory:
 *   pipeKeys.all()                  -> ["pipes"]
 *   pipeKeys.list(baseUrl)        -> ["pipes", "list", baseUrl]
 *   pipeKeys.pipe(baseUrl, name)  -> ["pipes", "pipe", baseUrl, name]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { pipes } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const pipeKeys = {
  all: () => [...endpointStore.getKeys(), "pipes"] as const,
  list: () => [...pipeKeys.all(), "list"] as const,
  pipe: (name: string) => [...pipeKeys.all(), "pipe", name] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function pipeListQueryOptions() {
  return queryOptions({
    queryKey: pipeKeys.list(),
    queryFn: () => pipes.listPipes(),
  })
}

export function pipeQueryOptions(name: string) {
  return queryOptions({
    queryKey: pipeKeys.pipe(name),
    queryFn: () => pipes.describePipe(name),
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createPipeMutationOptions() {
  return mutationOptions({
    mutationKey: [...pipeKeys.all(), "create"] as const,
    mutationFn: (opts: { name: string; sourceArn: string; targetArn: string }) =>
      pipes.createPipe(opts.name, opts.sourceArn, opts.targetArn),
  })
}

export function deletePipeMutationOptions() {
  return mutationOptions({
    mutationKey: [...pipeKeys.all(), "delete"] as const,
    mutationFn: (name: string) => pipes.deletePipe(name),
  })
}
