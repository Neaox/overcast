export interface EksCluster {
  name: string
  arn: string
  version: string
  status: string
  endpoint?: string
  createdAt?: number
}
