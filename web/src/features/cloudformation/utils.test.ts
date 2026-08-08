import type { BadgeProps } from "@/components/ui/badge"
import {
  canDeleteStack,
  canUpdateStack,
  failedResource,
  formatStatus,
  isRollbackStatus,
  isStackFailed,
  isStackInProgress,
  isStackRollingBack,
  resourceStatusVariant,
  stackStatusExplanation,
  stackStatusVariant,
} from "./utils"

/**
 * Every value of the CloudFormation `StackStatus` enum, verbatim from the API
 * reference, with the badge tone it must read as.
 *
 * https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_Stack.html
 *
 * The enum is spelled out rather than derived so that a status Overcast does
 * not yet emit — the IMPORT_* family — is still classified, and so that a test
 * covering only the states this change touched cannot pass by accident.
 */
const STACK_STATUSES: Record<string, BadgeProps["variant"]> = {
  CREATE_IN_PROGRESS: "warning",
  CREATE_FAILED: "danger",
  CREATE_COMPLETE: "success",
  ROLLBACK_IN_PROGRESS: "danger",
  ROLLBACK_FAILED: "danger",
  ROLLBACK_COMPLETE: "danger",
  DELETE_IN_PROGRESS: "warning",
  DELETE_FAILED: "danger",
  DELETE_COMPLETE: "success",
  UPDATE_IN_PROGRESS: "warning",
  UPDATE_COMPLETE_CLEANUP_IN_PROGRESS: "warning",
  UPDATE_COMPLETE: "success",
  UPDATE_FAILED: "danger",
  UPDATE_ROLLBACK_IN_PROGRESS: "danger",
  UPDATE_ROLLBACK_FAILED: "danger",
  UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS: "danger",
  UPDATE_ROLLBACK_COMPLETE: "danger",
  REVIEW_IN_PROGRESS: "info",
  IMPORT_IN_PROGRESS: "warning",
  IMPORT_COMPLETE: "success",
  IMPORT_ROLLBACK_IN_PROGRESS: "danger",
  IMPORT_ROLLBACK_FAILED: "danger",
  IMPORT_ROLLBACK_COMPLETE: "danger",
}

/**
 * Every value of the `ResourceStatus` enum, which is a different vocabulary
 * from `StackStatus`: it has DELETE_SKIPPED and the EXPORT_* family, and no
 * REVIEW_IN_PROGRESS or *_CLEANUP_IN_PROGRESS.
 *
 * https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_StackResource.html
 */
const RESOURCE_STATUSES: Record<string, BadgeProps["variant"]> = {
  CREATE_IN_PROGRESS: "warning",
  CREATE_FAILED: "danger",
  CREATE_COMPLETE: "success",
  DELETE_IN_PROGRESS: "warning",
  DELETE_FAILED: "danger",
  DELETE_COMPLETE: "success",
  // Not a failure: the resource was deliberately left behind, by a Retain
  // deletion policy or by DeleteStack's RetainResources. Neither green nor red.
  DELETE_SKIPPED: "default",
  UPDATE_IN_PROGRESS: "warning",
  UPDATE_FAILED: "danger",
  UPDATE_COMPLETE: "success",
  IMPORT_FAILED: "danger",
  IMPORT_COMPLETE: "success",
  IMPORT_IN_PROGRESS: "warning",
  IMPORT_ROLLBACK_IN_PROGRESS: "danger",
  IMPORT_ROLLBACK_FAILED: "danger",
  IMPORT_ROLLBACK_COMPLETE: "danger",
  EXPORT_FAILED: "danger",
  EXPORT_COMPLETE: "success",
  EXPORT_IN_PROGRESS: "warning",
  EXPORT_ROLLBACK_IN_PROGRESS: "danger",
  EXPORT_ROLLBACK_FAILED: "danger",
  EXPORT_ROLLBACK_COMPLETE: "danger",
  UPDATE_ROLLBACK_IN_PROGRESS: "danger",
  UPDATE_ROLLBACK_COMPLETE: "danger",
  UPDATE_ROLLBACK_FAILED: "danger",
  ROLLBACK_IN_PROGRESS: "danger",
  ROLLBACK_COMPLETE: "danger",
  ROLLBACK_FAILED: "danger",
}

/** The statuses AWS calls a "last known stable state" — the updatable set. */
const STABLE = [
  "CREATE_COMPLETE",
  "UPDATE_COMPLETE",
  "UPDATE_ROLLBACK_COMPLETE",
  "IMPORT_COMPLETE",
  "IMPORT_ROLLBACK_COMPLETE",
]

describe("stackStatusVariant", () => {
  it.each(Object.entries(STACK_STATUSES))("reads %s as %s", (status, expected) => {
    expect(stackStatusVariant(status)).toBe(expected)
  })

  // The bug this suite exists for. "ROLLBACK_COMPLETE".endsWith("_COMPLETE")
  // is true, so a suffix test that ran first painted a failed CDK deploy with
  // the same green badge as a successful one.
  it("never paints a rollback as a success", () => {
    for (const status of Object.keys(STACK_STATUSES)) {
      if (isRollbackStatus(status)) {
        expect(stackStatusVariant(status)).not.toBe("success")
      }
    }
  })

  // A rollback in progress is a deploy that has already failed and is being
  // undone. It must not look like a deploy that is still going well.
  it("distinguishes a rollback in progress from an ordinary in-progress update", () => {
    expect(stackStatusVariant("UPDATE_IN_PROGRESS")).toBe("warning")
    expect(stackStatusVariant("UPDATE_COMPLETE_CLEANUP_IN_PROGRESS")).toBe("warning")

    expect(stackStatusVariant("ROLLBACK_IN_PROGRESS")).toBe("danger")
    expect(stackStatusVariant("UPDATE_ROLLBACK_IN_PROGRESS")).toBe("danger")
    expect(stackStatusVariant("UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS")).toBe("danger")
  })

  // REVIEW_IN_PROGRESS ends in "_IN_PROGRESS", so it was the second arm the
  // suffix-first ordering made unreachable.
  it("keeps REVIEW_IN_PROGRESS informational rather than transitional", () => {
    expect(stackStatusVariant("REVIEW_IN_PROGRESS")).toBe("info")
  })

  it("falls back to default for an unknown status", () => {
    expect(stackStatusVariant("")).toBe("default")
    expect(stackStatusVariant("SOMETHING_ELSE")).toBe("default")
  })
})

describe("resourceStatusVariant", () => {
  it.each(Object.entries(RESOURCE_STATUSES))("reads %s as %s", (status, expected) => {
    expect(resourceStatusVariant(status)).toBe(expected)
  })

  // The delegation to stackStatusVariant is only sound because the two
  // vocabularies share one grammar. This pins the two statuses where they
  // could plausibly drift: the resource-only DELETE_SKIPPED, and the
  // EXPORT_* family that has no stack-status counterpart at all.
  it("classifies the resource-only statuses the stack enum never carries", () => {
    expect(resourceStatusVariant("DELETE_SKIPPED")).toBe("default")
    expect(resourceStatusVariant("EXPORT_COMPLETE")).toBe("success")
    expect(resourceStatusVariant("EXPORT_ROLLBACK_COMPLETE")).toBe("danger")
  })
})

describe("isRollbackStatus", () => {
  it("matches every rollback family, whichever operation unwound", () => {
    expect(isRollbackStatus("ROLLBACK_IN_PROGRESS")).toBe(true)
    expect(isRollbackStatus("ROLLBACK_COMPLETE")).toBe(true)
    expect(isRollbackStatus("ROLLBACK_FAILED")).toBe(true)
    expect(isRollbackStatus("UPDATE_ROLLBACK_COMPLETE")).toBe(true)
    expect(isRollbackStatus("UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS")).toBe(true)
    expect(isRollbackStatus("IMPORT_ROLLBACK_FAILED")).toBe(true)
    expect(isRollbackStatus("EXPORT_ROLLBACK_IN_PROGRESS")).toBe(true)
  })

  it("does not match the statuses that merely contain the word", () => {
    expect(isRollbackStatus("CREATE_COMPLETE")).toBe(false)
    expect(isRollbackStatus("UPDATE_COMPLETE_CLEANUP_IN_PROGRESS")).toBe(false)
    expect(isRollbackStatus("REVIEW_IN_PROGRESS")).toBe(false)
  })
})

describe("isStackFailed", () => {
  const failed = [
    "CREATE_FAILED",
    "DELETE_FAILED",
    "UPDATE_FAILED",
    "ROLLBACK_FAILED",
    "ROLLBACK_COMPLETE",
    "UPDATE_ROLLBACK_FAILED",
    "UPDATE_ROLLBACK_COMPLETE",
    "IMPORT_ROLLBACK_FAILED",
    "IMPORT_ROLLBACK_COMPLETE",
  ]

  it.each(Object.keys(STACK_STATUSES))("classifies %s", (status) => {
    expect(isStackFailed(status)).toBe(failed.includes(status))
  })

  // A stack that is still unwinding has not finished failing yet; the banner
  // uses isStackRollingBack for that half.
  it("does not count a rollback still in flight as finished", () => {
    expect(isStackFailed("ROLLBACK_IN_PROGRESS")).toBe(false)
    expect(isStackFailed("UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS")).toBe(false)
  })
})

describe("isStackRollingBack", () => {
  it.each(Object.keys(STACK_STATUSES))("classifies %s", (status) => {
    const expected = [
      "ROLLBACK_IN_PROGRESS",
      "UPDATE_ROLLBACK_IN_PROGRESS",
      "UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS",
      "IMPORT_ROLLBACK_IN_PROGRESS",
    ].includes(status)
    expect(isStackRollingBack(status)).toBe(expected)
  })
})

describe("canUpdateStack", () => {
  // AWS enumerates the "last known stable state" set on RollbackStack, and
  // ROLLBACK_COMPLETE is pointedly not in it.
  // https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_RollbackStack.html
  it.each(Object.keys(STACK_STATUSES))("classifies %s", (status) => {
    expect(canUpdateStack(status)).toBe(STABLE.includes(status))
  })

  it("refuses the delete-only state but allows the recoverable one", () => {
    expect(canUpdateStack("ROLLBACK_COMPLETE")).toBe(false)
    expect(canUpdateStack("UPDATE_ROLLBACK_COMPLETE")).toBe(true)
  })
})

describe("isStackInProgress", () => {
  it.each(Object.keys(STACK_STATUSES))("classifies %s", (status) => {
    const expected = status !== "REVIEW_IN_PROGRESS" && status.endsWith("_IN_PROGRESS")
    expect(isStackInProgress(status)).toBe(expected)
  })

  // Nothing is happening to a REVIEW_IN_PROGRESS stack: it exists only because
  // a change set was created against it and never executed. A spinner there
  // spins forever.
  it("does not treat REVIEW_IN_PROGRESS as transitional", () => {
    expect(isStackInProgress("REVIEW_IN_PROGRESS")).toBe(false)
  })
})

describe("canDeleteStack", () => {
  it.each(Object.keys(STACK_STATUSES))("classifies %s", (status) => {
    const expected = status !== "DELETE_COMPLETE" && !isStackInProgress(status)
    expect(canDeleteStack(status)).toBe(expected)
  })

  it("offers delete on the states whose only exit is deletion", () => {
    expect(canDeleteStack("ROLLBACK_COMPLETE")).toBe(true)
    expect(canDeleteStack("REVIEW_IN_PROGRESS")).toBe(true)
    expect(canDeleteStack("DELETE_COMPLETE")).toBe(false)
    expect(canDeleteStack("CREATE_IN_PROGRESS")).toBe(false)
  })
})

describe("stackStatusExplanation", () => {
  it("says a ROLLBACK_COMPLETE stack can only be deleted", () => {
    const text = stackStatusExplanation("ROLLBACK_COMPLETE")
    expect(text).toBeDefined()
    expect(text).toMatch(/only be deleted/i)
  })

  it("says an UPDATE_ROLLBACK_COMPLETE stack can be updated again", () => {
    const text = stackStatusExplanation("UPDATE_ROLLBACK_COMPLETE")
    expect(text).toBeDefined()
    expect(text).toMatch(/updated again/i)
    expect(text).not.toMatch(/only be deleted/i)
  })

  it("explains every rollback status, and leaves the healthy ones alone", () => {
    for (const status of Object.keys(STACK_STATUSES)) {
      if (isRollbackStatus(status)) {
        expect(stackStatusExplanation(status), status).toBeTruthy()
      }
    }
    expect(stackStatusExplanation("CREATE_COMPLETE")).toBeUndefined()
    expect(stackStatusExplanation("UPDATE_IN_PROGRESS")).toBeUndefined()
    // CREATE_FAILED needs no gloss: its name plus StackStatusReason says it.
    expect(stackStatusExplanation("CREATE_FAILED")).toBeUndefined()
  })
})

describe("failedResource", () => {
  it("finds the resource that actually failed", () => {
    const found = failedResource([
      { LogicalResourceId: "Bucket", ResourceStatus: "DELETE_COMPLETE" },
      {
        LogicalResourceId: "Queue",
        ResourceStatus: "CREATE_FAILED",
        ResourceStatusReason: "queue name already in use",
      },
    ])
    expect(found).toEqual({ logicalId: "Queue", reason: "queue name already in use" })
  })

  it("ignores a failed resource that carries no reason", () => {
    expect(failedResource([{ LogicalResourceId: "Queue", ResourceStatus: "CREATE_FAILED" }])).toBe(
      undefined,
    )
  })

  it("returns undefined when nothing failed", () => {
    expect(
      failedResource([{ LogicalResourceId: "Bucket", ResourceStatus: "CREATE_COMPLETE" }]),
    ).toBe(undefined)
  })
})

describe("formatStatus", () => {
  it("title-cases a CONSTANT_CASE status", () => {
    expect(formatStatus("UPDATE_ROLLBACK_COMPLETE")).toBe("Update Rollback Complete")
  })
})
