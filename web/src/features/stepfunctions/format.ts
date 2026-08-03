/**
 * Presentation helpers shared by the Step Functions views.
 */

type BadgeVariant = "default" | "success" | "danger" | "warning" | "info"

/** Maps an execution status to the badge colour that reads correctly for it. */
export function executionStatusVariant(status: string | undefined): BadgeVariant {
  switch (status) {
    case "SUCCEEDED":
      return "success"
    case "RUNNING":
      return "info"
    case "FAILED":
    case "TIMED_OUT":
      return "danger"
    case "ABORTED":
      return "warning"
    default:
      return "default"
  }
}

/** Maps a history event type to a badge colour by what it means for the run. */
export function historyEventVariant(type: string | undefined): BadgeVariant {
  if (!type) return "default"
  if (type.endsWith("Failed") || type.endsWith("TimedOut") || type.endsWith("Aborted")) {
    return "danger"
  }
  if (type.endsWith("Succeeded")) return "success"
  if (type.endsWith("Started") || type.endsWith("Scheduled")) return "info"
  return "default"
}

/** Formats an SDK timestamp for a table cell, or an em dash when absent. */
export function formatTimestamp(value: Date | undefined): string {
  if (!value) return "—"
  return value.toLocaleString()
}

/** Pretty-prints a JSON payload string, leaving non-JSON text untouched. */
export function prettyJSON(value: string | undefined): string {
  if (!value) return ""
  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch {
    return value
  }
}

/**
 * Extracts the state name a history event refers to, so the history table can
 * show which state each event belongs to without the caller reaching into
 * every per-type detail field.
 */
export function historyEventStateName(event: {
  stateEnteredEventDetails?: { name?: string }
  stateExitedEventDetails?: { name?: string }
}): string {
  return event.stateEnteredEventDetails?.name ?? event.stateExitedEventDetails?.name ?? ""
}

/**
 * Extracts the error and cause an event carries, whichever of the many typed
 * detail fields it arrived in. This is what surfaces an Overcast
 * "not supported" failure to the user instead of a bare FAILED.
 */
export function historyEventFailure(event: Record<string, unknown>): {
  error: string
  cause: string
} {
  for (const [key, value] of Object.entries(event)) {
    if (!key.endsWith("EventDetails") || typeof value !== "object" || value === null) continue
    const details = value as { error?: string; cause?: string }
    if (details.error || details.cause) {
      return { error: details.error ?? "", cause: details.cause ?? "" }
    }
  }
  return { error: "", cause: "" }
}
