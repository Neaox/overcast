import { HOST_ROUTE_LABELS, hostRoutedUrl } from "@/lib/host-routed-url"
import type { EmulatorEndpoint } from "@/services/discovery"

/**
 * The invoke URL for a REST (v1) resource path.
 *
 * REST v1 has no invoke-URL field — real AWS has none either, so the console
 * composes one client-side. It composes the canonical host-routed shape
 *
 *   {scheme}://{apiId}.execute-api.{region}.{host}[:{port}]/{stage}{path}
 *
 * which is the grammar the emulator's router dispatches on and the one every
 * server-minted URL already uses (Lambda function URLs, API Gateway v2
 * `apiEndpoint`, AppSync `uris`). See docs/plans/host-routing-precedence.md §8.
 *
 * The path-style `/restapis/{apiId}/{stage}/_user_request_{path}` form the
 * emulator also serves is the fallback for bases that cannot carry a
 * subdomain — a bare `localhost` or an IP. It is a working URL, just not the
 * one an AWS SDK or the real console would produce.
 *
 * `stageName` is undefined until the API has a stage; REST v1 cannot be invoked
 * without one, so the URL is incomplete in both forms until it does.
 */
export function restInvokeUrl(
  endpoint: EmulatorEndpoint,
  apiId: string,
  stageName: string | undefined,
  path: string,
): string {
  const stagePath = stageName ? `/${stageName}${path}` : path
  const hostRouted = hostRoutedUrl(
    endpoint.baseUrl,
    HOST_ROUTE_LABELS.executeApi,
    apiId,
    endpoint.region,
    stagePath,
  )
  if (hostRouted) return hostRouted

  const base = endpoint.baseUrl.replace(/\/$/, "")
  if (stageName) {
    return `${base}/restapis/${apiId}/${stageName}/_user_request_${path}`
  }
  return `${base}/restapis/${apiId}/_user_request_${path}`
}
