---
title: "AppRegistry operations"
description: "Every AppRegistry operation Overcast declares — 22 of 22 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - appregistry
  - docs
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# AppRegistry operations

All 22 listed operations are implemented. Back to [AppRegistry](../appregistry.md).

## Summary

| Category                 | ✅ Supported |
| ------------------------ | ------------ |
| Application lifecycle    | 5            |
| Resource associations    | 4            |
| Attribute groups         | 8            |
| Tagging                  | 3            |
| CloudFormation resources | 2            |

---

## Endpoints

### Application lifecycle

| Operation           | Status       | Notes                                                                                                                                                                            | AWS Docs                                                                                             |
| ------------------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `CreateApplication` | ✅ Supported | Auto-populates `applicationTag.awsApplication = <arn>` to match real AppRegistry. ID is a generated short identifier; ARN uses the standard `arn:aws:servicecatalog:...` format. | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_CreateApplication.html) |
| `GetApplication`    | ✅ Supported | Lookup accepts application name, ID, or ARN.                                                                                                                                     | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_GetApplication.html)    |
| `ListApplications`  | ✅ Supported | No pagination (`nextToken` never returned).                                                                                                                                      | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_ListApplications.html)  |
| `DeleteApplication` | ✅ Supported | Also removes all resource associations for the application.                                                                                                                      | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_DeleteApplication.html) |
| `UpdateApplication` | ✅ Supported | Updates `name` (with collision detection) and `description`; bumps `lastUpdateTime`.                                                                                             | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_UpdateApplication.html) |

### Resource associations

| Operation                 | Status       | Notes                                                                                                                               | AWS Docs                                                                                                   |
| ------------------------- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `AssociateResource`       | ✅ Supported | Only `resourceType=CFN_STACK` is exercised today, but any resource type/ARN pair is accepted. URL-encoded resource ARN in the path. | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_AssociateResource.html)       |
| `DisassociateResource`    | ✅ Supported |                                                                                                                                     | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_DisassociateResource.html)    |
| `ListAssociatedResources` | ✅ Supported | No pagination.                                                                                                                      | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_ListAssociatedResources.html) |
| `GetAssociatedResource`   | ✅ Supported |                                                                                                                                     | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_GetAssociatedResource.html)   |

### Attribute groups

| Operation                       | Status       | Notes                                                                         | AWS Docs                                                                                                         |
| ------------------------------- | ------------ | ----------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `CreateAttributeGroup`          | ✅ Supported | Inert tier — attributes JSON is stored verbatim.                              | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_CreateAttributeGroup.html)          |
| `GetAttributeGroup`             | ✅ Supported | Lookup accepts name, ID, or ARN.                                              | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_GetAttributeGroup.html)             |
| `ListAttributeGroups`           | ✅ Supported | No pagination.                                                                | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_ListAttributeGroups.html)           |
| `UpdateAttributeGroup`          | ✅ Supported | Name, description, and attributes can all be patched.                         | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_UpdateAttributeGroup.html)          |
| `DeleteAttributeGroup`          | ✅ Supported | Does not cascade to associations.                                             | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_DeleteAttributeGroup.html)          |
| `AssociateAttributeGroup`       | ✅ Supported | Attribute group is accepted by name, ID, or ARN.                              | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_AssociateAttributeGroup.html)       |
| `DisassociateAttributeGroup`    | ✅ Supported |                                                                               | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_DisassociateAttributeGroup.html)    |
| `ListAssociatedAttributeGroups` | ✅ Supported | Returns the associated attribute group IDs for an application. No pagination. | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_ListAssociatedAttributeGroups.html) |

### Tagging

| Operation             | Status       | Notes                                                             | AWS Docs                                                                                               |
| --------------------- | ------------ | ----------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `TagResource`         | ✅ Supported | Inert tier — merges into the shared ARN-keyed tag store.          | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_TagResource.html)         |
| `UntagResource`       | ✅ Supported | `tagKeys` removed from the stored map.                            | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Returns the stored map, or `{}` for an ARN with no recorded tags. | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_ListTagsForResource.html) |

### CloudFormation resources

| Operation                                             | Status       | Notes                                                                                                    | AWS Docs                                                                                                                               |
| ----------------------------------------------------- | ------------ | -------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| `AWS::ServiceCatalogAppRegistry::Application`         | ✅ Supported | `GetAtt` attributes: `Id`, `Arn`, `Name`, `ApplicationName`, `ApplicationTagKey`, `ApplicationTagValue`. | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_AWS::ServiceCatalogAppRegistry::Application.html)         |
| `AWS::ServiceCatalogAppRegistry::ResourceAssociation` | ✅ Supported | Physical ID is `<appId>/<resourceType>/<resource>`.                                                      | [docs](https://docs.aws.amazon.com/servicecatalog/latest/dg/API_app-registry_AWS::ServiceCatalogAppRegistry::ResourceAssociation.html) |

<!-- END overcast:capabilities -->
