import type { SearchResult } from "@/lib/search"
import { createWorkerClient } from "@/lib/worker-client"

interface WorkerResponse {
  id: number
  results: SearchResult[]
}

// The shared kernel (lib/worker-client.ts) owns the lifecycle: this client
// used to hang a search forever if the worker died and to throw from
// construction where Worker is unavailable — now a dead or unbuildable
// worker resolves searches with no results instead, and one failure gets a
// respawn before the session gives up.
const docsWorker = createWorkerClient<WorkerResponse>({
  create: () =>
    new Worker(new URL("../../workers/docs-search.worker.ts", import.meta.url), {
      type: "module",
    }),
  failureLimit: 2,
})

export function searchDocsInWorker(
  query: string,
  options: { signal?: AbortSignal; limit?: number } = {},
): Promise<SearchResult[]> {
  const outcome = docsWorker.request<SearchResult[]>({
    message: (id) => ({ type: "search", id, query, limit: options.limit ?? 8 }),
    decode: (reply) => reply.results,
    // Failure and abort both answer "no docs results" — the same shape the
    // worker itself replies with when its fetch fails.
    fallback: () => [],
    signal: options.signal,
    cancelMessage: (id) => ({ type: "cancel", id }),
  })
  return outcome.async ? outcome.promise : Promise.resolve(outcome.value)
}
