---
title: "Local VPCs for CDK"
description: "CDK VPC lookups are designed for stable AWS environments. Local Overcast VPCs are torn down and recreated often, so their generated vpc-*, subnet-*, and rtb-* IDs change between deploys. The simplest way to avoid that churn is to let CDK create the local VPC..."
section: "CDK"
tags:
  - cdk
  - docs
  - local
  - vpc
  - vpcs
---

# Local VPCs for CDK

CDK VPC lookups are designed for stable AWS environments. Local Overcast VPCs are often torn down and recreated, so their generated `vpc-*`, `subnet-*`, and `rtb-*` IDs can change between deploys. Importing those IDs means keeping something outside CDK — a bootstrap script, a metadata file, or `cdk.context.json` — in sync with whatever the last teardown produced.

The simplest way out is to stop importing. Give the local environment its own stack that **creates** the VPC. CDK then owns the IDs, so there is nothing to look up, cache, or hand-maintain, and a teardown/redeploy cycle just produces new IDs that CDK already knows.

## Recommended Pattern

1. Put local-only resources in their own stack — a `LocalResourcesStack` that creates the VPC.
2. Have the local stage create that stack and pass its VPC to the application stacks.
3. Keep application stacks environment-agnostic: they accept an `ec2.IVpc` and never decide where it came from.
4. For real AWS environments, have the stage supply the VPC from a stack that calls `Vpc.fromLookup` instead.

The environment-specific decision lives in one place — which stack the stage creates — and every other stack is written once.

## The Local Resources Stack

```typescript
import * as cdk from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";
import type { Construct } from "constructs";

export class LocalResourcesStack extends cdk.Stack {
  readonly vpc: ec2.IVpc;

  constructor(scope: Construct, id: string, props?: cdk.StackProps) {
    super(scope, id, props);

    this.vpc = new ec2.Vpc(this, "Vpc", {
      maxAzs: 2,
      natGateways: 1,
      subnetConfiguration: [
        { name: "Public", subnetType: ec2.SubnetType.PUBLIC, cidrMask: 24 },
        {
          name: "Private",
          subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS,
          cidrMask: 24,
        },
      ],
    });
  }
}
```

This is an ordinary CDK stack, deployed by `cdk deploy` like any other. It uses only resource types Overcast supports through CloudFormation — `AWS::EC2::VPC`, `Subnet`, `RouteTable`, `Route`, `SubnetRouteTableAssociation`, `InternetGateway`, `VPCGatewayAttachment`, `NatGateway`, and `EIP`.

Because the VPC is created rather than imported, CDK also emits the subnet metadata it later relies on: each subnet is tagged `aws-cdk:subnet-type` and `aws-cdk:subnet-name`, public subnets get a `0.0.0.0/0` route to the internet gateway, and private subnets get one to the NAT gateway. Constructs that default to private subnets — scheduled Fargate tasks, VPC-attached Lambda functions — find a private-with-egress subnet group without any extra tagging work.

> Overcast stores NAT gateways and route tables as metadata. They are enough for CDK's subnet classification, but they do not imply real NAT data-plane routing. See [EC2 limitations](../services/ec2.md#limitations-and-divergences-from-aws).

## Wiring It Up In The Stage

The stage picks which stack supplies the VPC:

```typescript
class LocalStage extends cdk.Stage {
  constructor(scope: Construct, id: string) {
    // No env — see "Availability Zones" below.
    super(scope, id);

    const resources = new LocalResourcesStack(this, "LocalResources");

    new WorkerStack(this, "Worker", { vpc: resources.vpc });
    new ApiStack(this, "Api", { vpc: resources.vpc });
  }
}

class ProdStage extends cdk.Stage {
  constructor(scope: Construct, id: string, props: ProdStageProps) {
    super(scope, id, props);

    const network = new NetworkStack(this, "Network", { vpcId: props.vpcId });

    new WorkerStack(this, "Worker", { vpc: network.vpc });
    new ApiStack(this, "Api", { vpc: network.vpc });
  }
}
```

`NetworkStack` is the real-AWS counterpart, and it is just as small:

```typescript
export class NetworkStack extends cdk.Stack {
  readonly vpc: ec2.IVpc;

  constructor(scope: Construct, id: string, props: NetworkStackProps) {
    super(scope, id, props);

    // fromLookup needs a stack with a concrete account and region.
    this.vpc = ec2.Vpc.fromLookup(this, "Vpc", { vpcId: props.vpcId });
  }
}
```

Passing a VPC between stacks in the same stage is ordinary CDK: the consuming stack references it through `Fn::ImportValue`, and CDK adds the stack dependency for you. Overcast's CloudFormation engine resolves cross-stack exports — see the [CloudFormation service reference](../services/cloudformation.md).

## Application Stacks Stay Environment-Agnostic

```typescript
interface WorkerStackProps extends cdk.StackProps {
  vpc: ec2.IVpc;
}

class WorkerStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: WorkerStackProps) {
    super(scope, id, props);

    // Use props.vpc normally in Lambda, ECS, RDS, and other constructs.
  }
}
```

No `isLocal` flag, no branch on account or region, no local metadata file to load. The stack is identical in every environment; only the stage differs.

## Why Not Create The VPC At Stage Scope?

It is tempting to skip the extra stack and put the VPC directly in the stage:

```typescript
class LocalStage extends cdk.Stage {
  constructor(scope: Construct, id: string) {
    super(scope, id);

    // Do not do this: there is no Stack ancestor for the VPC.
    const vpc = new ec2.Vpc(this, "LocalVpc", { maxAzs: 2 });

    new WorkerStack(this, "Worker", { vpc });
  }
}
```

CDK rejects it:

```text
Vpc2 at 'Local/LocalVpc' should be created in the scope of a Stack, but no Stack found
```

A stage is an orchestration boundary, not a CloudFormation stack. VPC constructs — created or imported — hold stack-scoped tokens and must live under a `Stack`. `Vpc.fromVpcAttributes` at stage scope fails the same way. That is what the local resources stack is for: the stage owns the local/prod decision, a stack owns the constructs.

## Availability Zones

`ec2.Vpc` reads its availability zones from the stack, and the stack answers differently depending on whether it has a concrete environment:

- **Environment-agnostic** (no `env` on the stack or its stage) — CDK emits `Fn::GetAZs` intrinsics, which Overcast's CloudFormation engine evaluates directly. Nothing is looked up and `cdk.context.json` stays empty.
- **Environment-specific** (`env: { account, region }`) — CDK asks the `availability-zones` context provider instead, which assumes the bootstrap lookup role. If it cannot reach a bootstrapped Overcast, synth falls back to the placeholder zones `dummy1a`/`dummy1b`/`dummy1c` and bakes them into the template.

Leaving the local stage environment-agnostic is the smaller path. `cdk deploy` still resolves the target account and region from the active credentials, which for Overcast are the dummy `test`/`test` pair whose `sts:GetCallerIdentity` answers with the configured account and region — see [Using AWS CDK](../cdk.md).

If you do want a concrete `env` on the local stage, run synth against a bootstrapped Overcast so the lookup resolves: `DescribeAvailabilityZones` is supported and returns three zones per region. The resulting `availability-zones:*` context entry is stable across teardowns in a way `vpc-provider:*` entries are not.

## Importing A VPC Created Outside CDK

If something other than CDK creates the local VPC, the import options are:

- **`Vpc.fromLookup`** calls CDK's context provider and caches the result in `cdk.context.json`. The cache key includes account, region, and lookup filters. If a teardown recreates the VPC with new IDs, the file accumulates stale entries; CDK does not garbage-collect them. It fits a stable environment such as staging or production.
- **`Vpc.fromVpcAttributes`** takes the IDs directly, so nothing is cached — but something has to supply the current IDs on every deploy.

Both need a stack scope, so the stage cannot import the VPC itself. A small provider keeps the choice in the stage while the construct is still created in a stack:

```typescript
export interface VpcProvider {
  getVpc(scope: cdk.Stack): ec2.IVpc;
}

export class AttributesVpcProvider implements VpcProvider {
  constructor(private readonly attrs: ec2.VpcAttributes) {}

  getVpc(scope: cdk.Stack): ec2.IVpc {
    return ec2.Vpc.fromVpcAttributes(scope, "Vpc", this.attrs);
  }
}

export class LookupVpcProvider implements VpcProvider {
  constructor(private readonly vpcId: string) {}

  getVpc(scope: cdk.Stack): ec2.IVpc {
    return ec2.Vpc.fromLookup(scope, "Vpc", { vpcId: this.vpcId });
  }
}
```

Stacks then take a `VpcProvider` instead of an `ec2.IVpc` and call `props.vpcProvider.getVpc(this)` in the constructor. They stay environment-agnostic either way — the difference is only whether the stage hands over a VPC or a recipe for getting one.

For a VPC built outside CDK, the shape CDK expects is the one `ec2.Vpc` would have produced: public subnets tagged `aws-cdk:subnet-type=Public` and `aws-cdk:subnet-name=Public` with a default route to an internet gateway, and private subnets tagged `aws-cdk:subnet-type=Private` and `aws-cdk:subnet-name=Private` with a default route to a NAT gateway.

## Notes

- `cdk.context.json` is a lookup cache, not desired state. Committing `vpc-provider:*` entries for disposable local VPCs pins IDs that no longer exist.
- Overcast generates `vpc-*`, `subnet-*`, and `rtb-*` IDs the way real AWS does, so they are not selectable. A pattern that depends on knowing them in advance will not hold.
- `allowPublicSubnet: true` silences the Lambda warning but does not fix connectivity: Lambda functions in public subnets do not receive public IPs. A private-with-egress subnet is the placement that warning is asking for.

## Troubleshooting

If CDK reports no private subnet groups, check the tags and routes on the live VPC:

```bash
aws ec2 describe-subnets --filters "Name=vpc-id,Values=$VPC_ID"
```

```bash
aws ec2 describe-route-tables --filters "Name=vpc-id,Values=$VPC_ID"
```

A private subnet group needs both the private CDK tags and a route table with a default route to a `nat-*` gateway.

If CDK starts using stale VPC IDs, the lookup cache is holding entries for a VPC that no longer exists:

```bash
npx cdk context --clear
```

A local resources stack avoids the situation entirely: no lookup, so no cache to go stale.

## Related Docs

- [Using AWS CDK](../cdk.md) — bootstrap and deploy CDK stacks against Overcast.
- [EC2 / VPC service reference](../services/ec2.md) — VPC support, Docker-backed network behavior, and limitations.
- [CloudFormation service reference](../services/cloudformation.md) — supported resource provisioning through CDK/CloudFormation.
- [Using AWS SDKs and CLI](../sdk-cli.md) — endpoint and credential configuration for local AWS clients.
