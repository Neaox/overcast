//go:build dev

package appsync

// restOperation documents an AWS AppSync REST API operation exposed by the
// emulator. capgen consumes this manifest to validate capability declarations
// for REST-routed operations that cannot be discovered from Query/JSON action
// dispatch maps.
type restOperation struct {
	Method    string
	Path      string
	Operation string
}

var restOperations = []restOperation{
	{Method: "POST", Path: "/v1/tags/*", Operation: "TagResource"},
	{Method: "DELETE", Path: "/v1/tags/*", Operation: "UntagResource"},
	{Method: "GET", Path: "/v1/tags/*", Operation: "ListTagsForResource"},

	{Method: "POST", Path: "/v1/domainnames", Operation: "CreateDomainName"},
	{Method: "GET", Path: "/v1/domainnames", Operation: "ListDomainNames"},
	{Method: "GET", Path: "/v1/domainnames/{domainName}", Operation: "GetDomainName"},
	{Method: "POST", Path: "/v1/domainnames/{domainName}", Operation: "UpdateDomainName"},
	{Method: "DELETE", Path: "/v1/domainnames/{domainName}", Operation: "DeleteDomainName"},
	{Method: "POST", Path: "/v1/domainnames/{domainName}/apiassociation", Operation: "AssociateApi"},
	{Method: "GET", Path: "/v1/domainnames/{domainName}/apiassociation", Operation: "GetApiAssociation"},
	{Method: "DELETE", Path: "/v1/domainnames/{domainName}/apiassociation", Operation: "DisassociateApi"},

	{Method: "POST", Path: "/v1/apis", Operation: "CreateGraphqlApi"},
	{Method: "GET", Path: "/v1/apis", Operation: "ListGraphqlApis"},
	{Method: "GET", Path: "/v1/apis/{apiId}", Operation: "GetGraphqlApi"},
	{Method: "POST", Path: "/v1/apis/{apiId}", Operation: "UpdateGraphqlApi"},
	{Method: "DELETE", Path: "/v1/apis/{apiId}", Operation: "DeleteGraphqlApi"},
	{Method: "POST", Path: "/v1/apis/{apiId}/schemacreation", Operation: "StartSchemaCreation"},
	{Method: "GET", Path: "/v1/apis/{apiId}/schemacreation", Operation: "GetSchemaCreationStatus"},
	{Method: "GET", Path: "/v1/apis/{apiId}/schema", Operation: "GetIntrospectionSchema"},
	{Method: "POST", Path: "/v1/apis/{apiId}/apikeys", Operation: "CreateApiKey"},
	{Method: "GET", Path: "/v1/apis/{apiId}/apikeys", Operation: "ListApiKeys"},
	{Method: "POST", Path: "/v1/apis/{apiId}/apikeys/{keyId}", Operation: "UpdateApiKey"},
	{Method: "DELETE", Path: "/v1/apis/{apiId}/apikeys/{keyId}", Operation: "DeleteApiKey"},
	{Method: "POST", Path: "/v1/apis/{apiId}/datasources", Operation: "CreateDataSource"},
	{Method: "GET", Path: "/v1/apis/{apiId}/datasources", Operation: "ListDataSources"},
	{Method: "GET", Path: "/v1/apis/{apiId}/datasources/{name}", Operation: "GetDataSource"},
	{Method: "POST", Path: "/v1/apis/{apiId}/datasources/{name}", Operation: "UpdateDataSource"},
	{Method: "DELETE", Path: "/v1/apis/{apiId}/datasources/{name}", Operation: "DeleteDataSource"},
	{Method: "POST", Path: "/v1/apis/{apiId}/functions", Operation: "CreateFunction"},
	{Method: "GET", Path: "/v1/apis/{apiId}/functions", Operation: "ListFunctions"},
	{Method: "GET", Path: "/v1/apis/{apiId}/functions/{functionId}", Operation: "GetFunction"},
	{Method: "POST", Path: "/v1/apis/{apiId}/functions/{functionId}", Operation: "UpdateFunction"},
	{Method: "DELETE", Path: "/v1/apis/{apiId}/functions/{functionId}", Operation: "DeleteFunction"},
	{Method: "GET", Path: "/v1/apis/{apiId}/functions/{functionId}/resolvers", Operation: "ListResolversByFunction"},
	{Method: "POST", Path: "/v1/apis/{apiId}/types", Operation: "CreateType"},
	{Method: "GET", Path: "/v1/apis/{apiId}/types", Operation: "ListTypes"},
	{Method: "GET", Path: "/v1/apis/{apiId}/types/{typeName}", Operation: "GetType"},
	{Method: "POST", Path: "/v1/apis/{apiId}/types/{typeName}", Operation: "UpdateType"},
	{Method: "DELETE", Path: "/v1/apis/{apiId}/types/{typeName}", Operation: "DeleteType"},
	{Method: "POST", Path: "/v1/apis/{apiId}/types/{typeName}/resolvers", Operation: "CreateResolver"},
	{Method: "GET", Path: "/v1/apis/{apiId}/types/{typeName}/resolvers", Operation: "ListResolvers"},
	{Method: "GET", Path: "/v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}", Operation: "GetResolver"},
	{Method: "POST", Path: "/v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}", Operation: "UpdateResolver"},
	{Method: "DELETE", Path: "/v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}", Operation: "DeleteResolver"},
	{Method: "POST", Path: "/v1/apis/{apiId}/ApiCaches", Operation: "CreateApiCache"},
	{Method: "GET", Path: "/v1/apis/{apiId}/ApiCaches", Operation: "GetApiCache"},
	{Method: "POST", Path: "/v1/apis/{apiId}/ApiCaches/update", Operation: "UpdateApiCache"},
	{Method: "DELETE", Path: "/v1/apis/{apiId}/ApiCaches", Operation: "DeleteApiCache"},
	{Method: "DELETE", Path: "/v1/apis/{apiId}/FlushCache", Operation: "FlushApiCache"},
	{Method: "PUT", Path: "/v1/apis/{apiId}/environmentVariables", Operation: "PutGraphqlApiEnvironmentVariables"},
	{Method: "GET", Path: "/v1/apis/{apiId}/environmentVariables", Operation: "GetGraphqlApiEnvironmentVariables"},
	{Method: "GET", Path: "/v1/apis/{apiId}/sourceApiAssociations", Operation: "ListSourceApiAssociations"},
	{Method: "POST", Path: "/v1/apis/{apiId}/evaluateMappingTemplate", Operation: "EvaluateMappingTemplate"},
	{Method: "POST", Path: "/v1/apis/{apiId}/evaluateCode", Operation: "EvaluateCode"},

	{Method: "POST", Path: "/v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations", Operation: "AssociateSourceGraphqlApi"},
	{Method: "GET", Path: "/v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations/{associationId}", Operation: "GetSourceApiAssociation"},
	{Method: "DELETE", Path: "/v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations/{associationId}", Operation: "DisassociateSourceGraphqlApi"},
	{Method: "POST", Path: "/v1/mergedApis/{mergedApiIdentifier}/sourceApiAssociations/{associationId}/merge", Operation: "StartSchemaMerge"},
	{Method: "POST", Path: "/v1/sourceApis/{sourceApiIdentifier}/mergedApiAssociations", Operation: "AssociateMergedGraphqlApi"},
	{Method: "DELETE", Path: "/v1/sourceApis/{sourceApiIdentifier}/mergedApiAssociations/{associationId}", Operation: "DisassociateMergedGraphqlApi"},

	{Method: "POST", Path: "/_appsync/{apiId}/graphql", Operation: "ExecuteGraphQL"},
	{Method: "POST", Path: "/v2/apis", Operation: "CreateApi"},
	{Method: "GET", Path: "/v2/apis", Operation: "ListApis"},
	{Method: "GET", Path: "/v2/apis/{apiId}", Operation: "GetApi"},
	{Method: "POST", Path: "/v2/apis/{apiId}", Operation: "UpdateApi"},
	{Method: "DELETE", Path: "/v2/apis/{apiId}", Operation: "DeleteApi"},
	{Method: "POST", Path: "/v2/apis/{apiId}/channelNamespaces", Operation: "CreateChannelNamespace"},
	{Method: "GET", Path: "/v2/apis/{apiId}/channelNamespaces", Operation: "ListChannelNamespaces"},
	{Method: "GET", Path: "/v2/apis/{apiId}/channelNamespaces/{name}", Operation: "GetChannelNamespace"},
	{Method: "POST", Path: "/v2/apis/{apiId}/channelNamespaces/{name}", Operation: "UpdateChannelNamespace"},
	{Method: "DELETE", Path: "/v2/apis/{apiId}/channelNamespaces/{name}", Operation: "DeleteChannelNamespace"},
}
