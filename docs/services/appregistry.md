---
title: "AppRegistry — Service Catalog AppRegistry"
description: "Groups related resources into named applications, with CloudFormation stack and CDK awsApplication tag association wired through to the web console."
section: "Service Reference"
tags:
  - appregistry
  - docs
  - service-catalog
  - services
---

# AppRegistry — Service Catalog AppRegistry

Groups related resources into named applications, and associates CloudFormation
stacks and CDK-tagged resources with them automatically.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

APP=$(aws servicecatalog-appregistry create-application --name my-app \
  --query application.id --output text)

aws servicecatalog-appregistry associate-resource \
  --application "$APP" --resource-type CFN_STACK --resource my-stack

aws servicecatalog-appregistry list-associated-resources --application "$APP"
```

## What works

| Area | Behaviour |
| --- | --- |
| Resources | Applications, attribute groups, resource associations and tagging — all 22 modelled operations, over REST-JSON under `/applications`. |
| CloudFormation | `AWS::ServiceCatalogAppRegistry::Application` and `::ResourceAssociation` are provisioned resource types. The application exposes `Id`, `Arn`, `Name`, `ApplicationName`, `ApplicationTagKey` and `ApplicationTagValue` to `Fn::GetAtt`; an association's physical ID is `<appId>/<resourceType>/<resource>`, and `ResourceType` defaults to `CFN_STACK`. |
| CDK `awsApplication` tags | The provisioner scans each resource's `Tags` for an `awsApplication=<app-arn>` entry — the one CDK's `Application` L2 construct propagates — and records a direct association immediately after provisioning, under AWS's `RESOURCE_TAG_VALUE` resource type. Those resources come back from `ListAssociatedResources` without the console having to expand the parent stack. |
| Web console | A resource detail page shows a "belongs to application X" banner when a match is found. |

## Differences from AWS

| Area | Overcast | AWS |
| --- | --- | --- |
| Enforcement | Associations are records. Nothing about an application governs, restricts or provisions anything | Integrated with Service Catalog governance |
| `awsApplication` tag scan | Runs on resource **create** only, and a failure is logged rather than failing the stack | Continuous |
| Application-scoped cost, resource groups, attribute-group syncing | Not modelled | Supported |

## Gotchas

> [!IMPORTANT]
> `/applications` is shared with [AppConfig](./appconfig.md), which models the same
> path tree. Overcast picks the service from the SigV4 credential scope: a request
> signed as `servicecatalog` reaches AppRegistry, one signed as `appconfig` reaches
> AppConfig, and an unsigned or unparseable request reaches AppRegistry. Every AWS
> SDK and the CLI sign correctly.

<!-- BEGIN overcast:capabilities -->

## Operations

All 22 listed operations are implemented.
Per-operation status, notes and AWS API links: [AppRegistry operations](appregistry/operations.md).

<!-- END overcast:capabilities -->

## Related

- [CloudFormation](./cloudformation.md) — what creates most associations
- [AppConfig](./appconfig.md) — the other service on `/applications`
- [AWS API reference](https://docs.aws.amazon.com/servicecatalog/latest/dg/applications.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
