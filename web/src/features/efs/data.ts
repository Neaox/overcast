/**
 * EFS query/mutation definitions.
 *
 * Key factory:
 *   efsKeys.all()               -> [...endpoint, "efs"]
 *   efsKeys.fileSystems()       -> [...endpoint, "efs", "file-systems"]
 *   efsKeys.mountTargets(fsId)  -> [...endpoint, "efs", "mount-targets", fsId]
 *   efsKeys.accessPoints(fsId)  -> [...endpoint, "efs", "access-points", fsId]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import type { CreateFileSystemCommandInput } from "@aws-sdk/client-efs"
import { efs } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const efsKeys = {
  all: () => [...endpointStore.getKeys(), "efs"] as const,
  fileSystems: () => [...efsKeys.all(), "file-systems"] as const,
  mountTargets: (fsId: string) => [...efsKeys.all(), "mount-targets", fsId] as const,
  accessPoints: (fsId: string) => [...efsKeys.all(), "access-points", fsId] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function efsFileSystemsQueryOptions() {
  return queryOptions({
    queryKey: efsKeys.fileSystems(),
    queryFn: () => efs.listFileSystems(),
    staleTime: 5_000,
  })
}

export function efsMountTargetsQueryOptions(fsId: string) {
  return queryOptions({
    queryKey: efsKeys.mountTargets(fsId),
    queryFn: () => efs.listMountTargets(fsId),
    staleTime: 5_000,
  })
}

export function efsAccessPointsQueryOptions(fsId: string) {
  return queryOptions({
    queryKey: efsKeys.accessPoints(fsId),
    queryFn: () => efs.listAccessPoints(fsId),
    staleTime: 5_000,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createFileSystemMutationOptions() {
  return mutationOptions({
    mutationKey: [...efsKeys.fileSystems(), "create"] as const,
    mutationFn: (opts: CreateFileSystemCommandInput) => efs.createFileSystem(opts),
  })
}

export function deleteFileSystemMutationOptions() {
  return mutationOptions({
    mutationKey: [...efsKeys.fileSystems(), "delete"] as const,
    mutationFn: (id: string) => efs.deleteFileSystem(id),
  })
}
