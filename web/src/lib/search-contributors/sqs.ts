import { sqs } from "@/services/api"
import type { SQSQueue } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<SQSQueue>({
  id: "sqs",
  cacheKey: (ep) => ["sqs", "queues", ep.baseUrl],
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
