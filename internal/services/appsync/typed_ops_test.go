package appsync

import (
	"testing"

	"github.com/Neaox/overcast/internal/protocol/op"
)

func TestTypedOps_matchDispatchSurface(t *testing.T) {
	h := &Handler{}
	ops := h.typedOps()
	expected := []string{
		"CreateGraphqlApi",
		"GetGraphqlApi",
		"ListGraphqlApis",
		"UpdateGraphqlApi",
		"DeleteGraphqlApi",
		"TagResource",
		"UntagResource",
		"ListTagsForResource",
		"CreateDomainName",
		"GetDomainName",
		"ListDomainNames",
		"UpdateDomainName",
		"DeleteDomainName",
		"AssociateApi",
		"GetApiAssociation",
		"DisassociateApi",
		"StartSchemaCreation",
		"GetSchemaCreationStatus",
		"GetIntrospectionSchema",
		"CreateApiKey",
		"ListApiKeys",
		"UpdateApiKey",
		"DeleteApiKey",
		"CreateDataSource",
		"GetDataSource",
		"ListDataSources",
		"UpdateDataSource",
		"DeleteDataSource",
		"CreateFunction",
		"GetFunction",
		"ListFunctions",
		"UpdateFunction",
		"DeleteFunction",
		"ListResolversByFunction",
		"CreateResolver",
		"GetResolver",
		"ListResolvers",
		"UpdateResolver",
		"DeleteResolver",
		"GetType",
		"CreateType",
		"UpdateType",
		"DeleteType",
		"ListTypes",
		"CreateApiCache",
		"GetApiCache",
		"UpdateApiCache",
		"DeleteApiCache",
		"FlushApiCache",
		"PutGraphqlApiEnvironmentVariables",
		"GetGraphqlApiEnvironmentVariables",
		"AssociateSourceGraphqlApi",
		"GetSourceApiAssociation",
		"ListSourceApiAssociations",
		"DisassociateSourceGraphqlApi",
		"AssociateMergedGraphqlApi",
		"DisassociateMergedGraphqlApi",
		"StartSchemaMerge",
		"EvaluateMappingTemplate",
		"EvaluateCode",
		"CreateApi",
		"GetApi",
		"ListApis",
		"UpdateApi",
		"DeleteApi",
		"CreateChannelNamespace",
		"GetChannelNamespace",
		"ListChannelNamespaces",
		"UpdateChannelNamespace",
		"DeleteChannelNamespace",
	}

	if len(ops) != len(expected) {
		t.Fatalf("typed ops len = %d, expected %d", len(ops), len(expected))
	}
	for _, name := range expected {
		operation, ok := ops[name]
		if !ok {
			t.Fatalf("missing typed op %q", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed op %q has Name() %q", name, operation.Name())
		}
	}
	for name, operation := range ops {
		if _, ok := operation.(*op.Raw); ok {
			t.Fatalf("%s registered as raw operation", name)
		}
	}
}
