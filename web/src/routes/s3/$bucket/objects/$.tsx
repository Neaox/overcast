/**
 * The object browser, addressed by path.
 *
 * The splat is the browser's whole position: `""` is the bucket root, a value
 * ending in "/" is the folder to list, and anything else is the object to open
 * the inspector on. See `features/s3/object-location.ts`.
 */
import { createFileRoute } from "@tanstack/react-router"
import { BucketDetail } from "@/features/s3/components/bucket-detail"

export interface ObjectBrowserSearch {
  /**
   * Which stored revision the inspector reads, when the object was opened from
   * the version history. Absent means the current version — distinct from the
   * literal id `"null"` that an unversioned write is stored under.
   */
  versionId?: string
}

export const Route = createFileRoute("/s3/$bucket/objects/$")({
  // The splat is a label here, not a position: the folder/object distinction
  // does not change the title, and the param has had its trailing slash
  // trimmed away regardless.
  head: ({ params }) => {
    const where = params._splat
    return { meta: [{ title: `${where ? `${where} — ` : ""}${params.bucket} — S3 — Overcast` }] }
  },
  validateSearch: (search: Record<string, unknown>): ObjectBrowserSearch => ({
    versionId: typeof search.versionId === "string" ? search.versionId : undefined,
  }),
  component: BucketDetail,
})
