import { describe, expect, it } from "vitest"
import type { EcsTask } from "@/types"
import {
  diagnosticLogExcerpt,
  failedContainer,
  findRecentServiceFailures,
  selectTasksForView,
} from "./diagnostics"

const runningTask = task({ id: "running", lastStatus: "RUNNING" })
const failedTask = task({
  id: "failed",
  lastStatus: "STOPPED",
  stoppedAt: "2026-08-05T05:58:00.000Z",
  group: "service:website",
  exitCode: 2,
})

describe("selectTasksForView", () => {
  it("shows recently stopped tasks automatically when nothing is running", () => {
    // Given: a cluster with only stopped tasks.
    const stopped = [failedTask]

    // When: the default automatic task view is selected.
    const result = selectTasksForView(stopped, "auto")

    // Then: stopped tasks are visible with an explicit fallback signal.
    expect(result.tasks).toEqual(stopped)
    expect(result.effectiveView).toBe("stopped")
    expect(result.isStoppedFallback).toBe(true)
  })

  it("prefers stopped failures while a replacement is only pending", () => {
    // Given: a crash loop with one replacement pending and a failed predecessor.
    const pending = task({ id: "pending", lastStatus: "PENDING" })

    // When: the automatic view is selected.
    const result = selectTasksForView([pending, failedTask], "auto")

    // Then: the completed failure remains the default diagnostic evidence.
    expect(result.effectiveView).toBe("stopped")
    expect(result.tasks).toEqual([failedTask])
  })

  it("keeps a service failure filter when opening stopped tasks", () => {
    // Given: failures from two different services and one running task.
    const tasks = [
      failedTask,
      task({
        id: "worker-failure",
        lastStatus: "STOPPED",
        stoppedAt: "2026-08-05T05:57:00.000Z",
        group: "service:worker",
        exitCode: 1,
      }),
      runningTask,
    ]

    // When: stopped tasks for website are requested.
    const result = selectTasksForView(tasks, "stopped", "website")

    // Then: unrelated scheduler noise is not mixed into the result.
    expect(result.tasks.map((item) => item.taskArn)).toEqual([failedTask.taskArn])
  })

  it("bounds large stopped-task histories to the newest fifty", () => {
    // Given: a noisy crash loop with more stopped tasks than the table should render.
    const tasks = Array.from({ length: 55 }, (_, index) =>
      task({
        id: `failure-${index}`,
        lastStatus: "STOPPED",
        stoppedAt: new Date(Date.parse("2026-08-05T05:00:00.000Z") + index * 1_000).toISOString(),
        exitCode: 1,
      }),
    )

    // When: stopped tasks are selected.
    const result = selectTasksForView(tasks, "stopped")

    // Then: the total remains visible but only the newest fifty are rendered.
    expect(result.stoppedCount).toBe(55)
    expect(result.tasks).toHaveLength(50)
    expect(result.tasks.at(0)?.taskArn).toContain("failure-54")
    expect(result.isTruncated).toBe(true)
  })
})

describe("findRecentServiceFailures", () => {
  it("aggregates recent non-zero exits and orders the newest first", () => {
    // Given: two recent failed tasks plus an old failure and a clean stop.
    const tasks = [
      failedTask,
      task({
        id: "newest",
        lastStatus: "STOPPED",
        stoppedAt: "2026-08-05T05:59:00.000Z",
        group: "service:website",
        exitCode: 127,
      }),
      task({
        id: "old",
        lastStatus: "STOPPED",
        stoppedAt: "2026-08-04T03:00:00.000Z",
        group: "service:website",
        exitCode: 1,
      }),
      task({
        id: "clean",
        lastStatus: "STOPPED",
        stoppedAt: "2026-08-05T05:59:30.000Z",
        group: "service:website",
        exitCode: 0,
      }),
    ]

    // When: failures from the last fifteen minutes are selected.
    const failures = findRecentServiceFailures(
      "website",
      tasks,
      Date.parse("2026-08-05T06:00:00.000Z"),
    )

    // Then: only actionable recent failures remain, newest first.
    expect(failures.map((item) => item.taskArn.split("/").at(-1))).toEqual(["newest", "failed"])
  })

  it("correlates diagnostics to the failed task instead of a later clean essential exit", () => {
    // Given: an earlier entrypoint failure followed by a newer task that exited cleanly.
    const tasks = [
      failedTask,
      task({
        id: "clean-replacement",
        lastStatus: "STOPPED",
        stoppedAt: "2026-08-05T05:59:30.000Z",
        group: "service:website",
        exitCode: 0,
        stopCode: "EssentialContainerExited",
        stoppedReason: "Essential container in task exited",
      }),
    ]

    // When: the service's recent failures are selected.
    const failures = findRecentServiceFailures(
      "website",
      tasks,
      Date.parse("2026-08-05T06:00:00.000Z"),
    )

    // Then: the clean replacement cannot steal the failed task's diagnostic banner or logs.
    expect(failures.map((item) => item.taskArn.split("/").at(-1))).toEqual(["failed"])
  })

  it("keeps a failure reason from a second container when another exited cleanly", () => {
    // Given: one clean sidecar and one container that failed before recording an exit code.
    const mixedFailure: EcsTask = {
      ...failedTask,
      taskArn: failedTask.taskArn.replace("failed", "mixed-failure"),
      containers: [
        { name: "sidecar", lastStatus: "STOPPED", exitCode: 0 },
        { name: "app", lastStatus: "STOPPED", reason: "CannotPullContainerError" },
      ],
    }

    // When: recent service failures are selected.
    const failures = findRecentServiceFailures(
      "website",
      [mixedFailure],
      Date.parse("2026-08-05T06:00:00.000Z"),
    )

    // Then: clean evidence from one container cannot hide another container's failure.
    expect(failures).toEqual([mixedFailure])
  })

  it("keeps a task startup failure when only a clean sidecar has an exit code", () => {
    // Given: a sidecar exited cleanly while the main container failed before it could start.
    const startupFailure: EcsTask = {
      ...failedTask,
      taskArn: failedTask.taskArn.replace("failed", "startup-failure"),
      stopCode: "TaskFailedToStart",
      stoppedReason: "ResourceInitializationError",
      containers: [
        { name: "sidecar", lastStatus: "STOPPED", exitCode: 0 },
        { name: "app", lastStatus: "STOPPED" },
      ],
    }

    // When: the failure and its diagnostic container are selected.
    const failures = findRecentServiceFailures(
      "website",
      [startupFailure],
      Date.parse("2026-08-05T06:00:00.000Z"),
    )

    // Then: a clean sidecar cannot suppress the startup failure or become its likely cause.
    expect(failures).toEqual([startupFailure])
    expect(failedContainer(startupFailure)?.name).toBe("app")
  })
})

describe("diagnosticLogExcerpt", () => {
  it("prefers the final useful lines where entrypoint errors are normally reported", () => {
    // Given: noisy startup output followed by the actual syntax error.
    const logs = [
      "starting wordpress",
      "loading configuration",
      "entrypoint.sh: line 18: syntax error near unexpected token `fi'",
    ].join("\n")

    // When: the compact service diagnostic excerpt is built.
    const excerpt = diagnosticLogExcerpt(logs)

    // Then: the root cause is present without the full log history.
    expect(excerpt).toContain("syntax error near unexpected token")
    expect(excerpt).not.toContain("starting wordpress")
  })
})

function task({
  id,
  lastStatus,
  stoppedAt,
  group,
  exitCode,
  stopCode,
  stoppedReason,
}: {
  id: string
  lastStatus: string
  stoppedAt?: string
  group?: string
  exitCode?: number
  stopCode?: string
  stoppedReason?: string
}): EcsTask {
  return {
    taskArn: `arn:aws:ecs:us-east-1:123456789012:task/demo/${id}`,
    taskDefinitionArn: "arn:aws:ecs:us-east-1:123456789012:task-definition/app:1",
    clusterArn: "arn:aws:ecs:us-east-1:123456789012:cluster/demo",
    lastStatus,
    desiredStatus: lastStatus,
    stoppedAt,
    stopCode,
    stoppedReason,
    group,
    containers: [{ name: "app", lastStatus, exitCode }],
  }
}
