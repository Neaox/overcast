import { awsClients } from "../aws-clients"
import {
  ListTopicsCommand,
  CreateTopicCommand,
  DeleteTopicCommand,
  ListSubscriptionsCommand,
  ListSubscriptionsByTopicCommand,
  SubscribeCommand,
  UnsubscribeCommand,
  PublishCommand,
} from "@aws-sdk/client-sns"

async function resolveTopicArn(name: string): Promise<string> {
  const res = await awsClients.sns().send(new ListTopicsCommand({}))
  const match = (res.Topics ?? []).find((t) => t.TopicArn?.split(":").pop() === name)
  if (!match?.TopicArn) throw new Error(`Topic not found: ${name}`)
  return match.TopicArn
}

export const sns = {
  listTopics: async () => {
    const res = await awsClients.sns().send(new ListTopicsCommand({}))
    return res.Topics ?? []
  },

  createTopic: async (name: string) => {
    return await awsClients.sns().send(new CreateTopicCommand({ Name: name }))
  },

  deleteTopic: async (name: string) => {
    const arn = await resolveTopicArn(name)
    await awsClients.sns().send(new DeleteTopicCommand({ TopicArn: arn }))
  },

  listSubscriptions: async (topicName: string) => {
    const arn = await resolveTopicArn(topicName)
    const res = await awsClients.sns().send(new ListSubscriptionsByTopicCommand({ TopicArn: arn }))
    return res.Subscriptions ?? []
  },

  listSubscriptionsByEndpoint: async (endpoint?: string) => {
    const res = await awsClients.sns().send(new ListSubscriptionsCommand({}))
    const subs = res.Subscriptions ?? []
    return endpoint ? subs.filter((s) => s.Endpoint === endpoint) : subs
  },

  subscribe: async (topicName: string, protocol: string, subscriptionEndpoint: string) => {
    const arn = await resolveTopicArn(topicName)
    return await awsClients
      .sns()
      .send(
        new SubscribeCommand({ TopicArn: arn, Protocol: protocol, Endpoint: subscriptionEndpoint }),
      )
  },

  unsubscribe: async (subscriptionArn: string) => {
    await awsClients.sns().send(new UnsubscribeCommand({ SubscriptionArn: subscriptionArn }))
  },

  publish: async (
    topicName: string,
    opts: {
      message: string
      subject?: string
      messageStructure?: "json"
      messageGroupId?: string
      messageDeduplicationId?: string
      messageAttributes?: Record<string, { dataType: string; stringValue: string }>
    },
  ) => {
    const arn = await resolveTopicArn(topicName)
    return await awsClients.sns().send(
      new PublishCommand({
        TopicArn: arn,
        Message: opts.message,
        ...(opts.subject ? { Subject: opts.subject } : {}),
        ...(opts.messageStructure ? { MessageStructure: opts.messageStructure } : {}),
        ...(opts.messageGroupId ? { MessageGroupId: opts.messageGroupId } : {}),
        ...(opts.messageDeduplicationId
          ? { MessageDeduplicationId: opts.messageDeduplicationId }
          : {}),
        ...(opts.messageAttributes
          ? {
              MessageAttributes: Object.fromEntries(
                Object.entries(opts.messageAttributes).map(([k, v]) => [
                  k,
                  { DataType: v.dataType, StringValue: v.stringValue },
                ]),
              ),
            }
          : {}),
      }),
    )
  },
}
