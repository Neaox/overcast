import type { BadgeProps } from "@/components/ui/badge"

/** Converts a CONSTANT_CASE status string to Title Case for display. */
export function formatStatus(status: string): string {
  return status
    .split("_")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
    .join(" ")
}

/** Maps a CloudFormation stack status to a Badge variant. */
export function stackStatusVariant(status: string): BadgeProps["variant"] {
  if (status.endsWith("_COMPLETE")) return "success"
  if (status.endsWith("_FAILED")) return "danger"
  if (status.endsWith("_IN_PROGRESS")) return "warning"
  if (status === "ROLLBACK_COMPLETE") return "danger"
  if (status === "REVIEW_IN_PROGRESS") return "info"
  return "default"
}

/** Maps a stack resource status to a Badge variant. */
export function resourceStatusVariant(status: string): BadgeProps["variant"] {
  return stackStatusVariant(status)
}

/** Returns true if a stack is currently in a transitional (in-progress) state. */
export function isStackInProgress(status: string): boolean {
  return status.endsWith("_IN_PROGRESS")
}

/** Returns true if a stack can be updated (not in a terminal or in-progress state). */
export function canUpdateStack(status: string): boolean {
  return (
    status === "CREATE_COMPLETE" ||
    status === "UPDATE_COMPLETE" ||
    status === "UPDATE_ROLLBACK_COMPLETE"
  )
}

/** Returns true if the stack ended in a failure or rollback-complete state. */
export function isStackFailed(status: string): boolean {
  return (
    status.endsWith("_FAILED") ||
    status === "ROLLBACK_COMPLETE" ||
    status === "UPDATE_ROLLBACK_COMPLETE"
  )
}

/** Returns true if a stack can be deleted. */
export function canDeleteStack(status: string): boolean {
  return status !== "DELETE_COMPLETE" && !status.endsWith("_IN_PROGRESS")
}
