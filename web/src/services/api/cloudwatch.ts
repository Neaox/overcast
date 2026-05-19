import {
  CloudWatchClient,
  DescribeAlarmsCommand,
  GetMetricStatisticsCommand,
  ListMetricsCommand,
  type Datapoint,
  type Dimension,
  type Metric,
  type MetricAlarm,
  type Statistic,
  type StandardUnit,
} from "@aws-sdk/client-cloudwatch"
import { awsClients } from "../aws-clients"

export interface MetricStatisticsResult {
  label?: string
  datapoints: Datapoint[]
}

function client(): CloudWatchClient {
  return awsClients.cloudwatch()
}

function sortDatapointsAscending(datapoints: Datapoint[]): Datapoint[] {
  return [...datapoints].sort((left, right) => {
    const leftTime = left.Timestamp?.getTime() ?? 0
    const rightTime = right.Timestamp?.getTime() ?? 0
    return leftTime - rightTime
  })
}

export const cloudwatch = {
  listMetrics: async (namespace?: string): Promise<Metric[]> => {
    const response = await client().send(
      new ListMetricsCommand({
        ...(namespace ? { Namespace: namespace } : {}),
      }),
    )
    return response.Metrics ?? []
  },

  getMetricStatistics: async (params: {
    namespace: string
    metricName: string
    dimensions?: Dimension[]
    startTime: Date
    endTime: Date
    period: number
    stat: Statistic
    unit?: StandardUnit
  }): Promise<MetricStatisticsResult> => {
    const response = await client().send(
      new GetMetricStatisticsCommand({
        Namespace: params.namespace,
        MetricName: params.metricName,
        Dimensions: params.dimensions,
        StartTime: params.startTime,
        EndTime: params.endTime,
        Period: params.period,
        Statistics: [params.stat],
        ...(params.unit ? { Unit: params.unit } : {}),
      }),
    )
    return {
      label: response.Label,
      datapoints: sortDatapointsAscending(response.Datapoints ?? []),
    }
  },

  describeAlarms: async (): Promise<MetricAlarm[]> => {
    const response = await client().send(new DescribeAlarmsCommand({}))
    return response.MetricAlarms ?? []
  },
}
