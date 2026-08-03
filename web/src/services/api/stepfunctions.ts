import { awsClients } from "../aws-clients"
import {
  ListStateMachinesCommand,
  CreateStateMachineCommand,
  DeleteStateMachineCommand,
  DescribeStateMachineCommand,
  ListExecutionsCommand,
  DescribeExecutionCommand,
  GetExecutionHistoryCommand,
  StartExecutionCommand,
} from "@aws-sdk/client-sfn"

export type { StateMachineListItem as StateMachine } from "@aws-sdk/client-sfn"
export type {
  ExecutionListItem,
  DescribeExecutionCommandOutput,
  DescribeStateMachineCommandOutput,
  HistoryEvent,
} from "@aws-sdk/client-sfn"

export const stepfunctions = {
  listStateMachines: async () => {
    const res = await awsClients.sfn().send(new ListStateMachinesCommand({}))
    return res.stateMachines ?? []
  },

  describeStateMachine: async (arn: string) =>
    awsClients.sfn().send(new DescribeStateMachineCommand({ stateMachineArn: arn })),

  createStateMachine: async (name: string) => {
    await awsClients.sfn().send(
      new CreateStateMachineCommand({
        name,
        definition: JSON.stringify({
          Comment: `State machine ${name}`,
          StartAt: "Pass",
          States: { Pass: { Type: "Pass", End: true } },
        }),
        roleArn: "arn:aws:iam::000000000000:role/StepFunctionsRole",
      }),
    )
  },

  deleteStateMachine: async (arn: string) => {
    await awsClients.sfn().send(new DeleteStateMachineCommand({ stateMachineArn: arn }))
  },

  listExecutions: async (stateMachineArn: string) => {
    const res = await awsClients.sfn().send(new ListExecutionsCommand({ stateMachineArn }))
    return res.executions ?? []
  },

  describeExecution: async (executionArn: string) =>
    awsClients.sfn().send(new DescribeExecutionCommand({ executionArn })),

  getExecutionHistory: async (executionArn: string) => {
    const res = await awsClients
      .sfn()
      .send(new GetExecutionHistoryCommand({ executionArn, includeExecutionData: true }))
    return res.events ?? []
  },

  startExecution: async (stateMachineArn: string, input: string) =>
    awsClients.sfn().send(new StartExecutionCommand({ stateMachineArn, input: input || "{}" })),
}
