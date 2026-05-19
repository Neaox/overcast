import { awsClients } from "../aws-clients"
import {
  ListKeysCommand,
  DescribeKeyCommand,
  CreateKeyCommand,
  DisableKeyCommand,
  EnableKeyCommand,
  ScheduleKeyDeletionCommand,
  ListAliasesCommand,
} from "@aws-sdk/client-kms"

export type {
  KeyListEntry as KmsKeySummary,
  KeyMetadata as KmsKeyDetail,
} from "@aws-sdk/client-kms"

export const kms = {
  listKeys: async () => {
    const res = await awsClients.kms().send(new ListKeysCommand({}))
    return res.Keys ?? []
  },

  describeKey: async (keyId: string) => {
    const client = awsClients.kms()
    const [keyRes, aliasRes] = await Promise.all([
      client.send(new DescribeKeyCommand({ KeyId: keyId })),
      client.send(new ListAliasesCommand({ KeyId: keyId })),
    ])
    return {
      metadata: keyRes.KeyMetadata ?? null,
      aliases: (aliasRes.Aliases ?? []).map((a) => a.AliasName ?? ""),
    }
  },

  createKey: async (description?: string) => {
    await awsClients.kms().send(new CreateKeyCommand({ Description: description }))
  },

  enableKey: async (keyId: string) => {
    await awsClients.kms().send(new EnableKeyCommand({ KeyId: keyId }))
  },

  disableKey: async (keyId: string) => {
    await awsClients.kms().send(new DisableKeyCommand({ KeyId: keyId }))
  },

  scheduleKeyDeletion: async (keyId: string, pendingWindowInDays = 7) => {
    await awsClients
      .kms()
      .send(
        new ScheduleKeyDeletionCommand({ KeyId: keyId, PendingWindowInDays: pendingWindowInDays }),
      )
  },
}
