---
title: "API Gateway — Amazon API Gateway"
description: "API Gateway (REST v1 and HTTP v2) uses a REST API with path-based routing. REST API v1 is mounted at /restapis, HTTP API v2 at /v2/apis."
section: "Service Reference"
tags:
  - amazon
  - api
  - apigateway
  - docs
  - gateway
  - services
---

# API Gateway — Amazon API Gateway

> AWS docs: https://docs.aws.amazon.com/apigateway/latest/api/Welcome.html

API Gateway (REST v1 and HTTP v2) uses a REST API with path-based routing.
REST API v1 is mounted at `/restapis`, HTTP API v2 at `/v2/apis`.

---

## Known limitations

- **No VTL template mapping.** Integration request/response templates are not evaluated as VTL — values are passed through as-is.
- **Partial authorizer enforcement.** `COGNITO_USER_POOLS` (REST v1) and `JWT` (HTTP v2) authorizers are validated (RS256 signature + expiry + issuer/audience). Lambda (`TOKEN`, `REQUEST`) and IAM authorizers are stored but not enforced at request time.
- **No API key validation.** API keys and usage plans are stored but not enforced at request time.
- **No request validation.** Request validators are stored but not enforced at request time.
- **No WebSocket execution.** WEBSOCKET protocol type is accepted on creation but execution is not implemented.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category               | ✅ Supported | ❌ Unsupported |
| ---------------------- | ------------ | -------------- |
| REST API v1 management | 24           |                |
| REST API v1 stages     | 7            |                |
| REST API v1 other      | 33           | 2              |
| HTTP API v2 management | 15           |                |
| HTTP API v2 stages     | 6            |                |
| HTTP API v2 other      | 17           |                |
| REST API v1 execution  | 1            |                |

---

## Endpoints

### REST API v1 management

| Operation                   | Status       | Notes                                                          | AWS Docs                                                                                     |
| --------------------------- | ------------ | -------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `CreateRestApi`             | ✅ Supported | Creates API with root `/` resource; default EDGE endpoint type | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateRestApi.html)             |
| `GetRestApi`                | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetRestApi.html)                |
| `GetRestApis`               | ✅ Supported | Pagination not yet implemented                                 | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetRestApis.html)               |
| `DeleteRestApi`             | ✅ Supported | Cascade deletes resources, stages, and deployments             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteRestApi.html)             |
| `UpdateRestApi`             | ✅ Supported | Patch operations on name and description                       | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateRestApi.html)             |
| `CreateResource`            | ✅ Supported | Computes full path from parent chain                           | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateResource.html)            |
| `GetResource`               | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetResource.html)               |
| `GetResources`              | ✅ Supported | Pagination not yet implemented                                 | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetResources.html)              |
| `DeleteResource`            | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteResource.html)            |
| `UpdateResource`            | ✅ Supported | Patch `/pathPart`                                              | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateResource.html)            |
| `PutMethod`                 | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_PutMethod.html)                 |
| `GetMethod`                 | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetMethod.html)                 |
| `DeleteMethod`              | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteMethod.html)              |
| `UpdateMethod`              | ✅ Supported | Patch `/authorizationType`, `/authorizerId`, `/apiKeyRequired` | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateMethod.html)              |
| `PutIntegration`            | ✅ Supported | AWS_PROXY/MOCK/HTTP_PROXY/HTTP/AWS types; uri, httpMethod      | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_PutIntegration.html)            |
| `GetIntegration`            | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetIntegration.html)            |
| `DeleteIntegration`         | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteIntegration.html)         |
| `UpdateIntegration`         | ✅ Supported | Patch integrationType, integrationUri, payloadFormatVersion    | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateIntegration.html)         |
| `PutMethodResponse`         | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_PutMethodResponse.html)         |
| `GetMethodResponse`         | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetMethodResponse.html)         |
| `DeleteMethodResponse`      | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteMethodResponse.html)      |
| `PutIntegrationResponse`    | ✅ Supported | responseTemplates, selectionPattern                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_PutIntegrationResponse.html)    |
| `GetIntegrationResponse`    | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetIntegrationResponse.html)    |
| `DeleteIntegrationResponse` | ✅ Supported |                                                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteIntegrationResponse.html) |

### REST API v1 stages

| Operation          | Status       | Notes                                                       | AWS Docs                                                                            |
| ------------------ | ------------ | ----------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| `CreateDeployment` | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateDeployment.html) |
| `GetDeployments`   | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetDeployments.html)   |
| `CreateStage`      | ✅ Supported | Links to a deployment                                       | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateStage.html)      |
| `GetStage`         | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetStage.html)         |
| `GetStages`        | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetStages.html)        |
| `UpdateStage`      | ✅ Supported | Patch description, autoDeploy, deploymentId, stageVariables | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateStage.html)      |
| `DeleteStage`      | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteStage.html)      |

### REST API v1 other

| Operation                | Status         | Notes                                                      | AWS Docs                                                                                  |
| ------------------------ | -------------- | ---------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `CreateModel`            | ✅ Supported   | id, name, contentType, schema, description                 | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateModel.html)            |
| `GetModel`               | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetModel.html)               |
| `GetModels`              | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetModels.html)              |
| `DeleteModel`            | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteModel.html)            |
| `CreateAuthorizer`       | ✅ Supported   | JWT and REQUEST types; config stored                       | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateAuthorizer.html)       |
| `GetAuthorizer`          | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetAuthorizer.html)          |
| `GetAuthorizers`         | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetAuthorizers.html)         |
| `DeleteAuthorizer`       | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteAuthorizer.html)       |
| `CreateRequestValidator` | ✅ Supported   | validateRequestBody, validateRequestParams                 | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateRequestValidator.html) |
| `GetRequestValidators`   | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetRequestValidators.html)   |
| `DeleteRequestValidator` | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteRequestValidator.html) |
| `CreateApiKey`           | ✅ Supported   | auto-generated key value                                   | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateApiKey.html)           |
| `GetApiKey`              | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetApiKey.html)              |
| `GetApiKeys`             | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetApiKeys.html)             |
| `DeleteApiKey`           | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteApiKey.html)           |
| `CreateUsagePlan`        | ✅ Supported   | throttle and quota stored (not enforced)                   | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateUsagePlan.html)        |
| `GetUsagePlan`           | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetUsagePlan.html)           |
| `GetUsagePlans`          | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetUsagePlans.html)          |
| `DeleteUsagePlan`        | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteUsagePlan.html)        |
| `CreateUsagePlanKey`     | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateUsagePlanKey.html)     |
| `GetUsagePlanKeys`       | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetUsagePlanKeys.html)       |
| `DeleteUsagePlanKey`     | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteUsagePlanKey.html)     |
| `CreateDomainName`       | ✅ Supported   | Inert metadata; no routing effect                          | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateDomainName.html)       |
| `GetDomainNames`         | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetDomainNames.html)         |
| `DeleteDomainName`       | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteDomainName.html)       |
| `CreateBasePathMapping`  | ✅ Supported   | Stored under the domain name                               | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateBasePathMapping.html)  |
| `GetBasePathMappings`    | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetBasePathMappings.html)    |
| `CreateVpcLink`          | ✅ Supported   | Status immediately AVAILABLE; no VPC connectivity enforced | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateVpcLink.html)          |
| `GetVpcLinks`            | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetVpcLinks.html)            |
| `DeleteVpcLink`          | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteVpcLink.html)          |
| `TagResource`            | ✅ Supported   | PUT /tags/{arn} — merges tags; ARN may contain slashes     | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_TagResource.html)            |
| `UntagResource`          | ✅ Supported   | DELETE /tags/{arn}?tagKeys=k1,k2                           | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UntagResource.html)          |
| `GetTags`                | ✅ Supported   |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetTags.html)                |
| `GetAccount`             | ❌ Unsupported |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetAccount.html)             |
| `UpdateAccount`          | ❌ Unsupported |                                                            | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateAccount.html)          |

### HTTP API v2 management

| Operation           | Status       | Notes                                                       | AWS Docs                                                                               |
| ------------------- | ------------ | ----------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `CreateV2Api`       | ✅ Supported | HTTP and WEBSOCKET protocol types; default route selection  | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateV2Api.html)         |
| `GetV2Api`          | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Api.html)            |
| `GetV2Apis`         | ✅ Supported | Pagination not yet implemented                              | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Apis.html)           |
| `UpdateV2Api`       | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateV2Api.html)         |
| `DeleteV2Api`       | ✅ Supported | Cascade deletes routes, integrations, stages, deployments   | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteV2Api.html)         |
| `CreateV2Route`     | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateV2Route.html)       |
| `GetV2Route`        | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Route.html)          |
| `GetV2Routes`       | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Routes.html)         |
| `DeleteV2Route`     | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteV2Route.html)       |
| `UpdateV2Route`     | ✅ Supported | Patch routeKey, target, authorizationType                   | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateV2Route.html)       |
| `CreateIntegration` | ✅ Supported | AWS_PROXY / HTTP_PROXY types; payloadFormatVersion 1.0/2.0  | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateV2Integration.html) |
| `GetV2Integration`  | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Integration.html)    |
| `GetV2Integrations` | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Integrations.html)   |
| `DeleteIntegration` | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteV2Integration.html) |
| `UpdateIntegration` | ✅ Supported | Patch integrationType, integrationUri, payloadFormatVersion | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateV2Integration.html) |

### HTTP API v2 stages

| Operation            | Status       | Notes | AWS Docs                                                                              |
| -------------------- | ------------ | ----- | ------------------------------------------------------------------------------------- |
| `CreateV2Deployment` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateV2Deployment.html) |
| `GetV2Deployments`   | ✅ Supported |       | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Deployments.html)   |
| `CreateV2Stage`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateV2Stage.html)      |
| `GetV2Stage`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Stage.html)         |
| `GetV2Stages`        | ✅ Supported |       | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Stages.html)        |
| `DeleteV2Stage`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteV2Stage.html)      |

### HTTP API v2 other

| Operation            | Status       | Notes                                                       | AWS Docs                                                                              |
| -------------------- | ------------ | ----------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `UpdateV2Stage`      | ✅ Supported | Patch description, autoDeploy, deploymentId, stageVariables | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UpdateV2Stage.html)      |
| `CreateV2Authorizer` | ✅ Supported | JWT and REQUEST types; config stored                        | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateV2Authorizer.html) |
| `GetV2Authorizer`    | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Authorizer.html)    |
| `GetV2Authorizers`   | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Authorizers.html)   |
| `DeleteV2Authorizer` | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteV2Authorizer.html) |
| `CreateV2DomainName` | ✅ Supported | Inert metadata; no routing effect                           | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateV2DomainName.html) |
| `GetV2DomainNames`   | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2DomainNames.html)   |
| `DeleteV2DomainName` | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteV2DomainName.html) |
| `CreateV2VpcLink`    | ✅ Supported | Status immediately AVAILABLE; no VPC connectivity enforced  | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateV2VpcLink.html)    |
| `GetV2VpcLinks`      | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2VpcLinks.html)      |
| `DeleteV2VpcLink`    | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_DeleteV2VpcLink.html)    |
| `CreateV2ApiMapping` | ✅ Supported | Stored under the domain name                                | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_CreateV2ApiMapping.html) |
| `GetV2ApiMappings`   | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2ApiMappings.html)   |
| `TagV2Resource`      | ✅ Supported | POST /v2/tags/{arn} — merges tags; ARN may contain slashes  | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_TagV2Resource.html)      |
| `UntagV2Resource`    | ✅ Supported | DELETE /v2/tags/{arn}?tagKeys=k1,k2                         | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_UntagV2Resource.html)    |
| `GetV2Tags`          | ✅ Supported |                                                             | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_GetV2Tags.html)          |
| `ExecuteV2API`       | ✅ Supported | AWS_PROXY and HTTP_PROXY integration types                  | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_ExecuteV2API.html)       |

### REST API v1 execution

| Operation        | Status       | Notes                                                                                                                                      | AWS Docs                                                                          |
| ---------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------- |
| `ExecuteRestAPI` | ✅ Supported | Lambda proxy/non-proxy, HTTP_PROXY, HTTP, and MOCK integrations; stage variable substitution; base64 Lambda responses decoded before write | [docs](https://docs.aws.amazon.com/apigateway/latest/api/API_ExecuteRestAPI.html) |

<!-- END overcast:capabilities -->
