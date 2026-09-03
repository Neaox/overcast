---
title: "Importing a VPC into CDK"
description: "When something other than CDK owns the local VPC: which import to use, what availability zones resolve to, and how to keep the choice out of the application stacks."
section: "CDK"
tags:
  - cdk
  - docs
  - lookup
  - vpc
  - vpcs
---

# Importing a VPC into CDK

Import a VPC when something other than a CDK stack created it. Locally that is
the harder path — [Local VPCs for CDK](./local-vpc.md) has the stack that
creates one, which is the option with nothing to go stale — so reach for an
import when the VPC is not CDK's to create.

| Import | Caches IDs | Fits |
| --- | --- | --- |
| `Vpc.fromLookup` | Yes, in `cdk.context.json`, keyed on account, region and lookup filters | A stable environment: staging, production |
| `Vpc.fromVpcAttributes` | No — it takes the IDs directly | Anywhere something can supply the current IDs on every deploy |

A teardown that recreates the VPC with new IDs leaves the `cdk.context.json`
entry behind; CDK does not garbage-collect it. Both calls need a stack scope, so
a stage cannot import the VPC itself.

## The real-AWS counterpart

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

## Keeping the choice in the stage

A small provider keeps the decision where the local/prod decision already lives
while the construct is still created inside a stack:

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

Stacks then take a `VpcProvider` instead of an `ec2.IVpc` and call
`props.vpcProvider.getVpc(this)` in the constructor. They stay
environment-agnostic either way; the difference is only whether the stage hands
over a VPC or a recipe for getting one.

## What CDK expects of a VPC it did not create

The shape CDK expects is the one `ec2.Vpc` would have produced: public subnets
tagged `aws-cdk:subnet-type=Public` and `aws-cdk:subnet-name=Public` with a
default route to an internet gateway, and private subnets tagged
`aws-cdk:subnet-type=Private` and `aws-cdk:subnet-name=Private` with a default
route to a NAT gateway.

Two more things do not survive an import:

- `cdk.context.json` is a lookup cache, not desired state. Committing `vpc-provider:*` entries for disposable local VPCs pins IDs that no longer exist.
- Overcast generates `vpc-*`, `subnet-*` and `rtb-*` IDs the way real AWS does, so they are not selectable. A pattern that depends on knowing them in advance will not hold.

## Availability zones

`ec2.Vpc` reads its availability zones from the stack, and the stack answers
differently depending on whether it has a concrete environment:

| Stack | Where the zones come from | Cost |
| --- | --- | --- |
| Environment-agnostic (no `env` on the stack or its stage) | `Fn::GetAZs` intrinsics, which Overcast's CloudFormation engine evaluates directly | Nothing is looked up and `cdk.context.json` stays empty |
| Environment-specific (`env: { account, region }`) | The `availability-zones` context provider, assuming the bootstrap lookup role | A synth that cannot reach a bootstrapped Overcast bakes the placeholder zones `dummy1a`/`dummy1b`/`dummy1c` into the template |

Leaving the local stage environment-agnostic is the smaller path. `cdk deploy`
still resolves the target account and region from the active credentials, which
for Overcast are the dummy `test`/`test` pair whose `sts:GetCallerIdentity`
answers with the configured account and region — see
[Using AWS CDK](../cdk.md).

For a concrete `env` on the local stage, run synth against a bootstrapped
Overcast so the lookup resolves: `DescribeAvailabilityZones` is supported and
returns three zones per region. The resulting `availability-zones:*` context
entry is stable across teardowns in a way `vpc-provider:*` entries are not.

## Troubleshooting

**CDK reports no private subnet groups.** Check the tags and routes on the live
VPC:

```bash
aws ec2 describe-subnets --filters "Name=vpc-id,Values=$VPC_ID"
```

```bash
aws ec2 describe-route-tables --filters "Name=vpc-id,Values=$VPC_ID"
```

A private subnet group needs both the private CDK tags and a route table with a
default route to a `nat-*` gateway.

**CDK is using stale VPC IDs.** The lookup cache is holding entries for a VPC
that no longer exists:

```bash
npx cdk context --clear
```

A local resources stack avoids the situation entirely: no lookup, so no cache to
go stale.

## Related

- [Local VPCs for CDK](./local-vpc.md) — the stack that creates the VPC instead of importing one
- [CDK resource type coverage](./resource-types.md) — the EC2 and VPC types CloudFormation provisions
- [Using AWS CDK](../cdk.md) — bootstrap and deploy CDK stacks against Overcast
- [EC2 / VPC service reference](../services/ec2.md) — VPC support, filters, and the default VPC
- [Using AWS SDKs and CLI](../sdk-cli.md) — endpoint and credential configuration for local AWS clients
- [All documentation](../README.md) — every guide and service page
