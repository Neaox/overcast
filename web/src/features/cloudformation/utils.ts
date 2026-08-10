import type { BadgeProps } from "@/components/ui/badge"
import type { StackResourceSummary } from "@aws-sdk/client-cloudformation"

/** Converts a CONSTANT_CASE status string to Title Case for display. */
export function formatStatus(status: string): string {
  return status
    .split("_")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
    .join(" ")
}

/**
 * True for any status that describes a rollback, whichever operation unwound.
 *
 * CloudFormation has no "ordinary" rollback to set against a failure rollback:
 * a rollback is always the response to a failure, or a `RollbackStack` call on
 * a stack that a failure already left in `CREATE_FAILED` / `UPDATE_FAILED`.
 * What the enum distinguishes is *which* operation was rolled back —
 * `ROLLBACK_*` a create, `UPDATE_ROLLBACK_*` an update, `IMPORT_ROLLBACK_*` an
 * import, `EXPORT_ROLLBACK_*` an export — so every one of them means the
 * deployment did not land. The reason lives in `StackStatusReason` and in the
 * per-event `ResourceStatusReason`, never in the status itself.
 */
export function isRollbackStatus(status: string): boolean {
  return /(?:^|_)ROLLBACK_/.test(status)
}

/**
 * Maps a CloudFormation stack status to a Badge variant.
 *
 * **Order matters, and specific cases must come first.** `endsWith` is a
 * catch-all: every arm below it only runs for statuses it did not swallow.
 * Two arms used to sit underneath one — `ROLLBACK_COMPLETE` ends in
 * `_COMPLETE` and `REVIEW_IN_PROGRESS` ends in `_IN_PROGRESS` — so both were
 * dead code, and a CDK deploy that failed and rolled back wore the same green
 * badge as one that succeeded.
 */
export function stackStatusVariant(status: string): BadgeProps["variant"] {
  // A stack that exists only because a change set was created against it.
  // Nothing has been provisioned and nothing is happening.
  if (status === "REVIEW_IN_PROGRESS") return "info"
  if (status.endsWith("_FAILED")) return "danger"
  // Before the *_COMPLETE and *_IN_PROGRESS suffixes: a rollback that has
  // finished is still a failed deployment, and one still running is a
  // deployment that has already failed — neither is a healthy stack.
  if (isRollbackStatus(status)) return "danger"
  if (status.endsWith("_COMPLETE")) return "success"
  if (status.endsWith("_IN_PROGRESS")) return "warning"
  return "default"
}

/**
 * Maps a stack resource status to a Badge variant.
 *
 * `ResourceStatus` is a different enum from `StackStatus` — it carries
 * `DELETE_SKIPPED` and the `EXPORT_*` family, and lacks `REVIEW_IN_PROGRESS`
 * and the `*_CLEANUP_IN_PROGRESS` states. Delegating is still correct because
 * the classifier keys off the grammar the two vocabularies share (the
 * `_FAILED` / `_COMPLETE` / `_IN_PROGRESS` suffixes and the rollback infix)
 * rather than off an enumerated list of stack statuses, and because the one
 * resource status outside that grammar, `DELETE_SKIPPED`, correctly lands on
 * the neutral default: a resource left behind by a `Retain` deletion policy
 * is neither a success nor a failure. `utils.test.ts` pins the whole resource
 * enum so this stays true if either vocabulary grows.
 */
export function resourceStatusVariant(status: string): BadgeProps["variant"] {
  return stackStatusVariant(status)
}

/** Returns true if a stack is currently in a transitional (in-progress) state. */
export function isStackInProgress(status: string): boolean {
  // REVIEW_IN_PROGRESS carries the suffix but is not transitional: no
  // operation is running, so a spinner on it never stops and the Delete
  // action gets hidden on a stack AWS is perfectly willing to delete.
  return status !== "REVIEW_IN_PROGRESS" && status.endsWith("_IN_PROGRESS")
}

/** Returns true if a stack is currently unwinding an operation that failed. */
export function isStackRollingBack(status: string): boolean {
  return isRollbackStatus(status) && status.endsWith("_IN_PROGRESS")
}

/**
 * The statuses AWS calls a "last known stable state" — exactly the set a
 * stack can be updated from.
 *
 * https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_RollbackStack.html
 *
 * `ROLLBACK_COMPLETE` is pointedly absent: a create that rolled back never
 * reached `CREATE_COMPLETE`, so there is no prior state to update onto.
 */
const STABLE_STATUSES = new Set([
  "CREATE_COMPLETE",
  "UPDATE_COMPLETE",
  "UPDATE_ROLLBACK_COMPLETE",
  "IMPORT_COMPLETE",
  "IMPORT_ROLLBACK_COMPLETE",
])

/** Returns true if a stack can be updated (it has a last known stable state). */
export function canUpdateStack(status: string): boolean {
  return STABLE_STATUSES.has(status)
}

/** Returns true if the stack ended in a failure or rollback-complete state. */
export function isStackFailed(status: string): boolean {
  return status.endsWith("_FAILED") || (isRollbackStatus(status) && status.endsWith("_COMPLETE"))
}

/** Returns true if a stack can be deleted. */
export function canDeleteStack(status: string): boolean {
  return status !== "DELETE_COMPLETE" && !isStackInProgress(status)
}

/**
 * A plain-English gloss on a rollback status, for statuses whose name does not
 * say what it means for the user — chiefly that `ROLLBACK_COMPLETE` is
 * delete-only while `UPDATE_ROLLBACK_COMPLETE` is recoverable.
 *
 * Undefined for everything else, including the plain `*_FAILED` states: those
 * say what happened in their name, and `StackStatusReason` says why.
 */
export function stackStatusExplanation(status: string): string | undefined {
  switch (status) {
    case "ROLLBACK_IN_PROGRESS":
      return "Stack creation failed. CloudFormation is deleting everything it created."
    case "ROLLBACK_COMPLETE":
      return (
        "Stack creation failed and everything it created was rolled back. " +
        "The stack never reached Create Complete, so it has no previous state to update onto — " +
        "it can only be deleted, then created again once the template is fixed."
      )
    case "ROLLBACK_FAILED":
      return (
        "Stack creation failed and the rollback could not finish. " +
        "Some resources may still exist; deleting the stack clears them."
      )
    case "UPDATE_ROLLBACK_IN_PROGRESS":
      return "The update failed. CloudFormation is restoring the previous template."
    case "UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS":
      return "The update was rolled back. CloudFormation is removing what the failed update created."
    case "UPDATE_ROLLBACK_COMPLETE":
      return (
        "The update failed and was rolled back. " +
        "The stack is back on its previous template and can be updated again."
      )
    case "UPDATE_ROLLBACK_FAILED":
      return (
        "The update failed and the rollback could not finish. " +
        "The stack cannot be updated until the rollback completes."
      )
    case "IMPORT_ROLLBACK_IN_PROGRESS":
      return "The import failed. CloudFormation is restoring the previous template."
    case "IMPORT_ROLLBACK_COMPLETE":
      return (
        "The import failed and was rolled back. " +
        "The stack is back on its previous template and can be updated again."
      )
    case "IMPORT_ROLLBACK_FAILED":
      return "The import failed and the rollback could not finish."
    default:
      return undefined
  }
}

/** The resource a deployment failed on, and the reason it gave. */
export interface FailedResource {
  logicalId: string
  reason: string
}

/**
 * The fields of a stack resource {@link failedResource} reads.
 *
 * `LastUpdatedTimestamp` is optional here although `StackResourceSummary`
 * declares it required-but-nullable: the function orders by it when it is there
 * and falls back to list order when it is not, so a caller — or a test — with
 * nothing to stamp does not have to invent one.
 */
type FailedResourceCandidate = Pick<
  StackResourceSummary,
  "LogicalResourceId" | "ResourceStatus" | "ResourceStatusReason"
> &
  Partial<Pick<StackResourceSummary, "LastUpdatedTimestamp">>

/**
 * Finds the resource that actually failed.
 *
 * CloudFormation clears `StackStatusReason` once a rollback reaches its
 * terminal state, so a `ROLLBACK_COMPLETE` stack carries no stack-level reason
 * at all. The reason survives on the resource that failed and on the event
 * timeline, which is where the UI has to go to answer "why".
 *
 * That is the real AWS behaviour, not a shortcut, and it has been checked
 * twice — so it does not need checking a third time. AWS's own `describe-stacks`
 * example for a stack in `UPDATE_ROLLBACK_COMPLETE` carries no
 * `StackStatusReason` key at all, and the CLI prints the field as `null` rather
 * than omitting it when it exists, so the absence is the answer. The summary a
 * failed deploy is remembered for — "The following resource(s) failed to
 * create: [X]" — belongs to the `DisableRollback` `CREATE_FAILED` path and to
 * the events' `ResourceStatusReason`, never to a rolled-back stack.
 * See #823.
 *
 * The most recently stamped failure wins, because the question the banner
 * answers is why *this* deployment failed. A stack deployed more than once can
 * be holding a record from an earlier attempt — that is a server-side bug where
 * it happens, but the banner reads whatever the API returns and a stack
 * persisted before the fix still carries one, so scanning in list order sooner
 * or later reports a reason that has already been superseded.
 *
 * Ties go to the earlier entry: CloudFormation stops at the first failure on
 * each provisioning path, so among resources that failed in the same operation
 * the first in template order is the one that started it.
 */
export function failedResource(
  resources: readonly FailedResourceCandidate[],
): FailedResource | undefined {
  let found: FailedResource | undefined
  let foundAt = -Infinity
  for (const r of resources) {
    if (!r.ResourceStatusReason || !(r.ResourceStatus ?? "").endsWith("_FAILED")) continue
    // An absent timestamp cannot be ordered against anything, so it only wins
    // when nothing else has claimed the slot.
    const at = r.LastUpdatedTimestamp?.getTime() ?? -Infinity
    if (found && at <= foundAt) continue
    found = { logicalId: r.LogicalResourceId ?? "", reason: r.ResourceStatusReason }
    foundAt = at
  }
  return found
}
