/**
 * CloudWatch Logs query definitions.
 *
 * Key factory:
 *   logsKeys.all                       -> ["logs"]
 *   logsKeys.groups()                  -> ["logs", "groups"]
 *   logsKeys.filter(logGroupName)      -> ["logs", "filter", logGroupName]
 */

import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const logsKeys = {
  all: () => [...endpointStore.getKeys(), "logs"] as const,
  groups: () => [...logsKeys.all(), "groups"] as const,
  filter: (logGroupName: string) => [...logsKeys.all(), "filter", logGroupName] as const,
}
