import { sqs } from "@/services/api"
import { sqsKeys } from "@/features/sqs/data"
import type { SQSQueue } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<SQSQueue>({
  id: "sqs",
  // Reuse the feature's own key factory so this can never drift out of sync
  // with the shape (and endpoint/region scoping) the real query uses.
  cacheKey: () => sqsKeys.queues(),
  fetchAll: () => sqs.listQueues(),
  matchFields: (q) => [q.name, q.arn, q.url],
  toResult: (q) => ({
    id: `sqs:${q.name}`,
    label: q.name,
    sublabel: q.arn,
    service: "SQS",
    serviceKey: "/sqs",
    type: q.name.endsWith(".fifo") ? "FIFO Queue" : "Queue",
    href: `/sqs/${encodeURIComponent(q.name)}`,
  }),
})
