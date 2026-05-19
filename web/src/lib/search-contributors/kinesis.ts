import { kinesis } from "@/services/api"
import type { KinesisStream } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<KinesisStream>({
  id: "kinesis",
  cacheKey: (ep) => ["kinesis", "streams", ep.baseUrl],
  fetchAll: () => kinesis.listStreams(),
  matchFields: (s) => [s.name, s.arn],
  toResult: (s) => ({
    id: `kinesis:${s.name}`,
    label: s.name,
    sublabel: s.arn,
    service: "Kinesis",
    serviceKey: "/kinesis",
    type: "Data Stream",
    href: `/kinesis/${encodeURIComponent(s.name)}`,
  }),
})
