/**
 * AppRegistry query definitions.
 *
 * Key factory:
 *   appKeys.all                                  -> ["appregistry"]
 *   appKeys.list()                               -> ["appregistry", "list"]
 *   appKeys.detail(id)                           -> ["appregistry", "detail", id]
 *   appKeys.resources(id)                        -> ["appregistry", "resources", id]
 *   appKeys.reverseMap()                         -> ["appregistry", "reverse-map"]
 */

import { queryOptions } from "@tanstack/react-query"
import { appregistry, cloudformation } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

export const appKeys = {
  all: () => [...endpointStore.getKeys(), "appregistry"] as const,
  list: () => [...appKeys.all(), "list"] as const,
  detail: (id: string) => [...appKeys.all(), "detail", id] as const,
  resources: (id: string) => [...appKeys.all(), "resources", id] as const,
  reverseMap: () => [...appKeys.all(), "reverse-map"] as const,
}

export function applicationsQueryOptions() {
  return queryOptions({
    queryKey: appKeys.list(),
    queryFn: () => appregistry.listApplications(),
  })
}

export function applicationQueryOptions(id: string) {
  return queryOptions({
    queryKey: appKeys.detail(id),
    queryFn: () => appregistry.getApplication(id),
    enabled: !!id,
  })
}

export function applicationResourcesQueryOptions(id: string) {
  return queryOptions({
    queryKey: appKeys.resources(id),
    queryFn: () => appregistry.listAssociatedResources(id),
    enabled: !!id,
  })
}

/**
 * Reverse-map query: any resource key → owning application summary.
 *
 * Builds one map shared across all resource detail pages so the ownership
 * banner doesn't refetch per page. The map is keyed by *every* identifier a
 * resource might be addressed by:
 *
 *   1. The stack's own ARN (for the CFN stack detail page).
 *   2. The stack's name (common in user-facing URLs).
 *   3. Any per-resource association recorded by the provisioner from CDK's
 *      `awsApplication` tag — these come back from ListAssociatedResources
 *      directly and are keyed by their physical ID.
 *   4. Each child resource's PhysicalResourceId from ListStackResources —
 *      kept as a fallback for stacks that were created without CDK's tag
 *      (e.g. hand-written templates or raw CFN).
 *   5. When any indexed key looks like an ARN, the last slash- or
 *      colon-separated segment is indexed too so pages holding a bare name
 *      still hit.
 */
export interface OwningApplication {
  id: string
  name: string
  arn: string
}

function addKey(
  map: Record<string, OwningApplication>,
  key: string | undefined,
  owner: OwningApplication,
) {
  if (!key) return
  map[key] = owner
  // Also index the bare tail of ARN-like identifiers so callers can hit with a name.
  const slash = key.lastIndexOf("/")
  if (slash !== -1 && slash < key.length - 1) {
    map[key.slice(slash + 1)] = owner
  }
  const colon = key.lastIndexOf(":")
  if (colon !== -1 && colon < key.length - 1) {
    map[key.slice(colon + 1)] = owner
  }
}

export function applicationReverseMapQueryOptions() {
  return queryOptions({
    queryKey: appKeys.reverseMap(),
    queryFn: async (): Promise<Record<string, OwningApplication>> => {
      const apps = await appregistry.listApplications()
      const map: Record<string, OwningApplication> = {}

      await Promise.all(
        apps.map(async (app) => {
          const owner: OwningApplication = { id: app.id, name: app.name, arn: app.arn }
          const resources = await appregistry.listAssociatedResources(app.id)

          for (const r of resources) {
            // Associated resource is usually a CFN stack ARN — index the ARN
            // and extract the stack name for a second index, then expand the
            // stack's child resources via ListStackResources.
            addKey(map, r.arn, owner)

            if (r.resourceType === "CFN_STACK") {
              // Extract stack name from ARN: arn:aws:cloudformation:<r>:<acct>:stack/<name>/<uuid>
              const stackName = stackNameFromArn(r.arn)
              addKey(map, stackName, owner)
              if (stackName) {
                try {
                  const children = await cloudformation.listStackResources(stackName)
                  for (const child of children) {
                    addKey(map, child.PhysicalResourceId, owner)
                  }
                } catch {
                  // Stack may have been deleted or not yet visible — ignore.
                }
              }
            }
          }
        }),
      )
      return map
    },
    staleTime: 30_000,
  })
}

function stackNameFromArn(arn: string): string | undefined {
  // arn:aws:cloudformation:<region>:<acct>:stack/<name>/<uuid>
  const marker = ":stack/"
  const i = arn.indexOf(marker)
  if (i === -1) return undefined
  const rest = arn.slice(i + marker.length)
  const slash = rest.indexOf("/")
  return slash === -1 ? rest : rest.slice(0, slash)
}
