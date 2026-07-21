---
title: "Local VPCs for CDK"
description: "CDK VPC lookups are designed for stable AWS environments. Local Overcast VPCs are often torn down and recreated, so their generated vpc-*, subnet-*, and rtb-* IDs can change..."
section: "CDK"
tags:
  - cdk
  - docs
  - local
  - vpc
  - vpcs
---

# Local VPCs for CDK

CDK VPC lookups are designed for stable AWS environments. Local Overcast VPCs are often torn down and recreated, so their generated `vpc-*`, `subnet-*`, and `rtb-*` IDs can change between deploys. This page describes the recommended pattern for keeping local CDK deployments clean without adding local-only conditionals throughout application stacks.

## Recommended Pattern

Treat the local VPC as environment bootstrap state:

1. Create or repair the local VPC with a bootstrap script, using stable tags or names to find existing resources.
2. Write the current VPC, subnet, and route table IDs to an ignored local metadata file.
3. Let the CDK stage choose a VPC provider based on environment.
4. Let each stack call that provider inside the stack constructor.
5. Use `Vpc.fromLookup` for real AWS environments and `Vpc.fromVpcAttributes` for Overcast.

This keeps local-specific decisions in the stage while still satisfying CDK's requirement that imported VPC constructs are created under a stack.

## Why Not `fromLookup` Locally?

`Vpc.fromLookup` calls CDK's context provider and caches the result in `cdk.context.json`. The cache key includes account, region, and lookup filters such as VPC ID. If local teardown recreates the VPC with new IDs, the context file accumulates stale entries.

That is normal CDK behavior. CDK does not garbage-collect old lookup entries, and Overcast should not make EC2 IDs user-selectable just to stabilize this cache. Real AWS does not let callers choose VPC, subnet, or route table IDs.

Use `fromLookup` when the looked-up environment is stable, such as staging or production. Use `fromVpcAttributes` when local bootstrap already knows the exact current IDs.

## Why Not Create the VPC at Stage Scope?

It is tempting to centralize the VPC import directly in the stage:

```typescript
class LocalStage extends cdk.Stage {
  constructor(scope: Construct, id: string, props: LocalStageProps) {
    super(scope, id, props);

    // Do not do this: there is no Stack ancestor for the imported VPC.
    const vpc = ec2.Vpc.fromVpcAttributes(this, "LocalVpc", props.vpcAttrs);

    const myFirstStack = new MyStack(this, "MyFirstStack", { vpc });
    const mySecondStack = new MySecondStack(this, "MySecondStack", { vpc });
  }
}
```

CDK rejects this with an error like:

```text
ImportedVpc2 at 'Local/LocalVpc' should be created in the scope of a Stack, but no Stack found
```

A stage is an orchestration boundary, not a CloudFormation stack. Imported VPC constructs contain stack-scoped tokens and must be created under a `Stack`.

The clean compromise is a stage-owned provider or factory. The stage owns the local/prod decision; the stack owns the actual imported construct.

## VPC Provider Pattern

Define a small provider interface:

```typescript
import * as cdk from "aws-cdk-lib";
import * as ec2 from "aws-cdk-lib/aws-ec2";

export interface VpcProvider {
  getVpc(scope: cdk.Stack): ec2.IVpc;
}

export class LocalVpcProvider implements VpcProvider {
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

The stage chooses the provider:

```typescript
class AppStage extends cdk.Stage {
  constructor(scope: Construct, id: string, props: AppStageProps) {
    super(scope, id, props);

    const vpcProvider = props.isLocal
      ? new LocalVpcProvider(props.localVpcAttrs)
      : new LookupVpcProvider(props.vpcId);

    new WorkerStack(this, "WorkerStack", {
      env: props.env,
      vpcProvider,
    });
  }
}
```

Stacks stay environment-agnostic:

```typescript
interface WorkerStackProps extends cdk.StackProps {
  vpcProvider: VpcProvider;
}

class WorkerStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: WorkerStackProps) {
    super(scope, id, props);

    const vpc = props.vpcProvider.getVpc(this);

    // Use vpc normally in Lambda, ECS, RDS, and other constructs.
  }
}
```

## Local Metadata File

The bootstrap script should write a file that is ignored by git, for example `.overcast/local-vpc.json`:

```json
{
  "vpcId": "vpc-28971700",
  "availabilityZones": ["ap-southeast-2a", "ap-southeast-2b"],
  "privateSubnetIds": ["subnet-aa4670ad", "subnet-87b56dcd"],
  "privateSubnetRouteTableIds": ["rtb-e410f38c", "rtb-e410f38c"],
  "publicSubnetIds": ["subnet-2625bedb"],
  "publicSubnetRouteTableIds": ["rtb-654955d5"]
}
```

Add it to `.gitignore`:

```gitignore
.overcast/local-vpc.json
```

Then load it in the stage setup code and pass it to `LocalVpcProvider`.

## Bootstrap Requirements

For constructs that default to private subnets, such as scheduled Fargate tasks, local bootstrap should create a VPC shape that CDK recognizes as private-with-egress:

- One or more public subnets tagged with `aws-cdk:subnet-type=Public` and `aws-cdk:subnet-name=Public`.
- One or more private subnets tagged with `aws-cdk:subnet-type=Private` and `aws-cdk:subnet-name=Private`.
- A public route table associated with public subnets and a `0.0.0.0/0` route to an internet gateway.
- A private route table associated with private subnets and a `0.0.0.0/0` route to a NAT gateway.

Overcast stores NAT gateways and route tables as metadata. They are enough for CDK subnet classification, but they do not imply real NAT data-plane routing. See [EC2 limitations](../services/ec2.md#limitations-and-divergences-from-aws).

## What Not To Do

- Do not commit local `vpc-provider:*` context entries for disposable Overcast VPCs.
- Do not make Overcast recreate resources from `cdk.context.json`; that file is a lookup cache, not desired state.
- Do not add `allowPublicSubnet: true` just to suppress Lambda warnings. Lambda functions in public subnets do not receive public IPs, so public subnet placement does not provide internet access.
- Do not make Overcast IDs static or user-selectable; real AWS generates these IDs.

## Troubleshooting

If CDK says there are no private subnet groups, inspect the local metadata and live EC2 responses:

```bash
aws ec2 describe-subnets --filters "Name=vpc-id,Values=$VPC_ID"
aws ec2 describe-route-tables --filters "Name=vpc-id,Values=$VPC_ID"
```

The private subnet group needs both private CDK tags and a private route table with a default route to `nat-*`.

If CDK starts using stale VPC IDs, remove local lookup cache entries or avoid `fromLookup` locally:

```bash
npx cdk context --clear
```

For repeatable local workflows, prefer the provider pattern above so local deploys do not depend on `cdk.context.json` at all.

## Related Docs

- [Using AWS CDK](../cdk.md) — bootstrap and deploy CDK stacks against Overcast.
- [EC2 / VPC service reference](../services/ec2.md) — VPC support, Docker-backed network behavior, and limitations.
- [CloudFormation service reference](../services/cloudformation.md) — supported resource provisioning through CDK/CloudFormation.
- [Using AWS SDKs and CLI](../sdk-cli.md) — endpoint and credential configuration for local AWS clients.
