package cloudformation_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const appsyncMinimalTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "GraphqlApi": {
      "Type": "AWS::AppSync::GraphQLApi",
      "Properties": {
        "Name": "cfn-appsync-api",
        "AuthenticationType": "API_KEY"
      }
    },
    "Schema": {
      "Type": "AWS::AppSync::GraphQLSchema",
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "Definition": "type Query { hello: String }"
      }
    },
    "NoneDataSource": {
      "Type": "AWS::AppSync::DataSource",
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "Name": "NoneDS",
        "Type": "NONE"
      }
    },
    "HelloResolver": {
      "Type": "AWS::AppSync::Resolver",
      "DependsOn": ["Schema", "NoneDataSource"],
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "TypeName": "Query",
        "FieldName": "hello",
        "DataSourceName": "NoneDS",
        "Kind": "UNIT",
        "RequestMappingTemplate": "{\"version\":\"2018-05-29\",\"payload\":\"world\"}",
        "ResponseMappingTemplate": "$util.toJson($context.result)"
      }
    },
    "ApiKey": {
      "Type": "AWS::AppSync::ApiKey",
      "DependsOn": "Schema",
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "Description": "cfn explicit key"
      }
    }
  },
  "Outputs": {
    "ApiId": {"Value": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]}},
    "ApiRef": {"Value": {"Ref": "GraphqlApi"}},
    "GraphQLUrl": {"Value": {"Fn::GetAtt": ["GraphqlApi", "GraphQLUrl"]}},
    "ApiArn": {"Value": {"Fn::GetAtt": ["GraphqlApi", "Arn"]}},
    "DataSourceRef": {"Value": {"Ref": "NoneDataSource"}},
    "DataSourceName": {"Value": {"Fn::GetAtt": ["NoneDataSource", "Name"]}},
    "DataSourceArn": {"Value": {"Fn::GetAtt": ["NoneDataSource", "DataSourceArn"]}},
    "ResolverArn": {"Value": {"Fn::GetAtt": ["HelloResolver", "ResolverArn"]}},
    "ApiKey": {"Value": {"Fn::GetAtt": ["ApiKey", "ApiKey"]}},
    "ApiKeyId": {"Value": {"Fn::GetAtt": ["ApiKey", "ApiKeyId"]}},
    "ApiKeyArn": {"Value": {"Ref": "ApiKey"}}
  }
}`

const appsyncPipelineTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "GraphqlApi": {
      "Type": "AWS::AppSync::GraphQLApi",
      "Properties": {
        "Name": "cfn-appsync-pipeline-api",
        "AuthenticationType": "API_KEY"
      }
    },
    "Schema": {
      "Type": "AWS::AppSync::GraphQLSchema",
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "Definition": "type Query { hello: String }"
      }
    },
    "NoneDataSource": {
      "Type": "AWS::AppSync::DataSource",
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "Name": "NoneDS",
        "Type": "NONE"
      }
    },
    "HelloFunction": {
      "Type": "AWS::AppSync::FunctionConfiguration",
      "DependsOn": ["Schema", "NoneDataSource"],
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "Name": "HelloFn",
        "DataSourceName": "NoneDS",
        "FunctionVersion": "2018-05-29",
        "RequestMappingTemplate": "{\"version\":\"2018-05-29\",\"payload\":\"pipeline-world\"}",
        "ResponseMappingTemplate": "$util.toJson($context.result)"
      }
    },
    "PipelineResolver": {
      "Type": "AWS::AppSync::Resolver",
      "DependsOn": ["Schema", "HelloFunction"],
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "TypeName": "Query",
        "FieldName": "hello",
        "Kind": "PIPELINE",
        "PipelineConfig": {
          "Functions": [{"Fn::GetAtt": ["HelloFunction", "FunctionId"]}]
        },
        "RequestMappingTemplate": "{}",
        "ResponseMappingTemplate": "$util.toJson($context.prev.result)"
      }
    },
    "ApiKey": {
      "Type": "AWS::AppSync::ApiKey",
      "DependsOn": "Schema",
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "Description": "cfn explicit key"
      }
    }
  },
  "Outputs": {
    "ApiId": {"Value": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]}},
    "ApiKey": {"Value": {"Fn::GetAtt": ["ApiKey", "ApiKey"]}},
    "FunctionId": {"Value": {"Fn::GetAtt": ["HelloFunction", "FunctionId"]}},
    "FunctionArn": {"Value": {"Fn::GetAtt": ["HelloFunction", "FunctionArn"]}}
  }
}`

func TestCreateStack_AppSyncUsableGraphQLApi(t *testing.T) {
	// Given: a CDK-like AppSync stack template
	srv := helpers.NewTestServer(t)
	stackName := "appsync-usable-api"

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{appsyncMinimalTemplate},
	})
	defer resp.Body.Close()

	// Then: the stack reaches CREATE_COMPLETE and exposes AWS-shaped outputs
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	outputs := describeStackOutputs(t, srv, stackName)
	for _, key := range []string{"ApiId", "ApiRef", "GraphQLUrl", "ApiArn", "ApiKey", "ApiKeyId", "ApiKeyArn"} {
		if outputs[key] == "" {
			t.Fatalf("expected output %s to be set; outputs=%#v", key, outputs)
		}
	}
	if outputs["ApiRef"] != outputs["ApiArn"] {
		t.Fatalf("expected GraphQLApi Ref to be its ARN, got ref=%q arn=%q", outputs["ApiRef"], outputs["ApiArn"])
	}
	if !strings.Contains(outputs["ApiArn"], "arn:aws:appsync:") {
		t.Fatalf("expected AppSync API ARN, got %q", outputs["ApiArn"])
	}
	if outputs["DataSourceRef"] != outputs["DataSourceArn"] {
		t.Fatalf("expected DataSource Ref to be its ARN, got ref=%q arn=%q", outputs["DataSourceRef"], outputs["DataSourceArn"])
	}
	if outputs["DataSourceName"] != "NoneDS" {
		t.Fatalf("expected data source name NoneDS, got %q", outputs["DataSourceName"])
	}

	// And: the CloudFormation resources created real AppSync state, not stubs
	appsyncAssertOK(t, appsyncGet(t, srv, "/v1/apis/"+outputs["ApiId"]))
	appsyncAssertOK(t, appsyncGet(t, srv, "/v1/apis/"+outputs["ApiId"]+"/schemacreation"))
	appsyncAssertOK(t, appsyncGet(t, srv, "/v1/apis/"+outputs["ApiId"]+"/datasources/NoneDS"))
	appsyncAssertOK(t, appsyncGet(t, srv, "/v1/apis/"+outputs["ApiId"]+"/types/Query/resolvers/hello"))
	assertAppSyncApiKeyListed(t, srv, outputs["ApiId"], outputs["ApiKeyId"])

	// And: the locally deployed GraphQL endpoint can execute using the stack API key output
	graphqlResp := appsyncGraphQL(t, srv, outputs["ApiId"], outputs["ApiKey"], `{ hello }`)
	defer graphqlResp.Body.Close()
	helpers.AssertStatus(t, graphqlResp, http.StatusOK)
	var result struct {
		Data struct {
			Hello string `json:"hello"`
		} `json:"data"`
	}
	helpers.DecodeJSON(t, graphqlResp, &result)
	if result.Data.Hello != "world" {
		t.Fatalf("expected GraphQL hello=world, got %q", result.Data.Hello)
	}
}

func TestCreateStack_AppSyncPipelineResolver(t *testing.T) {
	// Given: a CDK-like AppSync pipeline resolver stack template
	srv := helpers.NewTestServer(t)
	stackName := "appsync-pipeline-api"

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{appsyncPipelineTemplate},
	})
	defer resp.Body.Close()

	// Then: the function configuration is real AppSync state and execution uses it
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	outputs := describeStackOutputs(t, srv, stackName)
	for _, key := range []string{"ApiId", "ApiKey", "FunctionId", "FunctionArn"} {
		if outputs[key] == "" {
			t.Fatalf("expected output %s to be set; outputs=%#v", key, outputs)
		}
	}
	appsyncAssertOK(t, appsyncGet(t, srv, "/v1/apis/"+outputs["ApiId"]+"/functions/"+outputs["FunctionId"]))
	graphqlResp := appsyncGraphQL(t, srv, outputs["ApiId"], outputs["ApiKey"], `{ hello }`)
	defer graphqlResp.Body.Close()
	helpers.AssertStatus(t, graphqlResp, http.StatusOK)
	var result struct {
		Data struct {
			Hello string `json:"hello"`
		} `json:"data"`
	}
	helpers.DecodeJSON(t, graphqlResp, &result)
	if result.Data.Hello != "pipeline-world" {
		t.Fatalf("expected GraphQL hello=pipeline-world, got %q", result.Data.Hello)
	}
}

func TestDeleteStack_CleansUpAppSyncResources(t *testing.T) {
	// Given: an AppSync stack with executable API state
	srv := helpers.NewTestServer(t)
	stackName := "appsync-delete-api"
	createAppSyncStack(t, srv, stackName, appsyncMinimalTemplate)
	outputs := describeStackOutputs(t, srv, stackName)
	appsyncAssertOK(t, appsyncGet(t, srv, "/v1/apis/"+outputs["ApiId"]))

	// When: the stack is deleted
	resp := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "DELETE_COMPLETE")

	// Then: the parent API is deleted, cascading schema, keys, data sources, functions, and resolvers
	apiResp := appsyncGet(t, srv, "/v1/apis/"+outputs["ApiId"])
	defer apiResp.Body.Close()
	helpers.AssertStatus(t, apiResp, http.StatusNotFound)
}

func TestUpdateStack_AppSyncResolverTemplate(t *testing.T) {
	// Given: an AppSync stack whose resolver returns v1
	srv := helpers.NewTestServer(t)
	stackName := "appsync-update-api"
	createAppSyncStack(t, srv, stackName, strings.Replace(appsyncMinimalTemplate, `\"world\"`, `\"v1\"`, 1))
	outputs := describeStackOutputs(t, srv, stackName)
	assertAppSyncGraphQLHello(t, srv, outputs["ApiId"], outputs["ApiKey"], "v1")

	// When: UpdateStack changes only the resolver mapping template payload
	updatedTemplate := strings.Replace(appsyncMinimalTemplate, `\"world\"`, `\"v2\"`, 1)
	resp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{updatedTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")
	updatedOutputs := describeStackOutputs(t, srv, stackName)

	// Then: execution changes and API/key references remain stable
	if updatedOutputs["ApiId"] != outputs["ApiId"] || updatedOutputs["ApiKey"] != outputs["ApiKey"] {
		t.Fatalf("expected in-place AppSync update, before=%#v after=%#v", outputs, updatedOutputs)
	}
	assertAppSyncGraphQLHello(t, srv, updatedOutputs["ApiId"], updatedOutputs["ApiKey"], "v2")
}

func createAppSyncStack(t *testing.T, srv *helpers.TestServer, stackName, template string) {
	t.Helper()
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
}

func assertAppSyncGraphQLHello(t *testing.T, srv *helpers.TestServer, apiID, apiKey, want string) {
	t.Helper()
	graphqlResp := appsyncGraphQL(t, srv, apiID, apiKey, `{ hello }`)
	defer graphqlResp.Body.Close()
	helpers.AssertStatus(t, graphqlResp, http.StatusOK)
	var result struct {
		Data struct {
			Hello string `json:"hello"`
		} `json:"data"`
	}
	helpers.DecodeJSON(t, graphqlResp, &result)
	if result.Data.Hello != want {
		t.Fatalf("expected GraphQL hello=%s, got %q", want, result.Data.Hello)
	}
}

func describeStackOutputs(t *testing.T, srv *helpers.TestServer, stackName string) map[string]string {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": []string{stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	var result struct {
		Outputs []struct {
			Key   string `xml:"OutputKey"`
			Value string `xml:"OutputValue"`
		} `xml:"DescribeStacksResult>Stacks>member>Outputs>member"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeStacksResponse: %v\nbody: %s", err, body)
	}
	outputs := make(map[string]string, len(result.Outputs))
	for _, output := range result.Outputs {
		outputs[output.Key] = output.Value
	}
	return outputs
}

func appsyncGet(t *testing.T, srv *helpers.TestServer, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("appsync GET %s: %v", path, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("appsync GET %s: %v", path, err)
	}
	return resp
}

func appsyncGraphQL(t *testing.T, srv *helpers.TestServer, apiID, apiKey, query string) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		t.Fatalf("marshal GraphQL body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/_appsync/"+apiID+"/graphql", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("appsync GraphQL request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("appsync GraphQL: %v", err)
	}
	return resp
}

func appsyncAssertOK(t *testing.T, resp *http.Response) {
	t.Helper()
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func assertAppSyncApiKeyListed(t *testing.T, srv *helpers.TestServer, apiID, keyID string) {
	t.Helper()
	resp := appsyncGet(t, srv, "/v1/apis/"+apiID+"/apikeys")
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		APIKeys []struct {
			ID string `json:"id"`
		} `json:"apiKeys"`
	}
	helpers.DecodeJSON(t, resp, &result)
	for _, key := range result.APIKeys {
		if key.ID == keyID {
			return
		}
	}
	t.Fatalf("expected AppSync API key %q in list %#v", keyID, result.APIKeys)
}
