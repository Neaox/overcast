import { awsClients } from "../aws-clients"
import {
  DescribeStacksCommand,
  ListStacksCommand,
  CreateStackCommand,
  UpdateStackCommand,
  DeleteStackCommand,
  GetTemplateCommand,
  ListStackResourcesCommand,
  DescribeStackEventsCommand,
  ValidateTemplateCommand,
  type CreateStackCommandInput,
  type UpdateStackCommandInput,
  type StackResourceSummary,
  type StackEvent,
} from "@aws-sdk/client-cloudformation"

export const cloudformation = {
  listStacks: async () => {
    const res = await awsClients.cloudformation().send(new ListStacksCommand({}))
    return res.StackSummaries ?? []
  },

  describeStack: async (stackName: string) => {
    const res = await awsClients
      .cloudformation()
      .send(new DescribeStacksCommand({ StackName: stackName }))
    const stack = res.Stacks?.[0]
    if (!stack) throw new Error(`Stack ${stackName} not found`)
    return stack
  },

  createStack: async (
    opts: Pick<
      CreateStackCommandInput,
      "StackName" | "TemplateBody" | "Parameters" | "Tags" | "Capabilities"
    >,
  ) => {
    const res = await awsClients.cloudformation().send(new CreateStackCommand(opts))
    return res.StackId ?? ""
  },

  updateStack: async (
    opts: Pick<
      UpdateStackCommandInput,
      "StackName" | "TemplateBody" | "Parameters" | "Tags" | "Capabilities"
    >,
  ) => {
    const res = await awsClients.cloudformation().send(new UpdateStackCommand(opts))
    return res.StackId ?? ""
  },

  deleteStack: async (stackName: string) => {
    await awsClients.cloudformation().send(new DeleteStackCommand({ StackName: stackName }))
  },

  getTemplate: async (stackName: string) => {
    const res = await awsClients
      .cloudformation()
      .send(new GetTemplateCommand({ StackName: stackName }))
    return res.TemplateBody ?? ""
  },

  listStackResources: async (stackName: string) => {
    const client = awsClients.cloudformation()
    const all: StackResourceSummary[] = []
    let nextToken: string | undefined
    do {
      const res = await client.send(
        new ListStackResourcesCommand({ StackName: stackName, NextToken: nextToken }),
      )
      all.push(...(res.StackResourceSummaries ?? []))
      nextToken = res.NextToken
    } while (nextToken)
    return all
  },

  describeStackEvents: async (
    stackName: string,
    nextToken?: string,
  ): Promise<{ events: StackEvent[]; nextToken: string | undefined }> => {
    const client = awsClients.cloudformation()
    const res = await client.send(
      new DescribeStackEventsCommand({ StackName: stackName, NextToken: nextToken }),
    )
    return {
      events: res.StackEvents ?? [],
      nextToken: res.NextToken,
    }
  },

  validateTemplate: async (templateBody: string) => {
    await awsClients
      .cloudformation()
      .send(new ValidateTemplateCommand({ TemplateBody: templateBody }))
  },
}
