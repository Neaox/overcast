package groups

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// AppSync returns the AppSync service group.
func AppSync() ServiceGroup {
	g := &appsyncCliGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// apis
			"CreateGraphqlApi": g.CreateGraphqlApi,
			"GetGraphqlApi":    g.GetGraphqlApi,
			"UpdateGraphqlApi": g.UpdateGraphqlApi,
			"ListGraphqlApis":  g.ListGraphqlApis,
			"DeleteGraphqlApi": g.DeleteGraphqlApi,
			// schemas
			"StartSchemaCreation":     g.StartSchemaCreation,
			"GetSchemaCreationStatus": g.GetSchemaCreationStatus,
			// api-keys
			"CreateApiKey": g.CreateApiKey,
			"ListApiKeys":  g.ListApiKeys,
			"UpdateApiKey": g.UpdateApiKey,
			"DeleteApiKey": g.DeleteApiKey,
			// datasources
			"CreateDataSource": g.CreateDataSource,
			"GetDataSource":    g.GetDataSource,
			"UpdateDataSource": g.UpdateDataSource,
			"ListDataSources":  g.ListDataSources,
			"DeleteDataSource": g.DeleteDataSource,
			// functions
			"CreateFunction": g.CreateFunction,
			"GetFunction":    g.GetFunction,
			"UpdateFunction": g.UpdateFunction,
			"ListFunctions":  g.ListFunctions,
			"DeleteFunction": g.DeleteFunction,
			// resolvers
			"CreateResolver":          g.CreateResolver,
			"GetResolver":             g.GetResolver,
			"UpdateResolver":          g.UpdateResolver,
			"ListResolvers":           g.ListResolvers,
			"ListResolversByFunction": g.ListResolversByFunction,
			"DeleteResolver":          g.DeleteResolver,
			// types
			"CreateType": g.CreateType,
			"GetType":    g.GetType,
			"UpdateType": g.UpdateType,
			"ListTypes":  g.ListTypes,
			"DeleteType": g.DeleteType,
			// tags
			"TagResource":         g.TagResource,
			"ListTagsForResource": g.ListTagsForResource,
			"UntagResource":       g.UntagResource,
			// env-vars
			"PutGraphqlApiEnvironmentVariables": g.PutGraphqlApiEnvironmentVariables,
			"GetGraphqlApiEnvironmentVariables": g.GetGraphqlApiEnvironmentVariables,
			// domains
			"CreateDomainName":  g.CreateDomainName,
			"GetDomainName":     g.GetDomainName,
			"UpdateDomainName":  g.UpdateDomainName,
			"ListDomainNames":   g.ListDomainNames,
			"AssociateApi":      g.AssociateApi,
			"GetApiAssociation": g.GetApiAssociation,
			"DisassociateApi":   g.DisassociateApi,
			"DeleteDomainName":  g.DeleteDomainName,
			// cache
			"CreateApiCache": g.CreateApiCache,
			"GetApiCache":    g.GetApiCache,
			"UpdateApiCache": g.UpdateApiCache,
			"FlushApiCache":  g.FlushApiCache,
			"DeleteApiCache": g.DeleteApiCache,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"appsync-apis":        g.setupApis,
			"appsync-schemas":     g.setupGeneric,
			"appsync-api-keys":    g.setupGeneric,
			"appsync-datasources": g.setupGeneric,
			"appsync-functions":   g.setupFunctions,
			"appsync-resolvers":   g.setupResolvers,
			"appsync-types":       g.setupTypes,
			"appsync-tags":        g.setupGeneric,
			"appsync-env-vars":    g.setupGeneric,
			"appsync-domains":     g.setupGeneric,
			"appsync-cache":       g.setupGeneric,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"appsync-apis":        g.teardownApis,
			"appsync-schemas":     g.teardownGeneric,
			"appsync-api-keys":    g.teardownGeneric,
			"appsync-datasources": g.teardownGeneric,
			"appsync-functions":   g.teardownGeneric,
			"appsync-resolvers":   g.teardownGeneric,
			"appsync-types":       g.teardownGeneric,
			"appsync-tags":        g.teardownGeneric,
			"appsync-env-vars":    g.teardownGeneric,
			"appsync-domains":     g.teardownDomains,
			"appsync-cache":       g.teardownCache,
		},
	}
}

type appsyncCliGroup struct{}

// ── helpers ────────────────────────────────────────────────────────────────

func (g *appsyncCliGroup) createAPI(t *harness.TestContext, suffix string) (string, string, error) {
	name := fmt.Sprintf("compat-%s-%s", suffix, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-graphql-api",
		"--name", name, "--authentication-type", "API_KEY",
	)
	if err != nil {
		return "", "", err
	}
	api, _ := out["graphqlApi"].(map[string]interface{})
	id, _ := api["apiId"].(string)
	arn, _ := api["arn"].(string)
	if id == "" {
		return "", "", fmt.Errorf("createAPI: missing apiId")
	}
	return id, arn, nil
}

func (g *appsyncCliGroup) deleteAPI(t *harness.TestContext, key string) {
	if id := t.GetString(key); id != "" {
		awscli.Run(t.Endpoint, t.Region, "appsync", "delete-graphql-api", "--api-id", id) //nolint:errcheck
	}
}

// ── setup/teardown ─────────────────────────────────────────────────────────

func (g *appsyncCliGroup) setupApis(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *appsyncCliGroup) teardownApis(_ context.Context, t *harness.TestContext) error {
	g.deleteAPI(t, "api_id")
	return nil
}

func (g *appsyncCliGroup) setupGeneric(_ context.Context, t *harness.TestContext) error {
	id, arn, err := g.createAPI(t, "grp")
	if err != nil {
		return err
	}
	t.Set("api_id", id)
	t.Set("api_arn", arn)
	return nil
}

func (g *appsyncCliGroup) teardownGeneric(_ context.Context, t *harness.TestContext) error {
	g.deleteAPI(t, "api_id")
	return nil
}

func (g *appsyncCliGroup) setupFunctions(_ context.Context, t *harness.TestContext) error {
	id, _, err := g.createAPI(t, "fn")
	if err != nil {
		return err
	}
	t.Set("api_id", id)
	// Create a NONE data source for functions.
	_, err = awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-data-source",
		"--api-id", id, "--name", "FnNoneDS", "--type", "NONE",
	)
	return err
}

func (g *appsyncCliGroup) setupResolvers(_ context.Context, t *harness.TestContext) error {
	id, _, err := g.createAPI(t, "res")
	if err != nil {
		return err
	}
	t.Set("api_id", id)
	sdl := base64.StdEncoding.EncodeToString([]byte("type Query { hello: String, goodbye: String, extra: String }"))
	_, err = awscli.RunOutput(t.Endpoint, t.Region, "appsync", "start-schema-creation",
		"--api-id", id, "--definition", sdl,
	)
	if err != nil {
		return err
	}
	_, err = awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-data-source",
		"--api-id", id, "--name", "ResNoneDS", "--type", "NONE",
	)
	if err != nil {
		return err
	}
	fnOut, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-function",
		"--api-id", id, "--name", "ResFn", "--data-source-name", "ResNoneDS",
		"--request-mapping-template", "{}", "--response-mapping-template", "{}",
	)
	if err != nil {
		return err
	}
	fc, _ := fnOut["functionConfiguration"].(map[string]interface{})
	fnID, _ := fc["functionId"].(string)
	t.Set("fn_id", fnID)
	return nil
}

func (g *appsyncCliGroup) setupTypes(_ context.Context, t *harness.TestContext) error {
	id, _, err := g.createAPI(t, "types")
	if err != nil {
		return err
	}
	t.Set("api_id", id)
	sdl := base64.StdEncoding.EncodeToString([]byte("type Query { hello: String }"))
	_, err = awscli.RunOutput(t.Endpoint, t.Region, "appsync", "start-schema-creation",
		"--api-id", id, "--definition", sdl,
	)
	return err
}

func (g *appsyncCliGroup) teardownDomains(_ context.Context, t *harness.TestContext) error {
	if dn := t.GetString("domain_name"); dn != "" {
		awscli.Run(t.Endpoint, t.Region, "appsync", "disassociate-api", "--domain-name", dn)   //nolint:errcheck
		awscli.Run(t.Endpoint, t.Region, "appsync", "delete-domain-name", "--domain-name", dn) //nolint:errcheck
	}
	g.deleteAPI(t, "api_id")
	return nil
}

func (g *appsyncCliGroup) teardownCache(_ context.Context, t *harness.TestContext) error {
	if id := t.GetString("api_id"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "appsync", "delete-api-cache", "--api-id", id) //nolint:errcheck
	}
	g.deleteAPI(t, "api_id")
	return nil
}

// ── appsync-apis ───────────────────────────────────────────────────────────

func (g *appsyncCliGroup) CreateGraphqlApi(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-graphql-api",
		"--name", name, "--authentication-type", "API_KEY",
	)
	if err != nil {
		return err
	}
	api, _ := out["graphqlApi"].(map[string]interface{})
	id, _ := api["apiId"].(string)
	arn, _ := api["arn"].(string)
	if id == "" {
		return fmt.Errorf("CreateGraphqlApi: missing apiId")
	}
	t.Set("api_id", id)
	t.Set("api_arn", arn)
	return nil
}

func (g *appsyncCliGroup) GetGraphqlApi(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	if id == "" {
		return fmt.Errorf("GetGraphqlApi: no api_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "get-graphql-api", "--api-id", id)
	if err != nil {
		return err
	}
	if out["graphqlApi"] == nil {
		return fmt.Errorf("GetGraphqlApi: missing graphqlApi")
	}
	return nil
}

func (g *appsyncCliGroup) UpdateGraphqlApi(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	if id == "" {
		return fmt.Errorf("UpdateGraphqlApi: no api_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "update-graphql-api",
		"--api-id", id, "--name", fmt.Sprintf("compat-updated-%s", t.RunID),
		"--authentication-type", "API_KEY",
	)
	if err != nil {
		return err
	}
	if out["graphqlApi"] == nil {
		return fmt.Errorf("UpdateGraphqlApi: missing graphqlApi")
	}
	return nil
}

func (g *appsyncCliGroup) ListGraphqlApis(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "list-graphql-apis")
	return err
}

func (g *appsyncCliGroup) DeleteGraphqlApi(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-del-%s", t.RunID)
	out, _ := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-graphql-api",
		"--name", name, "--authentication-type", "API_KEY",
	)
	api, _ := out["graphqlApi"].(map[string]interface{})
	id, _ := api["apiId"].(string)
	if id == "" {
		return fmt.Errorf("DeleteGraphqlApi: could not create API to delete")
	}
	return awscli.Run(t.Endpoint, t.Region, "appsync", "delete-graphql-api", "--api-id", id)
}

// ── appsync-schemas ────────────────────────────────────────────────────────

func (g *appsyncCliGroup) StartSchemaCreation(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	sdl := base64.StdEncoding.EncodeToString([]byte("type Query { hello: String }"))
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "start-schema-creation",
		"--api-id", id, "--definition", sdl,
	)
	if err != nil {
		return err
	}
	if out["status"] == nil {
		return fmt.Errorf("StartSchemaCreation: missing status")
	}
	return nil
}

func (g *appsyncCliGroup) GetSchemaCreationStatus(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "get-schema-creation-status", "--api-id", id)
	if err != nil {
		return err
	}
	if out["status"] == nil {
		return fmt.Errorf("GetSchemaCreationStatus: missing status")
	}
	return nil
}

// ── appsync-api-keys ───────────────────────────────────────────────────────

func (g *appsyncCliGroup) CreateApiKey(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-api-key",
		"--api-id", id, "--description", "compat test key",
	)
	if err != nil {
		return err
	}
	key, _ := out["apiKey"].(map[string]interface{})
	keyID, _ := key["id"].(string)
	if keyID == "" {
		return fmt.Errorf("CreateApiKey: missing id")
	}
	t.Set("key_id", keyID)
	return nil
}

func (g *appsyncCliGroup) ListApiKeys(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "list-api-keys", "--api-id", id)
	return err
}

func (g *appsyncCliGroup) UpdateApiKey(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	keyID := t.GetString("key_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "update-api-key",
		"--api-id", id, "--id", keyID, "--description", "updated compat key",
	)
	return err
}

func (g *appsyncCliGroup) DeleteApiKey(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	cr, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-api-key", "--api-id", id)
	if err != nil {
		return err
	}
	key, _ := cr["apiKey"].(map[string]interface{})
	keyID, _ := key["id"].(string)
	return awscli.Run(t.Endpoint, t.Region, "appsync", "delete-api-key", "--api-id", id, "--id", keyID)
}

// ── appsync-datasources ────────────────────────────────────────────────────

func (g *appsyncCliGroup) CreateDataSource(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-data-source",
		"--api-id", id, "--name", "CompatNoneDS", "--type", "NONE",
	)
	if err != nil {
		return err
	}
	ds, _ := out["dataSource"].(map[string]interface{})
	if ds["name"] != "CompatNoneDS" {
		return fmt.Errorf("CreateDataSource: name mismatch")
	}
	return nil
}

func (g *appsyncCliGroup) GetDataSource(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "get-data-source",
		"--api-id", id, "--name", "CompatNoneDS",
	)
	if err != nil {
		return err
	}
	ds, _ := out["dataSource"].(map[string]interface{})
	if ds["dataSourceArn"] == nil {
		return fmt.Errorf("GetDataSource: missing ARN")
	}
	return nil
}

func (g *appsyncCliGroup) UpdateDataSource(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "update-data-source",
		"--api-id", id, "--name", "CompatNoneDS", "--type", "NONE", "--description", "updated",
	)
	return err
}

func (g *appsyncCliGroup) ListDataSources(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "list-data-sources", "--api-id", id)
	return err
}

func (g *appsyncCliGroup) DeleteDataSource(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-data-source",
		"--api-id", id, "--name", "CompatDelDS", "--type", "NONE",
	)
	if err != nil {
		return err
	}
	return awscli.Run(t.Endpoint, t.Region, "appsync", "delete-data-source", "--api-id", id, "--name", "CompatDelDS")
}

// ── appsync-functions ──────────────────────────────────────────────────────

func (g *appsyncCliGroup) CreateFunction(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-function",
		"--api-id", id, "--name", "CompatFn", "--data-source-name", "FnNoneDS",
		"--request-mapping-template", `{"version":"2018-05-29","payload":{}}`,
		"--response-mapping-template", "$util.toJson($context.result)",
	)
	if err != nil {
		return err
	}
	fc, _ := out["functionConfiguration"].(map[string]interface{})
	fnID, _ := fc["functionId"].(string)
	if fnID == "" {
		return fmt.Errorf("CreateFunction: missing functionId")
	}
	t.Set("fn_id", fnID)
	return nil
}

func (g *appsyncCliGroup) GetFunction(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	fnID := t.GetString("fn_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "get-function",
		"--api-id", id, "--function-id", fnID,
	)
	return err
}

func (g *appsyncCliGroup) UpdateFunction(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	fnID := t.GetString("fn_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "update-function",
		"--api-id", id, "--function-id", fnID, "--name", "CompatFnUpdated",
		"--data-source-name", "FnNoneDS",
		"--request-mapping-template", `{"version":"2018-05-29","payload":{}}`,
		"--response-mapping-template", "$util.toJson($context.result)",
	)
	return err
}

func (g *appsyncCliGroup) ListFunctions(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "list-functions", "--api-id", id)
	return err
}

func (g *appsyncCliGroup) DeleteFunction(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	cr, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-function",
		"--api-id", id, "--name", "CompatFnDel", "--data-source-name", "FnNoneDS",
		"--request-mapping-template", "{}", "--response-mapping-template", "{}",
	)
	if err != nil {
		return err
	}
	fc, _ := cr["functionConfiguration"].(map[string]interface{})
	fnID, _ := fc["functionId"].(string)
	return awscli.Run(t.Endpoint, t.Region, "appsync", "delete-function", "--api-id", id, "--function-id", fnID)
}

// ── appsync-resolvers ──────────────────────────────────────────────────────

func (g *appsyncCliGroup) CreateResolver(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-resolver",
		"--api-id", id, "--type-name", "Query", "--field-name", "hello",
		"--data-source-name", "ResNoneDS", "--kind", "UNIT",
		"--request-mapping-template", `{"version":"2018-05-29","payload":"world"}`,
		"--response-mapping-template", "$util.toJson($context.result)",
	)
	if err != nil {
		return err
	}
	r, _ := out["resolver"].(map[string]interface{})
	if r["fieldName"] == nil {
		return fmt.Errorf("CreateResolver: missing fieldName")
	}
	return nil
}

func (g *appsyncCliGroup) GetResolver(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "get-resolver",
		"--api-id", id, "--type-name", "Query", "--field-name", "hello",
	)
	if err != nil {
		return err
	}
	r, _ := out["resolver"].(map[string]interface{})
	if r["resolverArn"] == nil {
		return fmt.Errorf("GetResolver: missing resolverArn")
	}
	return nil
}

func (g *appsyncCliGroup) UpdateResolver(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "update-resolver",
		"--api-id", id, "--type-name", "Query", "--field-name", "hello",
		"--data-source-name", "ResNoneDS", "--kind", "UNIT",
		"--request-mapping-template", `{"version":"2018-05-29","payload":"updated"}`,
		"--response-mapping-template", "$util.toJson($context.result)",
	)
	return err
}

func (g *appsyncCliGroup) ListResolvers(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "list-resolvers",
		"--api-id", id, "--type-name", "Query",
	)
	return err
}

func (g *appsyncCliGroup) ListResolversByFunction(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	fnID := t.GetString("fn_id")
	// Create a pipeline resolver that uses the function.
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-resolver",
		"--api-id", id, "--type-name", "Query", "--field-name", "goodbye",
		"--kind", "PIPELINE",
		"--pipeline-config", fmt.Sprintf(`{"functions":["%s"]}`, fnID),
		"--request-mapping-template", "{}",
		"--response-mapping-template", "$util.toJson($context.result)",
	)
	if err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "list-resolvers-by-function",
		"--api-id", id, "--function-id", fnID,
	)
	if err != nil {
		return err
	}
	resolvers, _ := out["resolvers"].([]interface{})
	if len(resolvers) == 0 {
		return fmt.Errorf("ListResolversByFunction: expected at least one resolver")
	}
	return nil
}

func (g *appsyncCliGroup) DeleteResolver(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-resolver",
		"--api-id", id, "--type-name", "Query", "--field-name", "extra",
		"--data-source-name", "ResNoneDS", "--kind", "UNIT",
	)
	if err != nil {
		return err
	}
	return awscli.Run(t.Endpoint, t.Region, "appsync", "delete-resolver",
		"--api-id", id, "--type-name", "Query", "--field-name", "extra",
	)
}

// ── appsync-types ──────────────────────────────────────────────────────────

func (g *appsyncCliGroup) CreateType(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-type",
		"--api-id", id,
		"--definition", "type CompatType { id: ID, name: String }",
		"--format", "SDL",
	)
	if err != nil {
		return err
	}
	tp, _ := out["type"].(map[string]interface{})
	if tp["name"] == nil {
		return fmt.Errorf("CreateType: missing name")
	}
	return nil
}

func (g *appsyncCliGroup) GetType(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "get-type",
		"--api-id", id, "--type-name", "CompatType", "--format", "SDL",
	)
	return err
}

func (g *appsyncCliGroup) UpdateType(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "update-type",
		"--api-id", id, "--type-name", "CompatType",
		"--definition", "type CompatType { id: ID, name: String, age: Int }",
		"--format", "SDL",
	)
	return err
}

func (g *appsyncCliGroup) ListTypes(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "list-types",
		"--api-id", id, "--format", "SDL",
	)
	return err
}

func (g *appsyncCliGroup) DeleteType(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-type",
		"--api-id", id,
		"--definition", "type CompatDelType { x: String }",
		"--format", "SDL",
	)
	if err != nil {
		return err
	}
	return awscli.Run(t.Endpoint, t.Region, "appsync", "delete-type", "--api-id", id, "--type-name", "CompatDelType")
}

// ── appsync-tags ───────────────────────────────────────────────────────────

func (g *appsyncCliGroup) TagResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("api_arn")
	return awscli.Run(t.Endpoint, t.Region, "appsync", "tag-resource",
		"--resource-arn", arn, "--tags", "env=test,suite=compat",
	)
}

func (g *appsyncCliGroup) ListTagsForResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("api_arn")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "list-tags-for-resource",
		"--resource-arn", arn,
	)
	if err != nil {
		return err
	}
	tags, _ := out["tags"].(map[string]interface{})
	if tags["env"] != "test" {
		return fmt.Errorf("ListTagsForResource: tag value mismatch")
	}
	return nil
}

func (g *appsyncCliGroup) UntagResource(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("api_arn")
	return awscli.Run(t.Endpoint, t.Region, "appsync", "untag-resource",
		"--resource-arn", arn, "--tag-keys", "suite",
	)
}

// ── appsync-env-vars ───────────────────────────────────────────────────────

func (g *appsyncCliGroup) PutGraphqlApiEnvironmentVariables(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "put-graphql-api-environment-variables",
		"--api-id", id, "--environment-variables", `{"DB_HOST":"localhost","DB_PORT":"5432"}`,
	)
	return err
}

func (g *appsyncCliGroup) GetGraphqlApiEnvironmentVariables(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "get-graphql-api-environment-variables",
		"--api-id", id,
	)
	if err != nil {
		return err
	}
	env, _ := out["environmentVariables"].(map[string]interface{})
	if env["DB_HOST"] != "localhost" {
		return fmt.Errorf("GetGraphqlApiEnvironmentVariables: value mismatch")
	}
	return nil
}

// ── appsync-domains ────────────────────────────────────────────────────────

func (g *appsyncCliGroup) CreateDomainName(_ context.Context, t *harness.TestContext) error {
	dn := fmt.Sprintf("%s.example.com", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-domain-name",
		"--domain-name", dn,
		"--certificate-arn", "arn:aws:acm:us-east-1:000000000000:certificate/test",
	)
	if err != nil {
		return err
	}
	cfg, _ := out["domainNameConfig"].(map[string]interface{})
	if cfg["domainName"] == nil {
		return fmt.Errorf("CreateDomainName: missing domainName")
	}
	t.Set("domain_name", dn)
	return nil
}

func (g *appsyncCliGroup) GetDomainName(_ context.Context, t *harness.TestContext) error {
	dn := t.GetString("domain_name")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "get-domain-name", "--domain-name", dn)
	return err
}

func (g *appsyncCliGroup) UpdateDomainName(_ context.Context, t *harness.TestContext) error {
	dn := t.GetString("domain_name")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "update-domain-name",
		"--domain-name", dn, "--description", "updated-domain",
	)
	return err
}

func (g *appsyncCliGroup) ListDomainNames(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "list-domain-names")
	return err
}

func (g *appsyncCliGroup) AssociateApi(_ context.Context, t *harness.TestContext) error {
	dn := t.GetString("domain_name")
	id := t.GetString("api_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "associate-api",
		"--domain-name", dn, "--api-id", id,
	)
	if err != nil {
		return err
	}
	if out["apiAssociation"] == nil {
		return fmt.Errorf("AssociateApi: missing apiAssociation")
	}
	return nil
}

func (g *appsyncCliGroup) GetApiAssociation(_ context.Context, t *harness.TestContext) error {
	dn := t.GetString("domain_name")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "get-api-association", "--domain-name", dn)
	if err != nil {
		return err
	}
	if out["apiAssociation"] == nil {
		return fmt.Errorf("GetApiAssociation: missing apiAssociation")
	}
	return nil
}

func (g *appsyncCliGroup) DisassociateApi(_ context.Context, t *harness.TestContext) error {
	dn := t.GetString("domain_name")
	return awscli.Run(t.Endpoint, t.Region, "appsync", "disassociate-api", "--domain-name", dn)
}

func (g *appsyncCliGroup) DeleteDomainName(_ context.Context, t *harness.TestContext) error {
	dn := fmt.Sprintf("del-%s.example.com", t.RunID)
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-domain-name",
		"--domain-name", dn,
		"--certificate-arn", "arn:aws:acm:us-east-1:000000000000:certificate/test",
	)
	if err != nil {
		return err
	}
	return awscli.Run(t.Endpoint, t.Region, "appsync", "delete-domain-name", "--domain-name", dn)
}

// ── appsync-cache ──────────────────────────────────────────────────────────

func (g *appsyncCliGroup) CreateApiCache(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "create-api-cache",
		"--api-id", id, "--type", "T2_SMALL",
		"--api-caching-behavior", "FULL_REQUEST_CACHING", "--ttl", "300",
	)
	if err != nil {
		return err
	}
	if out["apiCache"] == nil {
		return fmt.Errorf("CreateApiCache: missing apiCache")
	}
	return nil
}

func (g *appsyncCliGroup) GetApiCache(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "get-api-cache", "--api-id", id)
	return err
}

func (g *appsyncCliGroup) UpdateApiCache(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "appsync", "update-api-cache",
		"--api-id", id, "--type", "T2_MEDIUM",
		"--api-caching-behavior", "FULL_REQUEST_CACHING", "--ttl", "600",
	)
	return err
}

func (g *appsyncCliGroup) FlushApiCache(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	return awscli.Run(t.Endpoint, t.Region, "appsync", "flush-api-cache", "--api-id", id)
}

func (g *appsyncCliGroup) DeleteApiCache(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("api_id")
	return awscli.Run(t.Endpoint, t.Region, "appsync", "delete-api-cache", "--api-id", id)
}
