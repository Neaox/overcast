import { awsClients } from "../aws-clients"
import {
  ListSecretsCommand,
  CreateSecretCommand,
  DescribeSecretCommand,
  GetSecretValueCommand,
  PutSecretValueCommand,
  DeleteSecretCommand,
} from "@aws-sdk/client-secrets-manager"

export const secretsmanager = {
  listSecrets: async () => {
    const res = await awsClients.secretsmanager().send(new ListSecretsCommand({}))
    return res.SecretList ?? []
  },

  createSecret: async (params: { Name: string; SecretString?: string; Description?: string }) => {
    return await awsClients.secretsmanager().send(new CreateSecretCommand(params))
  },

  describeSecret: async (secretId: string) => {
    return await awsClients.secretsmanager().send(new DescribeSecretCommand({ SecretId: secretId }))
  },

  getSecretValue: async (secretId: string) => {
    const res = await awsClients
      .secretsmanager()
      .send(new GetSecretValueCommand({ SecretId: secretId }))
    return {
      ...res,
      SecretBinary: res.SecretBinary ? btoa(String.fromCharCode(...res.SecretBinary)) : undefined,
    }
  },

  updateSecretValue: async (secretId: string, secretString: string) => {
    return await awsClients
      .secretsmanager()
      .send(new PutSecretValueCommand({ SecretId: secretId, SecretString: secretString }))
  },

  deleteSecret: async (secretId: string) => {
    await awsClients
      .secretsmanager()
      .send(new DeleteSecretCommand({ SecretId: secretId, ForceDeleteWithoutRecovery: true }))
  },
}
