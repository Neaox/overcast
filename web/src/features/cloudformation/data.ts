/**
 * CloudFormation query/mutation definitions.
 *
 * Key factory:
 *   cfnKeys.all                                  -> ["cloudformation"]
 *   cfnKeys.stacks()                             -> ["cloudformation", "stacks"]
 *   cfnKeys.stackList(baseUrl)                   -> ["cloudformation", "stacks", baseUrl]
 *   cfnKeys.stack()                              -> ["cloudformation", "stack"]
 *   cfnKeys.stackDetail(baseUrl, name)           -> ["cloudformation", "stack", baseUrl, name]
 *   cfnKeys.resources()                          -> ["cloudformation", "resources"]
 *   cfnKeys.resourceList(baseUrl, name)          -> ["cloudformation", "resources", baseUrl, name]
 *   cfnKeys.events()                             -> ["cloudformation", "events"]
 *   cfnKeys.eventList(baseUrl, name)             -> ["cloudformation", "events", baseUrl, name]
 *   cfnKeys.template()                           -> ["cloudformation", "template"]
 *   cfnKeys.templateDetail(baseUrl, name)        -> ["cloudformation", "template", baseUrl, name]
 */

import { queryOptions, infiniteQueryOptions, mutationOptions } from "@tanstack/react-query"
import { cloudformation } from "@/services/api"
import type {
  CreateStackCommandInput,
  UpdateStackCommandInput,
} from "@aws-sdk/client-cloudformation"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const cfnKeys = {
  all: () => [...endpointStore.getKeys(), "cloudformation"] as const,
  stacks: () => [...cfnKeys.all(), "stacks"] as const,
  stack: () => [...cfnKeys.all(), "stack"] as const,
  stackDetail: (name: string) => [...cfnKeys.stack(), name] as const,
  resources: () => [...cfnKeys.all(), "resources"] as const,
  resourceList: (name: string) => [...cfnKeys.resources(), name] as const,
  events: () => [...cfnKeys.all(), "events"] as const,
  eventList: (name: string) => [...cfnKeys.events(), name] as const,
  template: () => [...cfnKeys.all(), "template"] as const,
  templateDetail: (name: string) => [...cfnKeys.template(), name] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function cfnStacksQueryOptions() {
  return queryOptions({
    queryKey: cfnKeys.stacks(),
    queryFn: () => cloudformation.listStacks(),
  })
}

export function cfnStackQueryOptions(name: string) {
  return queryOptions({
    queryKey: cfnKeys.stackDetail(name),
    queryFn: () => cloudformation.describeStack(name),
  })
}

export function cfnResourcesQueryOptions(name: string) {
  return queryOptions({
    queryKey: cfnKeys.resourceList(name),
    queryFn: () => cloudformation.listStackResources(name),
  })
}

export function cfnEventsInfiniteQueryOptions(name: string) {
  return infiniteQueryOptions({
    queryKey: cfnKeys.eventList(name),
    queryFn: ({ pageParam }) => cloudformation.describeStackEvents(name, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextToken ?? undefined,
  })
}

export function cfnTemplateQueryOptions(name: string) {
  return queryOptions({
    queryKey: cfnKeys.templateDetail(name),
    queryFn: () => cloudformation.getTemplate(name),
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createStackMutationOptions() {
  return mutationOptions({
    mutationKey: [...cfnKeys.stacks(), "create"] as const,
    mutationFn: (
      opts: Pick<
        CreateStackCommandInput,
        "StackName" | "TemplateBody" | "Parameters" | "Tags" | "Capabilities"
      >,
    ) => cloudformation.createStack(opts),
  })
}

export function updateStackMutationOptions() {
  return mutationOptions({
    mutationKey: [...cfnKeys.stacks(), "update"] as const,
    mutationFn: (
      opts: Pick<
        UpdateStackCommandInput,
        "StackName" | "TemplateBody" | "Parameters" | "Tags" | "Capabilities"
      >,
    ) => cloudformation.updateStack(opts),
  })
}

export function deleteStackMutationOptions() {
  return mutationOptions({
    mutationKey: [...cfnKeys.stacks(), "delete"] as const,
    mutationFn: (name: string) => cloudformation.deleteStack(name),
  })
}
