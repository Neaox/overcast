import { awsClients } from "../aws-clients"
import {
  DescribeParametersCommand,
  GetParameterCommand,
  PutParameterCommand,
  DeleteParameterCommand,
  GetParameterHistoryCommand,
} from "@aws-sdk/client-ssm"

export type {
  ParameterMetadata as SsmParameter,
  Parameter as SsmParameterValue,
  ParameterHistory as SsmParameterHistoryEntry,
} from "@aws-sdk/client-ssm"

export const ssm = {
  listParameters: async () => {
    const res = await awsClients.ssm().send(new DescribeParametersCommand({}))
    return res.Parameters ?? []
  },

  getParameter: async (name: string) => {
    const res = await awsClients
      .ssm()
      .send(new GetParameterCommand({ Name: name, WithDecryption: true }))
    return res.Parameter ?? null
  },

  putParameter: async (name: string, value: string, type = "String") => {
    await awsClients.ssm().send(
      new PutParameterCommand({
        Name: name,
        Value: value,
        Type: type as "String" | "SecureString" | "StringList",
        Overwrite: true,
      }),
    )
  },

  deleteParameter: async (name: string) => {
    await awsClients.ssm().send(new DeleteParameterCommand({ Name: name }))
  },

  getParameterHistory: async (name: string) => {
    const res = await awsClients
      .ssm()
      .send(new GetParameterHistoryCommand({ Name: name, WithDecryption: true }))
    return res.Parameters ?? []
  },
}
