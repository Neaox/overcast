import type { EcsContainer, EcsTask } from "@/types"

export type TaskView = "auto" | "running" | "stopped"

export const RECENT_FAILURE_WINDOW_MS = 15 * 60 * 1000
export const STOPPED_TASK_DISPLAY_LIMIT = 50

export interface TaskViewSelection {
  tasks: EcsTask[]
  effectiveView: Exclude<TaskView, "auto">
  isStoppedFallback: boolean
  activeCount: number
  stoppedCount: number
  isTruncated: boolean
}

export function selectTasksForView(
  tasks: EcsTask[],
  view: TaskView,
  serviceName?: string,
): TaskViewSelection {
  const serviceTasks = serviceName
    ? tasks.filter((task) => task.group === `service:${serviceName}`)
    : tasks
  const running = serviceTasks.filter((task) => task.lastStatus !== "STOPPED")
  const allStopped = serviceTasks
    .filter((task) => task.lastStatus === "STOPPED")
    .toSorted((left, right) => taskTime(right) - taskTime(left))
  const stopped = allStopped.slice(0, STOPPED_TASK_DISPLAY_LIMIT)
  const hasRunningTask = running.some((task) => task.lastStatus === "RUNNING")
  const effectiveView =
    view === "auto" ? (hasRunningTask || stopped.length === 0 ? "running" : "stopped") : view

  return {
    tasks: effectiveView === "running" ? running : stopped,
    effectiveView,
    isStoppedFallback: view === "auto" && !hasRunningTask && stopped.length > 0,
    activeCount: running.length,
    stoppedCount: allStopped.length,
    isTruncated: effectiveView === "stopped" && allStopped.length > stopped.length,
  }
}

export function findRecentServiceFailures(
  serviceName: string,
  tasks: EcsTask[],
  now = Date.now(),
  windowMs = RECENT_FAILURE_WINDOW_MS,
): EcsTask[] {
  return tasks
    .filter(
      (task) =>
        task.group === `service:${serviceName}` &&
        task.lastStatus === "STOPPED" &&
        isFailedTask(task) &&
        isWithinWindow(task, now, windowMs),
    )
    .toSorted((left, right) => taskTime(right) - taskTime(left))
}

export function isFailedTask(task: EcsTask): boolean {
  if (task.containers.some((container) => (container.exitCode ?? 0) !== 0 || container.reason)) {
    return true
  }
  return task.stopCode === "TaskFailedToStart" || task.stopCode === "EssentialContainerExited"
}

export function failedContainer(task: EcsTask): EcsContainer | undefined {
  return (
    task.containers.find((container) => (container.exitCode ?? 0) !== 0) ??
    task.containers.find((container) => container.reason) ??
    task.containers.at(0)
  )
}

export function diagnosticLogExcerpt(logs: string): string {
  const usefulLines = logs
    .split(/\r?\n/)
    .map((line) => line.trimEnd())
    .filter((line) => line.trim().length > 0)
    .slice(-2)
  const excerpt = usefulLines.join("\n")
  return excerpt.length > 600 ? `${excerpt.slice(-597)}...` : excerpt
}

export function isServiceFailureEvent(message: string): boolean {
  return (
    message.includes("unable to place") ||
    message.includes("unable to consistently start") ||
    message.includes("deployment failed")
  )
}

export function isSecondaryServiceFailureEvent(message: string): boolean {
  return message.includes("marked for removal")
}

function isWithinWindow(task: EcsTask, now: number, windowMs: number): boolean {
  const stoppedAt = taskTime(task)
  return stoppedAt > 0 && stoppedAt <= now && stoppedAt >= now - windowMs
}

function taskTime(task: EcsTask): number {
  const value = task.stoppedAt ?? task.createdAt
  const parsed = value ? Date.parse(value) : 0
  return Number.isNaN(parsed) ? 0 : parsed
}
