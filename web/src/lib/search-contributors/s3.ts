import { s3 } from "@/services/api"
import type { S3Bucket } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<S3Bucket>({
  id: "s3",
  cacheKey: (ep) => ["s3", "buckets", ep.baseUrl],
  fetchAll: () => s3.listBuckets(),
  matchFields: (b) => [b.name, `arn:aws:s3:::${b.name}`],
  toResult: (b) => ({
    id: `s3:${b.name}`,
    label: b.name,
    sublabel: `arn:aws:s3:::${b.name}`,
    service: "S3",
    serviceKey: "/s3",
    type: "Bucket",
    href: `/s3/${encodeURIComponent(b.name)}`,
  }),
})
