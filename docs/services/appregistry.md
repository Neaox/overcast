---
title: "AppRegistry — Service Catalog AppRegistry"
description: "Service Catalog AppRegistry groups related AWS resources into named applications. Overcast implements the REST-JSON path-based API under /applications — all operations return..."
section: "Service Reference"
tags:
  - appregistry
  - catalog
  - docs
  - service
  - services
---

# AppRegistry — Service Catalog AppRegistry

Service Catalog AppRegistry groups related AWS resources into named applications.
Overcast implements the REST-JSON path-based API under `/applications` — all
operations return JSON and error with `application/json` envelopes.

A CloudFormation stack or any standalone resource can be associated with an
application; the web UI shows a "belongs to application X" banner on resource
detail pages when a match is found.

---

## CloudFormation integration

| Resource type                                         | Status       | Notes                                                                                                    |
| ----------------------------------------------------- | ------------ | -------------------------------------------------------------------------------------------------------- |
| `AWS::ServiceCatalogAppRegistry::Application`         | ✅ Supported | `GetAtt` attributes: `Id`, `Arn`, `Name`, `ApplicationName`, `ApplicationTagKey`, `ApplicationTagValue`. |
| `AWS::ServiceCatalogAppRegistry::ResourceAssociation` | ✅ Supported | Physical ID is `<appId>/<resourceType>/<resource>`.                                                      |

**CDK `awsApplication` tag auto-association:** the provisioner scans each
resource's `Tags` for an `awsApplication=<app-arn>` entry (propagated by CDK's
`Application` L2 construct) and records a direct association with the owning
application immediately after provisioning. Resources tagged this way are
returned from `ListAssociatedResources` without requiring the web UI to expand
the parent stack.

<!-- BEGIN overcast:capabilities -->

## Operations

All 22 listed operations are implemented.
Per-operation status, notes and AWS API links: [AppRegistry operations](appregistry/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/servicecatalog/latest/dg/applications.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
