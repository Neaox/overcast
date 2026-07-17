import type { SearchResult } from "@/lib/search"

interface WorkerResponse {
  id: number
  results: SearchResult[]
}

let worker: Worker | null = null
let nextID = 0
const pending = new Map<number, (results: SearchResult[]) => void>()

function getWorker(): Worker {
  if (!worker) {
    worker = new Worker(new URL("../../workers/docs-search.worker.ts", import.meta.url), {
      type: "module",
    })
    worker.onmessage = (event: MessageEvent<WorkerResponse>) => {
      const resolve = pending.get(event.data.id)
      if (!resolve) return
      pending.delete(event.data.id)
      resolve(event.data.results)
    }
  }
  return worker
}

export function searchDocsInWorker(
  query: string,
  options: { signal?: AbortSignal; limit?: number } = {},
): Promise<SearchResult[]> {
  const id = ++nextID
  const docsWorker = getWorker()
  return new Promise((resolve) => {
    const finish = (results: SearchResult[]) => {
      pending.delete(id)
      options.signal?.removeEventListener("abort", cleanup)
      resolve(results)
    }
    const cleanup = () => {
      pending.delete(id)
      docsWorker.postMessage({ type: "cancel", id })
      resolve([])
    }
    pending.set(id, finish)
    options.signal?.addEventListener("abort", cleanup, { once: true })
    docsWorker.postMessage({
      type: "search",
      id,
      query,
      limit: options.limit ?? 8,
    })
  })
}
