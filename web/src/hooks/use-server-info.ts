import { useQuery, queryOptions } from "@tanstack/react-query"
import { endpointResolver, fetchServerInfo } from "@/services/discovery"
import type { ServerInfo } from "@/services/discovery"

export const serverInfoKeys = {
  all: ["server-info"] as const,
  endpoint: (baseUrl: string) => [...serverInfoKeys.all, baseUrl] as const,
}

export function serverInfoQueryOptions(baseUrl = endpointResolver.get().baseUrl) {
  return queryOptions({
    queryKey: serverInfoKeys.endpoint(baseUrl),
    queryFn: () => fetchServerInfo(baseUrl),
  })
}

function bootstrapServerInfo(baseUrl: string): ServerInfo | undefined {
  if (typeof window === "undefined" || window.__OVERCAST__?.debug === undefined) return undefined
  if (window.__OVERCAST__.apiBaseUrl?.replace(/\/$/, "") !== baseUrl) return undefined
  return {
    region: window.__OVERCAST__.region,
    debug: window.__OVERCAST__.debug,
  }
}

export function useDebugEnabled(): boolean {
  const baseUrl = endpointResolver.get().baseUrl
  const { data } = useQuery({
    ...serverInfoQueryOptions(baseUrl),
    placeholderData: bootstrapServerInfo(baseUrl),
  })
  return data?.debug === true
}
