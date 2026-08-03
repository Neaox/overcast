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
  wiring: () => [...pipeKeys.all(), "wiring"] as const,
  deliveries: (name: string) => [...pipeKeys.all(), "deliveries", name] as const,
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

/**
 * Every pipe's resolved source/enrichment/target types and whether the
 * emulator can actually run the combination. The list and detail views both
 * read it, so a pipe that is stored but inert is never shown as if it were
 * running.
 */
export function pipeWiringQueryOptions() {
  return queryOptions({
    queryKey: pipeKeys.wiring(),
    queryFn: () => pipes.listWiring(),
  })
}

/** Recent executions of one pipe, newest first. */
export function pipeDeliveriesQueryOptions(name: string) {
  return queryOptions({
    queryKey: pipeKeys.deliveries(name),
    queryFn: () => pipes.listDeliveries(name),
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createPipeMutationOptions() {
  return mutationOptions({
    mutationKey: [...pipeKeys.all(), "create"] as const,
    mutationFn: (opts: {
      name: string
      sourceArn: string
      targetArn: string
      enrichmentArn?: string
    }) => pipes.createPipe(opts.name, opts.sourceArn, opts.targetArn, opts.enrichmentArn),
  })
}

export function deletePipeMutationOptions() {
  return mutationOptions({
    mutationKey: [...pipeKeys.all(), "delete"] as const,
    mutationFn: (name: string) => pipes.deletePipe(name),
  })
}
