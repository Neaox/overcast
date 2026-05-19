import { awsClients } from "../aws-clients"
import {
  DescribeInstancesCommand,
  RunInstancesCommand,
  TerminateInstancesCommand,
  StartInstancesCommand,
  StopInstancesCommand,
  ModifyInstanceAttributeCommand,
  DescribeVpcsCommand,
  CreateVpcCommand,
  DeleteVpcCommand,
  DescribeSecurityGroupsCommand,
  CreateSecurityGroupCommand,
  DeleteSecurityGroupCommand,
  DescribeSubnetsCommand,
  DescribeRouteTablesCommand,
  DescribeInternetGatewaysCommand,
  CreateInternetGatewayCommand,
  DeleteInternetGatewayCommand,
  AttachInternetGatewayCommand,
  DetachInternetGatewayCommand,
  DescribeVpcPeeringConnectionsCommand,
  CreateVpcPeeringConnectionCommand,
  AcceptVpcPeeringConnectionCommand,
  DeleteVpcPeeringConnectionCommand,
  AllocateAddressCommand,
  ReleaseAddressCommand,
  DescribeAddressesCommand,
  AssociateAddressCommand,
  DisassociateAddressCommand,
  CreateNatGatewayCommand,
  DescribeNatGatewaysCommand,
  DeleteNatGatewayCommand,
  DescribeVpcEndpointsCommand,
  type _InstanceType as Ec2InstanceType,
} from "@aws-sdk/client-ec2"
import type {
  Ec2Instance,
  Ec2Vpc,
  Ec2SecurityGroup,
  Ec2Subnet,
  Ec2RouteTable,
  Ec2InternetGateway,
  Ec2VpcPeeringConnection,
  Ec2ElasticIp,
  Ec2NatGateway,
  Ec2VpcEndpoint,
} from "@/types"

export const ec2 = {
  listInstances: async (): Promise<Ec2Instance[]> => {
    const res = await awsClients.ec2().send(new DescribeInstancesCommand({}))
    const instances: Ec2Instance[] = []
    for (const r of res.Reservations ?? []) {
      for (const i of r.Instances ?? []) {
        instances.push({
          instanceId: i.InstanceId ?? "",
          imageId: i.ImageId ?? "",
          instanceType: i.InstanceType ?? "",
          state: {
            code: i.State?.Code ?? 0,
            name: i.State?.Name ?? "unknown",
          },
          privateIpAddress: i.PrivateIpAddress,
          vpcId: i.VpcId,
          subnetId: i.SubnetId,
          securityGroups: i.SecurityGroups?.map((sg) => ({
            groupId: sg.GroupId ?? "",
            groupName: sg.GroupName ?? "",
          })),
          launchTime: i.LaunchTime?.toISOString(),
          tags: i.Tags?.map((t) => ({ key: t.Key ?? "", value: t.Value ?? "" })),
        })
      }
    }
    return instances
  },

  runInstances: async (opts: {
    imageId: string
    instanceType: string
    minCount: number
    maxCount: number
    subnetId?: string
    securityGroupIds?: string[]
  }): Promise<Ec2Instance[]> => {
    const res = await awsClients.ec2().send(
      new RunInstancesCommand({
        ImageId: opts.imageId,
        InstanceType: opts.instanceType as Ec2InstanceType,
        MinCount: opts.minCount,
        MaxCount: opts.maxCount,
        SubnetId: opts.subnetId,
        SecurityGroupIds: opts.securityGroupIds,
      }),
    )
    return (res.Instances ?? []).map((i) => ({
      instanceId: i.InstanceId ?? "",
      imageId: i.ImageId ?? "",
      instanceType: i.InstanceType ?? "",
      state: { code: i.State?.Code ?? 0, name: i.State?.Name ?? "pending" },
      privateIpAddress: i.PrivateIpAddress,
      vpcId: i.VpcId,
      subnetId: i.SubnetId,
      securityGroups: i.SecurityGroups?.map((sg) => ({
        groupId: sg.GroupId ?? "",
        groupName: sg.GroupName ?? "",
      })),
      launchTime: i.LaunchTime?.toISOString(),
      tags: i.Tags?.map((t) => ({ key: t.Key ?? "", value: t.Value ?? "" })),
    }))
  },

  terminateInstances: async (instanceIds: string[]): Promise<void> => {
    await awsClients.ec2().send(new TerminateInstancesCommand({ InstanceIds: instanceIds }))
  },

  startInstances: async (instanceIds: string[]): Promise<void> => {
    await awsClients.ec2().send(new StartInstancesCommand({ InstanceIds: instanceIds }))
  },

  stopInstances: async (instanceIds: string[]): Promise<void> => {
    await awsClients.ec2().send(new StopInstancesCommand({ InstanceIds: instanceIds }))
  },

  listVpcs: async (): Promise<Ec2Vpc[]> => {
    const res = await awsClients.ec2().send(new DescribeVpcsCommand({}))
    return (res.Vpcs ?? []).map((v) => {
      const tags = v.Tags?.map((t) => ({ key: t.Key ?? "", value: t.Value ?? "" }))
      const networkStatus = tags?.find((t) => t.key === "overcast:network-status")?.value
      return {
        vpcId: v.VpcId ?? "",
        cidrBlock: v.CidrBlock ?? "",
        state: v.State ?? "unknown",
        isDefault: v.IsDefault ?? false,
        networkStatus,
        tags,
      }
    })
  },

  createVpc: async (cidrBlock: string): Promise<Ec2Vpc> => {
    const res = await awsClients.ec2().send(new CreateVpcCommand({ CidrBlock: cidrBlock }))
    const v = res.Vpc!
    return {
      vpcId: v.VpcId ?? "",
      cidrBlock: v.CidrBlock ?? "",
      state: v.State ?? "available",
      isDefault: v.IsDefault ?? false,
    }
  },

  deleteVpc: async (vpcId: string): Promise<void> => {
    await awsClients.ec2().send(new DeleteVpcCommand({ VpcId: vpcId }))
  },

  listSecurityGroups: async (): Promise<Ec2SecurityGroup[]> => {
    const res = await awsClients.ec2().send(new DescribeSecurityGroupsCommand({}))
    return (res.SecurityGroups ?? []).map((sg) => ({
      groupId: sg.GroupId ?? "",
      groupName: sg.GroupName ?? "",
      description: sg.Description ?? "",
      vpcId: sg.VpcId,
      ipPermissions: (sg.IpPermissions ?? []).map((p) => ({
        ipProtocol: p.IpProtocol ?? "",
        fromPort: p.FromPort,
        toPort: p.ToPort,
        ipRanges: p.IpRanges?.map((r) => ({ cidrIp: r.CidrIp ?? "" })),
      })),
      ipPermissionsEgress: (sg.IpPermissionsEgress ?? []).map((p) => ({
        ipProtocol: p.IpProtocol ?? "",
        fromPort: p.FromPort,
        toPort: p.ToPort,
        ipRanges: p.IpRanges?.map((r) => ({ cidrIp: r.CidrIp ?? "" })),
      })),
    }))
  },

  createSecurityGroup: async (opts: {
    groupName: string
    description: string
    vpcId?: string
  }): Promise<string> => {
    const res = await awsClients.ec2().send(
      new CreateSecurityGroupCommand({
        GroupName: opts.groupName,
        Description: opts.description,
        VpcId: opts.vpcId,
      }),
    )
    return res.GroupId ?? ""
  },

  deleteSecurityGroup: async (groupId: string): Promise<void> => {
    await awsClients.ec2().send(new DeleteSecurityGroupCommand({ GroupId: groupId }))
  },

  listSubnets: async (): Promise<Ec2Subnet[]> => {
    const res = await awsClients.ec2().send(new DescribeSubnetsCommand({}))
    return (res.Subnets ?? []).map((s) => ({
      subnetId: s.SubnetId ?? "",
      vpcId: s.VpcId ?? "",
      cidrBlock: s.CidrBlock ?? "",
      availabilityZone: s.AvailabilityZone ?? "",
    }))
  },

  listRouteTables: async (): Promise<Ec2RouteTable[]> => {
    const res = await awsClients.ec2().send(new DescribeRouteTablesCommand({}))
    return (res.RouteTables ?? []).map((rt) => ({
      routeTableId: rt.RouteTableId ?? "",
      vpcId: rt.VpcId ?? "",
      routes: (rt.Routes ?? []).map((r) => ({
        destinationCidrBlock: r.DestinationCidrBlock ?? "",
        gatewayId: r.GatewayId,
        origin: r.Origin ?? "",
      })),
      associations: (rt.Associations ?? []).map((a) => ({
        associationId: a.RouteTableAssociationId ?? "",
        routeTableId: a.RouteTableId ?? "",
        subnetId: a.SubnetId,
        main: a.Main ?? false,
      })),
    }))
  },

  listInternetGateways: async (): Promise<Ec2InternetGateway[]> => {
    const res = await awsClients.ec2().send(new DescribeInternetGatewaysCommand({}))
    return (res.InternetGateways ?? []).map((igw) => ({
      internetGatewayId: igw.InternetGatewayId ?? "",
      attachments: (igw.Attachments ?? []).map((a) => ({
        vpcId: a.VpcId ?? "",
        state: a.State ?? "",
      })),
    }))
  },

  createInternetGateway: async (): Promise<Ec2InternetGateway> => {
    const res = await awsClients.ec2().send(new CreateInternetGatewayCommand({}))
    const igw = res.InternetGateway!
    return {
      internetGatewayId: igw.InternetGatewayId ?? "",
      attachments: (igw.Attachments ?? []).map((a) => ({
        vpcId: a.VpcId ?? "",
        state: a.State ?? "",
      })),
    }
  },

  deleteInternetGateway: async (internetGatewayId: string): Promise<void> => {
    await awsClients
      .ec2()
      .send(new DeleteInternetGatewayCommand({ InternetGatewayId: internetGatewayId }))
  },

  attachInternetGateway: async (internetGatewayId: string, vpcId: string): Promise<void> => {
    await awsClients
      .ec2()
      .send(
        new AttachInternetGatewayCommand({ InternetGatewayId: internetGatewayId, VpcId: vpcId }),
      )
  },

  detachInternetGateway: async (internetGatewayId: string, vpcId: string): Promise<void> => {
    await awsClients
      .ec2()
      .send(
        new DetachInternetGatewayCommand({ InternetGatewayId: internetGatewayId, VpcId: vpcId }),
      )
  },

  listVpcPeeringConnections: async (): Promise<Ec2VpcPeeringConnection[]> => {
    const res = await awsClients.ec2().send(new DescribeVpcPeeringConnectionsCommand({}))
    return (res.VpcPeeringConnections ?? []).map((pcx) => ({
      vpcPeeringConnectionId: pcx.VpcPeeringConnectionId ?? "",
      requesterVpcInfo: {
        vpcId: pcx.RequesterVpcInfo?.VpcId ?? "",
        ownerId: pcx.RequesterVpcInfo?.OwnerId ?? "",
        cidrBlock: pcx.RequesterVpcInfo?.CidrBlock ?? "",
        region: pcx.RequesterVpcInfo?.Region ?? "",
      },
      accepterVpcInfo: {
        vpcId: pcx.AccepterVpcInfo?.VpcId ?? "",
        ownerId: pcx.AccepterVpcInfo?.OwnerId ?? "",
        cidrBlock: pcx.AccepterVpcInfo?.CidrBlock ?? "",
        region: pcx.AccepterVpcInfo?.Region ?? "",
      },
      status: {
        code: pcx.Status?.Code ?? "",
        message: pcx.Status?.Message ?? "",
      },
    }))
  },

  createVpcPeeringConnection: async (
    vpcId: string,
    peerVpcId: string,
  ): Promise<Ec2VpcPeeringConnection> => {
    const res = await awsClients
      .ec2()
      .send(new CreateVpcPeeringConnectionCommand({ VpcId: vpcId, PeerVpcId: peerVpcId }))
    const pcx = res.VpcPeeringConnection!
    return {
      vpcPeeringConnectionId: pcx.VpcPeeringConnectionId ?? "",
      requesterVpcInfo: {
        vpcId: pcx.RequesterVpcInfo?.VpcId ?? "",
        ownerId: pcx.RequesterVpcInfo?.OwnerId ?? "",
        cidrBlock: pcx.RequesterVpcInfo?.CidrBlock ?? "",
        region: pcx.RequesterVpcInfo?.Region ?? "",
      },
      accepterVpcInfo: {
        vpcId: pcx.AccepterVpcInfo?.VpcId ?? "",
        ownerId: pcx.AccepterVpcInfo?.OwnerId ?? "",
        cidrBlock: pcx.AccepterVpcInfo?.CidrBlock ?? "",
        region: pcx.AccepterVpcInfo?.Region ?? "",
      },
      status: {
        code: pcx.Status?.Code ?? "",
        message: pcx.Status?.Message ?? "",
      },
    }
  },

  acceptVpcPeeringConnection: async (pcxId: string): Promise<void> => {
    await awsClients
      .ec2()
      .send(new AcceptVpcPeeringConnectionCommand({ VpcPeeringConnectionId: pcxId }))
  },

  deleteVpcPeeringConnection: async (pcxId: string): Promise<void> => {
    await awsClients
      .ec2()
      .send(new DeleteVpcPeeringConnectionCommand({ VpcPeeringConnectionId: pcxId }))
  },

  // ─── Elastic IPs ──────────────────────────────────────────────────────────

  listElasticIps: async (): Promise<Ec2ElasticIp[]> => {
    const res = await awsClients.ec2().send(new DescribeAddressesCommand({}))
    return (res.Addresses ?? []).map((a) => ({
      allocationId: a.AllocationId ?? "",
      publicIp: a.PublicIp ?? "",
      domain: a.Domain ?? "vpc",
      associationId: a.AssociationId,
      instanceId: a.InstanceId,
      networkInterfaceId: a.NetworkInterfaceId,
      privateIpAddress: a.PrivateIpAddress,
    }))
  },

  allocateAddress: async (): Promise<Ec2ElasticIp> => {
    const res = await awsClients.ec2().send(new AllocateAddressCommand({ Domain: "vpc" }))
    return {
      allocationId: res.AllocationId ?? "",
      publicIp: res.PublicIp ?? "",
      domain: res.Domain ?? "vpc",
    }
  },

  releaseAddress: async (allocationId: string): Promise<void> => {
    await awsClients.ec2().send(new ReleaseAddressCommand({ AllocationId: allocationId }))
  },

  associateAddress: async (opts: {
    allocationId: string
    instanceId?: string
  }): Promise<string> => {
    const res = await awsClients.ec2().send(
      new AssociateAddressCommand({
        AllocationId: opts.allocationId,
        InstanceId: opts.instanceId,
      }),
    )
    return res.AssociationId ?? ""
  },

  disassociateAddress: async (associationId: string): Promise<void> => {
    await awsClients.ec2().send(new DisassociateAddressCommand({ AssociationId: associationId }))
  },

  // ─── NAT Gateways ─────────────────────────────────────────────────────────

  listNatGateways: async (): Promise<Ec2NatGateway[]> => {
    const res = await awsClients.ec2().send(new DescribeNatGatewaysCommand({}))
    return (res.NatGateways ?? []).map((gw) => ({
      natGatewayId: gw.NatGatewayId ?? "",
      subnetId: gw.SubnetId ?? "",
      vpcId: gw.VpcId ?? "",
      state: gw.State ?? "unknown",
      allocationId: gw.NatGatewayAddresses?.[0]?.AllocationId,
      publicIp: gw.NatGatewayAddresses?.[0]?.PublicIp,
      privateIp: gw.NatGatewayAddresses?.[0]?.PrivateIp,
      createTime: gw.CreateTime?.toISOString(),
      tags: gw.Tags?.map((t) => ({ key: t.Key ?? "", value: t.Value ?? "" })),
    }))
  },

  createNatGateway: async (opts: {
    subnetId: string
    allocationId: string
  }): Promise<Ec2NatGateway> => {
    const res = await awsClients.ec2().send(
      new CreateNatGatewayCommand({
        SubnetId: opts.subnetId,
        AllocationId: opts.allocationId,
      }),
    )
    const gw = res.NatGateway!
    return {
      natGatewayId: gw.NatGatewayId ?? "",
      subnetId: gw.SubnetId ?? "",
      vpcId: gw.VpcId ?? "",
      state: gw.State ?? "pending",
      allocationId: gw.NatGatewayAddresses?.[0]?.AllocationId,
      publicIp: gw.NatGatewayAddresses?.[0]?.PublicIp,
      privateIp: gw.NatGatewayAddresses?.[0]?.PrivateIp,
      createTime: gw.CreateTime?.toISOString(),
    }
  },

  deleteNatGateway: async (natGatewayId: string): Promise<void> => {
    await awsClients.ec2().send(new DeleteNatGatewayCommand({ NatGatewayId: natGatewayId }))
  },

  // ─── Instance attributes ──────────────────────────────────────────────────

  modifyInstanceType: async (instanceId: string, instanceType: string): Promise<void> => {
    await awsClients.ec2().send(
      new ModifyInstanceAttributeCommand({
        InstanceId: instanceId,
        InstanceType: { Value: instanceType },
      }),
    )
  },

  // ─── VPC Endpoints ────────────────────────────────────────────────────────

  listVpcEndpoints: async (vpcId?: string): Promise<Ec2VpcEndpoint[]> => {
    const filters = vpcId ? [{ Name: "vpc-id", Values: [vpcId] }] : undefined
    const res = await awsClients.ec2().send(new DescribeVpcEndpointsCommand({ Filters: filters }))
    return (res.VpcEndpoints ?? []).map((ep) => ({
      vpcEndpointId: ep.VpcEndpointId ?? "",
      vpcId: ep.VpcId ?? "",
      serviceName: ep.ServiceName ?? "",
      state: ep.State ?? "unknown",
      vpcEndpointType: ep.VpcEndpointType ?? "Gateway",
    }))
  },
}
