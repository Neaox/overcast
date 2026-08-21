import { s3 } from "@/services/api"
import { s3Keys } from "@/features/s3/data"
import type { S3Bucket } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<S3Bucket>({
  id: "s3",
  // Reuse the feature's own key factory so this can never drift out of sync
  // with the shape (and endpoint/region scoping) the real query uses.
  cacheKey: () => s3Keys.buckets(),
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
