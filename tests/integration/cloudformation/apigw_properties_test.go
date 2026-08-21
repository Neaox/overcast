package cloudformation_test

// Integration coverage for issue #528: CloudFormation parsed several API
// Gateway properties into nothing while the receiving apigateway service
// already implemented them — the same class of bug as #521's IAM properties
// (provisioner_iam_reconcile.go). Stage Variables/StageVariables is the
// headline case: handler_execution.go already reads stage.Variables into the
// request's stageVariables at execution time, and UpdateStage/UpdateV2Stage
// already accept the field, but CreateStage/CreateV2Stage never received it
// from CloudFormation. These tests provision through CloudFormation and then
// read the result back through the apigateway service's own REST endpoints
// (GetStage, GetMethod, ...) and, for the execution-time claim, by actually
// invoking the deployed stage — an HTTP_PROXY integration whose URI is
// "${stageVariables.backendUrl}" exercises exactly the same stageVars value
// a Lambda proxy integration's event.stageVariables would receive, without
// requiring Docker.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// apigwCall issues a direct JSON request against the apigateway service's own
// REST endpoints (not through CloudFormation), to read back what a
// provisioned resource actually holds.
func apigwCall(t *testing.T, srv *helpers.TestServer, method, path string, body string) *http.Response {
	t.Helper()
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	} else {
		req, err = http.NewRequest(method, srv.URL+path, nil)
	}
	if err != nil {
		t.Fatalf("apigwCall %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("apigwCall %s %s: %v", method, path, err)
	}
	return resp
}

func createApigwCFNStack(t *testing.T, srv *helpers.TestServer, stackName, template string) {
	t.Helper()
	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, stackName, "CREATE_COMPLETE", "ROLLBACK_COMPLETE", "ROLLBACK_FAILED"); status != "CREATE_COMPLETE" {
		events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
		defer events.Body.Close()
		t.Fatalf("create stack ended in %s: %s", status, readBody(t, events))
	}
}

func updateApigwCFNStack(t *testing.T, srv *helpers.TestServer, stackName, template string) {
	t.Helper()
	resp := cfnQuery(t, srv, "UpdateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, stackName, "UPDATE_COMPLETE", "UPDATE_ROLLBACK_COMPLETE", "UPDATE_FAILED", "UPDATE_ROLLBACK_FAILED"); status != "UPDATE_COMPLETE" {
		events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
		defer events.Body.Close()
		t.Fatalf("update stack ended in %s: %s", status, readBody(t, events))
	}
}

// ---- Stage v1: Variables round-trip + honoured at execution time ----------

func apigwV1StageTemplate(backendURL string) string {
	return fmt.Sprintf(`{
		"Resources": {
			"RestApi": {"Type": "AWS::ApiGateway::RestApi", "Properties": {"Name": "stagevar-api"}},
			"VarsResource": {
				"Type": "AWS::ApiGateway::Resource",
				"Properties": {
					"RestApiId": {"Ref": "RestApi"},
					"ParentId": {"Fn::GetAtt": ["RestApi", "RootResourceId"]},
					"PathPart": "vars"
				}
			},
			"GetMethod": {
				"Type": "AWS::ApiGateway::Method",
				"Properties": {
					"RestApiId": {"Ref": "RestApi"},
					"ResourceId": {"Ref": "VarsResource"},
					"HttpMethod": "GET",
					"AuthorizationType": "NONE",
					"Integration": {
						"Type": "HTTP_PROXY",
						"IntegrationHttpMethod": "GET",
						"Uri": "${stageVariables.backendUrl}"
					}
				}
			},
			"Deployment": {
				"Type": "AWS::ApiGateway::Deployment",
				"DependsOn": ["GetMethod"],
				"Properties": {"RestApiId": {"Ref": "RestApi"}}
			},
			"Stage": {
				"Type": "AWS::ApiGateway::Stage",
				"Properties": {
					"RestApiId": {"Ref": "RestApi"},
					"DeploymentId": {"Ref": "Deployment"},
					"StageName": "test",
					"Variables": {"backendUrl": %q},
					"Tags": [{"Key": "env", "Value": "test"}]
				}
			}
		},
		"Outputs": {"ApiId": {"Value": {"Ref": "RestApi"}}}
	}`, backendURL)
}

func TestCFN_ApiGatewayStage_variablesRoundTripAndExecute(t *testing.T) {
	upstream1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"resolved":"first"}`)
	}))
	defer upstream1.Close()
	upstream2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"resolved":"second"}`)
	}))
	defer upstream2.Close()

	srv := helpers.NewTestServer(t)
	stackName := "apigw-stage-vars"
	createApigwCFNStack(t, srv, stackName, apigwV1StageTemplate(upstream1.URL))

	apiID := stackOutput(t, srv, stackName, "ApiId")

	// GetStage: Variables reached CreateStage (was silently dropped before #528).
	stageResp := apigwCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/stages/test", "")
	helpers.AssertStatus(t, stageResp, http.StatusOK)
	stageBody := helpers.ReadBody(t, stageResp)
	if !strings.Contains(stageBody, `"backendUrl":"`+upstream1.URL+`"`) {
		t.Fatalf("GetStage: expected variables.backendUrl=%s, got %s", upstream1.URL, stageBody)
	}
	if !strings.Contains(stageBody, `"env":"test"`) {
		t.Fatalf("GetStage: expected tags.env=test, got %s", stageBody)
	}

	// Execute: the stage variable is honoured at request time (same stageVars
	// value a Lambda proxy integration's event.stageVariables would carry).
	execResp := apigwCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/test/_user_request_/vars", "")
	helpers.AssertStatus(t, execResp, http.StatusOK)
	execBody := helpers.ReadBody(t, execResp)
	if !strings.Contains(execBody, `"resolved":"first"`) {
		t.Fatalf("execute: expected to reach upstream1, got %s", execBody)
	}

	// UpdateStack with a changed Variables value exercises UpdateStage's new
	// per-key "/variables/{name}" patch path.
	updateApigwCFNStack(t, srv, stackName, apigwV1StageTemplate(upstream2.URL))

	stageResp2 := apigwCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/stages/test", "")
	helpers.AssertStatus(t, stageResp2, http.StatusOK)
	stageBody2 := helpers.ReadBody(t, stageResp2)
	if !strings.Contains(stageBody2, `"backendUrl":"`+upstream2.URL+`"`) {
		t.Fatalf("GetStage after update: expected variables.backendUrl=%s, got %s", upstream2.URL, stageBody2)
	}

	execResp2 := apigwCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/test/_user_request_/vars", "")
	helpers.AssertStatus(t, execResp2, http.StatusOK)
	execBody2 := helpers.ReadBody(t, execResp2)
	if !strings.Contains(execBody2, `"resolved":"second"`) {
		t.Fatalf("execute after update: expected to reach upstream2, got %s", execBody2)
	}
}

// ---- Stage v2: StageVariables round-trip + honoured at execution time -----

func apigwV2StageTemplate(backendURL string) string {
	return fmt.Sprintf(`{
		"Resources": {
			"Api": {"Type": "AWS::ApiGatewayV2::Api", "Properties": {"Name": "stagevar-v2-api", "ProtocolType": "HTTP"}},
			"Integration": {
				"Type": "AWS::ApiGatewayV2::Integration",
				"Properties": {
					"ApiId": {"Ref": "Api"},
					"IntegrationType": "HTTP_PROXY",
					"IntegrationMethod": "GET",
					"IntegrationUri": "${stageVariables.backendUrl}",
					"PayloadFormatVersion": "1.0"
				}
			},
			"Route": {
				"Type": "AWS::ApiGatewayV2::Route",
				"Properties": {
					"ApiId": {"Ref": "Api"},
					"RouteKey": "GET /vars",
					"Target": {"Fn::Join": ["/", ["integrations", {"Ref": "Integration"}]]}
				}
			},
			"Stage": {
				"Type": "AWS::ApiGatewayV2::Stage",
				"Properties": {
					"ApiId": {"Ref": "Api"},
					"StageName": "test",
					"AutoDeploy": true,
					"StageVariables": {"backendUrl": %q},
					"Tags": {"env": "test"}
				}
			}
		},
		"Outputs": {"ApiId": {"Value": {"Ref": "Api"}}}
	}`, backendURL)
}

func TestCFN_ApiGatewayV2Stage_stageVariablesRoundTripAndExecute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"resolved":"v2"}`)
	}))
	defer upstream.Close()

	srv := helpers.NewTestServer(t)
	stackName := "apigw-v2-stage-vars"
	createApigwCFNStack(t, srv, stackName, apigwV2StageTemplate(upstream.URL))

	apiID := stackOutput(t, srv, stackName, "ApiId")

	stageResp := apigwCall(t, srv, http.MethodGet, "/v2/apis/"+apiID+"/stages/test", "")
	helpers.AssertStatus(t, stageResp, http.StatusOK)
	stageBody := helpers.ReadBody(t, stageResp)
	if !strings.Contains(stageBody, `"backendUrl":"`+upstream.URL+`"`) {
		t.Fatalf("GetV2Stage: expected stageVariables.backendUrl=%s, got %s", upstream.URL, stageBody)
	}
	if !strings.Contains(stageBody, `"env":"test"`) {
		t.Fatalf("GetV2Stage: expected tags.env=test, got %s", stageBody)
	}

	execResp := apigwCall(t, srv, http.MethodGet, "/v2/apis/"+apiID+"/stages/test/vars", "")
	helpers.AssertStatus(t, execResp, http.StatusOK)
	execBody := helpers.ReadBody(t, execResp)
	if !strings.Contains(execBody, `"resolved":"v2"`) {
		t.Fatalf("execute: expected to reach upstream, got %s", execBody)
	}
}

// ---- Method: AuthorizerId, RequestParameters, MethodResponses -------------
// ---- and the new Authorizer/Model/RequestValidator resource types ---------

const apigwMethodPropertiesTemplate = `{
	"Resources": {
		"RestApi": {"Type": "AWS::ApiGateway::RestApi", "Properties": {"Name": "method-props-api"}},
		"Authorizer": {
			"Type": "AWS::ApiGateway::Authorizer",
			"Properties": {
				"RestApiId": {"Ref": "RestApi"},
				"Name": "my-authorizer",
				"Type": "REQUEST",
				"AuthorizerUri": "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:auth-fn/invocations",
				"IdentitySource": "method.request.header.Authorization",
				"AuthorizerResultTtlInSeconds": 120
			}
		},
		"Model": {
			"Type": "AWS::ApiGateway::Model",
			"Properties": {
				"RestApiId": {"Ref": "RestApi"},
				"Name": "WidgetModel",
				"ContentType": "application/json",
				"Schema": "{\"type\":\"object\"}"
			}
		},
		"Validator": {
			"Type": "AWS::ApiGateway::RequestValidator",
			"Properties": {
				"RestApiId": {"Ref": "RestApi"},
				"Name": "body-validator",
				"ValidateRequestBody": true,
				"ValidateRequestParameters": false
			}
		},
		"WidgetResource": {
			"Type": "AWS::ApiGateway::Resource",
			"Properties": {
				"RestApiId": {"Ref": "RestApi"},
				"ParentId": {"Fn::GetAtt": ["RestApi", "RootResourceId"]},
				"PathPart": "widgets"
			}
		},
		"PostMethod": {
			"Type": "AWS::ApiGateway::Method",
			"Properties": {
				"RestApiId": {"Ref": "RestApi"},
				"ResourceId": {"Ref": "WidgetResource"},
				"HttpMethod": "POST",
				"AuthorizationType": "CUSTOM",
				"AuthorizerId": {"Ref": "Authorizer"},
				"RequestParameters": {"method.request.querystring.id": true},
				"MethodResponses": [
					{"StatusCode": "200", "ResponseParameters": {"method.response.header.Location": true}}
				],
				"Integration": {"Type": "MOCK"}
			}
		}
	},
	"Outputs": {
		"ApiId": {"Value": {"Ref": "RestApi"}},
		"AuthorizerId": {"Value": {"Ref": "Authorizer"}},
		"ModelName": {"Value": {"Ref": "Model"}},
		"ValidatorId": {"Value": {"Ref": "Validator"}}
	}
}`

func TestCFN_ApiGatewayMethod_authorizerAndResponsesRoundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "apigw-method-props"
	createApigwCFNStack(t, srv, stackName, apigwMethodPropertiesTemplate)

	apiID := stackOutput(t, srv, stackName, "ApiId")
	authorizerID := stackOutput(t, srv, stackName, "AuthorizerId")
	modelName := stackOutput(t, srv, stackName, "ModelName")
	validatorID := stackOutput(t, srv, stackName, "ValidatorId")

	if modelName != "WidgetModel" {
		t.Fatalf("expected Ref(Model) to be the model name, got %q", modelName)
	}

	// The Authorizer/Model/RequestValidator resource types provisioned for
	// real (previously: no CFN handler at all for any of the three).
	authResp := apigwCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/authorizers/"+authorizerID, "")
	helpers.AssertStatus(t, authResp, http.StatusOK)
	authBody := helpers.ReadBody(t, authResp)
	if !strings.Contains(authBody, `"name":"my-authorizer"`) || !strings.Contains(authBody, `"type":"REQUEST"`) {
		t.Fatalf("GetAuthorizer: unexpected body %s", authBody)
	}

	modelResp := apigwCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/models/"+modelName, "")
	helpers.AssertStatus(t, modelResp, http.StatusOK)
	modelBody := helpers.ReadBody(t, modelResp)
	if !strings.Contains(modelBody, `"contentType":"application/json"`) {
		t.Fatalf("GetModel: unexpected body %s", modelBody)
	}

	validatorsResp := apigwCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/requestvalidators", "")
	helpers.AssertStatus(t, validatorsResp, http.StatusOK)
	validatorsBody := helpers.ReadBody(t, validatorsResp)
	if !strings.Contains(validatorsBody, validatorID) || !strings.Contains(validatorsBody, `"validateRequestBody":true`) {
		t.Fatalf("GetRequestValidators: unexpected body %s", validatorsBody)
	}

	// The method itself: AuthorizerId, RequestParameters, and MethodResponses
	// (via PutMethodResponse) all reached the service.
	methodResp := apigwCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/resources/"+resourceIDFromWidgetPath(t, srv, apiID)+"/methods/POST", "")
	helpers.AssertStatus(t, methodResp, http.StatusOK)
	methodBody := helpers.ReadBody(t, methodResp)
	if !strings.Contains(methodBody, `"authorizerId":"`+authorizerID+`"`) {
		t.Fatalf("GetMethod: expected authorizerId=%s, got %s", authorizerID, methodBody)
	}
	if !strings.Contains(methodBody, `"method.request.querystring.id":true`) {
		t.Fatalf("GetMethod: expected requestParameters round-tripped, got %s", methodBody)
	}
	if !strings.Contains(methodBody, `"method.response.header.Location":true`) {
		t.Fatalf("GetMethod: expected methodResponses round-tripped, got %s", methodBody)
	}
}

// resourceIDFromWidgetPath looks up the /widgets resource's ID via GetResources,
// since the CFN template's logical ID doesn't expose it as an Output cheaply.
func resourceIDFromWidgetPath(t *testing.T, srv *helpers.TestServer, apiID string) string {
	t.Helper()
	resp := apigwCall(t, srv, http.MethodGet, "/restapis/"+apiID+"/resources", "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Item []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"item"`
	}
	helpers.DecodeJSON(t, resp, &result)
	for _, r := range result.Item {
		if r.Path == "/widgets" {
			return r.ID
		}
	}
	t.Fatalf("no /widgets resource found")
	return ""
}

// ---- Route v2: AuthorizerId round-trip -------------------------------------

func TestCFN_ApiGatewayV2Route_authorizerIdRoundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Create a v2 authorizer directly (AWS::ApiGatewayV2::Authorizer has no
	// CFN handler; out of #528's scope), then reference its ID by value from
	// the template — proving CFN forwards whatever AuthorizerId it is given.
	authResp := apigwCall(t, srv, http.MethodPost, "/v2/apis", `{"name":"v2-authz-api","protocolType":"HTTP"}`)
	helpers.AssertStatus(t, authResp, http.StatusCreated)
	var apiResult struct {
		ApiID string `json:"apiId"`
	}
	helpers.DecodeJSON(t, authResp, &apiResult)

	createAuthResp := apigwCall(t, srv, http.MethodPost, "/v2/apis/"+apiResult.ApiID+"/authorizers",
		`{"name":"my-v2-authorizer","authorizerType":"REQUEST","identitySource":"$request.header.Authorization"}`)
	helpers.AssertStatus(t, createAuthResp, http.StatusCreated)
	var authResult struct {
		AuthorizerID string `json:"authorizerId"`
	}
	helpers.DecodeJSON(t, createAuthResp, &authResult)
	if authResult.AuthorizerID == "" {
		t.Fatalf("expected non-empty authorizerId from CreateV2Authorizer")
	}

	stackName := "apigw-v2-route-authorizer"
	template := fmt.Sprintf(`{
		"Resources": {
			"Route": {
				"Type": "AWS::ApiGatewayV2::Route",
				"Properties": {
					"ApiId": %q,
					"RouteKey": "GET /secure",
					"AuthorizationType": "CUSTOM",
					"AuthorizerId": %q
				}
			}
		},
		"Outputs": {"RouteId": {"Value": {"Ref": "Route"}}}
	}`, apiResult.ApiID, authResult.AuthorizerID)
	createApigwCFNStack(t, srv, stackName, template)

	routeID := stackOutput(t, srv, stackName, "RouteId")
	routeResp := apigwCall(t, srv, http.MethodGet, "/v2/apis/"+apiResult.ApiID+"/routes/"+routeID, "")
	helpers.AssertStatus(t, routeResp, http.StatusOK)
	routeBody := helpers.ReadBody(t, routeResp)
	if !strings.Contains(routeBody, `"authorizerId":"`+authResult.AuthorizerID+`"`) {
		t.Fatalf("GetV2Route: expected authorizerId=%s, got %s", authResult.AuthorizerID, routeBody)
	}
}

// ---- RestApi / V2 Api: Body import fails loudly instead of an empty API ---

func TestCFN_ApiGatewayRestApi_bodyImportFailsLoudly(t *testing.T) {
	srv := helpers.NewTestServer(t)
	template := `{
		"Resources": {
			"RestApi": {
				"Type": "AWS::ApiGateway::RestApi",
				"Properties": {
					"Name": "openapi-import-api",
					"Body": {"openapi": "3.0.1", "info": {"title": "x", "version": "1"}, "paths": {}}
				}
			}
		}
	}`
	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {"apigw-body-import"}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, "apigw-body-import", "CREATE_COMPLETE", "ROLLBACK_COMPLETE", "ROLLBACK_FAILED"); status == "CREATE_COMPLETE" {
		t.Fatalf("expected Body import to fail the stack rather than silently provision an empty API")
	}
}

func TestCFN_ApiGatewayV2Api_bodyImportFailsLoudly(t *testing.T) {
	srv := helpers.NewTestServer(t)
	template := `{
		"Resources": {
			"Api": {
				"Type": "AWS::ApiGatewayV2::Api",
				"Properties": {
					"Name": "openapi-import-v2-api",
					"ProtocolType": "HTTP",
					"Body": "openapi: 3.0.1"
				}
			}
		}
	}`
	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {"apigw-v2-body-import"}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, "apigw-v2-body-import", "CREATE_COMPLETE", "ROLLBACK_COMPLETE", "ROLLBACK_FAILED"); status == "CREATE_COMPLETE" {
		t.Fatalf("expected Body import to fail the stack rather than silently provision a routeless API")
	}
}

// ---- RestApi: Policy, Tags, BinaryMediaTypes, DisableExecuteApiEndpoint ---

func TestCFN_ApiGatewayRestApi_scalarPropertiesRoundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "apigw-restapi-props"
	template := `{
		"Resources": {
			"RestApi": {
				"Type": "AWS::ApiGateway::RestApi",
				"Properties": {
					"Name": "restapi-props-api",
					"Policy": "{\"Version\":\"2012-10-17\",\"Statement\":[]}",
					"BinaryMediaTypes": ["image/png"],
					"DisableExecuteApiEndpoint": true,
					"Tags": [{"Key": "team", "Value": "platform"}]
				}
			}
		},
		"Outputs": {"ApiId": {"Value": {"Ref": "RestApi"}}}
	}`
	createApigwCFNStack(t, srv, stackName, template)
	apiID := stackOutput(t, srv, stackName, "ApiId")

	resp := apigwCall(t, srv, http.MethodGet, "/restapis/"+apiID, "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, `"policy"`) {
		t.Fatalf("GetRestApi: expected policy to round-trip, got %s", body)
	}
	if !strings.Contains(body, `"image/png"`) {
		t.Fatalf("GetRestApi: expected binaryMediaTypes to round-trip, got %s", body)
	}
	if !strings.Contains(body, `"disableExecuteApiEndpoint":true`) {
		t.Fatalf("GetRestApi: expected disableExecuteApiEndpoint=true, got %s", body)
	}
	if !strings.Contains(body, `"team":"platform"`) {
		t.Fatalf("GetRestApi: expected tags.team=platform, got %s", body)
	}

	// Update: Policy and DisableExecuteApiEndpoint patch in place.
	updateTemplate := `{
		"Resources": {
			"RestApi": {
				"Type": "AWS::ApiGateway::RestApi",
				"Properties": {
					"Name": "restapi-props-api",
					"Policy": "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Deny\"}]}",
					"DisableExecuteApiEndpoint": false
				}
			}
		},
		"Outputs": {"ApiId": {"Value": {"Ref": "RestApi"}}}
	}`
	updateApigwCFNStack(t, srv, stackName, updateTemplate)
	resp2 := apigwCall(t, srv, http.MethodGet, "/restapis/"+apiID, "")
	helpers.AssertStatus(t, resp2, http.StatusOK)
	body2 := helpers.ReadBody(t, resp2)
	if !strings.Contains(body2, `Deny`) {
		t.Fatalf("GetRestApi after update: expected updated policy, got %s", body2)
	}
	// disableExecuteApiEndpoint is `omitempty` and false is the zero value, so
	// a successful reset back to false drops the field entirely rather than
	// serializing it explicitly — absence here is the update having applied.
	if strings.Contains(body2, `"disableExecuteApiEndpoint":true`) {
		t.Fatalf("GetRestApi after update: expected disableExecuteApiEndpoint to reset to false, got %s", body2)
	}
}

// ---- shared helper ----------------------------------------------------------

// stackOutput reads one Outputs value from DescribeStacks.
func stackOutput(t *testing.T, srv *helpers.TestServer, stackName, outputKey string) string {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()
	body := string(readBody(t, resp))
	marker := "<OutputKey>" + outputKey + "</OutputKey><OutputValue>"
	idx := strings.Index(body, marker)
	if idx == -1 {
		t.Fatalf("output %q not found in DescribeStacks response: %s", outputKey, body)
	}
	rest := body[idx+len(marker):]
	end := strings.Index(rest, "</OutputValue>")
	if end == -1 {
		t.Fatalf("malformed OutputValue for %q: %s", outputKey, body)
	}
	return rest[:end]
}
