interface SearchRequest {
  type: "search"
  id: number
  query: string
  limit: number
}

interface CancelRequest {
  type: "cancel"
  id: number
}

interface DocsSearchResult {
  id: string
  label: string
  sublabel?: string
  service: string
  serviceKey: string
  type: string
  href: string
}

interface DocsSearchResponse {
  results?: Array<{
    ID: number
    Href: string
    Title: string
    Description: string
    Section: string
    Score: number
  }>
}

const controllers = new Map<number, AbortController>()

self.onmessage = (event: MessageEvent<SearchRequest | CancelRequest>) => {
  const msg = event.data
  if (msg.type === "cancel") {
    controllers.get(msg.id)?.abort()
    controllers.delete(msg.id)
    return
  }
  void search(msg)
}

async function search(msg: SearchRequest): Promise<void> {
  const controller = new AbortController()
  controllers.set(msg.id, controller)
  try {
    const params = new URLSearchParams({ q: msg.query, limit: String(msg.limit) })
    const res = await fetch(`/api/docs/search?${params.toString()}`, {
      signal: controller.signal,
    })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = (await res.json()) as DocsSearchResponse
    const results = (data.results ?? []).map(toSearchResult)
    postMessage({ id: msg.id, results })
  } catch (err) {
    if ((err as Error).name !== "AbortError") {
      postMessage({ id: msg.id, results: [] })
    }
  } finally {
    controllers.delete(msg.id)
  }
}

function toSearchResult(doc: NonNullable<DocsSearchResponse["results"]>[number]): DocsSearchResult {
  return {
    id: `docs:${doc.ID}`,
    label: doc.Title,
    sublabel: doc.Description,
    service: "Documentation",
    serviceKey: "/docs",
    type: doc.Section,
    href: `/docs?path=${encodeURIComponent(doc.Href)}`,
  }
}
