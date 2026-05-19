export interface EcsCluster {
  clusterName: string
  clusterArn: string
  status: string
  runningTasksCount: number
  activeServicesCount: number
  pendingTasksCount: number
  registeredContainerInstancesCount: number
  tags?: Array<{ key: string; value: string }>
}

export interface EcsContainerInstance {
  containerInstanceArn: string
  ec2InstanceId?: string
  status: string
  agentConnected: boolean
  runningTasksCount: number
  pendingTasksCount: number
  registeredAt?: number
}

export interface EcsTaskDefinition {
  family: string
  revision: number
  taskDefinitionArn: string
  status: string
  cpu?: string
  memory?: string
}

export interface EcsTask {
  taskArn: string
  taskDefinitionArn: string
  clusterArn: string
  lastStatus: string
  desiredStatus: string
  launchType?: string
  createdAt?: string
  startedAt?: string
  stoppedAt?: string
  stoppedReason?: string
  containers: EcsContainer[]
}

export interface EcsContainer {
  containerArn?: string
  name: string
  image?: string
  lastStatus: string
  exitCode?: number
  networkBindings?: Array<{ hostPort?: number; containerPort?: number; protocol?: string }>
}

export interface EcsService {
  serviceName: string
  serviceArn: string
  clusterArn: string
  taskDefinition: string
  desiredCount: number
  runningCount: number
  pendingCount: number
  status: string
  launchType: string
  deployments: EcsDeployment[]
  events: EcsServiceEvent[]
  createdAt: string
}

export interface EcsDeployment {
  id: string
  status: string
  taskDefinition: string
  desiredCount: number
  runningCount: number
  pendingCount: number
  createdAt: string
}

export interface EcsServiceEvent {
  id: string
  createdAt: string
  message: string
}
