import { awsClients } from "../aws-clients"
import {
  DescribeLogGroupsCommand,
  CreateLogGroupCommand,
  DeleteLogGroupCommand,
  DescribeLogStreamsCommand,
  CreateLogStreamCommand,
  DeleteLogStreamCommand,
  GetLogEventsCommand,
  PutLogEventsCommand,
  FilterLogEventsCommand,
} from "@aws-sdk/client-cloudwatch-logs"

export const logs = {
  listGroups: async (prefix?: string) => {
    const res = await awsClients
      .logs()
      .send(new DescribeLogGroupsCommand({ logGroupNamePrefix: prefix || undefined }))
    return res.logGroups ?? []
  },

  createGroup: async (name: string) => {
    await awsClients.logs().send(new CreateLogGroupCommand({ logGroupName: name }))
  },

  deleteGroup: async (name: string) => {
    await awsClients.logs().send(new DeleteLogGroupCommand({ logGroupName: name }))
  },

  listStreams: async (groupName: string, prefix?: string) => {
    const res = await awsClients.logs().send(
      new DescribeLogStreamsCommand({
        logGroupName: groupName,
        logStreamNamePrefix: prefix || undefined,
      }),
    )
    return res.logStreams ?? []
  },

  createStream: async (groupName: string, name: string) => {
    await awsClients
      .logs()
      .send(new CreateLogStreamCommand({ logGroupName: groupName, logStreamName: name }))
  },

  deleteStream: async (groupName: string, streamName: string) => {
    await awsClients
      .logs()
      .send(new DeleteLogStreamCommand({ logGroupName: groupName, logStreamName: streamName }))
  },

  getEvents: async (
    groupName: string,
    streamName: string,
    opts: {
      startTime?: number
      endTime?: number
      nextToken?: string
      limit?: number
      startFromHead?: boolean
    } = {},
  ) => {
    const res = await awsClients.logs().send(
      new GetLogEventsCommand({
        logGroupName: groupName,
        logStreamName: streamName,
        ...(opts.nextToken == null ? { startFromHead: opts.startFromHead ?? false } : {}),
        ...(opts.nextToken != null ? { nextToken: opts.nextToken } : {}),
        ...(opts.startTime != null ? { startTime: opts.startTime } : {}),
        ...(opts.endTime != null ? { endTime: opts.endTime } : {}),
        ...(opts.limit != null ? { limit: opts.limit } : {}),
      }),
    )
    return {
      events: res.events ?? [],
      nextBackwardToken: res.nextBackwardToken,
      nextForwardToken: res.nextForwardToken,
    }
  },

  putEvents: async (
    groupName: string,
    streamName: string,
    events: Array<{ timestamp: number; message: string }>,
  ) => {
    await awsClients.logs().send(
      new PutLogEventsCommand({
        logGroupName: groupName,
        logStreamName: streamName,
        logEvents: events,
      }),
    )
  },

  filterEvents: async (
    groupName: string,
    opts: {
      filterPattern?: string
      startTime?: number
      endTime?: number
      logStreamNames?: string[]
      logStreamNamePrefix?: string
    } = {},
  ) => {
    const res = await awsClients.logs().send(
      new FilterLogEventsCommand({
        logGroupName: groupName,
        ...(opts.filterPattern ? { filterPattern: opts.filterPattern } : {}),
        ...(opts.startTime != null ? { startTime: opts.startTime } : {}),
        ...(opts.endTime != null ? { endTime: opts.endTime } : {}),
        ...(opts.logStreamNames?.length ? { logStreamNames: opts.logStreamNames } : {}),
        ...(opts.logStreamNamePrefix ? { logStreamNamePrefix: opts.logStreamNamePrefix } : {}),
      }),
    )
    return {
      events: res.events ?? [],
      searchedLogStreams: res.searchedLogStreams ?? [],
    }
  },
}
