// ─── Server-supplied config (GET /config) ────────────────────────────────────
//
// The compat server exposes context the UI can't derive itself — notably
// whether the compat server is running inside a dev container, which changes
// how "open handler" links work. Rather than construct `vscode://file/...`
// URIs on the client (which break in devcontainers because the host VS Code
// can't resolve `/workspace` paths), the UI POSTs to `/open` and lets the
// server shell out to the `code` CLI. That CLI, when run inside a container
// attached to a VS Code host window, forwards the open request through the
// VS Code Server shim — transparent to us.

let configLoaded = false;
let configPromise: Promise<void> | null = null;

export function loadServerConfig(): Promise<void> {
  if (configPromise) return configPromise;
  configPromise = fetch("/config")
    .then((r) => (r.ok ? r.json() : null))
    .then(() => {
      configLoaded = true;
    })
    .catch(() => {});
  return configPromise;
}

/**
 * Request that the compat server open the service handler directory for a
 * given AWS service in the developer's editor. Works transparently on the
 * host or inside a dev container — the server shells out to `code --goto`.
 * Returns true on success so the caller can show transient feedback.
 */
export async function openHandler(service: string): Promise<boolean> {
  if (!configLoaded) return false;
  try {
    const res = await fetch("/open", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ path: `internal/services/${service}/` }),
    });
    return res.ok;
  } catch {
    return false;
  }
}

/** True once GET /config has responded — gates rendering of open-handler UI. */
export function hasServerConfig(): boolean {
  return configLoaded;
}

/**
 * Build a shell command that reruns exactly one failing test against a
 * locally-running overcast. Developers copy this into a terminal to
 * iterate on a single failure without rerunning the whole suite.
 */
/**
 * Extract AWS request IDs from a test error message. SDKs embed these in
 * error strings in several shapes (bare UUIDs, `RequestId=…`, `request id:
 * …`, …) and overcast logs them as `request_id` on every served request —
 * so surfacing the ID gives developers a grep anchor into the server log.
 * Returns a deduped, order-preserving list.
 */
export function extractRequestIds(err: string | undefined): string[] {
  if (!err) return [];
  const out = new Set<string>();
  const uuid =
    /\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi;
  for (const m of err.matchAll(uuid)) out.add(m[0].toLowerCase());
  const labeled = /request[-_ ]?id[=:]\s*["']?([A-Za-z0-9-]{8,})["']?/gi;
  for (const m of err.matchAll(labeled)) out.add(m[1]);
  return [...out];
}

export function reproduceCommand(opts: {
  suite: string;
  group: string;
  test: string;
}): string {
  const pair = `${opts.group}:${opts.test}`;
  return (
    `OVERCAST_COMPAT_TEST_PAIRS='${pair}' ` +
    `bash compat/suites/${opts.suite}/run.sh`
  );
}

/** Format a millisecond duration to a compact human string: 1m 23s / 45s / <1s */
export function formatDuration(ms: number): string {
  if (ms < 1000) return "<1s";
  const secs = Math.round(ms / 1000);
  if (secs < 60) return `${secs}s`;
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  return s === 0 ? `${m}m` : `${m}m ${s}s`;
}

/**
 * Formats a remaining-time estimate for display.
 * Quantizes to 30s steps to prevent rapid flipping near minute boundaries
 * (e.g. alternating between ~1m and ~2m when the true value is ~90s).
 */
export function formatETA(remainingMs: number): string {
  const secs = Math.round(remainingMs / 1000);
  const snapped = Math.max(30, Math.round(secs / 30) * 30);
  if (snapped < 60) return `~${snapped}s left`;
  const m = Math.floor(snapped / 60);
  const s = snapped % 60;
  return s > 0 ? `~${m}m ${s}s left` : `~${m}m left`;
}
