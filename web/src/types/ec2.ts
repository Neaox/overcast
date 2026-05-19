export interface Ec2Instance {
  instanceId: string
  imageId: string
  instanceType: string
  state: { code: number; name: string }
  privateIpAddress?: string
  vpcId?: string
  subnetId?: string
  securityGroups?: Array<{ groupId: string; groupName: string }>
  launchTime?: string
  tags?: Array<{ key: string; value: string }>
}

export interface Ec2Vpc {
  vpcId: string
  cidrBlock: string
  state: string
  isDefault: boolean
  /** Populated from the overcast:network-status synthetic tag. "ok" when absent. */
  networkStatus?: string
  tags?: Array<{ key: string; value: string }>
}

export interface Ec2ElasticIp {
  allocationId: string
  publicIp: string
  domain: string
  associationId?: string
  instanceId?: string
  networkInterfaceId?: string
  privateIpAddress?: string
}

export interface Ec2NatGateway {
  natGatewayId: string
  subnetId: string
  vpcId: string
  state: string
  allocationId?: string
  publicIp?: string
  privateIp?: string
  createTime?: string
  tags?: Array<{ key: string; value: string }>
}

export interface Ec2SecurityGroup {
  groupId: string
  groupName: string
  description: string
  vpcId?: string
  ipPermissions: Ec2IpPermission[]
  ipPermissionsEgress: Ec2IpPermission[]
}

export interface Ec2IpPermission {
  ipProtocol: string
  fromPort?: number
  toPort?: number
  ipRanges?: Array<{ cidrIp: string }>
}

export interface Ec2Subnet {
  subnetId: string
  vpcId: string
  cidrBlock: string
  availabilityZone: string
}

export interface Ec2RouteTable {
  routeTableId: string
  vpcId: string
  routes: Array<{
    destinationCidrBlock: string
    gatewayId?: string
    origin: string
  }>
  associations: Array<{
    associationId: string
    routeTableId: string
    subnetId?: string
    main: boolean
  }>
}

export interface Ec2InternetGateway {
  internetGatewayId: string
  attachments: Array<{ vpcId: string; state: string }>
}

export interface Ec2VpcPeeringConnection {
  vpcPeeringConnectionId: string
  requesterVpcInfo: { vpcId: string; ownerId: string; cidrBlock: string; region: string }
  accepterVpcInfo: { vpcId: string; ownerId: string; cidrBlock: string; region: string }
  status: { code: string; message: string }
}

export interface Ec2VpcEndpoint {
  vpcEndpointId: string
  vpcId: string
  serviceName: string
  state: string
  vpcEndpointType: string
}
