/**
 * Presentation helpers for the Auto Scaling page.
 *
 * Kept out of the components so the mapping from AWS's vocabulary to a badge
 * tone — the part that is easy to get subtly wrong — is unit-testable.
 */

export type BadgeTone = "default" | "outline" | "accent" | "success" | "warning" | "danger" | "info"

/** lifecycleTone maps an instance's LifecycleState to a badge tone. */
export function lifecycleTone(state: string | undefined): BadgeTone {
  switch (state) {
    case "InService":
      return "success"
    case "Pending":
      return "info"
    case "Pending:Wait":
    case "Terminating:Wait":
      return "warning"
    case "Terminating":
    case "Terminated":
      return "danger"
    default:
      return "default"
  }
}

/** healthTone maps an instance's HealthStatus to a badge tone. */
export function healthTone(status: string | undefined): BadgeTone {
  return status === "Healthy" ? "success" : status ? "danger" : "default"
}

/** activityTone maps a scaling activity's StatusCode to a badge tone. */
export function activityTone(statusCode: string | undefined): BadgeTone {
  switch (statusCode) {
    case "Successful":
      return "success"
    case "Failed":
      return "danger"
    case "Cancelled":
      return "warning"
    case undefined:
      return "default"
    default:
      // PreInService, InProgress, WaitingForInstanceId, MidLifecycleAction …
      return "info"
  }
}

/**
 * capacitySummary is how many instances are actually in service against what
 * was asked for. This is the number the page exists to show — a desired
 * capacity nothing acts on is the failure the reconciler was built to prevent.
 */
export function capacitySummary(inService: number, desired: number | undefined): string {
  return `${inService} / ${desired ?? 0}`
}

/**
 * isConverged reports whether every instance has reached a settled state.
 *
 * Deliberately stricter than comparing in-service against desired: a group
 * that has reached its desired count while still tearing an instance down has
 * not settled, and capacitySummary alone would read as though it had.
 */
export function isConverged(
  instances: { LifecycleState?: string }[],
  desired: number | undefined,
): boolean {
  const inService = instances.filter((i) => i.LifecycleState === "InService").length
  return inService === (desired ?? 0) && instances.length === inService
}

/** formatTime renders an AWS timestamp for the activity table. */
export function formatTime(value: Date | string | undefined): string {
  if (!value) return "—"
  const date = typeof value === "string" ? new Date(value) : value
  if (Number.isNaN(date.getTime())) return "—"
  return date.toLocaleTimeString()
}
