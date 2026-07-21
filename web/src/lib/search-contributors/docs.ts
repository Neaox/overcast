import { registerContributor } from "@/lib/search"
import { searchDocsInWorker } from "./docs-worker-client"

registerContributor({
  id: "docs",
  search(query, ctx) {
    return searchDocsInWorker(query, { signal: ctx.signal, limit: 8 })
  },
})
