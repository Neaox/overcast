import { awsClients } from "../aws-clients"
import {
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
} from "@aws-sdk/client-iam"

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
}
