---
title: "CloudFormation dynamic references"
description: "How {{resolve:...}} references resolve in Overcast: which schemes work, when they are substituted, why a reference is compared as written, and what an unresolvable one does."
section: "Service Reference"
tags:
  - cloudformation
  - docs
  - secrets
  - services
---

# CloudFormation dynamic references

How a `{{resolve:…}}` reference in a [CloudFormation](../cloudformation.md)
template resolves, and what it does when it cannot.

A dynamic reference is plain text inside a property value — not an intrinsic —
that CloudFormation substitutes at deploy time.

| Scheme | Overcast |
| --- | --- |
| `secretsmanager` | Resolves against the emulated service, reading exactly what `GetSecretValue` would return |
| `ssm` | Resolves against the emulated service. An explicit parameter version is accepted but resolves to the current value, with a warning |
| `ssm-secure` | Resolves against the emulated service, with decryption, in the properties AWS enumerates for secure strings and nowhere else. Refused outright in a custom resource's properties |
| `s3` | Not supported; fails the resource rather than resolving to something wrong |

Resolution happens after the intrinsic functions, so a reference built by `Fn::Sub`
or `Fn::Join` resolves once the surrounding value is complete. A resolved value is
never rescanned — secret content containing `{{resolve:` is data, not a reference.

## `ssm-secure` only where AWS allows it

A secure string may be read into the properties AWS lists under
[resources that support dynamic parameter patterns for secure strings](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references-ssm-secure-strings.html#template-parameters-dynamic-patterns-resources),
and nowhere else. `AWS::RDS::DBInstance`'s `MasterUserPassword` is on that
list; an SQS `QueueName` is not.

Anywhere else the reference **fails the resource** and the stack rolls back,
naming the reference and the property path — `SSM Secure reference is not
supported in: [AWS::SQS::Queue/Properties/QueueName]`, the wording AWS itself
uses. A list index is not part of the path: every element of
`AWS::OpsWorks::Stack`'s `RdsDbInstances` allows `DbPassword`. The parameter is
never decrypted on the way to being refused.

AWS rejects such a template at `CreateStack`, before any resource is created;
Overcast fails the resource instead, so the template is caught either way but
the failure arrives as a stack event rather than a `ValidationError`.

`secretsmanager` and plain `ssm` carry no such restriction — a secret can be
read into any property.

## A reference is compared as written, never as resolved

Change detection and the stored resource properties both keep the literal text:

- Rotating a secret behind an unchanged template does not make the resource look
  changed, matching AWS — to push a new value you must change the resource in the
  template. No `GetSecretValue` call is made for an unchanged containing resource,
  so a no-op stack update also succeeds if the secret is no longer available.
- A resolved secret is never written to Overcast's state. Only the service the
  property belongs to ever sees it, as on AWS.
- **Outputs leave references literal.** A `{{resolve:…}}` in an `Outputs` value
  comes back as the reference text, matching CloudFormation, so a secret is not
  published through `DescribeStacks`.
- **A reference creates no dependency.** Only `Ref`, `Fn::GetAtt` and `Fn::Sub`
  order resources, so a resource reading a secret created by the same template
  needs an explicit `DependsOn`, as on AWS.

A reference that cannot be resolved **fails the resource** and the stack rolls
back, rather than creating it with the literal `{{resolve:…}}` text in place of a
value.

## Related

- [CloudFormation](../cloudformation.md) — quick start and what works
- [CloudFormation limitations](./limitations.md) — the divergence table and the status machine
- [CloudFormation troubleshooting](./troubleshooting.md) — stuck stacks and failed deploys
- [Secrets Manager](../secretsmanager.md) — the service a `secretsmanager` reference reads
- [Systems Manager](../ssm.md) — the service an `ssm` reference reads
