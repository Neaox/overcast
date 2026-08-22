import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { iam } from "@/services/api/iam"
import type { SimulationRequest } from "@/services/api/iam"
import { endpointStore } from "@/services/endpoint-store"

// ─── Tabs ───────────────────────────────────────────────────────────────────

/**
 * The IAM tabs that carry a resource list — `"simulator"` has no filter/noun
 * of its own. Lives here rather than in `iam-page.tsx` because that file's
 * exports must stay component-only for Fast Refresh; the route file needs
 * this to validate the `tab` search param.
 */
export const IAM_TABS = ["users", "roles", "policies", "groups", "simulator"] as const
export type IamTab = (typeof IAM_TABS)[number]

// ─── Key factory ───────────────────────────────────────────────────────────

export const iamKeys = {
  all: () => [...endpointStore.getKeys(), "iam"] as const,
  users: () => [...iamKeys.all(), "users"] as const,
  roles: () => [...iamKeys.all(), "roles"] as const,
  policies: () => [...iamKeys.all(), "policies"] as const,
  groups: () => [...iamKeys.all(), "groups"] as const,
  groupMembers: (groupName: string) => [...iamKeys.groups(), groupName, "members"] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function iamUsersQueryOptions() {
  return queryOptions({ queryKey: iamKeys.users(), queryFn: () => iam.listUsers() })
}

export function iamRolesQueryOptions() {
  return queryOptions({ queryKey: iamKeys.roles(), queryFn: () => iam.listRoles() })
}

export function iamPoliciesQueryOptions() {
  return queryOptions({ queryKey: iamKeys.policies(), queryFn: () => iam.listPolicies() })
}

export function iamGroupsQueryOptions() {
  return queryOptions({ queryKey: iamKeys.groups(), queryFn: () => iam.listGroups() })
}

/** Members of one group. Fetched lazily, when the group's row is expanded. */
export function iamGroupMembersQueryOptions(groupName: string) {
  return queryOptions({
    queryKey: iamKeys.groupMembers(groupName),
    queryFn: () => iam.listGroupMembers(groupName),
    enabled: !!groupName,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createUserMutationOptions() {
  return mutationOptions({
    mutationKey: [...iamKeys.users(), "create"] as const,
    mutationFn: (userName: string) => iam.createUser(userName),
  })
}

export function deleteUserMutationOptions() {
  return mutationOptions({
    mutationKey: [...iamKeys.users(), "delete"] as const,
    mutationFn: (userName: string) => iam.deleteUser(userName),
  })
}

export function createRoleMutationOptions() {
  return mutationOptions({
    mutationKey: [...iamKeys.roles(), "create"] as const,
    mutationFn: (roleName: string) => iam.createRole(roleName),
  })
}

export function deleteRoleMutationOptions() {
  return mutationOptions({
    mutationKey: [...iamKeys.roles(), "delete"] as const,
    mutationFn: (roleName: string) => iam.deleteRole(roleName),
  })
}

export function createPolicyMutationOptions() {
  return mutationOptions({
    mutationKey: [...iamKeys.policies(), "create"] as const,
    mutationFn: (policyName: string) => iam.createPolicy(policyName),
  })
}

export function deletePolicyMutationOptions() {
  return mutationOptions({
    mutationKey: [...iamKeys.policies(), "delete"] as const,
    mutationFn: (policyArn: string) => iam.deletePolicy(policyArn),
  })
}

export function createGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...iamKeys.groups(), "create"] as const,
    mutationFn: (groupName: string) => iam.createGroup(groupName),
  })
}

export function simulatePolicyMutationOptions() {
  return mutationOptions({
    mutationKey: [...iamKeys.all(), "simulate"] as const,
    mutationFn: (req: SimulationRequest) => iam.simulate(req),
  })
}

export function deleteGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...iamKeys.groups(), "delete"] as const,
    mutationFn: (groupName: string) => iam.deleteGroup(groupName),
  })
}
