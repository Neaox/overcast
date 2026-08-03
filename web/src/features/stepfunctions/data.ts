import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { stepfunctions } from "@/services/api/stepfunctions"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const sfnKeys = {
  all: () => [...endpointStore.getKeys(), "stepfunctions"] as const,
  stateMachines: () => [...sfnKeys.all(), "stateMachines"] as const,
  stateMachine: (arn: string) => [...sfnKeys.stateMachines(), arn] as const,
  executions: (stateMachineArn: string) =>
    [...sfnKeys.all(), "executions", stateMachineArn] as const,
  execution: (executionArn: string) => [...sfnKeys.all(), "execution", executionArn] as const,
  executionHistory: (executionArn: string) =>
    [...sfnKeys.execution(executionArn), "history"] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function sfnStateMachinesQueryOptions() {
  return queryOptions({
    queryKey: sfnKeys.stateMachines(),
    queryFn: () => stepfunctions.listStateMachines(),
  })
}

export function sfnStateMachineQueryOptions(arn: string) {
  return queryOptions({
    queryKey: sfnKeys.stateMachine(arn),
    queryFn: () => stepfunctions.describeStateMachine(arn),
    enabled: arn !== "",
  })
}

export function sfnExecutionsQueryOptions(stateMachineArn: string) {
  return queryOptions({
    queryKey: sfnKeys.executions(stateMachineArn),
    queryFn: () => stepfunctions.listExecutions(stateMachineArn),
    enabled: stateMachineArn !== "",
  })
}

export function sfnExecutionQueryOptions(executionArn: string) {
  return queryOptions({
    queryKey: sfnKeys.execution(executionArn),
    queryFn: () => stepfunctions.describeExecution(executionArn),
    enabled: executionArn !== "",
  })
}

export function sfnExecutionHistoryQueryOptions(executionArn: string) {
  return queryOptions({
    queryKey: sfnKeys.executionHistory(executionArn),
    queryFn: () => stepfunctions.getExecutionHistory(executionArn),
    enabled: executionArn !== "",
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createStateMachineMutationOptions() {
  return mutationOptions({
    mutationKey: [...sfnKeys.stateMachines(), "create"] as const,
    mutationFn: (name: string) => stepfunctions.createStateMachine(name),
  })
}

export function deleteStateMachineMutationOptions() {
  return mutationOptions({
    mutationKey: [...sfnKeys.stateMachines(), "delete"] as const,
    mutationFn: (arn: string) => stepfunctions.deleteStateMachine(arn),
  })
}

export function startExecutionMutationOptions() {
  return mutationOptions({
    mutationKey: [...sfnKeys.all(), "startExecution"] as const,
    mutationFn: ({ stateMachineArn, input }: { stateMachineArn: string; input: string }) =>
      stepfunctions.startExecution(stateMachineArn, input),
  })
}
