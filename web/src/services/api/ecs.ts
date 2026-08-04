import { awsClients } from "../aws-clients"
import {
  CreateClusterCommand,
  ListClustersCommand,
  DescribeClustersCommand,
  DeleteClusterCommand,
  RegisterTaskDefinitionCommand,
  DescribeTaskDefinitionCommand,
  ListTaskDefinitionsCommand,
  DeregisterTaskDefinitionCommand,
  RunTaskCommand,
  StopTaskCommand,
  DescribeTasksCommand,
  ListTasksCommand,
  CreateServiceCommand,
  UpdateServiceCommand,
  DeleteServiceCommand,
  DescribeServicesCommand,
  ListServicesCommand,
  ListTagsForResourceCommand,
  ListTaskDefinitionFamiliesCommand,
  ListContainerInstancesCommand,
  DescribeContainerInstancesCommand,
  type ContainerDefinition,
  type NetworkMode,
  type LaunchType,
  type DesiredStatus,
} from "@aws-sdk/client-ecs"
import type {
  EcsCluster,
  EcsTaskDefinition,
  EcsTask,
  EcsService,
  EcsContainerInstance,
} from "@/types"

export const ecs = {
  listClusters: async (): Promise<EcsCluster[]> => {
    const client = awsClients.ecs()
    const listRes = await client.send(new ListClustersCommand({}))
    const arns = listRes.clusterArns ?? []
    if (arns.length === 0) return []
    const descRes = await client.send(
      new DescribeClustersCommand({ clusters: arns, include: ["TAGS"] }),
    )
    return (descRes.clusters ?? []).map((c) => ({
      clusterName: c.clusterName ?? "",
      clusterArn: c.clusterArn ?? "",
      status: c.status ?? "UNKNOWN",
      runningTasksCount: c.runningTasksCount ?? 0,
      activeServicesCount: c.activeServicesCount ?? 0,
      pendingTasksCount: c.pendingTasksCount ?? 0,
      registeredContainerInstancesCount: c.registeredContainerInstancesCount ?? 0,
      tags: c.tags?.map((t) => ({ key: t.key ?? "", value: t.value ?? "" })),
    }))
  },

  createCluster: async (name: string): Promise<EcsCluster> => {
    const res = await awsClients.ecs().send(new CreateClusterCommand({ clusterName: name }))
    const c = res.cluster!
    return {
      clusterName: c.clusterName ?? "",
      clusterArn: c.clusterArn ?? "",
      status: c.status ?? "ACTIVE",
      runningTasksCount: c.runningTasksCount ?? 0,
      activeServicesCount: c.activeServicesCount ?? 0,
      pendingTasksCount: c.pendingTasksCount ?? 0,
      registeredContainerInstancesCount: c.registeredContainerInstancesCount ?? 0,
    }
  },

  deleteCluster: async (cluster: string): Promise<void> => {
    await awsClients.ecs().send(new DeleteClusterCommand({ cluster }))
  },

  describeCluster: async (cluster: string): Promise<EcsCluster> => {
    const res = await awsClients.ecs().send(new DescribeClustersCommand({ clusters: [cluster] }))
    const c = (res.clusters ?? []).at(0)
    if (!c) throw new Error(`Cluster "${cluster}" not found`)
    return {
      clusterName: c.clusterName ?? "",
      clusterArn: c.clusterArn ?? "",
      status: c.status ?? "UNKNOWN",
      runningTasksCount: c.runningTasksCount ?? 0,
      activeServicesCount: c.activeServicesCount ?? 0,
      pendingTasksCount: c.pendingTasksCount ?? 0,
      registeredContainerInstancesCount: c.registeredContainerInstancesCount ?? 0,
    }
  },

  listTaskDefinitions: async (): Promise<EcsTaskDefinition[]> => {
    const client = awsClients.ecs()
    const listRes = await client.send(new ListTaskDefinitionsCommand({}))
    const arns = listRes.taskDefinitionArns ?? []
    const defs = await Promise.all(
      arns.map(async (arn) => {
        try {
          const desc = await client.send(new DescribeTaskDefinitionCommand({ taskDefinition: arn }))
          const td = desc.taskDefinition!
          return {
            family: td.family ?? "",
            revision: td.revision ?? 0,
            taskDefinitionArn: td.taskDefinitionArn ?? "",
            status: td.status ?? "UNKNOWN",
            cpu: td.cpu,
            memory: td.memory,
            networkMode: td.networkMode,
            requiresCompatibilities: td.requiresCompatibilities,
          }
        } catch {
          return {
            family: arn.split("/").pop()?.split(":")[0] ?? "",
            revision: 0,
            taskDefinitionArn: arn,
            status: "UNKNOWN",
          }
        }
      }),
    )
    return defs
  },

  registerTaskDefinition: async (json: string): Promise<EcsTaskDefinition> => {
    const parsed = JSON.parse(json) as {
      family: string
      containerDefinitions: ContainerDefinition[]
      cpu?: string
      memory?: string
      networkMode?: string
    }
    const res = await awsClients.ecs().send(
      new RegisterTaskDefinitionCommand({
        family: parsed.family,
        containerDefinitions: parsed.containerDefinitions,
        cpu: parsed.cpu,
        memory: parsed.memory,
        networkMode: parsed.networkMode as NetworkMode | undefined,
      }),
    )
    const td = res.taskDefinition!
    return {
      family: td.family ?? "",
      revision: td.revision ?? 0,
      taskDefinitionArn: td.taskDefinitionArn ?? "",
      status: td.status ?? "ACTIVE",
      cpu: td.cpu,
      memory: td.memory,
    }
  },

  deregisterTaskDefinition: async (taskDefinition: string): Promise<void> => {
    await awsClients.ecs().send(new DeregisterTaskDefinitionCommand({ taskDefinition }))
  },

  listTasks: async (
    cluster: string,
    desiredStatus: DesiredStatus = "RUNNING",
  ): Promise<EcsTask[]> => {
    const client = awsClients.ecs()
    const listRes = await client.send(new ListTasksCommand({ cluster, desiredStatus }))
    const arns = listRes.taskArns ?? []
    if (arns.length === 0) return []
    const descRes = await client.send(new DescribeTasksCommand({ cluster, tasks: arns }))
    return (descRes.tasks ?? []).map(mapTask)
  },

  describeTask: async (cluster: string, task: string): Promise<EcsTask | null> => {
    const res = await awsClients.ecs().send(new DescribeTasksCommand({ cluster, tasks: [task] }))
    const described = res.tasks?.[0]
    return described ? mapTask(described) : null
  },

  runTask: async (opts: {
    cluster: string
    taskDefinition: string
    count?: number
    launchType?: string
    subnets?: string[]
    securityGroups?: string[]
    assignPublicIp?: string
  }): Promise<EcsTask[]> => {
    const res = await awsClients.ecs().send(
      new RunTaskCommand({
        cluster: opts.cluster,
        taskDefinition: opts.taskDefinition,
        count: opts.count ?? 1,
        launchType: (opts.launchType as LaunchType | undefined) ?? "FARGATE",
        ...networkConfigurationFor(opts),
      }),
    )
    return (res.tasks ?? []).map(mapTask)
  },

  stopTask: async (cluster: string, task: string, reason?: string): Promise<void> => {
    await awsClients.ecs().send(new StopTaskCommand({ cluster, task, reason }))
  },

  listServices: async (cluster: string): Promise<EcsService[]> => {
    const client = awsClients.ecs()
    const listRes = await client.send(new ListServicesCommand({ cluster }))
    const arns = listRes.serviceArns ?? []
    if (arns.length === 0) return []
    const descRes = await client.send(new DescribeServicesCommand({ cluster, services: arns }))
    return (descRes.services ?? []).map(mapService)
  },

  createService: async (params: {
    cluster: string
    serviceName: string
    taskDefinition: string
    desiredCount: number
    launchType?: string
    subnets?: string[]
    securityGroups?: string[]
    assignPublicIp?: string
  }): Promise<EcsService> => {
    const res = await awsClients.ecs().send(
      new CreateServiceCommand({
        cluster: params.cluster,
        serviceName: params.serviceName,
        taskDefinition: params.taskDefinition,
        desiredCount: params.desiredCount,
        launchType: params.launchType as LaunchType | undefined,
        ...networkConfigurationFor(params),
      }),
    )
    return mapService(res.service!)
  },

  updateService: async (params: {
    cluster: string
    service: string
    desiredCount?: number
    taskDefinition?: string
  }): Promise<EcsService> => {
    const res = await awsClients.ecs().send(
      new UpdateServiceCommand({
        cluster: params.cluster,
        service: params.service,
        desiredCount: params.desiredCount,
        taskDefinition: params.taskDefinition,
      }),
    )
    return mapService(res.service!)
  },

  deleteService: async (params: { cluster: string; service: string }): Promise<void> => {
    await awsClients
      .ecs()
      .send(new DeleteServiceCommand({ cluster: params.cluster, service: params.service }))
  },

  listTagsForResource: async (
    resourceArn: string,
  ): Promise<Array<{ key: string; value: string }>> => {
    const res = await awsClients.ecs().send(new ListTagsForResourceCommand({ resourceArn }))
    return (res.tags ?? []).map((t) => ({ key: t.key ?? "", value: t.value ?? "" }))
  },

  listTaskDefinitionFamilies: async (prefix?: string): Promise<string[]> => {
    const res = await awsClients
      .ecs()
      .send(new ListTaskDefinitionFamiliesCommand(prefix ? { familyPrefix: prefix } : {}))
    return res.families ?? []
  },

  listContainerInstances: async (cluster: string): Promise<EcsContainerInstance[]> => {
    const client = awsClients.ecs()
    const listRes = await client.send(new ListContainerInstancesCommand({ cluster }))
    const arns = listRes.containerInstanceArns ?? []
    if (arns.length === 0) return []
    const descRes = await client.send(
      new DescribeContainerInstancesCommand({ cluster, containerInstances: arns }),
    )
    return (descRes.containerInstances ?? []).map((ci) => ({
      containerInstanceArn: ci.containerInstanceArn ?? "",
      ec2InstanceId: ci.ec2InstanceId,
      status: ci.status ?? "UNKNOWN",
      agentConnected: ci.agentConnected ?? false,
      runningTasksCount: ci.runningTasksCount ?? 0,
      pendingTasksCount: ci.pendingTasksCount ?? 0,
      registeredAt: ci.registeredAt?.getTime(),
    }))
  },
}

/**
 * Builds the awsvpc networkConfiguration ECS requires whenever the task
 * definition's networkMode is awsvpc — which every Fargate task definition's
 * is. Omitted entirely when no subnet was chosen, so a bridge-mode task
 * definition is not sent a configuration it cannot use.
 */
function networkConfigurationFor(opts: {
  subnets?: string[]
  securityGroups?: string[]
  assignPublicIp?: string
}) {
  const subnets = (opts.subnets ?? []).filter(Boolean)
  if (subnets.length === 0) return {}
  const securityGroups = (opts.securityGroups ?? []).filter(Boolean)
  return {
    networkConfiguration: {
      awsvpcConfiguration: {
        subnets,
        ...(securityGroups.length > 0 ? { securityGroups } : {}),
        ...(opts.assignPublicIp
          ? { assignPublicIp: opts.assignPublicIp as "ENABLED" | "DISABLED" }
          : {}),
      },
    },
  }
}

function mapTask(t: {
  taskArn?: string
  taskDefinitionArn?: string
  clusterArn?: string
  lastStatus?: string
  desiredStatus?: string
  launchType?: string
  createdAt?: Date
  startedAt?: Date
  stoppedAt?: Date
  stoppedReason?: string
  stopCode?: string
  startedBy?: string
  group?: string
  containers?: Array<{
    containerArn?: string
    name?: string
    image?: string
    lastStatus?: string
    exitCode?: number
    reason?: string
    networkBindings?: Array<{
      hostPort?: number
      containerPort?: number
      protocol?: string
    }>
  }>
}): EcsTask {
  return {
    taskArn: t.taskArn ?? "",
    taskDefinitionArn: t.taskDefinitionArn ?? "",
    clusterArn: t.clusterArn ?? "",
    lastStatus: t.lastStatus ?? "UNKNOWN",
    desiredStatus: t.desiredStatus ?? "UNKNOWN",
    launchType: t.launchType,
    createdAt: t.createdAt?.toISOString(),
    startedAt: t.startedAt?.toISOString(),
    stoppedAt: t.stoppedAt?.toISOString(),
    stoppedReason: t.stoppedReason,
    stopCode: t.stopCode,
    startedBy: t.startedBy,
    group: t.group,
    containers: (t.containers ?? []).map((c) => ({
      containerArn: c.containerArn,
      name: c.name ?? "",
      image: c.image,
      lastStatus: c.lastStatus ?? "UNKNOWN",
      exitCode: c.exitCode,
      reason: c.reason,
      networkBindings: c.networkBindings?.map((b) => ({
        hostPort: b.hostPort,
        containerPort: b.containerPort,
        protocol: b.protocol,
      })),
    })),
  }
}

function mapService(s: {
  serviceName?: string
  serviceArn?: string
  clusterArn?: string
  taskDefinition?: string
  desiredCount?: number
  runningCount?: number
  pendingCount?: number
  status?: string
  launchType?: string
  deployments?: Array<{
    id?: string
    status?: string
    taskDefinition?: string
    desiredCount?: number
    runningCount?: number
    pendingCount?: number
    failedTasks?: number
    rolloutState?: string
    rolloutStateReason?: string
    createdAt?: Date
  }>
  events?: Array<{
    id?: string
    createdAt?: Date
    message?: string
  }>
  createdAt?: Date
}): EcsService {
  return {
    serviceName: s.serviceName ?? "",
    serviceArn: s.serviceArn ?? "",
    clusterArn: s.clusterArn ?? "",
    taskDefinition: s.taskDefinition ?? "",
    desiredCount: s.desiredCount ?? 0,
    runningCount: s.runningCount ?? 0,
    pendingCount: s.pendingCount ?? 0,
    status: s.status ?? "UNKNOWN",
    launchType: s.launchType ?? "FARGATE",
    deployments: (s.deployments ?? []).map((d) => ({
      id: d.id ?? "",
      status: d.status ?? "",
      taskDefinition: d.taskDefinition ?? "",
      desiredCount: d.desiredCount ?? 0,
      runningCount: d.runningCount ?? 0,
      pendingCount: d.pendingCount ?? 0,
      failedTasks: d.failedTasks,
      rolloutState: d.rolloutState,
      rolloutStateReason: d.rolloutStateReason,
      createdAt: d.createdAt?.toISOString() ?? "",
    })),
    events: (s.events ?? []).map((e) => ({
      id: e.id ?? "",
      createdAt: e.createdAt?.toISOString() ?? "",
      message: e.message ?? "",
    })),
    createdAt: s.createdAt?.toISOString() ?? "",
  }
}
