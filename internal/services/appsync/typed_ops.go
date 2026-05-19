package appsync

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateGraphqlApi": op.NewTyped[createGraphqlApiRequest, createGraphqlApiResponse](
			"CreateGraphqlApi", h.createGraphqlApiTyped,
		),
		"GetGraphqlApi": op.NewTyped[getGraphqlApiRequest, getGraphqlApiResponse](
			"GetGraphqlApi", h.getGraphqlApiTyped,
		),
		"ListGraphqlApis": op.NewTyped[listGraphqlApisRequest, listGraphqlApisResponse](
			"ListGraphqlApis", h.listGraphqlApisTyped,
		),
		"UpdateGraphqlApi": op.NewTyped[updateGraphqlApiRequest, updateGraphqlApiResponse](
			"UpdateGraphqlApi", h.updateGraphqlApiTyped,
		),
		"DeleteGraphqlApi": op.NewTypedAny[deleteGraphqlApiRequest](
			"DeleteGraphqlApi", h.deleteGraphqlApiTyped,
		),
		"TagResource": op.NewTypedAny[tagResourceRequest](
			"TagResource", h.tagResourceTyped,
		),
		"UntagResource": op.NewTypedAny[untagResourceRequest](
			"UntagResource", h.untagResourceTyped,
		),
		"ListTagsForResource": op.NewTyped[listTagsForResourceRequest, listTagsForResourceResponse](
			"ListTagsForResource", h.listTagsForResourceTyped,
		),
		"CreateDomainName": op.NewTyped[createDomainNameRequest, createDomainNameResponse](
			"CreateDomainName", h.createDomainNameTyped,
		),
		"GetDomainName": op.NewTyped[getDomainNameRequest, getDomainNameResponse](
			"GetDomainName", h.getDomainNameTyped,
		),
		"ListDomainNames": op.NewTyped[listDomainNamesRequest, listDomainNamesResponse](
			"ListDomainNames", h.listDomainNamesTyped,
		),
		"UpdateDomainName": op.NewTyped[updateDomainNameRequest, updateDomainNameResponse](
			"UpdateDomainName", h.updateDomainNameTyped,
		),
		"DeleteDomainName": op.NewTypedAny[deleteDomainNameRequest](
			"DeleteDomainName", h.deleteDomainNameTyped,
		),
		"AssociateApi": op.NewTyped[associateApiRequest, associateApiResponse](
			"AssociateApi", h.associateApiTyped,
		),
		"GetApiAssociation": op.NewTyped[getApiAssociationRequest, getApiAssociationResponse](
			"GetApiAssociation", h.getApiAssociationTyped,
		),
		"DisassociateApi": op.NewTypedAny[disassociateApiRequest](
			"DisassociateApi", h.disassociateApiTyped,
		),
		"StartSchemaCreation": op.NewTyped[startSchemaCreationRequest, startSchemaCreationResponse](
			"StartSchemaCreation", h.startSchemaCreationTyped,
		),
		"GetSchemaCreationStatus": op.NewTyped[getSchemaCreationStatusRequest, getSchemaCreationStatusResponse](
			"GetSchemaCreationStatus", h.getSchemaCreationStatusTyped,
		),
		"GetIntrospectionSchema": op.NewTyped[getIntrospectionSchemaRequest, getIntrospectionSchemaResponse](
			"GetIntrospectionSchema", h.getIntrospectionSchemaTyped,
		),
		"CreateApiKey": op.NewTyped[createApiKeyRequest, createApiKeyResponse](
			"CreateApiKey", h.createApiKeyTyped,
		),
		"ListApiKeys": op.NewTyped[listApiKeysRequest, listApiKeysResponse](
			"ListApiKeys", h.listApiKeysTyped,
		),
		"UpdateApiKey": op.NewTyped[updateApiKeyRequest, updateApiKeyResponse](
			"UpdateApiKey", h.updateApiKeyTyped,
		),
		"DeleteApiKey": op.NewTypedAny[deleteApiKeyRequest](
			"DeleteApiKey", h.deleteApiKeyTyped,
		),
		"CreateDataSource": op.NewTyped[createDataSourceRequest, createDataSourceResponse](
			"CreateDataSource", h.createDataSourceTyped,
		),
		"GetDataSource": op.NewTyped[getDataSourceRequest, getDataSourceResponse](
			"GetDataSource", h.getDataSourceTyped,
		),
		"ListDataSources": op.NewTyped[listDataSourcesRequest, listDataSourcesResponse](
			"ListDataSources", h.listDataSourcesTyped,
		),
		"UpdateDataSource": op.NewTyped[updateDataSourceRequest, updateDataSourceResponse](
			"UpdateDataSource", h.updateDataSourceTyped,
		),
		"DeleteDataSource": op.NewTypedAny[deleteDataSourceRequest](
			"DeleteDataSource", h.deleteDataSourceTyped,
		),
		"CreateFunction": op.NewTyped[createFunctionRequest, createFunctionResponse](
			"CreateFunction", h.createFunctionTyped,
		),
		"GetFunction": op.NewTyped[getFunctionRequest, getFunctionResponse](
			"GetFunction", h.getFunctionTyped,
		),
		"ListFunctions": op.NewTyped[listFunctionsRequest, listFunctionsResponse](
			"ListFunctions", h.listFunctionsTyped,
		),
		"UpdateFunction": op.NewTyped[updateFunctionRequest, updateFunctionResponse](
			"UpdateFunction", h.updateFunctionTyped,
		),
		"DeleteFunction": op.NewTypedAny[deleteFunctionRequest](
			"DeleteFunction", h.deleteFunctionTyped,
		),
		"ListResolversByFunction": op.NewTyped[listResolversByFunctionRequest, listResolversByFunctionResponse](
			"ListResolversByFunction", h.listResolversByFunctionTyped,
		),
		"CreateResolver": op.NewTyped[createResolverRequest, createResolverResponse](
			"CreateResolver", h.createResolverTyped,
		),
		"GetResolver": op.NewTyped[getResolverRequest, getResolverResponse](
			"GetResolver", h.getResolverTyped,
		),
		"ListResolvers": op.NewTyped[listResolversRequest, listResolversResponse](
			"ListResolvers", h.listResolversTyped,
		),
		"UpdateResolver": op.NewTyped[updateResolverRequest, updateResolverResponse](
			"UpdateResolver", h.updateResolverTyped,
		),
		"DeleteResolver": op.NewTypedAny[deleteResolverRequest](
			"DeleteResolver", h.deleteResolverTyped,
		),
		"GetType": op.NewTyped[getTypeRequest, getTypeResponse](
			"GetType", h.getTypeTyped,
		),
		"CreateType": op.NewTyped[createTypeRequest, createTypeResponse](
			"CreateType", h.createTypeTyped,
		),
		"UpdateType": op.NewTyped[updateTypeRequest, updateTypeResponse](
			"UpdateType", h.updateTypeTyped,
		),
		"DeleteType": op.NewTypedAny[deleteTypeRequest](
			"DeleteType", h.deleteTypeTyped,
		),
		"ListTypes": op.NewTyped[listTypesRequest, listTypesResponse](
			"ListTypes", h.listTypesTyped,
		),
		"CreateApiCache": op.NewTyped[createApiCacheRequest, createApiCacheResponse](
			"CreateApiCache", h.createApiCacheTyped,
		),
		"GetApiCache": op.NewTyped[getApiCacheRequest, getApiCacheResponse](
			"GetApiCache", h.getApiCacheTyped,
		),
		"UpdateApiCache": op.NewTyped[updateApiCacheRequest, updateApiCacheResponse](
			"UpdateApiCache", h.updateApiCacheTyped,
		),
		"DeleteApiCache": op.NewTypedAny[deleteApiCacheRequest](
			"DeleteApiCache", h.deleteApiCacheTyped,
		),
		"FlushApiCache": op.NewTypedAny[flushApiCacheRequest](
			"FlushApiCache", h.flushApiCacheTyped,
		),
		"PutGraphqlApiEnvironmentVariables": op.NewTyped[putGraphqlApiEnvVarsRequest, putGraphqlApiEnvVarsResponse](
			"PutGraphqlApiEnvironmentVariables", h.putGraphqlApiEnvVarsTyped,
		),
		"GetGraphqlApiEnvironmentVariables": op.NewTyped[getGraphqlApiEnvVarsRequest, getGraphqlApiEnvVarsResponse](
			"GetGraphqlApiEnvironmentVariables", h.getGraphqlApiEnvVarsTyped,
		),
		"AssociateSourceGraphqlApi": op.NewTyped[associateSourceGraphqlApiRequest, associateSourceGraphqlApiResponse](
			"AssociateSourceGraphqlApi", h.associateSourceGraphqlApiTyped,
		),
		"GetSourceApiAssociation": op.NewTyped[getSourceApiAssociationRequest, getSourceApiAssociationResponse](
			"GetSourceApiAssociation", h.getSourceApiAssociationTyped,
		),
		"ListSourceApiAssociations": op.NewTyped[listSourceApiAssociationsRequest, listSourceApiAssociationsResponse](
			"ListSourceApiAssociations", h.listSourceApiAssociationsTyped,
		),
		"DisassociateSourceGraphqlApi": op.NewTypedAny[disassociateSourceGraphqlApiRequest](
			"DisassociateSourceGraphqlApi", h.disassociateSourceGraphqlApiTyped,
		),
		"AssociateMergedGraphqlApi": op.NewTyped[associateMergedGraphqlApiRequest, associateMergedGraphqlApiResponse](
			"AssociateMergedGraphqlApi", h.associateMergedGraphqlApiTyped,
		),
		"DisassociateMergedGraphqlApi": op.NewTypedAny[disassociateMergedGraphqlApiRequest](
			"DisassociateMergedGraphqlApi", h.disassociateMergedGraphqlApiTyped,
		),
		"StartSchemaMerge": op.NewTyped[startSchemaMergeRequest, startSchemaMergeResponse](
			"StartSchemaMerge", h.startSchemaMergeTyped,
		),
		"EvaluateMappingTemplate": op.NewTyped[evaluateMappingTemplateRequest, evaluateMappingTemplateResponse](
			"EvaluateMappingTemplate", h.evaluateMappingTemplateTyped,
		),
		"EvaluateCode": op.NewTyped[evaluateCodeRequest, evaluateCodeResponse](
			"EvaluateCode", h.evaluateCodeTyped,
		),
		"CreateApi": op.NewTyped[createEventApiRequest, createEventApiResponse](
			"CreateApi", h.createEventApiTyped,
		),
		"GetApi": op.NewTyped[getEventApiRequest, getEventApiResponse](
			"GetApi", h.getEventApiTyped,
		),
		"ListApis": op.NewTyped[listEventApisRequest, listEventApisResponse](
			"ListApis", h.listEventApisTyped,
		),
		"UpdateApi": op.NewTyped[updateEventApiRequest, updateEventApiResponse](
			"UpdateApi", h.updateEventApiTyped,
		),
		"DeleteApi": op.NewTypedAny[deleteEventApiRequest](
			"DeleteApi", h.deleteEventApiTyped,
		),
		"CreateChannelNamespace": op.NewTyped[createChannelNamespaceRequest, createChannelNamespaceResponse](
			"CreateChannelNamespace", h.createChannelNamespaceTyped,
		),
		"GetChannelNamespace": op.NewTyped[getChannelNamespaceRequest, getChannelNamespaceResponse](
			"GetChannelNamespace", h.getChannelNamespaceTyped,
		),
		"ListChannelNamespaces": op.NewTyped[listChannelNamespacesRequest, listChannelNamespacesResponse](
			"ListChannelNamespaces", h.listChannelNamespacesTyped,
		),
		"UpdateChannelNamespace": op.NewTyped[updateChannelNamespaceRequest, updateChannelNamespaceResponse](
			"UpdateChannelNamespace", h.updateChannelNamespaceTyped,
		),
		"DeleteChannelNamespace": op.NewTypedAny[deleteChannelNamespaceRequest](
			"DeleteChannelNamespace", h.deleteChannelNamespaceTyped,
		),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
