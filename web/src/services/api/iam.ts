import { awsClients } from "../aws-clients"
import {
  SimulateCustomPolicyCommand,
  SimulatePrincipalPolicyCommand,
  ListUsersCommand,
  CreateUserCommand,
  DeleteUserCommand,
  ListRolesCommand,
  CreateRoleCommand,
  DeleteRoleCommand,
  ListPoliciesCommand,
  CreatePolicyCommand,
  DeletePolicyCommand,
  ListGroupsCommand,
  CreateGroupCommand,
  DeleteGroupCommand,
} from "@aws-sdk/client-iam"

export type {
  User as IAMUser,
  Role as IAMRole,
  Policy as IAMPolicy,
  Group as IAMGroup,
  EvaluationResult as IAMEvaluationResult,
} from "@aws-sdk/client-iam"

/** One simulation request, as the simulator form collects it. */
export interface SimulationRequest {
  /** Principal ARN to simulate. Empty means "simulate the pasted policy alone". */
  policySourceArn?: string
  /** Policy documents to evaluate instead of, or alongside, the principal's. */
  policyInputList?: string[]
  actionNames: string[]
  resourceArns?: string[]
  /** Resource-based policy (e.g. an S3 bucket policy) to evaluate as well. */
  resourcePolicy?: string
  contextEntries?: { name: string; value: string }[]
}

export const iam = {
  listUsers: async () => {
    const res = await awsClients.iam().send(new ListUsersCommand({}))
    return res.Users ?? []
  },

  createUser: async (userName: string) => {
    await awsClients.iam().send(new CreateUserCommand({ UserName: userName }))
  },

  deleteUser: async (userName: string) => {
    await awsClients.iam().send(new DeleteUserCommand({ UserName: userName }))
  },

  listRoles: async () => {
    const res = await awsClients.iam().send(new ListRolesCommand({}))
    return res.Roles ?? []
  },

  createRole: async (roleName: string) => {
    await awsClients.iam().send(
      new CreateRoleCommand({
        RoleName: roleName,
        AssumeRolePolicyDocument: JSON.stringify({
          Version: "2012-10-17",
          Statement: [
            {
              Effect: "Allow",
              Principal: { Service: "lambda.amazonaws.com" },
              Action: "sts:AssumeRole",
            },
          ],
        }),
      }),
    )
  },

  deleteRole: async (roleName: string) => {
    await awsClients.iam().send(new DeleteRoleCommand({ RoleName: roleName }))
  },

  listPolicies: async () => {
    const res = await awsClients.iam().send(new ListPoliciesCommand({ Scope: "Local" }))
    return res.Policies ?? []
  },

  createPolicy: async (policyName: string) => {
    await awsClients.iam().send(
      new CreatePolicyCommand({
        PolicyName: policyName,
        PolicyDocument: JSON.stringify({
          Version: "2012-10-17",
          Statement: [{ Effect: "Allow", Action: "*", Resource: "*" }],
        }),
      }),
    )
  },

  deletePolicy: async (policyArn: string) => {
    await awsClients.iam().send(new DeletePolicyCommand({ PolicyArn: policyArn }))
  },

  listGroups: async () => {
    const res = await awsClients.iam().send(new ListGroupsCommand({}))
    return res.Groups ?? []
  },

  createGroup: async (groupName: string) => {
    await awsClients.iam().send(new CreateGroupCommand({ GroupName: groupName }))
  },

  deleteGroup: async (groupName: string) => {
    await awsClients.iam().send(new DeleteGroupCommand({ GroupName: groupName }))
  },

  /**
   * Runs a policy simulation. SimulatePrincipalPolicy is used when a principal
   * ARN is given (the principal's own attached, inline and group policies are
   * evaluated); SimulateCustomPolicy when only pasted documents are.
   */
  simulate: async (req: SimulationRequest) => {
    const shared = {
      ActionNames: req.actionNames,
      ResourceArns: req.resourceArns?.length ? req.resourceArns : undefined,
      ResourcePolicy: req.resourcePolicy || undefined,
      ContextEntries: req.contextEntries?.length
        ? req.contextEntries.map((entry) => ({
            ContextKeyName: entry.name,
            ContextKeyValues: [entry.value],
            ContextKeyType: "string" as const,
          }))
        : undefined,
    }

    if (req.policySourceArn) {
      const res = await awsClients.iam().send(
        new SimulatePrincipalPolicyCommand({
          PolicySourceArn: req.policySourceArn,
          PolicyInputList: req.policyInputList?.length ? req.policyInputList : undefined,
          ...shared,
        }),
      )
      return res.EvaluationResults ?? []
    }

    const res = await awsClients.iam().send(
      new SimulateCustomPolicyCommand({
        PolicyInputList: req.policyInputList ?? [],
        ...shared,
      }),
    )
    return res.EvaluationResults ?? []
  },
}
