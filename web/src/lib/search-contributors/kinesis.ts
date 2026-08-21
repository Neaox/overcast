import { kinesis } from "@/services/api"
import { kinesisKeys } from "@/features/kinesis/data"
import type { KinesisStream } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<KinesisStream>({
  id: "kinesis",
  // Reuse the feature's own key factory so this can never drift out of sync
  // with the shape (and endpoint/region scoping) the real query uses.
  cacheKey: () => kinesisKeys.streams(),
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
