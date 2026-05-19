import { awsClients } from "../aws-clients"
import {
  ListEventBusesCommand,
  CreateEventBusCommand,
  DeleteEventBusCommand,
  ListRulesCommand,
  PutRuleCommand,
  DeleteRuleCommand,
} from "@aws-sdk/client-eventbridge"

export type { EventBus, Rule as EventRule } from "@aws-sdk/client-eventbridge"

export const eventbridge = {
  listBuses: async () => {
    const res = await awsClients.eventbridge().send(new ListEventBusesCommand({}))
    return res.EventBuses ?? []
  },

  createBus: async (name: string) => {
    await awsClients.eventbridge().send(new CreateEventBusCommand({ Name: name }))
  },

  deleteBus: async (name: string) => {
    await awsClients.eventbridge().send(new DeleteEventBusCommand({ Name: name }))
  },

  listRules: async (eventBusName?: string) => {
    const res = await awsClients
      .eventbridge()
      .send(new ListRulesCommand({ EventBusName: eventBusName }))
    return res.Rules ?? []
  },

  createRule: async (name: string, eventBusName?: string) => {
    await awsClients.eventbridge().send(
      new PutRuleCommand({
        Name: name,
        EventBusName: eventBusName,
        EventPattern: JSON.stringify({ source: ["overcast.test"] }),
        State: "ENABLED",
      }),
    )
  },

  deleteRule: async (name: string, eventBusName?: string) => {
    await awsClients
      .eventbridge()
      .send(new DeleteRuleCommand({ Name: name, EventBusName: eventBusName }))
  },
}
