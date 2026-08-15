import { apiFetch, API_BASE, endpointHeaders, endpointResolver } from "./base"

/**
 * Settings → HTTPS backend (BFF: /api/settings/*, daemon: /_overcast/tls/*).
 *
 * These are emulator-specific endpoints with no AWS SDK equivalent, so plain
 * BFF fetches are the right tool here (see web/AGENTS.md).
 */

export type TrustStoreState = "installed" | "not_installed" | "unavailable" | "unknown"

export interface HttpsStatus {
  /** "off" (plain HTTP), "auto" (OVERCAST_TLS=auto) or "explicit" (own cert/key). */
  mode: "off" | "auto" | "explicit"
  /** The local CA certificate exists on the daemon's disk. */
  caExists: boolean
  trustStore: TrustStoreState
  /** The daemon runs in a container — trust must be installed from the host. */
  inContainer: boolean
  /** Host-side CLI command that installs trust when the daemon cannot. */
  hostCommand?: string
  /** The restart-with-TLS command; present while mode is "off". */
  restartCommand?: string
}

export interface HttpsSetupResult {
  ok: boolean
  caReady: boolean
  trustInstalled: boolean
  alreadyInstalled: boolean
  restartRequired: boolean
  restartCommand?: string
  hostCommand?: string
}

export const settings = {
  httpsStatus: () => apiFetch<HttpsStatus>("/settings/https"),
  /**
   * The in-daemon `overcast https enable`: mints the CA + server certificate
   * and (natively) installs the CA into the system trust store — the OS asks
   * the user to approve, which can take arbitrarily long, so no timeout.
   */
  enableHttps: () => apiFetch<HttpsSetupResult>("/settings/https/enable", { method: "POST" }),
  /**
   * Fetches the daemon's CA certificate (public half) for a user-initiated
   * download. Goes through fetch-as-blob rather than a bare <a href> so the
   * x-overcast-endpoint header still applies when the console is pointed at
   * a non-default emulator.
   */
  fetchCACert: async (): Promise<Blob> => {
    const endpoint = endpointResolver.get()
    const res = await fetch(`${API_BASE}/ca.pem`, { headers: endpointHeaders(endpoint) })
    if (!res.ok) {
      throw new Error(
        res.status === 404
          ? "No CA certificate exists yet — run the preparation step first."
          : `HTTP ${res.status}`,
      )
    }
    return res.blob()
  },
  /**
   * Probes whether the console answers on an https origin. `no-cors` keeps
   * this a pure reachability check: the promise resolves (opaquely) once a
   * trusted TLS handshake and HTTP exchange succeed, and rejects while the
   * daemon is down, still serving plain HTTP, or serving a certificate this
   * browser does not trust yet — exactly the three states the settings page
   * needs to distinguish "ready to switch" from "not yet".
   */
  probeHttps: async (httpsOrigin: string): Promise<boolean> => {
    try {
      await fetch(`${httpsOrigin}/api/health`, { mode: "no-cors", cache: "no-store" })
      return true
    } catch {
      return false
    }
  },
}
