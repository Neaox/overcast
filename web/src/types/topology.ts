export interface TopologyNode {
  id: string
  service: string
  label: string
  region: string
  streamEnabled?: boolean
  /** SQS only — visible messages waiting to be consumed */
  approximateNumberOfMessages?: number
  /** SQS only — messages currently in-flight (received but not yet deleted/returned) */
  approximateNumberOfMessagesNotVisible?: number
  /** CloudFormation stack name this resource belongs to (1:1 ownership). */
  stackName?: string
  /** VPC ID this resource belongs to (EC2 instances, RDS instances, etc.). */
  vpcId?: string
  /** Resource status string (e.g. RDS DBInstanceStatus: "available", "stopped", etc.). */
  status?: string
  /** VPC only — CIDR block (e.g. "10.0.0.0/16"). */
  cidrBlock?: string
  /** VPC only — number of subnets in this VPC. */
  subnetCount?: number
  /** VPC only — whether an internet gateway is attached. */
  hasInternetGateway?: boolean
  /** IGW only — the VPC ID attached to this internet gateway. */
  attachedVpcId?: string
  /** API Gateway only — protocol type (REST or HTTP). */
  protocolType?: string
  /** API Gateway only — number of routes or resources configured. */
  routeCount?: number
  /** API Gateway only — number of deployed stages. */
  stageCount?: number
  /** AppSync only — authentication type (API_KEY, AWS_IAM, etc.). */
  authenticationType?: string
  /** AppSync only — number of data sources attached. */
  dataSourceCount?: number
  /** AppSync only — number of resolvers configured. */
  resolverCount?: number
  /** ECR only — full push-ready repository URI (e.g. localhost:5000/my-repo). */
  repositoryUri?: string
}

export interface TopologyEdge {
  id: string
  source: string
  target: string
  type: string
  label?: string
  state?: string
  /** Source node's region (for cross-region edge rendering). */
  sourceRegion?: string
  /** Target node's region (for cross-region edge rendering). */
  targetRegion?: string
}

export interface TopologyResponse {
  /** Regions that were queried, in the order returned. */
  regions: string[]
  nodes: TopologyNode[]
  edges: TopologyEdge[]
}
