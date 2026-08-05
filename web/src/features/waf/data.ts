import { mutationOptions, queryOptions } from "@tanstack/react-query"
import { waf, type CreateWebACLValues, type WebACLSummary, type WAFScope } from "@/services/api/waf"
import { endpointStore } from "@/services/endpoint-store"

export const wafKeys = {
  all: () => [...endpointStore.getKeys(), "waf"] as const,
  webACLs: () => [...wafKeys.all(), "web-acls"] as const,
  webACL: (scope: WAFScope, id: string, name: string) =>
    [...wafKeys.webACLs(), scope, id, name] as const,
}

export function webACLsQueryOptions() {
  return queryOptions({ queryKey: wafKeys.webACLs(), queryFn: () => waf.listAllWebACLs() })
}

export function webACLQueryOptions(scope: WAFScope, id: string, name: string) {
  return queryOptions({
    queryKey: wafKeys.webACL(scope, id, name),
    queryFn: () => waf.getWebACL({ scope, id, name }),
    enabled: !!id && !!name,
  })
}

export function createWebACLMutationOptions() {
  return mutationOptions({
    mutationKey: [...wafKeys.webACLs(), "create"] as const,
    mutationFn: (values: CreateWebACLValues) => waf.createWebACL(values),
  })
}

export function deleteWebACLMutationOptions() {
  return mutationOptions({
    mutationKey: [...wafKeys.webACLs(), "delete"] as const,
    mutationFn: (value: WebACLSummary) => waf.deleteWebACL(value),
  })
}
