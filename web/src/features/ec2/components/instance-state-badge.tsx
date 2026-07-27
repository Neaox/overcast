import { Badge } from "@/components/ui/badge"

/**
 * Shared so the list and detail views cannot disagree about what a state
 * means. They previously each defined this, and had drifted into inverting
 * each other: one showed `stopped` as danger and `terminated` as neutral, the
 * other the reverse, so the same instance changed colour depending on which
 * page you were looking at.
 *
 * `terminated` is the destructive, irreversible one, so it takes danger.
 * `stopped` is an ordinary resting state and stays neutral.
 */
const VARIANT_BY_STATE: Record<string, "success" | "warning" | "danger" | "default"> = {
  running: "success",
  pending: "warning",
  stopping: "warning",
  "shutting-down": "warning",
  terminated: "danger",
  stopped: "default",
}

export function InstanceStateBadge({ state }: { state: string }) {
  return <Badge variant={VARIANT_BY_STATE[state] ?? "default"}>{state}</Badge>
}
