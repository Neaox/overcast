/**
 * S3 query/mutation definitions.
 *
 * Key factory follows the hierarchical pattern recommended by TkDodo:
 *   s3Keys.all                                  -> ["s3"]
 *   s3Keys.buckets()                            -> ["s3", "buckets"]
 *   s3Keys.bucketList(baseUrl)                  -> ["s3", "buckets", baseUrl]
 *   s3Keys.objects()                            -> ["s3", "objects"]
 *   s3Keys.objectList(baseUrl, bucket, prefix)  -> ["s3", "objects", baseUrl, bucket, prefix]
 *   s3Keys.meta()                               -> ["s3", "meta"]
 *   s3Keys.objectMeta(baseUrl, bucket, key)     -> ["s3", "meta", baseUrl, bucket, key]
 *
 * Queries — pass directly to useQuery:
 *   useQuery(s3Queries.buckets(endpoint.baseUrl))
 *
 * Mutations — all factories, always called. Spread into useMutation and add callbacks:
 *   useMutation({ ...deleteBucketMutationOptions(), onSuccess: () => ... })
 *   useMutation({ ...deleteObjectMutationOptions(bucket), onSuccess: () => ... })
 *
 * Mutation keys enable useIsMutating / useMutationState across components:
 *   useIsMutating({ mutationKey: deleteBucketMutationOptions().mutationKey })
 *
 * Invalidation uses the coarse keys so it catches all related queries:
 *   qc.invalidateQueries({ queryKey: s3Keys.buckets() })
 *   qc.invalidateQueries({ queryKey: s3Keys.objectList(baseUrl, bucket, prefix) })
 */

import { queryOptions, infiniteQueryOptions, mutationOptions } from "@tanstack/react-query"
import { s3 } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const s3Keys = {
  all: () => [...endpointStore.getKeys(), "s3"] as const,
  buckets: () => [...s3Keys.all(), "buckets"] as const,
  objects: () => [...s3Keys.all(), "objects"] as const,
  objectList: (bucket: string, prefix: string) => [...s3Keys.objects(), bucket, prefix] as const,
  meta: () => [...s3Keys.all(), "meta"] as const,
  objectMeta: (bucket: string, key: string) => [...s3Keys.meta(), bucket, key] as const,
  notification: () => [...s3Keys.all(), "notification"] as const,
  bucketNotification: (bucket: string) => [...s3Keys.notification(), bucket] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function s3BucketsQueryOptions() {
  return queryOptions({
    queryKey: s3Keys.buckets(),
    queryFn: () => s3.listBuckets(),
  })
}

export function s3ObjectsQueryOptions(bucket: string, prefix: string) {
  return infiniteQueryOptions({
    queryKey: s3Keys.objectList(bucket, prefix),
    queryFn: ({ pageParam }) =>
      s3.listObjects(bucket, { prefix, delimiter: "/", token: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextContinuationToken ?? undefined,
  })
}

export function s3ObjectMetaQueryOptions(bucket: string, key: string) {
  return queryOptions({
    queryKey: s3Keys.objectMeta(bucket, key),
    queryFn: () => s3.getObjectMetadata(bucket, key),
  })
}

export function s3BucketNotificationQueryOptions(bucket: string) {
  return queryOptions({
    queryKey: s3Keys.bucketNotification(bucket),
    queryFn: () => s3.getBucketNotification(bucket),
  })
}

export function s3BucketExistsQueryOptions(bucket: string) {
  return queryOptions({
    queryKey: [...s3Keys.all(), "bucket-exists", bucket] as const,
    queryFn: () => s3.headBucket(bucket),
    retry: false,
    staleTime: 30_000,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────
//
// All are factory functions — always called, never referenced directly.
// This keeps the call pattern uniform whether or not args are needed, and
// puts any scoping params (bucket) into the key for precise observation:
//   useIsMutating({ mutationKey: deleteObjectMutationOptions(bucket).mutationKey })

export function createBucketMutationOptions() {
  return mutationOptions({
    mutationKey: [...s3Keys.buckets(), "create"] as const,
    mutationFn: ({ name, region }: { name: string; region?: string }) =>
      s3.createBucket(name, region),
  })
}

export function deleteBucketMutationOptions() {
  return mutationOptions({
    mutationKey: [...s3Keys.buckets(), "delete"] as const,
    mutationFn: (name: string) => s3.deleteBucket(name),
  })
}

export function deleteObjectMutationOptions(bucket: string) {
  return mutationOptions({
    mutationKey: [...s3Keys.objectList(bucket, "*"), "delete"] as const,
    mutationFn: (key: string) => s3.deleteObject(bucket, key),
  })
}

export function deleteByPrefixMutationOptions(bucket: string) {
  return mutationOptions({
    mutationKey: [...s3Keys.objectList(bucket, "*"), "delete-prefix"] as const,
    mutationFn: (prefix: string) => s3.deleteObjectsByPrefix(bucket, prefix),
  })
}

export function putBucketNotificationMutationOptions(bucket: string) {
  return mutationOptions({
    mutationKey: [...s3Keys.notification(), bucket, "put"] as const,
    mutationFn: (config: Parameters<typeof s3.putBucketNotification>[1]) =>
      s3.putBucketNotification(bucket, config),
  })
}
