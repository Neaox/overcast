import { awsClients } from "../aws-clients"
import {
  ListEmailIdentitiesCommand,
  CreateEmailIdentityCommand,
  DeleteEmailIdentityCommand,
} from "@aws-sdk/client-sesv2"

export const ses = {
  listIdentities: async () => {
    const res = await awsClients.sesv2().send(new ListEmailIdentitiesCommand({}))
    return res.EmailIdentities ?? []
  },
  deleteIdentity: async (identity: string) => {
    await awsClients.sesv2().send(new DeleteEmailIdentityCommand({ EmailIdentity: identity }))
  },
  verifyIdentity: async (identity: string) => {
    const result = await awsClients
      .sesv2()
      .send(new CreateEmailIdentityCommand({ EmailIdentity: identity }))
    return { ...result, IdentityName: identity }
  },
}
