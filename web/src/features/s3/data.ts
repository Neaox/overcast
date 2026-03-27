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
import { api } from "@/services/api"

// ─── Key factory ───────────────────────────────────────────────────────────

export const s3Keys = {
  all: ["s3"] as const,
  buckets: () => [...s3Keys.all, "buckets"] as const,
  bucketList: (baseUrl: string) => [...s3Keys.buckets(), baseUrl] as const,
  objects: () => [...s3Keys.all, "objects"] as const,
  objectList: (baseUrl: string, bucket: string, prefix: string) =>
    [...s3Keys.objects(), baseUrl, bucket, prefix] as const,
  meta: () => [...s3Keys.all, "meta"] as const,
  objectMeta: (baseUrl: string, bucket: string, key: string) =>
    [...s3Keys.meta(), baseUrl, bucket, key] as const,
  notification: () => [...s3Keys.all, "notification"] as const,
  bucketNotification: (baseUrl: string, bucket: string) =>
    [...s3Keys.notification(), baseUrl, bucket] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export const s3Queries = {
  buckets(baseUrl: string) {
    return queryOptions({
      queryKey: s3Keys.bucketList(baseUrl),
      queryFn: () => api.s3.listBuckets(),
    })
  },

  objects(baseUrl: string, bucket: string, prefix: string) {
    return infiniteQueryOptions({
      queryKey: s3Keys.objectList(baseUrl, bucket, prefix),
      queryFn: ({ pageParam }) =>
        api.s3.listObjects(bucket, { prefix, delimiter: "/", token: pageParam }),
      initialPageParam: undefined as string | undefined,
      getNextPageParam: (lastPage) => lastPage.nextContinuationToken ?? undefined,
    })
  },

  objectMeta(baseUrl: string, bucket: string, key: string) {
    return queryOptions({
      queryKey: s3Keys.objectMeta(baseUrl, bucket, key),
      queryFn: () => api.s3.getObjectMetadata(bucket, key),
    })
  },

  bucketNotification(baseUrl: string, bucket: string) {
    return queryOptions({
      queryKey: s3Keys.bucketNotification(baseUrl, bucket),
      queryFn: () => api.s3.getBucketNotification(bucket),
    })
  },
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
      api.s3.createBucket(name, region),
  })
}

export function deleteBucketMutationOptions() {
  return mutationOptions({
    mutationKey: [...s3Keys.buckets(), "delete"] as const,
    mutationFn: (name: string) => api.s3.deleteBucket(name),
  })
}

export function deleteObjectMutationOptions(bucket: string) {
  return mutationOptions({
    mutationKey: [...s3Keys.objectList("*", bucket, "*"), "delete"] as const,
    mutationFn: (key: string) => api.s3.deleteObject(bucket, key),
  })
}

export function deleteByPrefixMutationOptions(bucket: string) {
  return mutationOptions({
    mutationKey: [...s3Keys.objectList("*", bucket, "*"), "delete-prefix"] as const,
    mutationFn: (prefix: string) => api.s3.deleteObjectsByPrefix(bucket, prefix),
  })
}
