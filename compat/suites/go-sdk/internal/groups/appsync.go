package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appsync"
	"github.com/aws/aws-sdk-go-v2/service/appsync/types"
)

func AppSync(c *clients.Clients) ServiceGroup {
	g := &appsyncGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// appsync-apis
			"CreateGraphqlApi": g.CreateGraphqlApi,
			"GetGraphqlApi":    g.GetGraphqlApi,
			"UpdateGraphqlApi": g.UpdateGraphqlApi,
			"ListGraphqlApis":  g.ListGraphqlApis,
			"DeleteGraphqlApi": g.DeleteGraphqlApi,
			// appsync-schemas
			"StartSchemaCreation":     g.StartSchemaCreation,
			"GetSchemaCreationStatus": g.GetSchemaCreationStatus,
			// appsync-api-keys
			"CreateApiKey": g.CreateApiKey,
			"ListApiKeys":  g.ListApiKeys,
			"UpdateApiKey": g.UpdateApiKey,
			"DeleteApiKey": g.DeleteApiKey,
			// appsync-datasources
			"CreateDataSource": g.CreateDataSource,
			"GetDataSource":    g.GetDataSource,
			"UpdateDataSource": g.UpdateDataSource,
			"ListDataSources":  g.ListDataSources,
			"DeleteDataSource": g.DeleteDataSource,
			// appsync-functions — group-qualified to avoid overriding Lambda names
			"appsync-functions:CreateFunction": g.CreateFunction,
			"appsync-functions:GetFunction":    g.GetFunction,
			"appsync-functions:UpdateFunction": g.UpdateFunction,
			"appsync-functions:ListFunctions":  g.ListFunctions,
			"appsync-functions:DeleteFunction": g.DeleteFunction,
			// appsync-resolvers
			"CreateResolver":          g.CreateResolver,
			"GetResolver":             g.GetResolver,
			"UpdateResolver":          g.UpdateResolver,
			"ListResolvers":           g.ListResolvers,
			"ListResolversByFunction": g.ListResolversByFunction,
			"DeleteResolver":          g.DeleteResolver,
			// appsync-types
			"CreateType": g.CreateType,
			"GetType":    g.GetType,
			"UpdateType": g.UpdateType,
			"ListTypes":  g.ListTypes,
			"DeleteType": g.DeleteType,
			// appsync-tags — group-qualified to avoid overriding SecretsManager names
			"appsync-tags:TagResource":         g.TagResource,
			"appsync-tags:ListTagsForResource": g.ListTagsForResource,
			"appsync-tags:UntagResource":       g.UntagResource,
			// appsync-env-vars
			"PutGraphqlApiEnvironmentVariables": g.PutGraphqlApiEnvironmentVariables,
			"GetGraphqlApiEnvironmentVariables": g.GetGraphqlApiEnvironmentVariables,
			// appsync-domains
			"CreateDomainName":  g.CreateDomainName,
			"GetDomainName":     g.GetDomainName,
			"UpdateDomainName":  g.UpdateDomainName,
			"ListDomainNames":   g.ListDomainNames,
			"AssociateApi":      g.AssociateApi,
			"GetApiAssociation": g.GetApiAssociation,
			"DisassociateApi":   g.DisassociateApi,
			"DeleteDomainName":  g.DeleteDomainName,
			// appsync-cache
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
			"appsync-tags":        g.setupTags,
			"appsync-env-vars":    g.setupGeneric,
			"appsync-domains":     g.setupGeneric,
			"appsync-cache":       g.setupGeneric,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"appsync-apis":        g.teardownApis,
			"appsync-schemas":     g.teardownGeneric,
			"appsync-api-keys":    g.teardownGeneric,
			"appsync-datasources": g.teardownGeneric,
			"appsync-functions":   g.teardownFunctions,
			"appsync-resolvers":   g.teardownGeneric,
			"appsync-types":       g.teardownGeneric,
			"appsync-tags":        g.teardownTags,
			"appsync-env-vars":    g.teardownGeneric,
			"appsync-domains":     g.teardownDomains,
			"appsync-cache":       g.teardownCache,
		},
	}
}

type appsyncGroup struct{ c *clients.Clients }

func (g *appsyncGroup) cl() *appsync.Client { return g.c.AppSync() }

func (g *appsyncGroup) setupApis(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *appsyncGroup) teardownApis(ctx context.Context, t *harness.TestContext) error {
	if id := t.GetString("appsync_api_id"); id != "" {
		g.cl().DeleteGraphqlApi(ctx, &appsync.DeleteGraphqlApiInput{ApiId: aws.String(id)}) //nolint:errcheck
	}
	return nil
}

func (g *appsyncGroup) CreateGraphqlApi(ctx context.Context, t *harness.TestContext) error {
	apiName := fmt.Sprintf("compat-%s", t.RunID)
	resp, err := g.cl().CreateGraphqlApi(ctx, &appsync.CreateGraphqlApiInput{
		Name:               aws.String(apiName),
		AuthenticationType: types.AuthenticationTypeApiKey,
	})
	if err != nil {
		return err
	}
	if resp.GraphqlApi == nil || resp.GraphqlApi.ApiId == nil {
		return fmt.Errorf("CreateGraphqlApi: missing apiId")
	}
	t.Set("appsync_api_id", *resp.GraphqlApi.ApiId)
	return nil
}

func (g *appsyncGroup) GetGraphqlApi(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("appsync_api_id")
	if apiID == "" {
		return fmt.Errorf("GetGraphqlApi: no api from CreateGraphqlApi")
	}
	resp, err := g.cl().GetGraphqlApi(ctx, &appsync.GetGraphqlApiInput{
		ApiId: aws.String(apiID),
	})
	if err != nil {
		return err
	}
	if resp.GraphqlApi == nil || resp.GraphqlApi.ApiId == nil {
		return fmt.Errorf("GetGraphqlApi: missing apiId")
	}
	return nil
}

func (g *appsyncGroup) ListGraphqlApis(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListGraphqlApis(ctx, &appsync.ListGraphqlApisInput{})
	return err
}

func (g *appsyncGroup) DeleteGraphqlApi(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("appsync_api_id")
	if apiID == "" {
		return nil
	}
	_, err := g.cl().DeleteGraphqlApi(ctx, &appsync.DeleteGraphqlApiInput{
		ApiId: aws.String(apiID),
	})
	return err
}

// ── appsync-functions ────────────────────────────────────────────────────────

func (g *appsyncGroup) setupFunctions(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateGraphqlApi(ctx, &appsync.CreateGraphqlApiInput{
		Name:               aws.String(fmt.Sprintf("compat-fn-%s", t.RunID)),
		AuthenticationType: types.AuthenticationTypeApiKey,
	})
	if err != nil {
		return err
	}
	apiID := aws.ToString(resp.GraphqlApi.ApiId)
	t.Set("fn_api_id", apiID)
	_, err = g.cl().CreateDataSource(ctx, &appsync.CreateDataSourceInput{
		ApiId: aws.String(apiID),
		Name:  aws.String("FnNoneDS"),
		Type:  types.DataSourceTypeNone,
	})
	return err
}

func (g *appsyncGroup) teardownFunctions(ctx context.Context, t *harness.TestContext) error {
	if id := t.GetString("fn_api_id"); id != "" {
		g.cl().DeleteGraphqlApi(ctx, &appsync.DeleteGraphqlApiInput{ApiId: aws.String(id)}) //nolint:errcheck
	}
	return nil
}

func (g *appsyncGroup) CreateFunction(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("fn_api_id")
	if apiID == "" {
		return fmt.Errorf("CreateFunction: no api from setup")
	}
	resp, err := g.cl().CreateFunction(ctx, &appsync.CreateFunctionInput{
		ApiId:                   aws.String(apiID),
		Name:                    aws.String("CompatFn"),
		DataSourceName:          aws.String("FnNoneDS"),
		RequestMappingTemplate:  aws.String(`{"version":"2018-05-29","payload":{}}`),
		ResponseMappingTemplate: aws.String("$util.toJson($context.result)"),
	})
	if err != nil {
		return err
	}
	if resp.FunctionConfiguration == nil || resp.FunctionConfiguration.FunctionId == nil {
		return fmt.Errorf("CreateFunction: missing functionId")
	}
	t.Set("fn_id", aws.ToString(resp.FunctionConfiguration.FunctionId))
	return nil
}

func (g *appsyncGroup) GetFunction(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("fn_api_id")
	fnID := t.GetString("fn_id")
	if apiID == "" || fnID == "" {
		return fmt.Errorf("GetFunction: missing api or function id from setup")
	}
	resp, err := g.cl().GetFunction(ctx, &appsync.GetFunctionInput{
		ApiId:      aws.String(apiID),
		FunctionId: aws.String(fnID),
	})
	if err != nil {
		return err
	}
	if resp.FunctionConfiguration == nil || resp.FunctionConfiguration.Name == nil {
		return fmt.Errorf("GetFunction: missing name")
	}
	return nil
}

func (g *appsyncGroup) UpdateFunction(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("fn_api_id")
	fnID := t.GetString("fn_id")
	if apiID == "" || fnID == "" {
		return fmt.Errorf("UpdateFunction: missing api or function id")
	}
	_, err := g.cl().UpdateFunction(ctx, &appsync.UpdateFunctionInput{
		ApiId:                   aws.String(apiID),
		FunctionId:              aws.String(fnID),
		Name:                    aws.String("CompatFnUpdated"),
		DataSourceName:          aws.String("FnNoneDS"),
		RequestMappingTemplate:  aws.String(`{"version":"2018-05-29","payload":{}}`),
		ResponseMappingTemplate: aws.String("$util.toJson($context.result)"),
	})
	return err
}

func (g *appsyncGroup) ListFunctions(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("fn_api_id")
	if apiID == "" {
		return fmt.Errorf("ListFunctions: no api from setup")
	}
	_, err := g.cl().ListFunctions(ctx, &appsync.ListFunctionsInput{ApiId: aws.String(apiID)})
	return err
}

func (g *appsyncGroup) DeleteFunction(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("fn_api_id")
	if apiID == "" {
		return fmt.Errorf("DeleteFunction: no api from setup")
	}
	cr, err := g.cl().CreateFunction(ctx, &appsync.CreateFunctionInput{
		ApiId:                   aws.String(apiID),
		Name:                    aws.String("CompatFnDel"),
		DataSourceName:          aws.String("FnNoneDS"),
		RequestMappingTemplate:  aws.String("{}"),
		ResponseMappingTemplate: aws.String("{}"),
	})
	if err != nil {
		return err
	}
	_, err = g.cl().DeleteFunction(ctx, &appsync.DeleteFunctionInput{
		ApiId:      aws.String(apiID),
		FunctionId: cr.FunctionConfiguration.FunctionId,
	})
	return err
}

// ── appsync-tags ─────────────────────────────────────────────────────────────

func (g *appsyncGroup) setupTags(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateGraphqlApi(ctx, &appsync.CreateGraphqlApiInput{
		Name:               aws.String(fmt.Sprintf("compat-tag-%s", t.RunID)),
		AuthenticationType: types.AuthenticationTypeApiKey,
	})
	if err != nil {
		return err
	}
	t.Set("tag_api_id", aws.ToString(resp.GraphqlApi.ApiId))
	t.Set("tag_api_arn", aws.ToString(resp.GraphqlApi.Arn))
	return nil
}

func (g *appsyncGroup) teardownTags(ctx context.Context, t *harness.TestContext) error {
	if id := t.GetString("tag_api_id"); id != "" {
		g.cl().DeleteGraphqlApi(ctx, &appsync.DeleteGraphqlApiInput{ApiId: aws.String(id)}) //nolint:errcheck
	}
	return nil
}

func (g *appsyncGroup) TagResource(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("tag_api_arn")
	if arn == "" {
		return fmt.Errorf("TagResource: no API ARN from setup")
	}
	_, err := g.cl().TagResource(ctx, &appsync.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        map[string]string{"env": "compat"},
	})
	return err
}

func (g *appsyncGroup) ListTagsForResource(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("tag_api_arn")
	if arn == "" {
		return fmt.Errorf("ListTagsForResource: no API ARN from setup")
	}
	resp, err := g.cl().ListTagsForResource(ctx, &appsync.ListTagsForResourceInput{
		ResourceArn: aws.String(arn),
	})
	if err != nil {
		return err
	}
	if resp.Tags["env"] != "compat" {
		return fmt.Errorf("ListTagsForResource: expected tag env=compat, got %v", resp.Tags)
	}
	return nil
}

func (g *appsyncGroup) UntagResource(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("tag_api_arn")
	if arn == "" {
		return fmt.Errorf("UntagResource: no API ARN from setup")
	}
	_, err := g.cl().UntagResource(ctx, &appsync.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"env"},
	})
	return err
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (g *appsyncGroup) setupGeneric(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateGraphqlApi(ctx, &appsync.CreateGraphqlApiInput{
		Name:               aws.String(fmt.Sprintf("compat-grp-%s", t.RunID)),
		AuthenticationType: types.AuthenticationTypeApiKey,
	})
	if err != nil {
		return err
	}
	t.Set("api_id", aws.ToString(resp.GraphqlApi.ApiId))
	t.Set("api_arn", aws.ToString(resp.GraphqlApi.Arn))
	return nil
}

func (g *appsyncGroup) teardownGeneric(ctx context.Context, t *harness.TestContext) error {
	if id := t.GetString("api_id"); id != "" {
		g.cl().DeleteGraphqlApi(ctx, &appsync.DeleteGraphqlApiInput{ApiId: aws.String(id)}) //nolint:errcheck
	}
	return nil
}

// ── appsync-apis: UpdateGraphqlApi ───────────────────────────────────────────

func (g *appsyncGroup) UpdateGraphqlApi(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("appsync_api_id")
	if apiID == "" {
		return fmt.Errorf("UpdateGraphqlApi: no api_id")
	}
	resp, err := g.cl().UpdateGraphqlApi(ctx, &appsync.UpdateGraphqlApiInput{
		ApiId:              aws.String(apiID),
		Name:               aws.String(fmt.Sprintf("compat-updated-%s", t.RunID)),
		AuthenticationType: types.AuthenticationTypeApiKey,
	})
	if err != nil {
		return err
	}
	if resp.GraphqlApi == nil {
		return fmt.Errorf("UpdateGraphqlApi: missing graphqlApi")
	}
	return nil
}

// ── appsync-schemas ──────────────────────────────────────────────────────────

func (g *appsyncGroup) StartSchemaCreation(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	resp, err := g.cl().StartSchemaCreation(ctx, &appsync.StartSchemaCreationInput{
		ApiId:      aws.String(apiID),
		Definition: []byte("type Query { hello: String }"),
	})
	if err != nil {
		return err
	}
	if resp.Status == "" {
		return fmt.Errorf("StartSchemaCreation: missing status")
	}
	return nil
}

func (g *appsyncGroup) GetSchemaCreationStatus(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	resp, err := g.cl().GetSchemaCreationStatus(ctx, &appsync.GetSchemaCreationStatusInput{
		ApiId: aws.String(apiID),
	})
	if err != nil {
		return err
	}
	if resp.Status == "" {
		return fmt.Errorf("GetSchemaCreationStatus: missing status")
	}
	return nil
}

// ── appsync-api-keys ─────────────────────────────────────────────────────────

func (g *appsyncGroup) CreateApiKey(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	resp, err := g.cl().CreateApiKey(ctx, &appsync.CreateApiKeyInput{
		ApiId:       aws.String(apiID),
		Description: aws.String("compat test key"),
	})
	if err != nil {
		return err
	}
	if resp.ApiKey == nil || resp.ApiKey.Id == nil {
		return fmt.Errorf("CreateApiKey: missing id")
	}
	t.Set("key_id", aws.ToString(resp.ApiKey.Id))
	return nil
}

func (g *appsyncGroup) ListApiKeys(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().ListApiKeys(ctx, &appsync.ListApiKeysInput{ApiId: aws.String(apiID)})
	return err
}

func (g *appsyncGroup) UpdateApiKey(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	keyID := t.GetString("key_id")
	_, err := g.cl().UpdateApiKey(ctx, &appsync.UpdateApiKeyInput{
		ApiId:       aws.String(apiID),
		Id:          aws.String(keyID),
		Description: aws.String("updated compat key"),
	})
	return err
}

func (g *appsyncGroup) DeleteApiKey(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	cr, err := g.cl().CreateApiKey(ctx, &appsync.CreateApiKeyInput{
		ApiId: aws.String(apiID),
	})
	if err != nil {
		return err
	}
	_, err = g.cl().DeleteApiKey(ctx, &appsync.DeleteApiKeyInput{
		ApiId: aws.String(apiID),
		Id:    cr.ApiKey.Id,
	})
	return err
}

// ── appsync-datasources ──────────────────────────────────────────────────────

func (g *appsyncGroup) CreateDataSource(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	resp, err := g.cl().CreateDataSource(ctx, &appsync.CreateDataSourceInput{
		ApiId: aws.String(apiID),
		Name:  aws.String("CompatNoneDS"),
		Type:  types.DataSourceTypeNone,
	})
	if err != nil {
		return err
	}
	if resp.DataSource == nil || aws.ToString(resp.DataSource.Name) != "CompatNoneDS" {
		return fmt.Errorf("CreateDataSource: name mismatch")
	}
	return nil
}

func (g *appsyncGroup) GetDataSource(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	resp, err := g.cl().GetDataSource(ctx, &appsync.GetDataSourceInput{
		ApiId: aws.String(apiID),
		Name:  aws.String("CompatNoneDS"),
	})
	if err != nil {
		return err
	}
	if resp.DataSource == nil || resp.DataSource.DataSourceArn == nil {
		return fmt.Errorf("GetDataSource: missing ARN")
	}
	return nil
}

func (g *appsyncGroup) UpdateDataSource(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().UpdateDataSource(ctx, &appsync.UpdateDataSourceInput{
		ApiId:       aws.String(apiID),
		Name:        aws.String("CompatNoneDS"),
		Type:        types.DataSourceTypeNone,
		Description: aws.String("updated"),
	})
	return err
}

func (g *appsyncGroup) ListDataSources(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().ListDataSources(ctx, &appsync.ListDataSourcesInput{ApiId: aws.String(apiID)})
	return err
}

func (g *appsyncGroup) DeleteDataSource(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().CreateDataSource(ctx, &appsync.CreateDataSourceInput{
		ApiId: aws.String(apiID),
		Name:  aws.String("CompatDelDS"),
		Type:  types.DataSourceTypeNone,
	})
	if err != nil {
		return err
	}
	_, err = g.cl().DeleteDataSource(ctx, &appsync.DeleteDataSourceInput{
		ApiId: aws.String(apiID),
		Name:  aws.String("CompatDelDS"),
	})
	return err
}

// ── appsync-resolvers ────────────────────────────────────────────────────────

func (g *appsyncGroup) setupResolvers(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateGraphqlApi(ctx, &appsync.CreateGraphqlApiInput{
		Name:               aws.String(fmt.Sprintf("compat-res-%s", t.RunID)),
		AuthenticationType: types.AuthenticationTypeApiKey,
	})
	if err != nil {
		return err
	}
	apiID := aws.ToString(resp.GraphqlApi.ApiId)
	t.Set("api_id", apiID)
	_, err = g.cl().StartSchemaCreation(ctx, &appsync.StartSchemaCreationInput{
		ApiId:      aws.String(apiID),
		Definition: []byte("type Query { hello: String, goodbye: String, extra: String }"),
	})
	if err != nil {
		return err
	}
	_, err = g.cl().CreateDataSource(ctx, &appsync.CreateDataSourceInput{
		ApiId: aws.String(apiID),
		Name:  aws.String("ResNoneDS"),
		Type:  types.DataSourceTypeNone,
	})
	if err != nil {
		return err
	}
	fnResp, err := g.cl().CreateFunction(ctx, &appsync.CreateFunctionInput{
		ApiId:                   aws.String(apiID),
		Name:                    aws.String("ResFn"),
		DataSourceName:          aws.String("ResNoneDS"),
		RequestMappingTemplate:  aws.String("{}"),
		ResponseMappingTemplate: aws.String("{}"),
	})
	if err != nil {
		return err
	}
	t.Set("fn_id", aws.ToString(fnResp.FunctionConfiguration.FunctionId))
	return nil
}

func (g *appsyncGroup) CreateResolver(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	resp, err := g.cl().CreateResolver(ctx, &appsync.CreateResolverInput{
		ApiId:                   aws.String(apiID),
		TypeName:                aws.String("Query"),
		FieldName:               aws.String("hello"),
		DataSourceName:          aws.String("ResNoneDS"),
		Kind:                    types.ResolverKindUnit,
		RequestMappingTemplate:  aws.String(`{"version":"2018-05-29","payload":"world"}`),
		ResponseMappingTemplate: aws.String("$util.toJson($context.result)"),
	})
	if err != nil {
		return err
	}
	if resp.Resolver == nil || resp.Resolver.FieldName == nil {
		return fmt.Errorf("CreateResolver: missing fieldName")
	}
	return nil
}

func (g *appsyncGroup) GetResolver(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	resp, err := g.cl().GetResolver(ctx, &appsync.GetResolverInput{
		ApiId:     aws.String(apiID),
		TypeName:  aws.String("Query"),
		FieldName: aws.String("hello"),
	})
	if err != nil {
		return err
	}
	if resp.Resolver == nil || resp.Resolver.ResolverArn == nil {
		return fmt.Errorf("GetResolver: missing resolverArn")
	}
	return nil
}

func (g *appsyncGroup) UpdateResolver(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().UpdateResolver(ctx, &appsync.UpdateResolverInput{
		ApiId:                   aws.String(apiID),
		TypeName:                aws.String("Query"),
		FieldName:               aws.String("hello"),
		DataSourceName:          aws.String("ResNoneDS"),
		Kind:                    types.ResolverKindUnit,
		RequestMappingTemplate:  aws.String(`{"version":"2018-05-29","payload":"updated"}`),
		ResponseMappingTemplate: aws.String("$util.toJson($context.result)"),
	})
	return err
}

func (g *appsyncGroup) ListResolvers(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().ListResolvers(ctx, &appsync.ListResolversInput{
		ApiId:    aws.String(apiID),
		TypeName: aws.String("Query"),
	})
	return err
}

func (g *appsyncGroup) ListResolversByFunction(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	fnID := t.GetString("fn_id")
	_, err := g.cl().CreateResolver(ctx, &appsync.CreateResolverInput{
		ApiId:     aws.String(apiID),
		TypeName:  aws.String("Query"),
		FieldName: aws.String("goodbye"),
		Kind:      types.ResolverKindPipeline,
		PipelineConfig: &types.PipelineConfig{
			Functions: []string{fnID},
		},
		RequestMappingTemplate:  aws.String("{}"),
		ResponseMappingTemplate: aws.String("$util.toJson($context.result)"),
	})
	if err != nil {
		return err
	}
	resp, err := g.cl().ListResolversByFunction(ctx, &appsync.ListResolversByFunctionInput{
		ApiId:      aws.String(apiID),
		FunctionId: aws.String(fnID),
	})
	if err != nil {
		return err
	}
	if len(resp.Resolvers) == 0 {
		return fmt.Errorf("ListResolversByFunction: expected at least one resolver")
	}
	return nil
}

func (g *appsyncGroup) DeleteResolver(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().CreateResolver(ctx, &appsync.CreateResolverInput{
		ApiId:          aws.String(apiID),
		TypeName:       aws.String("Query"),
		FieldName:      aws.String("extra"),
		DataSourceName: aws.String("ResNoneDS"),
		Kind:           types.ResolverKindUnit,
	})
	if err != nil {
		return err
	}
	_, err = g.cl().DeleteResolver(ctx, &appsync.DeleteResolverInput{
		ApiId:     aws.String(apiID),
		TypeName:  aws.String("Query"),
		FieldName: aws.String("extra"),
	})
	return err
}

// ── appsync-types ────────────────────────────────────────────────────────────

func (g *appsyncGroup) setupTypes(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateGraphqlApi(ctx, &appsync.CreateGraphqlApiInput{
		Name:               aws.String(fmt.Sprintf("compat-types-%s", t.RunID)),
		AuthenticationType: types.AuthenticationTypeApiKey,
	})
	if err != nil {
		return err
	}
	apiID := aws.ToString(resp.GraphqlApi.ApiId)
	t.Set("api_id", apiID)
	_, err = g.cl().StartSchemaCreation(ctx, &appsync.StartSchemaCreationInput{
		ApiId:      aws.String(apiID),
		Definition: []byte("type Query { hello: String }"),
	})
	return err
}

func (g *appsyncGroup) CreateType(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	resp, err := g.cl().CreateType(ctx, &appsync.CreateTypeInput{
		ApiId:      aws.String(apiID),
		Definition: aws.String("type CompatType { id: ID, name: String }"),
		Format:     types.TypeDefinitionFormatSdl,
	})
	if err != nil {
		return err
	}
	if resp.Type == nil || resp.Type.Name == nil {
		return fmt.Errorf("CreateType: missing name")
	}
	return nil
}

func (g *appsyncGroup) GetType(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().GetType(ctx, &appsync.GetTypeInput{
		ApiId:    aws.String(apiID),
		TypeName: aws.String("CompatType"),
		Format:   types.TypeDefinitionFormatSdl,
	})
	return err
}

func (g *appsyncGroup) UpdateType(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().UpdateType(ctx, &appsync.UpdateTypeInput{
		ApiId:      aws.String(apiID),
		TypeName:   aws.String("CompatType"),
		Definition: aws.String("type CompatType { id: ID, name: String, age: Int }"),
		Format:     types.TypeDefinitionFormatSdl,
	})
	return err
}

func (g *appsyncGroup) ListTypes(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().ListTypes(ctx, &appsync.ListTypesInput{
		ApiId:  aws.String(apiID),
		Format: types.TypeDefinitionFormatSdl,
	})
	return err
}

func (g *appsyncGroup) DeleteType(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().CreateType(ctx, &appsync.CreateTypeInput{
		ApiId:      aws.String(apiID),
		Definition: aws.String("type CompatDelType { x: String }"),
		Format:     types.TypeDefinitionFormatSdl,
	})
	if err != nil {
		return err
	}
	_, err = g.cl().DeleteType(ctx, &appsync.DeleteTypeInput{
		ApiId:    aws.String(apiID),
		TypeName: aws.String("CompatDelType"),
	})
	return err
}

// ── appsync-env-vars ─────────────────────────────────────────────────────────

func (g *appsyncGroup) PutGraphqlApiEnvironmentVariables(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().PutGraphqlApiEnvironmentVariables(ctx, &appsync.PutGraphqlApiEnvironmentVariablesInput{
		ApiId:                aws.String(apiID),
		EnvironmentVariables: map[string]string{"DB_HOST": "localhost", "DB_PORT": "5432"},
	})
	return err
}

func (g *appsyncGroup) GetGraphqlApiEnvironmentVariables(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	resp, err := g.cl().GetGraphqlApiEnvironmentVariables(ctx, &appsync.GetGraphqlApiEnvironmentVariablesInput{
		ApiId: aws.String(apiID),
	})
	if err != nil {
		return err
	}
	if resp.EnvironmentVariables["DB_HOST"] != "localhost" {
		return fmt.Errorf("GetGraphqlApiEnvironmentVariables: value mismatch")
	}
	return nil
}

// ── appsync-domains ──────────────────────────────────────────────────────────

func (g *appsyncGroup) teardownDomains(ctx context.Context, t *harness.TestContext) error {
	if dn := t.GetString("domain_name"); dn != "" {
		g.cl().DisassociateApi(ctx, &appsync.DisassociateApiInput{DomainName: aws.String(dn)})   //nolint:errcheck
		g.cl().DeleteDomainName(ctx, &appsync.DeleteDomainNameInput{DomainName: aws.String(dn)}) //nolint:errcheck
	}
	if id := t.GetString("api_id"); id != "" {
		g.cl().DeleteGraphqlApi(ctx, &appsync.DeleteGraphqlApiInput{ApiId: aws.String(id)}) //nolint:errcheck
	}
	return nil
}

func (g *appsyncGroup) CreateDomainName(ctx context.Context, t *harness.TestContext) error {
	dn := fmt.Sprintf("%s.example.com", t.RunID)
	resp, err := g.cl().CreateDomainName(ctx, &appsync.CreateDomainNameInput{
		DomainName:     aws.String(dn),
		CertificateArn: aws.String("arn:aws:acm:us-east-1:000000000000:certificate/test"),
	})
	if err != nil {
		return err
	}
	if resp.DomainNameConfig == nil || resp.DomainNameConfig.DomainName == nil {
		return fmt.Errorf("CreateDomainName: missing domainName")
	}
	t.Set("domain_name", dn)
	return nil
}

func (g *appsyncGroup) GetDomainName(ctx context.Context, t *harness.TestContext) error {
	dn := t.GetString("domain_name")
	_, err := g.cl().GetDomainName(ctx, &appsync.GetDomainNameInput{DomainName: aws.String(dn)})
	return err
}

func (g *appsyncGroup) UpdateDomainName(ctx context.Context, t *harness.TestContext) error {
	dn := t.GetString("domain_name")
	_, err := g.cl().UpdateDomainName(ctx, &appsync.UpdateDomainNameInput{
		DomainName:  aws.String(dn),
		Description: aws.String("updated-domain"),
	})
	return err
}

func (g *appsyncGroup) ListDomainNames(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListDomainNames(ctx, &appsync.ListDomainNamesInput{})
	return err
}

func (g *appsyncGroup) AssociateApi(ctx context.Context, t *harness.TestContext) error {
	dn := t.GetString("domain_name")
	apiID := t.GetString("api_id")
	resp, err := g.cl().AssociateApi(ctx, &appsync.AssociateApiInput{
		DomainName: aws.String(dn),
		ApiId:      aws.String(apiID),
	})
	if err != nil {
		return err
	}
	if resp.ApiAssociation == nil {
		return fmt.Errorf("AssociateApi: missing apiAssociation")
	}
	return nil
}

func (g *appsyncGroup) GetApiAssociation(ctx context.Context, t *harness.TestContext) error {
	dn := t.GetString("domain_name")
	resp, err := g.cl().GetApiAssociation(ctx, &appsync.GetApiAssociationInput{DomainName: aws.String(dn)})
	if err != nil {
		return err
	}
	if resp.ApiAssociation == nil {
		return fmt.Errorf("GetApiAssociation: missing apiAssociation")
	}
	return nil
}

func (g *appsyncGroup) DisassociateApi(ctx context.Context, t *harness.TestContext) error {
	dn := t.GetString("domain_name")
	_, err := g.cl().DisassociateApi(ctx, &appsync.DisassociateApiInput{DomainName: aws.String(dn)})
	return err
}

func (g *appsyncGroup) DeleteDomainName(ctx context.Context, t *harness.TestContext) error {
	dn := fmt.Sprintf("del-%s.example.com", t.RunID)
	_, err := g.cl().CreateDomainName(ctx, &appsync.CreateDomainNameInput{
		DomainName:     aws.String(dn),
		CertificateArn: aws.String("arn:aws:acm:us-east-1:000000000000:certificate/test"),
	})
	if err != nil {
		return err
	}
	_, err = g.cl().DeleteDomainName(ctx, &appsync.DeleteDomainNameInput{DomainName: aws.String(dn)})
	return err
}

// ── appsync-cache ────────────────────────────────────────────────────────────

func (g *appsyncGroup) teardownCache(ctx context.Context, t *harness.TestContext) error {
	if id := t.GetString("api_id"); id != "" {
		g.cl().DeleteApiCache(ctx, &appsync.DeleteApiCacheInput{ApiId: aws.String(id)})     //nolint:errcheck
		g.cl().DeleteGraphqlApi(ctx, &appsync.DeleteGraphqlApiInput{ApiId: aws.String(id)}) //nolint:errcheck
	}
	return nil
}

func (g *appsyncGroup) CreateApiCache(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	resp, err := g.cl().CreateApiCache(ctx, &appsync.CreateApiCacheInput{
		ApiId:              aws.String(apiID),
		Type:               types.ApiCacheTypeT2Small,
		ApiCachingBehavior: types.ApiCachingBehaviorFullRequestCaching,
		Ttl:                300,
	})
	if err != nil {
		return err
	}
	if resp.ApiCache == nil {
		return fmt.Errorf("CreateApiCache: missing apiCache")
	}
	return nil
}

func (g *appsyncGroup) GetApiCache(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().GetApiCache(ctx, &appsync.GetApiCacheInput{ApiId: aws.String(apiID)})
	return err
}

func (g *appsyncGroup) UpdateApiCache(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().UpdateApiCache(ctx, &appsync.UpdateApiCacheInput{
		ApiId:              aws.String(apiID),
		Type:               types.ApiCacheTypeT2Medium,
		ApiCachingBehavior: types.ApiCachingBehaviorFullRequestCaching,
		Ttl:                600,
	})
	return err
}

func (g *appsyncGroup) FlushApiCache(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().FlushApiCache(ctx, &appsync.FlushApiCacheInput{ApiId: aws.String(apiID)})
	return err
}

func (g *appsyncGroup) DeleteApiCache(ctx context.Context, t *harness.TestContext) error {
	apiID := t.GetString("api_id")
	_, err := g.cl().DeleteApiCache(ctx, &appsync.DeleteApiCacheInput{ApiId: aws.String(apiID)})
	return err
}
