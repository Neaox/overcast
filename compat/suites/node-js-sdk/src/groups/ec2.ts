/**
 * groups/ec2.ts — EC2 and VPC compatibility test groups for the Node.js suite.
 *
 * Status: NOT implemented in Overcast. All tests expected to fail with 501.
 * These tests define the coverage target for future EC2 and VPC implementation.
 *
 * Groups:
 *   ec2-instances             — instance lifecycle (describe, run, stop, start, terminate)
 *   ec2-vpc                   — VPC and subnet management
 *   ec2-security-group-rules  — security group ingress/egress rules
 *   ec2-keypairs              — key pair lifecycle
 */

import {
  DescribeInstancesCommand,
  DescribeInstanceTypesCommand,
  DescribeRegionsCommand,
  DescribeAvailabilityZonesCommand,
  DescribeImagesCommand,
  DescribeVpcsCommand,
  DescribeVpnGatewaysCommand,
  CreateVpcCommand,
  DeleteVpcCommand,
  CreateSubnetCommand,
  DeleteSubnetCommand,
  DescribeSubnetsCommand,
  CreateSecurityGroupCommand,
  DeleteSecurityGroupCommand,
  RunInstancesCommand,
  StopInstancesCommand,
  StartInstancesCommand,
  TerminateInstancesCommand,
  AuthorizeSecurityGroupIngressCommand,
  RevokeSecurityGroupIngressCommand,
  DescribeSecurityGroupsCommand,
  CreateKeyPairCommand,
  DescribeKeyPairsCommand,
  DeleteKeyPairCommand,
  CreateInternetGatewayCommand,
  AttachInternetGatewayCommand,
  DetachInternetGatewayCommand,
  DeleteInternetGatewayCommand,
} from "@aws-sdk/client-ec2";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeEC2Groups(suite: string): TestGroup[] {
  return [
    // ── ec2-instances ──────────────────────────────────────────────────────
    {
      suite,
      service: "ec2",
      name: "ec2-instances",
      tests: [
        {
          name: "DescribeRegions",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const resp = await ec2.send(new DescribeRegionsCommand({}));
            assert.ok(
              resp.Regions?.length,
              "DescribeRegions: empty Regions list",
            );
          },
        },
        {
          name: "DescribeAvailabilityZones",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            await ec2.send(new DescribeAvailabilityZonesCommand({}));
          },
        },
        {
          name: "DescribeInstanceTypes",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            await ec2.send(new DescribeInstanceTypesCommand({}));
          },
        },
        {
          name: "DescribeImages",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const resp = await ec2.send(new DescribeImagesCommand({}));
            assert.ok(resp.Images, "DescribeImages: missing Images");
          },
        },
        {
          name: "RunInstances",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const resp = await ec2.send(
              new RunInstancesCommand({
                ImageId: "ami-00000000",
                InstanceType: "t2.micro",
                MinCount: 1,
                MaxCount: 1,
              }),
            );
            assert.ok(
              resp.Instances?.length,
              "RunInstances: no instances returned",
            );
            assert.ok(
              resp.Instances[0].InstanceId,
              "RunInstances: missing InstanceId",
            );
            (ctx as Record<string, unknown>)["_instanceId"] =
              resp.Instances[0].InstanceId;
          },
        },
        {
          name: "DescribeInstances",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const instanceId = (ctx as Record<string, unknown>)[
              "_instanceId"
            ] as string;
            assert.ok(
              instanceId,
              "DescribeInstances: no instance from RunInstances",
            );
            const resp = await ec2.send(
              new DescribeInstancesCommand({
                InstanceIds: [instanceId],
              }),
            );
            assert.ok(
              resp.Reservations?.length,
              "DescribeInstances: no reservations returned",
            );
            assert.ok(
              resp.Reservations[0].Instances?.length,
              "DescribeInstances: no instances in reservation",
            );
          },
        },
        {
          name: "StopInstances",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const instanceId = (ctx as Record<string, unknown>)[
              "_instanceId"
            ] as string;
            assert.ok(
              instanceId,
              "StopInstances: no instance from RunInstances",
            );
            const resp = await ec2.send(
              new StopInstancesCommand({ InstanceIds: [instanceId] }),
            );
            assert.ok(
              resp.StoppingInstances?.length,
              "StopInstances: no StoppingInstances returned",
            );
            assert.ok(
              resp.StoppingInstances[0].CurrentState,
              "StopInstances: missing CurrentState",
            );
          },
        },
        {
          name: "StartInstances",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const instanceId = (ctx as Record<string, unknown>)[
              "_instanceId"
            ] as string;
            assert.ok(
              instanceId,
              "StartInstances: no instance from RunInstances",
            );
            const resp = await ec2.send(
              new StartInstancesCommand({ InstanceIds: [instanceId] }),
            );
            assert.ok(
              resp.StartingInstances?.length,
              "StartInstances: no StartingInstances returned",
            );
            assert.ok(
              resp.StartingInstances[0].CurrentState,
              "StartInstances: missing CurrentState",
            );
          },
        },
        {
          name: "TerminateInstances",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const instanceId = (ctx as Record<string, unknown>)[
              "_instanceId"
            ] as string;
            assert.ok(
              instanceId,
              "TerminateInstances: no instance from RunInstances",
            );
            const resp = await ec2.send(
              new TerminateInstancesCommand({ InstanceIds: [instanceId] }),
            );
            assert.ok(
              resp.TerminatingInstances?.length,
              "TerminateInstances: no TerminatingInstances returned",
            );
            assert.ok(
              resp.TerminatingInstances[0].PreviousState,
              "TerminateInstances: missing PreviousState",
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { ec2 } = makeClients(ctx);
        const instanceId = (ctx as Record<string, unknown>)[
          "_instanceId"
        ] as string;
        if (instanceId) {
          try {
            await ec2.send(
              new TerminateInstancesCommand({ InstanceIds: [instanceId] }),
            );
          } catch {}
        }
      },
    },
    // ── ec2-vpc ────────────────────────────────────────────────────────────
    {
      suite,
      service: "ec2",
      name: "ec2-vpc",
      tests: [
        {
          name: "CreateVpc",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const resp = await ec2.send(
              new CreateVpcCommand({ CidrBlock: "10.0.0.0/16" }),
            );
            assert.ok(resp.Vpc?.VpcId, "CreateVpc: missing VpcId");
            (ctx as Record<string, unknown>)["_vpcId"] = resp.Vpc.VpcId;
          },
        },
        {
          name: "DescribeVpcs",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const resp = await ec2.send(new DescribeVpcsCommand({}));
            assert.notStrictEqual(
              resp.Vpcs,
              undefined,
              "DescribeVpcs: missing Vpcs",
            );
          },
        },
        {
          name: "DescribeVpnGateways",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const vpcId = (ctx as Record<string, unknown>)["_vpcId"] as string;
            assert.ok(vpcId, "DescribeVpnGateways: no VPC from CreateVpc");
            const resp = await ec2.send(
              new DescribeVpnGatewaysCommand({
                Filters: [
                  { Name: "attachment.vpc-id", Values: [vpcId] },
                  { Name: "attachment.state", Values: ["attached"] },
                  { Name: "state", Values: ["available"] },
                ],
              }),
            );
            assert.deepStrictEqual(
              resp.VpnGateways ?? [],
              [],
              "DescribeVpnGateways: expected no VPN gateways",
            );
          },
        },
        {
          name: "CreateSubnet",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const vpcId = (ctx as Record<string, unknown>)["_vpcId"] as string;
            assert.ok(vpcId, "CreateSubnet: no VPC from CreateVpc");
            const resp = await ec2.send(
              new CreateSubnetCommand({
                VpcId: vpcId,
                CidrBlock: "10.0.1.0/24",
              }),
            );
            assert.ok(resp.Subnet?.SubnetId, "CreateSubnet: missing SubnetId");
            (ctx as Record<string, unknown>)["_subnetId"] =
              resp.Subnet.SubnetId;
          },
        },
        {
          name: "CreateSecurityGroup",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const vpcId = (ctx as Record<string, unknown>)["_vpcId"] as string;
            assert.ok(vpcId, "CreateSecurityGroup: no VPC from CreateVpc");
            const resp = await ec2.send(
              new CreateSecurityGroupCommand({
                GroupName: `compat-${ctx.runId}`,
                Description: "compat test group",
                VpcId: vpcId,
              }),
            );
            assert.ok(resp.GroupId, "CreateSecurityGroup: missing GroupId");
            (ctx as Record<string, unknown>)["_sgId"] = resp.GroupId;
          },
        },
        {
          name: "DeleteSecurityGroup",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const sgId = (ctx as Record<string, unknown>)["_sgId"] as string;
            if (!sgId) return;
            await ec2.send(new DeleteSecurityGroupCommand({ GroupId: sgId }));
          },
        },
        {
          name: "DescribeSubnets",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const subnetId = (ctx as Record<string, unknown>)[
              "_subnetId"
            ] as string;
            assert.ok(subnetId, "DescribeSubnets: no subnet from CreateSubnet");
            const resp = await ec2.send(
              new DescribeSubnetsCommand({ SubnetIds: [subnetId] }),
            );
            assert.ok(
              resp.Subnets?.length,
              "DescribeSubnets: no subnets returned",
            );
            assert.strictEqual(
              resp.Subnets[0].SubnetId,
              subnetId,
              `DescribeSubnets: expected SubnetId ${subnetId}`,
            );
          },
        },
        {
          name: "CreateInternetGateway",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const resp = await ec2.send(new CreateInternetGatewayCommand({}));
            assert.ok(
              resp.InternetGateway?.InternetGatewayId,
              "CreateInternetGateway: missing InternetGatewayId",
            );
            (ctx as Record<string, unknown>)["_igwId"] =
              resp.InternetGateway.InternetGatewayId;
          },
        },
        {
          name: "AttachInternetGateway",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const vpcId = (ctx as Record<string, unknown>)["_vpcId"] as string;
            const igwId = (ctx as Record<string, unknown>)["_igwId"] as string;
            assert.ok(
              vpcId && igwId,
              "AttachInternetGateway: missing VPC or IGW",
            );
            await ec2.send(
              new AttachInternetGatewayCommand({
                InternetGatewayId: igwId,
                VpcId: vpcId,
              }),
            );
          },
        },
        {
          name: "DeleteSubnet",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const subnetId = (ctx as Record<string, unknown>)[
              "_subnetId"
            ] as string;
            if (!subnetId) return;
            await ec2.send(new DeleteSubnetCommand({ SubnetId: subnetId }));
          },
        },
        {
          name: "DeleteVpc",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const vpcId = (ctx as Record<string, unknown>)["_vpcId"] as string;
            if (!vpcId) return;
            await ec2.send(new DeleteVpcCommand({ VpcId: vpcId }));
            const resp = await ec2.send(new DescribeVpcsCommand({}));
            assert.notStrictEqual(
              resp.Vpcs?.some((v) => v.VpcId, vpcId),
              `DeleteVpc: VPC ${vpcId} still present after delete`,
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { ec2 } = makeClients(ctx);
        // Delete in reverse creation order: security group, internet gateway, subnet, VPC
        const sgId = (ctx as Record<string, unknown>)["_sgId"] as string;
        if (sgId) {
          try {
            await ec2.send(new DeleteSecurityGroupCommand({ GroupId: sgId }));
          } catch {}
        }
        const igwId = (ctx as Record<string, unknown>)["_igwId"] as string;
        const vpcId = (ctx as Record<string, unknown>)["_vpcId"] as string;
        if (igwId && vpcId) {
          try {
            await ec2.send(
              new DetachInternetGatewayCommand({
                InternetGatewayId: igwId,
                VpcId: vpcId,
              }),
            );
          } catch {}
          try {
            await ec2.send(
              new DeleteInternetGatewayCommand({ InternetGatewayId: igwId }),
            );
          } catch {}
        }
        const subnetId = (ctx as Record<string, unknown>)[
          "_subnetId"
        ] as string;
        if (subnetId) {
          try {
            await ec2.send(new DeleteSubnetCommand({ SubnetId: subnetId }));
          } catch {}
        }
        if (vpcId) {
          try {
            await ec2.send(new DeleteVpcCommand({ VpcId: vpcId }));
          } catch {}
        }
      },
    },

    // ── ec2-security-group-rules ───────────────────────────────────────────
    {
      suite,
      service: "ec2",
      name: "ec2-security-group-rules",
      tests: [
        {
          name: "AuthorizeSecurityGroupIngress",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const sgId = (ctx as Record<string, unknown>)[
              "_sgRulesId"
            ] as string;
            assert.ok(sgId, "AuthorizeSecurityGroupIngress: no SG from setup");
            await ec2.send(
              new AuthorizeSecurityGroupIngressCommand({
                GroupId: sgId,
                IpPermissions: [
                  {
                    IpProtocol: "tcp",
                    FromPort: 443,
                    ToPort: 443,
                    IpRanges: [{ CidrIp: "10.0.0.0/8" }],
                  },
                ],
              }),
            );
          },
        },
        {
          name: "DescribeSecurityGroups",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const sgId = (ctx as Record<string, unknown>)[
              "_sgRulesId"
            ] as string;
            assert.ok(sgId, "DescribeSecurityGroups: no SG from setup");
            const resp = await ec2.send(
              new DescribeSecurityGroupsCommand({
                GroupIds: [sgId],
              }),
            );
            assert.ok(
              resp.SecurityGroups?.length,
              "DescribeSecurityGroups: no security groups returned",
            );
            const sg = resp.SecurityGroups[0];
            assert.ok(
              sg.IpPermissions?.some(
                (p) =>
                  p.IpProtocol === "tcp" &&
                  p.FromPort === 443 &&
                  p.ToPort === 443,
              ),
              "DescribeSecurityGroups: ingress rule not found",
            );
          },
        },
        {
          name: "RevokeSecurityGroupIngress",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const sgId = (ctx as Record<string, unknown>)[
              "_sgRulesId"
            ] as string;
            assert.ok(sgId, "RevokeSecurityGroupIngress: no SG from setup");
            await ec2.send(
              new RevokeSecurityGroupIngressCommand({
                GroupId: sgId,
                IpPermissions: [
                  {
                    IpProtocol: "tcp",
                    FromPort: 443,
                    ToPort: 443,
                    IpRanges: [{ CidrIp: "10.0.0.0/8" }],
                  },
                ],
              }),
            );
          },
        },
      ],
      setup: async (ctx) => {
        const { ec2 } = makeClients(ctx);
        const vpcResp = await ec2.send(
          new CreateVpcCommand({ CidrBlock: "10.100.0.0/16" }),
        );
        (ctx as Record<string, unknown>)["_sgRulesVpcId"] = vpcResp.Vpc!.VpcId!;
        const sgResp = await ec2.send(
          new CreateSecurityGroupCommand({
            GroupName: `compat-sgrules-${ctx.runId}`,
            Description: "compat SG rules test",
            VpcId: vpcResp.Vpc!.VpcId!,
          }),
        );
        (ctx as Record<string, unknown>)["_sgRulesId"] = sgResp.GroupId!;
      },
      teardown: async (ctx) => {
        const { ec2 } = makeClients(ctx);
        const sgId = (ctx as Record<string, unknown>)["_sgRulesId"] as string;
        if (sgId) {
          try {
            await ec2.send(new DeleteSecurityGroupCommand({ GroupId: sgId }));
          } catch {}
        }
        const vpcId = (ctx as Record<string, unknown>)[
          "_sgRulesVpcId"
        ] as string;
        if (vpcId) {
          try {
            await ec2.send(new DeleteVpcCommand({ VpcId: vpcId }));
          } catch {}
        }
      },
    },

    // ── ec2-keypairs ───────────────────────────────────────────────────────
    {
      suite,
      service: "ec2",
      name: "ec2-keypairs",
      tests: [
        {
          name: "CreateKeyPair",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const keyName = `compat-${ctx.runId}`;
            const resp = await ec2.send(
              new CreateKeyPairCommand({ KeyName: keyName }),
            );
            assert.ok(resp.KeyPairId, "CreateKeyPair: missing KeyPairId");
            assert.strictEqual(
              resp.KeyName,
              keyName,
              `CreateKeyPair: expected KeyName ${keyName}, got ${resp.KeyName}`,
            );
            (ctx as Record<string, unknown>)["_keyName"] = keyName;
          },
        },
        {
          name: "DescribeKeyPairs",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const keyName = (ctx as Record<string, unknown>)[
              "_keyName"
            ] as string;
            assert.ok(keyName, "DescribeKeyPairs: no key from CreateKeyPair");
            const resp = await ec2.send(
              new DescribeKeyPairsCommand({ KeyNames: [keyName] }),
            );
            assert.ok(
              resp.KeyPairs?.length,
              "DescribeKeyPairs: no key pairs returned",
            );
            assert.strictEqual(
              resp.KeyPairs[0].KeyName,
              keyName,
              `DescribeKeyPairs: expected KeyName ${keyName}`,
            );
          },
        },
        {
          name: "DeleteKeyPair",
          fn: async (ctx) => {
            const { ec2 } = makeClients(ctx);
            const keyName = (ctx as Record<string, unknown>)[
              "_keyName"
            ] as string;
            if (!keyName) return;
            await ec2.send(new DeleteKeyPairCommand({ KeyName: keyName }));
          },
        },
      ],
      teardown: async (ctx) => {
        const { ec2 } = makeClients(ctx);
        const keyName = (ctx as Record<string, unknown>)["_keyName"] as string;
        if (keyName) {
          try {
            await ec2.send(new DeleteKeyPairCommand({ KeyName: keyName }));
          } catch {}
        }
      },
    },
  ];
}
