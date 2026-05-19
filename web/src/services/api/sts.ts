import { awsClients } from "../aws-clients"
import { GetCallerIdentityCommand } from "@aws-sdk/client-sts"

export type { GetCallerIdentityCommandOutput as CallerIdentity } from "@aws-sdk/client-sts"

export const sts = {
  getCallerIdentity: async () => {
    return await awsClients.sts().send(new GetCallerIdentityCommand({}))
  },
}
