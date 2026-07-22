package cloudformation_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

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

func TestCreateStack_AppSyncGraphQLApiTags(t *testing.T) {
	// Given: a CDK-like AppSync stack template with CloudFormation tag entries
	srv := helpers.NewTestServer(t)
	stackName := "appsync-tagged-api"
	template := strings.Replace(appsyncMinimalTemplate,
		`"AuthenticationType": "API_KEY"`,
		`"AuthenticationType": "API_KEY", "Tags": [{"Key": "env", "Value": "test"}, {"Key": "team", "Value": "guides"}], "EnvironmentVariables": {"STAGE": "test", "GUIDE": "digital"}`,
		1,
	)
	template = strings.Replace(template,
		`"ApiArn": {"Value": {"Fn::GetAtt": ["GraphqlApi", "Arn"]}},`,
		`"ApiArn": {"Value": {"Fn::GetAtt": ["GraphqlApi", "Arn"]}}, "GraphQLEndpointArn": {"Value": {"Fn::GetAtt": ["GraphqlApi", "GraphQLEndpointArn"]}},`,
		1,
	)

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()

	// Then: the stack completes and the AppSync API has the expected tags
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	outputs := describeStackOutputs(t, srv, stackName)
	if outputs["GraphQLEndpointArn"] == "" {
		t.Fatalf("expected GraphQLEndpointArn output; outputs=%#v", outputs)
	}
	if outputs["GraphQLEndpointArn"] != outputs["ApiArn"] {
		t.Fatalf("expected GraphQLEndpointArn to match API ARN, got endpoint=%q arn=%q", outputs["GraphQLEndpointArn"], outputs["ApiArn"])
	}
	apiResp := appsyncGet(t, srv, "/v1/apis/"+outputs["ApiId"])
	defer apiResp.Body.Close()
	helpers.AssertStatus(t, apiResp, http.StatusOK)
	var apiResult struct {
		GraphqlAPI struct {
			Tags map[string]string `json:"tags"`
		} `json:"graphqlApi"`
	}
	helpers.DecodeJSON(t, apiResp, &apiResult)
	if apiResult.GraphqlAPI.Tags["env"] != "test" {
		t.Fatalf("expected API env tag test, got %#v", apiResult.GraphqlAPI.Tags)
	}
	if apiResult.GraphqlAPI.Tags["team"] != "guides" {
		t.Fatalf("expected API team tag guides, got %#v", apiResult.GraphqlAPI.Tags)
	}
	envResp := appsyncGet(t, srv, "/v1/apis/"+outputs["ApiId"]+"/environmentVariables")
	defer envResp.Body.Close()
	helpers.AssertStatus(t, envResp, http.StatusOK)
	var envResult struct {
		EnvironmentVariables map[string]string `json:"environmentVariables"`
	}
	helpers.DecodeJSON(t, envResp, &envResult)
	if envResult.EnvironmentVariables["STAGE"] != "test" {
		t.Fatalf("expected STAGE env var test, got %#v", envResult.EnvironmentVariables)
	}
	if envResult.EnvironmentVariables["GUIDE"] != "digital" {
		t.Fatalf("expected GUIDE env var digital, got %#v", envResult.EnvironmentVariables)
	}
}

func TestCreateStack_AppSyncApiKeyWithCdkStyleUpperBoundExpires(t *testing.T) {
	// Given: a CDK-like AppSync stack with an absolute API key expiry 365 days from synthesis
	srv := helpers.NewTestServer(t)
	expires := time.Now().UTC().Add(365 * 24 * time.Hour).Unix()
	stackName := "appsync-apikey-cdk-expires"
	template := strings.Replace(appsyncMinimalTemplate,
		`"Description": "cfn explicit key"`,
		fmt.Sprintf(`"Description": "cfn explicit key", "Expires": %d`, expires),
		1,
	)

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()

	// Then: the stack completes instead of rolling back during CreateApiKey
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
}

func TestCreateStack_AppSyncGraphQLSchemaWithAwsDirectives(t *testing.T) {
	// Given: a CDK-like AppSync stack template whose schema uses AppSync auth directives
	srv := helpers.NewTestServer(t)
	stackName := "appsync-schema-directives"
	template := strings.Replace(appsyncMinimalTemplate,
		`"Definition": "type Query { hello: String }"`,
		`"Definition": "type Query @aws_api_key { hello: String @aws_iam }"`,
		1,
	)

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()

	// Then: the stack completes instead of rolling back during StartSchemaCreation
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
}

func TestCreateStack_AppSyncGraphQLSchemaWithAwsScalars(t *testing.T) {
	// Given: a CDK-like AppSync stack template whose schema uses AppSync scalar types
	srv := helpers.NewTestServer(t)
	stackName := "appsync-schema-scalars"
	template := strings.Replace(appsyncMinimalTemplate,
		`"Definition": "type Query { hello: String }"`,
		`"Definition": "type Query { createdAt: AWSDateTime }"`,
		1,
	)

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()

	// Then: the stack completes instead of rolling back during StartSchemaCreation
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
}

func TestCreateStack_AppSyncS3BackedSchemaAndResolverTemplates(t *testing.T) {
	// Given: AppSync schema and resolver templates uploaded to local S3
	srv := helpers.NewTestServer(t)
	stackName := "appsync-s3-backed-api"
	s3PutObject(t, srv, "appsync-assets", "schema.graphql", "type Query { hello: String }")
	s3PutObject(t, srv, "appsync-assets", "request.vtl", `{"version":"2018-05-29","payload":"from-s3"}`)
	s3PutObject(t, srv, "appsync-assets", "response.vtl", `$util.toJson($context.result)`)
	template := strings.Replace(appsyncMinimalTemplate,
		`"Definition": "type Query { hello: String }"`,
		`"DefinitionS3Location": "`+srv.URL+`/appsync-assets/schema.graphql"`,
		1,
	)
	template = strings.Replace(template,
		`"RequestMappingTemplate": "{\"version\":\"2018-05-29\",\"payload\":\"world\"}"`,
		`"RequestMappingTemplateS3Location": "`+srv.URL+`/appsync-assets/request.vtl"`,
		1,
	)
	template = strings.Replace(template,
		`"ResponseMappingTemplate": "$util.toJson($context.result)"`,
		`"ResponseMappingTemplateS3Location": "`+srv.URL+`/appsync-assets/response.vtl"`,
		1,
	)

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()

	// Then: the S3-backed assets are fetched and the deployed API executes
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	outputs := describeStackOutputs(t, srv, stackName)
	assertAppSyncGraphQLHello(t, srv, outputs["ApiId"], outputs["ApiKey"], "from-s3")
}

func TestCreateStack_AppSyncDomainCacheAndSourceAssociation(t *testing.T) {
	// Given: a CloudFormation template using additional AppSync resource types
	srv := helpers.NewTestServer(t)
	stackName := "appsync-additional-resources"
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "SourceApi": {
      "Type": "AWS::AppSync::GraphQLApi",
      "Properties": {"Name": "source-api", "AuthenticationType": "API_KEY"}
    },
    "SourceSchema": {
      "Type": "AWS::AppSync::GraphQLSchema",
      "Properties": {"ApiId": {"Fn::GetAtt": ["SourceApi", "ApiId"]}, "Definition": "type Query { source: String }"}
    },
    "MergedApi": {
      "Type": "AWS::AppSync::GraphQLApi",
      "Properties": {"Name": "merged-api", "AuthenticationType": "API_KEY", "ApiType": "MERGED"}
    },
    "Domain": {
      "Type": "AWS::AppSync::DomainName",
      "Properties": {
        "DomainName": "api.example.com",
        "CertificateArn": "arn:aws:acm:us-east-1:000000000000:certificate/example",
        "Description": "test domain"
      }
    },
    "DomainAssoc": {
      "Type": "AWS::AppSync::DomainNameApiAssociation",
      "DependsOn": ["Domain", "SourceApi"],
      "Properties": {"DomainName": {"Ref": "Domain"}, "ApiId": {"Fn::GetAtt": ["SourceApi", "ApiId"]}}
    },
    "ApiCache": {
      "Type": "AWS::AppSync::ApiCache",
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["SourceApi", "ApiId"]},
        "Type": "SMALL",
        "ApiCachingBehavior": "FULL_REQUEST_CACHING",
        "Ttl": 1200,
        "TransitEncryptionEnabled": true,
        "AtRestEncryptionEnabled": true
      }
    },
    "SourceAssoc": {
      "Type": "AWS::AppSync::SourceApiAssociation",
      "DependsOn": ["SourceSchema", "MergedApi"],
      "Properties": {
        "MergedApiIdentifier": {"Fn::GetAtt": ["MergedApi", "ApiId"]},
        "SourceApiIdentifier": {"Fn::GetAtt": ["SourceApi", "ApiId"]},
        "SourceApiAssociationConfig": {"MergeType": "MANUAL_MERGE"}
      }
    }
  },
  "Outputs": {
    "SourceApiId": {"Value": {"Fn::GetAtt": ["SourceApi", "ApiId"]}},
    "MergedApiId": {"Value": {"Fn::GetAtt": ["MergedApi", "ApiId"]}},
    "DomainName": {"Value": {"Ref": "Domain"}},
    "DomainNameArn": {"Value": {"Fn::GetAtt": ["Domain", "DomainNameArn"]}},
    "AppSyncDomainName": {"Value": {"Fn::GetAtt": ["Domain", "AppSyncDomainName"]}},
    "HostedZoneId": {"Value": {"Fn::GetAtt": ["Domain", "HostedZoneId"]}},
    "DomainAssocRef": {"Value": {"Ref": "DomainAssoc"}},
    "ApiCacheRef": {"Value": {"Ref": "ApiCache"}},
    "SourceAssocRef": {"Value": {"Ref": "SourceAssoc"}},
    "SourceAssocStatus": {"Value": {"Fn::GetAtt": ["SourceAssoc", "SourceApiAssociationStatus"]}}
  }
}`

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()

	// Then: the additional AppSync resources are provisioned through real handlers
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	outputs := describeStackOutputs(t, srv, stackName)
	for _, key := range []string{"SourceApiId", "MergedApiId", "DomainName", "DomainNameArn", "AppSyncDomainName", "HostedZoneId", "DomainAssocRef", "ApiCacheRef", "SourceAssocRef", "SourceAssocStatus"} {
		if outputs[key] == "" {
			t.Fatalf("expected output %s to be set; outputs=%#v", key, outputs)
		}
	}
	if outputs["DomainName"] != "api.example.com" {
		t.Fatalf("expected domain ref to be domain name, got %q", outputs["DomainName"])
	}
	if outputs["SourceAssocStatus"] != "MERGE_SUCCESS" {
		t.Fatalf("expected source association MERGE_SUCCESS, got %q", outputs["SourceAssocStatus"])
	}
	appsyncAssertOK(t, appsyncGet(t, srv, "/v1/domainnames/api.example.com"))
	appsyncAssertOK(t, appsyncGet(t, srv, "/v1/domainnames/api.example.com/apiassociation"))
	appsyncAssertOK(t, appsyncGet(t, srv, "/v1/apis/"+outputs["SourceApiId"]+"/ApiCaches"))
}

func TestCreateStack_AppSyncEventsApiAndChannelNamespace(t *testing.T) {
	// Given: a CloudFormation template using AppSync Events API resources
	srv := helpers.NewTestServer(t)
	stackName := "appsync-events-api"
	s3PutObject(t, srv, "appsync-assets", "events-handler.js", "export function onPublish(ctx) { return ctx; }")
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "EventsApi": {
      "Type": "AWS::AppSync::Api",
      "Properties": {
        "Name": "cfn-events-api",
        "EventConfig": {
          "AuthProviders": [{"AuthType": "API_KEY"}],
          "ConnectionAuthModes": [{"AuthType": "API_KEY"}],
          "DefaultPublishAuthModes": [{"AuthType": "API_KEY"}],
          "DefaultSubscribeAuthModes": [{"AuthType": "API_KEY"}]
        },
        "Tags": [{"Key": "env", "Value": "test"}]
      }
    },
    "Namespace": {
      "Type": "AWS::AppSync::ChannelNamespace",
		"Properties": {
			"ApiId": {"Fn::GetAtt": ["EventsApi", "ApiId"]},
			"Name": "messages",
			"CodeS3Location": "` + srv.URL + `/appsync-assets/events-handler.js"
		}
	}
  },
  "Outputs": {
    "ApiId": {"Value": {"Fn::GetAtt": ["EventsApi", "ApiId"]}},
    "ApiArn": {"Value": {"Fn::GetAtt": ["EventsApi", "ApiArn"]}},
    "ApiRef": {"Value": {"Ref": "EventsApi"}},
    "HttpDns": {"Value": {"Fn::GetAtt": ["EventsApi", "Dns.Http"]}},
    "RealtimeDns": {"Value": {"Fn::GetAtt": ["EventsApi", "Dns.Realtime"]}},
    "NamespaceArn": {"Value": {"Fn::GetAtt": ["Namespace", "ChannelNamespaceArn"]}},
    "NamespaceRef": {"Value": {"Ref": "Namespace"}}
  }
}`

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()

	// Then: the Events resources are provisioned through real AppSync Events handlers
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	outputs := describeStackOutputs(t, srv, stackName)
	for _, key := range []string{"ApiId", "ApiArn", "ApiRef", "HttpDns", "RealtimeDns", "NamespaceArn", "NamespaceRef"} {
		if outputs[key] == "" {
			t.Fatalf("expected output %s to be set; outputs=%#v", key, outputs)
		}
	}
	if !strings.Contains(outputs["ApiArn"], "arn:aws:appsync:") {
		t.Fatalf("expected AppSync Events API ARN, got %q", outputs["ApiArn"])
	}
	if !strings.Contains(outputs["NamespaceArn"], "/channelNamespace/messages") {
		t.Fatalf("expected namespace ARN for messages, got %q", outputs["NamespaceArn"])
	}
	apiResp := appsyncEventsGet(t, srv, "/v2/apis/"+outputs["ApiId"])
	defer apiResp.Body.Close()
	helpers.AssertStatus(t, apiResp, http.StatusOK)
	var apiResult struct {
		API struct {
			EventConfig map[string]any `json:"eventConfig"`
		} `json:"api"`
	}
	helpers.DecodeJSON(t, apiResp, &apiResult)
	if _, ok := apiResult.API.EventConfig["authProviders"]; !ok {
		t.Fatalf("expected CFN EventConfig to be translated to lower-camel AppSync JSON, got %#v", apiResult.API.EventConfig)
	}
	nsResp := appsyncEventsGet(t, srv, "/v2/apis/"+outputs["ApiId"]+"/channelNamespaces/messages")
	defer nsResp.Body.Close()
	helpers.AssertStatus(t, nsResp, http.StatusOK)
	var nsResult struct {
		ChannelNamespace struct {
			CodeHandlers string `json:"codeHandlers"`
		} `json:"channelNamespace"`
	}
	helpers.DecodeJSON(t, nsResp, &nsResult)
	if nsResult.ChannelNamespace.CodeHandlers != "export function onPublish(ctx) { return ctx; }" {
		t.Fatalf("expected S3-backed channel code handlers, got %q", nsResult.ChannelNamespace.CodeHandlers)
	}

	// And: deleting the stack removes the Events API and cascades namespaces
	deleteResp := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{stackName}})
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "DELETE_COMPLETE")
	getResp := appsyncEventsGet(t, srv, "/v2/apis/"+outputs["ApiId"])
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
}

func TestCreateStack_AppSyncResolverWithOnlyResponseMappingTemplate(t *testing.T) {
	// Given: a CDK-like AppSync resolver that omits RequestMappingTemplate.
	srv := helpers.NewTestServer(t)
	stackName := "appsync-response-template-only"
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "GraphqlApi": {
      "Type": "AWS::AppSync::GraphQLApi",
      "Properties": {
        "Name": "cfn-appsync-response-only-api",
        "AuthenticationType": "API_KEY"
      }
    },
    "Schema": {
      "Type": "AWS::AppSync::GraphQLSchema",
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "Definition": "type Topic { id: ID! } type Mutation { createTopic: Topic } type Query { health: String }"
      }
    },
    "TopicDataSource": {
      "Type": "AWS::AppSync::DataSource",
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "Name": "appsync_datasource_l_ue1_digital_guides_topic",
        "Type": "NONE"
      }
    },
    "CreateTopicResolver": {
      "Type": "AWS::AppSync::Resolver",
      "DependsOn": ["Schema", "TopicDataSource"],
      "Properties": {
        "ApiId": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]},
        "TypeName": "Mutation",
        "FieldName": "createTopic",
        "DataSourceName": "appsync_datasource_l_ue1_digital_guides_topic",
        "Kind": "UNIT",
        "ResponseMappingTemplate": "#if (!$util.isNull($ctx.result.error))\n  $util.error($ctx.result.error.message, $ctx.result.error.type, $ctx.result.data, $ctx.result.errorInfo)\n#end\n\n$utils.toJson($ctx.result)"
      }
    }
  },
  "Outputs": {
    "ApiId": {"Value": {"Fn::GetAtt": ["GraphqlApi", "ApiId"]}},
    "ResolverArn": {"Value": {"Fn::GetAtt": ["CreateTopicResolver", "ResolverArn"]}}
  }
}`

	// When: CloudFormation creates the stack.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()

	// Then: the resolver is provisioned without an internal AppSync error.
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	outputs := describeStackOutputs(t, srv, stackName)
	if outputs["ResolverArn"] == "" {
		t.Fatalf("expected resolver ARN output; outputs=%#v", outputs)
	}
	appsyncAssertOK(t, appsyncGet(t, srv, "/v1/apis/"+outputs["ApiId"]+"/types/Mutation/resolvers/createTopic"))
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

func TestCreateStack_AppSyncPipelineFunctionS3BackedTemplates(t *testing.T) {
	// Given: a pipeline resolver stack whose function templates are S3-backed
	srv := helpers.NewTestServer(t)
	stackName := "appsync-pipeline-s3-function"
	s3PutObject(t, srv, "appsync-assets", "function-request.vtl", `{"version":"2018-05-29","payload":"function-from-s3"}`)
	s3PutObject(t, srv, "appsync-assets", "function-response.vtl", `$util.toJson($context.result)`)
	template := strings.Replace(appsyncPipelineTemplate,
		`"RequestMappingTemplate": "{\"version\":\"2018-05-29\",\"payload\":\"pipeline-world\"}"`,
		`"RequestMappingTemplateS3Location": "`+srv.URL+`/appsync-assets/function-request.vtl"`,
		1,
	)
	template = strings.Replace(template,
		`"ResponseMappingTemplate": "$util.toJson($context.result)"`,
		`"ResponseMappingTemplateS3Location": "`+srv.URL+`/appsync-assets/function-response.vtl"`,
		1,
	)

	// When: CloudFormation creates the stack
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()

	// Then: the S3-backed function templates are fetched and used by execution
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	outputs := describeStackOutputs(t, srv, stackName)
	assertAppSyncGraphQLHello(t, srv, outputs["ApiId"], outputs["ApiKey"], "function-from-s3")
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

func appsyncEventsGet(t *testing.T, srv *helpers.TestServer, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("appsync events GET %s: %v", path, err)
	}
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20250101/us-east-1/appsync/aws4_request, SignedHeaders=host, Signature=fake")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("appsync events GET %s: %v", path, err)
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

func s3PutObject(t *testing.T, srv *helpers.TestServer, bucket, key, body string) {
	t.Helper()
	bucketReq, err := http.NewRequest(http.MethodPut, srv.URL+"/"+bucket, nil)
	if err != nil {
		t.Fatalf("create bucket request: %v", err)
	}
	bucketResp, err := http.DefaultClient.Do(bucketReq)
	if err != nil {
		t.Fatalf("create bucket %s: %v", bucket, err)
	}
	bucketResp.Body.Close()
	if bucketResp.StatusCode != http.StatusOK && bucketResp.StatusCode != http.StatusConflict {
		t.Fatalf("create bucket %s: status %d", bucket, bucketResp.StatusCode)
	}

	objectReq, err := http.NewRequest(http.MethodPut, srv.URL+"/"+bucket+"/"+key, strings.NewReader(body))
	if err != nil {
		t.Fatalf("put object request: %v", err)
	}
	objectResp, err := http.DefaultClient.Do(objectReq)
	if err != nil {
		t.Fatalf("put object %s/%s: %v", bucket, key, err)
	}
	defer objectResp.Body.Close()
	helpers.AssertStatus(t, objectResp, http.StatusOK)
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
