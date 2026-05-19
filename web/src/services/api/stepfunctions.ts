import { awsClients } from "../aws-clients"
import {
  ListStateMachinesCommand,
  CreateStateMachineCommand,
  DeleteStateMachineCommand,
} from "@aws-sdk/client-sfn"

export type { StateMachineListItem as StateMachine } from "@aws-sdk/client-sfn"

export const stepfunctions = {
  listStateMachines: async () => {
    const res = await awsClients.sfn().send(new ListStateMachinesCommand({}))
    return res.stateMachines ?? []
  },

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
}
