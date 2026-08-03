import { awsClients } from "../aws-clients"
import {
  DescribeAutoScalingGroupsCommand,
  DescribeAutoScalingInstancesCommand,
  DescribeLifecycleHooksCommand,
  DescribePoliciesCommand,
  DescribeScalingActivitiesCommand,
  SetDesiredCapacityCommand,
} from "@aws-sdk/client-auto-scaling"

export const autoscaling = {
  listGroups: async () => {
    const res = await awsClients.autoscaling().send(new DescribeAutoScalingGroupsCommand({}))
    return res.AutoScalingGroups ?? []
  },

  listInstances: async () => {
    const res = await awsClients.autoscaling().send(new DescribeAutoScalingInstancesCommand({}))
    return res.AutoScalingInstances ?? []
  },

  listPolicies: async (groupName?: string) => {
    const res = await awsClients
      .autoscaling()
      .send(new DescribePoliciesCommand({ AutoScalingGroupName: groupName }))
    return res.ScalingPolicies ?? []
  },

  listLifecycleHooks: async (groupName: string) => {
    const res = await awsClients
      .autoscaling()
      .send(new DescribeLifecycleHooksCommand({ AutoScalingGroupName: groupName }))
    return res.LifecycleHooks ?? []
  },

  listActivities: async (groupName?: string) => {
    const res = await awsClients
      .autoscaling()
      .send(new DescribeScalingActivitiesCommand({ AutoScalingGroupName: groupName }))
    return res.Activities ?? []
  },

  setDesiredCapacity: async (groupName: string, desiredCapacity: number) => {
    await awsClients.autoscaling().send(
      new SetDesiredCapacityCommand({
        AutoScalingGroupName: groupName,
        DesiredCapacity: desiredCapacity,
      }),
    )
  },
}
