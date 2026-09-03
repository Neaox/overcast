---
title: "CDK limitations"
description: "What a cdk deploy against Overcast does not do: custom resources without Docker, container assets from a local registry, nested stack fetches, stubbed types, and no drift detection."
section: "CDK"
tags:
  - cdk
  - cloudformation
  - docs
  - limitations
---

# CDK limitations

A [`cdk deploy`](../cdk.md) against Overcast provisions real state for the
[supported resource types](./resource-types.md); these are the five places it
stops short of AWS.

| Limitation | Effect |
| --- | --- |
| Custom resources need Docker | Without it, the handler degrades to a stub physical ID |
| Container assets come from Overcast's own registry | `repositoryUri` names `localhost`, and images survive a restart |
| A nested stack's `TemplateURL` must be reachable | The child template is fetched during the parent's provisioning |
| Resource coverage is partial | Unsupported types are stubbed silently |
| No drift detection or stack policies | Three CloudFormation operations return `501` |

## Custom resource invocation requires Docker

`AWS::CloudFormation::CustomResource` and `Custom::*` types invoke the Lambda
function named by `ServiceToken`. When Docker is available, the Lambda executes
and its response (`PhysicalResourceId`, `Data`) becomes the resource's physical
ID and attributes. When Docker is unavailable, the handler degrades to a stub
physical ID so the stack can still deploy.

## Container assets are served from Overcast's own registry

A `DockerImageAsset` — an ECS `ContainerImage.fromAsset`, a Lambda
`DockerImageFunction`, or any construct that builds an image — is pushed to a
`registry:2` container Overcast starts on port `4510`, and the task or function
that needs it pulls from there. It works out of the box on native Linux and
Docker Desktop; only a remote Docker daemon needs an `insecure-registries` entry,
which Overcast checks at registry startup and logs remediation for.

Two consequences:

- **`repositoryUri` names `localhost`** even when `OVERCAST_HOSTNAME` is set to
  something else, because the Docker daemon — not your API client — is what dials
  it, and Docker trusts plain HTTP to `localhost` and bypasses proxies for it.
- **Assets survive a restart.** The registry's storage is a named Docker volume,
  so the next deploy skips rebuilding what the last one pushed. A registry that
  fell back to an ephemeral port, or one started with
  `OVERCAST_ECR_REGISTRY_PERSIST=false`, re-pushes — a few seconds, not a
  failure.

Details: [ECR § The repository URI](../services/ecr/limitations.md#the-repository-uri),
[§ Asking whether an image is published](../services/ecr/limitations.md#asking-whether-an-image-is-published)
and [§ Persistence](../services/ecr/limitations.md#persistence). What runs the
image is [ECS § Images published to the emulated ECR](../services/ecs/examples.md#images-published-to-the-emulated-ecr).

## Nested stack TemplateURL must be reachable

`AWS::CloudFormation::Stack` (nested stacks) is supported. The `TemplateURL`
must point to an S3 object or any URL reachable by the emulator. The child
template is fetched, parsed, and provisioned synchronously within the parent
stack's provisioning goroutine. Child outputs are exposed via
`Fn::GetAtt ["NestedStack", "Outputs.OutputKey"]`.

## Partial resource coverage

Not every CDK construct maps to a supported resource type, and a resource whose
type is unsupported is stubbed rather than refused. Each one says so on its own
`CREATE_COMPLETE` event, in an `Overcast:`-prefixed `ResourceStatusReason`, and
the server log carries a matching warning naming the type and the logical ID.

## No drift detection or stack policies

`DetectStackDrift`, `SetStackPolicy`, and `GetStackPolicy` return `501`.

## Related

- [CDK resource type coverage](./resource-types.md) — the 136 types, and what happens to the rest
- [CDK troubleshooting](./troubleshooting.md) — a deploy that fails or never completes
- [Using AWS CDK](../cdk.md) — bootstrap and deploy against Overcast
- [CloudFormation service reference](../services/cloudformation.md) — the provisioner and its `Fn::GetAtt` coverage
- [All documentation](../README.md) — every guide and service page
