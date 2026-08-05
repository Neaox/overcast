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
  /** awsvpc | bridge | host | none — decides whether a networkConfiguration is required. */
  networkMode?: string
  requiresCompatibilities?: string[]
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
  /** TaskFailedToStart | EssentialContainerExited | UserInitiated | ServiceSchedulerInitiated | … */
  stopCode?: string
  /** The deployment ID when a service started this task. */
  startedBy?: string
  group?: string
  containers: EcsContainer[]
}

export interface EcsContainer {
  containerArn?: string
  name: string
  image?: string
  lastStatus: string
  exitCode?: number
  reason?: string
  networkBindings?: Array<{ hostPort?: number; containerPort?: number; protocol?: string }>
}

export interface EcsTaskLogs {
  containerName: string
  taskArn: string
  logs: string
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
  failedTasks?: number
  /** COMPLETED | FAILED | IN_PROGRESS — reported only for the ECS controller. */
  rolloutState?: string
  rolloutStateReason?: string
  createdAt: string
}

export interface EcsServiceEvent {
  id: string
  createdAt: string
  message: string
}
