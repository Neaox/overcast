/**
 * The awsvpc networking a task needs. ECS requires this whenever the task
 * definition's networkMode is awsvpc, which is every Fargate task definition —
 * without it the call is rejected with "Network Configuration must be provided
 * when networkMode is 'awsvpc'."
 */
export interface AwsvpcNetworking {
  subnets: string[]
  securityGroups: string[]
  assignPublicIp: string
}

export const emptyAwsvpcNetworking: AwsvpcNetworking = {
  subnets: [],
  securityGroups: [],
  assignPublicIp: "DISABLED",
}
