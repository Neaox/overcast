import { awsClients } from "../aws-clients"
import {
  ListPipesCommand,
  CreatePipeCommand,
  DescribePipeCommand,
  DeletePipeCommand,
} from "@aws-sdk/client-pipes"

export const pipes = {
  listPipes: async () => {
    const res = await awsClients.pipes().send(new ListPipesCommand({}))
    return res.Pipes ?? []
  },

  createPipe: async (name: string, sourceArn: string, targetArn: string) => {
    return await awsClients.pipes().send(
      new CreatePipeCommand({
        Name: name,
        Source: sourceArn,
        Target: targetArn,
        RoleArn: "arn:aws:iam::000000000000:role/overcast-stub",
      }),
    )
  },

  describePipe: async (name: string) => {
    return await awsClients.pipes().send(new DescribePipeCommand({ Name: name }))
  },

  deletePipe: async (name: string) => {
    await awsClients.pipes().send(new DeletePipeCommand({ Name: name }))
  },
}
