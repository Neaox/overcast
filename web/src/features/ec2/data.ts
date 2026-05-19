/**
 * EC2 query/mutation definitions.
 *
 * Key factory:
 *   ec2Keys.all()                  -> [...endpoint, "ec2"]
 *   ec2Keys.instances()            -> [...endpoint, "ec2", "instances"]
 *   ec2Keys.vpcs()                 -> [...endpoint, "ec2", "vpcs"]
 *   ec2Keys.securityGroups()       -> [...endpoint, "ec2", "security-groups"]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { ec2 } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const ec2Keys = {
  all: () => [...endpointStore.getKeys(), "ec2"] as const,
  instances: () => [...ec2Keys.all(), "instances"] as const,
  vpcs: () => [...ec2Keys.all(), "vpcs"] as const,
  securityGroups: () => [...ec2Keys.all(), "security-groups"] as const,
  subnets: () => [...ec2Keys.all(), "subnets"] as const,
  routeTables: () => [...ec2Keys.all(), "route-tables"] as const,
  internetGateways: () => [...ec2Keys.all(), "internet-gateways"] as const,
  vpcPeeringConnections: () => [...ec2Keys.all(), "vpc-peering-connections"] as const,
  elasticIps: () => [...ec2Keys.all(), "elastic-ips"] as const,
  natGateways: () => [...ec2Keys.all(), "nat-gateways"] as const,
  vpcEndpoints: (vpcId?: string) =>
    vpcId
      ? ([...ec2Keys.all(), "vpc-endpoints", vpcId] as const)
      : ([...ec2Keys.all(), "vpc-endpoints"] as const),
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function ec2InstancesQueryOptions() {
  return queryOptions({
    queryKey: ec2Keys.instances(),
    queryFn: () => ec2.listInstances(),
    staleTime: 5_000,
  })
}

export function ec2InstanceDetailQueryOptions(instanceId: string) {
  return queryOptions({
    queryKey: [...ec2Keys.instances(), instanceId] as const,
    queryFn: async () => {
      const all = await ec2.listInstances()
      const inst = all.find((i) => i.instanceId === instanceId)
      if (!inst) throw new Error(`Instance ${instanceId} not found`)
      return inst
    },
    staleTime: 5_000,
  })
}

export function ec2VpcsQueryOptions() {
  return queryOptions({
    queryKey: ec2Keys.vpcs(),
    queryFn: () => ec2.listVpcs(),
    staleTime: 60_000,
  })
}

export function ec2SecurityGroupsQueryOptions() {
  return queryOptions({
    queryKey: ec2Keys.securityGroups(),
    queryFn: () => ec2.listSecurityGroups(),
    staleTime: 30_000,
  })
}

export function ec2SubnetsQueryOptions() {
  return queryOptions({
    queryKey: ec2Keys.subnets(),
    queryFn: () => ec2.listSubnets(),
    staleTime: 60_000,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function runInstancesMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.instances(), "run"] as const,
    mutationFn: (opts: {
      imageId: string
      instanceType: string
      minCount: number
      maxCount: number
      subnetId?: string
      securityGroupIds?: string[]
    }) => ec2.runInstances(opts),
  })
}

export function terminateInstancesMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.instances(), "terminate"] as const,
    mutationFn: (instanceIds: string[]) => ec2.terminateInstances(instanceIds),
  })
}

export function startInstancesMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.instances(), "start"] as const,
    mutationFn: (instanceIds: string[]) => ec2.startInstances(instanceIds),
  })
}

export function stopInstancesMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.instances(), "stop"] as const,
    mutationFn: (instanceIds: string[]) => ec2.stopInstances(instanceIds),
  })
}

export function createVpcMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.vpcs(), "create"] as const,
    mutationFn: (cidrBlock: string) => ec2.createVpc(cidrBlock),
  })
}

export function deleteVpcMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.vpcs(), "delete"] as const,
    mutationFn: (vpcId: string) => ec2.deleteVpc(vpcId),
  })
}

export function createSecurityGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.securityGroups(), "create"] as const,
    mutationFn: (opts: { groupName: string; description: string; vpcId?: string }) =>
      ec2.createSecurityGroup(opts),
  })
}

export function deleteSecurityGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.securityGroups(), "delete"] as const,
    mutationFn: (groupId: string) => ec2.deleteSecurityGroup(groupId),
  })
}

// ─── Route Tables, IGWs, Peering queries ──────────────────────────────────

export function ec2RouteTablesQueryOptions() {
  return queryOptions({
    queryKey: ec2Keys.routeTables(),
    queryFn: () => ec2.listRouteTables(),
    staleTime: 60_000,
  })
}

export function ec2InternetGatewaysQueryOptions() {
  return queryOptions({
    queryKey: ec2Keys.internetGateways(),
    queryFn: () => ec2.listInternetGateways(),
    staleTime: 60_000,
  })
}

// ─── Internet Gateway mutations ───────────────────────────────────────────

export function createInternetGatewayMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.internetGateways(), "create"] as const,
    mutationFn: () => ec2.createInternetGateway(),
  })
}

export function deleteInternetGatewayMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.internetGateways(), "delete"] as const,
    mutationFn: (internetGatewayId: string) => ec2.deleteInternetGateway(internetGatewayId),
  })
}

export function attachInternetGatewayMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.internetGateways(), "attach"] as const,
    mutationFn: (opts: { internetGatewayId: string; vpcId: string }) =>
      ec2.attachInternetGateway(opts.internetGatewayId, opts.vpcId),
  })
}

export function detachInternetGatewayMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.internetGateways(), "detach"] as const,
    mutationFn: (opts: { internetGatewayId: string; vpcId: string }) =>
      ec2.detachInternetGateway(opts.internetGatewayId, opts.vpcId),
  })
}

export function ec2VpcPeeringConnectionsQueryOptions() {
  return queryOptions({
    queryKey: ec2Keys.vpcPeeringConnections(),
    queryFn: () => ec2.listVpcPeeringConnections(),
    staleTime: 30_000,
  })
}

export function ec2VpcDetailQueryOptions(vpcId: string) {
  return queryOptions({
    queryKey: [...ec2Keys.vpcs(), vpcId] as const,
    queryFn: async () => {
      const all = await ec2.listVpcs()
      const vpc = all.find((v) => v.vpcId === vpcId)
      if (!vpc) throw new Error(`VPC ${vpcId} not found`)
      return vpc
    },
    staleTime: 30_000,
  })
}

// ─── Peering mutations ────────────────────────────────────────────────────

export function createVpcPeeringConnectionMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.vpcPeeringConnections(), "create"] as const,
    mutationFn: (opts: { vpcId: string; peerVpcId: string }) =>
      ec2.createVpcPeeringConnection(opts.vpcId, opts.peerVpcId),
  })
}

export function acceptVpcPeeringConnectionMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.vpcPeeringConnections(), "accept"] as const,
    mutationFn: (pcxId: string) => ec2.acceptVpcPeeringConnection(pcxId),
  })
}

export function deleteVpcPeeringConnectionMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.vpcPeeringConnections(), "delete"] as const,
    mutationFn: (pcxId: string) => ec2.deleteVpcPeeringConnection(pcxId),
  })
}

// ─── Elastic IPs ──────────────────────────────────────────────────────────

export function ec2ElasticIpsQueryOptions() {
  return queryOptions({
    queryKey: ec2Keys.elasticIps(),
    queryFn: () => ec2.listElasticIps(),
    staleTime: 30_000,
  })
}

export function allocateAddressMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.elasticIps(), "allocate"] as const,
    mutationFn: () => ec2.allocateAddress(),
  })
}

export function releaseAddressMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.elasticIps(), "release"] as const,
    mutationFn: (allocationId: string) => ec2.releaseAddress(allocationId),
  })
}

export function associateAddressMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.elasticIps(), "associate"] as const,
    mutationFn: (opts: { allocationId: string; instanceId?: string }) =>
      ec2.associateAddress(opts),
  })
}

export function disassociateAddressMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.elasticIps(), "disassociate"] as const,
    mutationFn: (associationId: string) => ec2.disassociateAddress(associationId),
  })
}

// ─── NAT Gateways ─────────────────────────────────────────────────────────

export function ec2NatGatewaysQueryOptions() {
  return queryOptions({
    queryKey: ec2Keys.natGateways(),
    queryFn: () => ec2.listNatGateways(),
    staleTime: 30_000,
  })
}

export function createNatGatewayMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.natGateways(), "create"] as const,
    mutationFn: (opts: { subnetId: string; allocationId: string }) => ec2.createNatGateway(opts),
  })
}

export function deleteNatGatewayMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.natGateways(), "delete"] as const,
    mutationFn: (natGatewayId: string) => ec2.deleteNatGateway(natGatewayId),
  })
}

// ─── VPC Endpoints ────────────────────────────────────────────────────────

export function ec2VpcEndpointsQueryOptions(vpcId?: string) {
  return queryOptions({
    queryKey: ec2Keys.vpcEndpoints(vpcId),
    queryFn: () => ec2.listVpcEndpoints(vpcId),
    staleTime: 30_000,
  })
}

// ─── Instance attribute mutations ─────────────────────────────────────────

export function modifyInstanceTypeMutationOptions() {
  return mutationOptions({
    mutationKey: [...ec2Keys.instances(), "modify-type"] as const,
    mutationFn: (opts: { instanceId: string; instanceType: string }) =>
      ec2.modifyInstanceType(opts.instanceId, opts.instanceType),
  })
}

