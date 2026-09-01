package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/overcast-sh/overcast/internal/config"
)

// ── ApiGateway PATCH operation builder ──────────────────────────────────────

// buildPatchOps diffs old properties vs new and returns JSON patch operations
// in the format ApiGateway expects: {"patchOperations": [{"op": "replace|add|remove", "path": "/key", "value": val}]}.
// Only emits ops for fields that differ between oldProps and newProps.
// fieldMap maps CFN property names to ApiGateway JSON path keys.
func buildPatchOps(oldProps, newProps map[string]any, fieldMap map[string]string) []map[string]any {
	var ops []map[string]any
	for propName, jsonPath := range fieldMap {
		oldVal, oldExists := oldProps[propName]
		newVal, newExists := newProps[propName]
		if !oldExists && !newExists {
			continue
		}
		if !oldExists && newExists {
			ops = append(ops, map[string]any{"op": "add", "path": jsonPath, "value": patchOpValue(newVal)})
		} else if oldExists && !newExists {
			ops = append(ops, map[string]any{"op": "remove", "path": jsonPath})
		} else if !reflect.DeepEqual(oldVal, newVal) {
			ops = append(ops, map[string]any{"op": "replace", "path": jsonPath, "value": patchOpValue(newVal)})
		}
	}
	return ops
}

// patchOpValue renders a property value the way AWS's own PatchOperation
// wire format expects: "value" is always a string, even for a boolean or
// numeric field (e.g. real UpdateMethod's apiKeyRequired patch carries
// "value": "true", not a JSON boolean). The service's patchOperation.Value
// field is typed string for the same reason — passing a raw JSON bool/number
// through fails to decode and was silently falling back to full resource
// replacement instead of applying the patch (caught by
// TestCFN_ApiGatewayRestApi_scalarPropertiesRoundTrip's
// DisableExecuteApiEndpoint update).
func patchOpValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case bool:
		return strconv.FormatBool(val)
	default:
		return fmt.Sprint(val)
	}
}

// mergeMapResourceTags is mergeResourceTags for the ApiGatewayV2 family,
// whose Tags property is a plain JSON object (`{"key": "value"}`) rather than
// the List<Tag> `[{"Key":..,"Value":..}]` shape most other resource types
// use (AWS::ApiGatewayV2::Api/Stage/DomainName/VpcLink all declare
// `Tags: Map` — see the CloudFormation resource spec, unlike
// AWS::ApiGateway::RestApi/Stage's `Tags: List of Tag`).
func mergeMapResourceTags(stackTags []Tag, rawResourceTags any) map[string]string {
	tags, ok := rawResourceTags.(map[string]any)
	if !ok {
		return mergeStackTags(stackTags, nil)
	}
	resourceTags := make(map[string]string, len(tags))
	for k, v := range tags {
		if s, ok := v.(string); ok {
			resourceTags[k] = s
		}
	}
	return mergeStackTags(stackTags, resourceTags)
}

// buildMapPatchOps diffs old vs new string-valued maps (e.g. stage Variables)
// and returns per-key JSON patch operations under pathPrefix+key — AWS's
// wire shape for map-valued stage properties (see UpdateStage/UpdateV2Stage
// docs), as opposed to buildPatchOps' single whole-value replace per field.
func buildMapPatchOps(oldMap, newMap map[string]any, pathPrefix string) []map[string]any {
	var ops []map[string]any
	for k, v := range newMap {
		if oldVal, ok := oldMap[k]; !ok || oldVal != v {
			ops = append(ops, map[string]any{"op": "replace", "path": pathPrefix + k, "value": v})
		}
	}
	for k := range oldMap {
		if _, ok := newMap[k]; !ok {
			ops = append(ops, map[string]any{"op": "remove", "path": pathPrefix + k})
		}
	}
	return ops
}

// ── AWS::ApiGateway::RestApi ───────────────────────────────────────────────

type apigwRestApiHandler struct{}

func (h *apigwRestApiHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Body/BodyS3Location define the API from an OpenAPI document; importing
	// one is not implemented. Provisioning would otherwise "succeed" with a
	// RestApi that has no resources or methods at all — a silent drop worse
	// than failing the stack, so fail loudly instead (same call as #521's
	// AWS::IAM::User.LoginProfile: dispatch to the gap rather than hide it).
	if _, ok := props["Body"]; ok {
		return "", nil, fmt.Errorf("AWS::ApiGateway::RestApi: Body (OpenAPI import) is not implemented; define the API with AWS::ApiGateway::Resource/Method instead")
	}
	if _, ok := props["BodyS3Location"]; ok {
		return "", nil, fmt.Errorf("AWS::ApiGateway::RestApi: BodyS3Location (OpenAPI import) is not implemented; define the API with AWS::ApiGateway::Resource/Method instead")
	}

	body := map[string]any{}
	// Name is optional on AWS::ApiGateway::RestApi; CreateRestApi requires it.
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	} else {
		body["name"] = rCtx.generatedName()
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	if ec, ok := props["EndpointConfiguration"].(map[string]any); ok {
		body["endpointConfiguration"] = ec
	}
	if v, _ := props["Policy"].(string); v != "" {
		body["policy"] = v
	}
	if tags := mergeResourceTags(rCtx.StackTags, props["Tags"]); len(tags) > 0 {
		body["tags"] = tags
	}
	if v, ok := props["BinaryMediaTypes"].([]any); ok {
		types := make([]string, 0, len(v))
		for _, t := range v {
			if s, _ := t.(string); s != "" {
				types = append(types, s)
			}
		}
		if len(types) > 0 {
			body["binaryMediaTypes"] = types
		}
	}
	if v, ok := props["DisableExecuteApiEndpoint"]; ok {
		body["disableExecuteApiEndpoint"] = v
	}
	// MinimumCompressionSize and ApiKeySourceType are accepted by CDK/CFN but
	// not implemented by the service (no request-body compression or API key
	// source distinction is emulated); intentionally not forwarded.

	data, _ := json.Marshal(body)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/restapis", "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateRestApi: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateRestApi: parse response: %w", err)
	}

	apiID, _ := resp["id"].(string)
	rootResourceID, _ := resp["rootResourceId"].(string)

	attrs := map[string]string{
		"RestApiId":      apiID,
		"RootResourceId": rootResourceID,
	}
	return apiID, attrs, nil
}

func (h *apigwRestApiHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/restapis/"+physicalID, "", nil)
	return teardownError("DeleteRestApi", rec, err)
}

func (h *apigwRestApiHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID := physicalID
	ops := buildPatchOps(oldProps, props, map[string]string{
		"Name":                      "/name",
		"Description":               "/description",
		"Policy":                    "/policy",
		"DisableExecuteApiEndpoint": "/disableExecuteApiEndpoint",
	})
	if len(ops) == 0 {
		return physicalID, nil, nil
	}
	body := map[string]any{"patchOperations": ops}
	data, _ := json.Marshal(body)
	path := "/restapis/" + apiID
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPatch, path, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("UpdateRestApi: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::ApiGateway::Resource ──────────────────────────────────────────────

type apigwResourceHandler struct{}

func (h *apigwResourceHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	restApiID, _ := props["RestApiId"].(string)
	parentID, _ := props["ParentId"].(string)
	pathPart, _ := props["PathPart"].(string)

	body := map[string]any{"pathPart": pathPart}
	data, _ := json.Marshal(body)

	path := fmt.Sprintf("/restapis/%s/resources/%s", restApiID, parentID)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateResource: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateResource: parse response: %w", err)
	}

	resourceID, _ := resp["id"].(string)

	// Physical ID encodes both IDs so Delete can reconstruct the path.
	// The "Ref" attribute override makes Ref return just the resource ID,
	// matching real AWS behaviour (ParentId on child resources uses Ref).
	physicalID := restApiID + "/" + resourceID
	attrs := map[string]string{
		"ResourceId": resourceID,
		"Ref":        resourceID,
	}
	return physicalID, attrs, nil
}

func (h *apigwResourceHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	path := fmt.Sprintf("/restapis/%s/resources/%s", parts[0], parts[1])
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return teardownError("DeleteResource", rec, err)
}

// ── AWS::ApiGateway::Method ────────────────────────────────────────────────

type apigwMethodHandler struct{}

func (h *apigwMethodHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	restApiID, _ := props["RestApiId"].(string)
	resourceID, _ := props["ResourceId"].(string)
	httpMethod, _ := props["HttpMethod"].(string)

	body := map[string]any{
		"authorizationType": "NONE",
	}
	if v, _ := props["AuthorizationType"].(string); v != "" {
		body["authorizationType"] = v
	}
	if v, ok := props["ApiKeyRequired"]; ok {
		body["apiKeyRequired"] = v
	}
	// AuthorizerId: without this a method wired to a CDK-created authorizer
	// (TokenAuthorizer/RequestAuthorizer/CfnAuthorizer) loses the association
	// entirely and every request to it evaluates as unauthenticated.
	if v, _ := props["AuthorizerId"].(string); v != "" {
		body["authorizerId"] = v
	}
	// RequestParameters is already the {"method.request.querystring.foo":
	// true} shape the service's PutMethod expects — CFN and the wire format
	// agree here, so it passes straight through.
	if v, ok := props["RequestParameters"].(map[string]any); ok && len(v) > 0 {
		body["requestParameters"] = v
	}
	// RequestModels, RequestValidatorId, OperationName and AuthorizationScopes
	// are accepted by CDK/CFN but the service has no field for any of them
	// (the first three are tracked by the P3 note on apigateway.Method) —
	// intentionally not forwarded rather than silently claimed as honoured.

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/restapis/%s/resources/%s/methods/%s", restApiID, resourceID, httpMethod)
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, path, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("PutMethod: %w", err)
	}

	// If Integration is specified, create it too.
	if integration, ok := props["Integration"].(map[string]any); ok {
		intBody := map[string]any{}
		if v, _ := integration["Type"].(string); v != "" {
			intBody["type"] = v
		}
		if v, _ := integration["IntegrationHttpMethod"].(string); v != "" {
			intBody["integrationHttpMethod"] = v
		}
		if v, _ := integration["Uri"].(string); v != "" {
			intBody["uri"] = v
		}
		intData, _ := json.Marshal(intBody)
		intPath := fmt.Sprintf("/restapis/%s/resources/%s/methods/%s/integration", restApiID, resourceID, httpMethod)
		if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, intPath, "application/json", intData); err != nil {
			return "", nil, fmt.Errorf("PutIntegration: %w", err)
		}
	}

	// MethodResponses is a list of {StatusCode, ResponseParameters,
	// ResponseModels}; PutMethodResponse takes StatusCode + ResponseParameters
	// (already the right shape). ResponseModels has no service-side field
	// (see apigateway.MethodResponse's TODO) and is not forwarded.
	if responses, ok := props["MethodResponses"].([]any); ok {
		for _, r := range responses {
			rm, ok := r.(map[string]any)
			if !ok {
				continue
			}
			statusCode, _ := rm["StatusCode"].(string)
			if statusCode == "" {
				continue
			}
			respBody := map[string]any{"statusCode": statusCode}
			if rp, ok := rm["ResponseParameters"].(map[string]any); ok && len(rp) > 0 {
				respBody["responseParameters"] = rp
			}
			respData, _ := json.Marshal(respBody)
			respPath := fmt.Sprintf("/restapis/%s/resources/%s/methods/%s/responses/%s", restApiID, resourceID, httpMethod, statusCode)
			if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, respPath, "application/json", respData); err != nil {
				return "", nil, fmt.Errorf("PutMethodResponse: %w", err)
			}
		}
	}

	physicalID := restApiID + "/" + resourceID + "/" + httpMethod
	attrs := map[string]string{
		"RestApiId":  restApiID,
		"ResourceId": resourceID,
		"HttpMethod": httpMethod,
	}
	return physicalID, attrs, nil
}

func (h *apigwMethodHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 3)
	if len(parts) != 3 {
		return nil
	}
	path := fmt.Sprintf("/restapis/%s/resources/%s/methods/%s", parts[0], parts[1], parts[2])
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return teardownError("DeleteMethod", rec, err)
}

func (h *apigwMethodHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	parts := strings.SplitN(physicalID, "/", 3)
	if len(parts) != 3 {
		return "", nil, errReplacementRequired
	}
	restApiID, resourceID, httpMethod := parts[0], parts[1], parts[2]

	if newRestApiID, _ := props["RestApiId"].(string); newRestApiID != "" && newRestApiID != restApiID {
		return "", nil, errReplacementRequired
	}
	if newResourceID, _ := props["ResourceId"].(string); newResourceID != "" && newResourceID != resourceID {
		return "", nil, errReplacementRequired
	}
	if newHttpMethod, _ := props["HttpMethod"].(string); newHttpMethod != "" && newHttpMethod != httpMethod {
		return "", nil, errReplacementRequired
	}

	ops := buildPatchOps(oldProps, props, map[string]string{
		"AuthorizationType": "/authorizationType",
		"ApiKeyRequired":    "/apiKeyRequired",
		"AuthorizerId":      "/authorizerId",
	})
	if len(ops) == 0 {
		return physicalID, nil, nil
	}
	body := map[string]any{"patchOperations": ops}
	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/restapis/%s/resources/%s/methods/%s", restApiID, resourceID, httpMethod)
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPatch, path, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("UpdateMethod: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::ApiGateway::Deployment ────────────────────────────────────────────

type apigwDeploymentHandler struct{}

func (h *apigwDeploymentHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	restApiID, _ := props["RestApiId"].(string)

	body := map[string]any{}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	if v, _ := props["StageName"].(string); v != "" {
		body["stageName"] = v
	}

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/restapis/%s/deployments", restApiID)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateDeployment: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateDeployment: parse response: %w", err)
	}

	deploymentID, _ := resp["id"].(string)
	physicalID := restApiID + "/" + deploymentID
	attrs := map[string]string{
		"DeploymentId": deploymentID,
		"Ref":          deploymentID,
	}
	return physicalID, attrs, nil
}

func (h *apigwDeploymentHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	// Deployments are cleaned up when the API is deleted; best-effort delete.
	return nil
}

func (h *apigwDeploymentHandler) Update(_ context.Context, _ http.Handler, _ *config.Config, _ string, _ map[string]any, _ map[string]any, _ *resolveContext) (string, map[string]string, error) {
	// Deployments are immutable in AWS (changes handled via logical-ID change).
	return "", nil, errReplacementRequired
}

// ── AWS::ApiGateway::Stage ─────────────────────────────────────────────────

type apigwStageHandler struct{}

func (h *apigwStageHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	restApiID, _ := props["RestApiId"].(string)
	stageName, _ := props["StageName"].(string)
	deploymentID, _ := props["DeploymentId"].(string)

	body := map[string]any{
		"stageName":    stageName,
		"deploymentId": deploymentID,
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	// Variables: live at execution time (handler_execution.go reads
	// stage.Variables into the request's StageVariables), so dropping this is
	// not cosmetic — every Lambda proxy integration behind the stage sees an
	// empty stageVariables map instead of what the template declared.
	if v, ok := props["Variables"].(map[string]any); ok && len(v) > 0 {
		body["variables"] = v
	}
	if tags := mergeResourceTags(rCtx.StackTags, props["Tags"]); len(tags) > 0 {
		body["tags"] = tags
	}
	// MethodSettings, TracingEnabled, AccessLogSetting, CacheClusterEnabled,
	// CacheClusterSize, ClientCertificateId, DocumentationVersion have no
	// service-side field (see apigateway.Stage's TODO) and are not forwarded.

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/restapis/%s/stages", restApiID)
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("CreateStage: %w", err)
	}

	physicalID := restApiID + "/" + stageName
	attrs := map[string]string{
		"StageName": stageName,
		"Ref":       stageName,
	}
	return physicalID, attrs, nil
}

func (h *apigwStageHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	path := fmt.Sprintf("/restapis/%s/stages/%s", parts[0], parts[1])
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return teardownError("DeleteStage", rec, err)
}

func (h *apigwStageHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return "", nil, errReplacementRequired
	}
	restApiID, stageName := parts[0], parts[1]

	if newRestApiID, _ := props["RestApiId"].(string); newRestApiID != "" && newRestApiID != restApiID {
		return "", nil, errReplacementRequired
	}
	if newStageName, _ := props["StageName"].(string); newStageName != "" && newStageName != stageName {
		return "", nil, errReplacementRequired
	}

	ops := buildPatchOps(oldProps, props, map[string]string{
		"Description":  "/description",
		"DeploymentId": "/deploymentId",
	})
	oldVars, _ := oldProps["Variables"].(map[string]any)
	newVars, _ := props["Variables"].(map[string]any)
	ops = append(ops, buildMapPatchOps(oldVars, newVars, "/variables/")...)
	if len(ops) == 0 {
		return physicalID, nil, nil
	}
	body := map[string]any{"patchOperations": ops}
	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/restapis/%s/stages/%s", restApiID, stageName)
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPatch, path, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("UpdateStage: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::ApiGatewayV2::Api ─────────────────────────────────────────────────

type apigwV2ApiHandler struct{}

func (h *apigwV2ApiHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Body defines the HTTP API from an OpenAPI document; importing one is
	// not implemented. Same call as the v1 RestApi Body/BodyS3Location case
	// above: fail the stack rather than silently provision a routeless API.
	if _, ok := props["Body"]; ok {
		return "", nil, fmt.Errorf("AWS::ApiGatewayV2::Api: Body (OpenAPI import) is not implemented; define the API with AWS::ApiGatewayV2::Route/Integration instead")
	}

	body := map[string]any{}
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	}
	if v, _ := props["ProtocolType"].(string); v != "" {
		body["protocolType"] = v
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	if v, _ := props["RouteSelectionExpression"].(string); v != "" {
		body["routeSelectionExpression"] = v
	}
	if v, ok := props["CorsConfiguration"].(map[string]any); ok {
		body["corsConfiguration"] = v
	}
	if tags := mergeMapResourceTags(rCtx.StackTags, props["Tags"]); len(tags) > 0 {
		body["tags"] = tags
	}
	if v, ok := props["DisableExecuteApiEndpoint"]; ok {
		body["disableExecuteApiEndpoint"] = v
	}
	// Target (quick-create default route+integration) and
	// ApiKeySelectionExpression have no service-side field and are not
	// forwarded; define routes/integrations as separate resources instead.

	data, _ := json.Marshal(body)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/v2/apis", "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateV2Api: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateV2Api: parse response: %w", err)
	}

	apiID, _ := resp["apiId"].(string)
	apiEndpoint, _ := resp["apiEndpoint"].(string)

	attrs := map[string]string{
		"ApiId":       apiID,
		"ApiEndpoint": apiEndpoint,
	}
	return apiID, attrs, nil
}

func (h *apigwV2ApiHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/v2/apis/"+physicalID, "", nil)
	return teardownError("DeleteApi", rec, err)
}

// ── AWS::ApiGatewayV2::Stage ───────────────────────────────────────────────

type apigwV2StageHandler struct{}

func (h *apigwV2StageHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)
	stageName, _ := props["StageName"].(string)

	body := map[string]any{
		"stageName": stageName,
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	if v, ok := props["AutoDeploy"]; ok {
		body["autoDeploy"] = v
	}
	// StageVariables: the same "supported but never passed" bug as v1's
	// Variables — UpdateV2Stage already accepts stageVariables (see
	// handler_http.go), CreateV2Stage's request struct already has the
	// field; only the CFN wiring was missing.
	if v, ok := props["StageVariables"].(map[string]any); ok && len(v) > 0 {
		body["stageVariables"] = v
	}
	if tags := mergeMapResourceTags(rCtx.StackTags, props["Tags"]); len(tags) > 0 {
		body["tags"] = tags
	}
	// DefaultRouteSettings, RouteSettings and AccessLogSettings have no
	// service-side field (see apigateway.StageV2's TODO) and are not
	// forwarded.

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/v2/apis/%s/stages", apiID)
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("CreateV2Stage: %w", err)
	}

	physicalID := apiID + "/" + stageName
	attrs := map[string]string{
		"StageName": stageName,
		"Ref":       stageName,
	}
	return physicalID, attrs, nil
}

func (h *apigwV2StageHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	path := fmt.Sprintf("/v2/apis/%s/stages/%s", parts[0], parts[1])
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return teardownError("DeleteStage", rec, err)
}

// ── AWS::ApiGatewayV2::Integration ─────────────────────────────────────────

type apigwV2IntegrationHandler struct{}

func (h *apigwV2IntegrationHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)

	body := map[string]any{}
	if v, _ := props["IntegrationType"].(string); v != "" {
		body["integrationType"] = v
	}
	if v, _ := props["IntegrationUri"].(string); v != "" {
		body["integrationUri"] = v
	}
	if v, _ := props["IntegrationMethod"].(string); v != "" {
		body["integrationMethod"] = v
	}
	if v, _ := props["PayloadFormatVersion"].(string); v != "" {
		body["payloadFormatVersion"] = v
	}
	if v, _ := props["ConnectionType"].(string); v != "" {
		body["connectionType"] = v
	}
	if v, ok := props["TimeoutInMillis"]; ok {
		body["timeoutInMillis"] = v
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	// RequestParameters, ResponseParameters, IntegrationSubtype, ConnectionId
	// (VPC Link), CredentialsArn, PassthroughBehavior and
	// TemplateSelectionExpression have no service-side field (see
	// apigateway.IntegrationV2's TODO) and are not forwarded.

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/v2/apis/%s/integrations", apiID)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateV2Integration: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateV2Integration: parse response: %w", err)
	}

	integrationID, _ := resp["integrationId"].(string)
	physicalID := apiID + "/" + integrationID
	attrs := map[string]string{
		"IntegrationId": integrationID,
		"Ref":           integrationID,
	}
	return physicalID, attrs, nil
}

func (h *apigwV2IntegrationHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	path := fmt.Sprintf("/v2/apis/%s/integrations/%s", parts[0], parts[1])
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return teardownError("DeleteIntegration", rec, err)
}

// ── AWS::ApiGatewayV2::Route ───────────────────────────────────────────────

type apigwV2RouteHandler struct{}

func (h *apigwV2RouteHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)

	body := map[string]any{}
	if v, _ := props["RouteKey"].(string); v != "" {
		body["routeKey"] = v
	}
	if v, _ := props["Target"].(string); v != "" {
		body["target"] = v
	}
	if v, _ := props["AuthorizationType"].(string); v != "" {
		body["authorizationType"] = v
	}
	// AuthorizerId: same drop as v1's Method — a route wired to a
	// CfnAuthorizer loses the association and evaluates as unauthenticated.
	if v, _ := props["AuthorizerId"].(string); v != "" {
		body["authorizerId"] = v
	}
	// AuthorizationScopes, RequestParameterConstraints and OperationName have
	// no service-side field (see apigateway.RouteV2's TODO) and are not
	// forwarded.

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/v2/apis/%s/routes", apiID)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateV2Route: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateV2Route: parse response: %w", err)
	}

	routeID, _ := resp["routeId"].(string)
	physicalID := apiID + "/" + routeID
	attrs := map[string]string{
		"RouteId": routeID,
		"Ref":     routeID,
	}
	return physicalID, attrs, nil
}

func (h *apigwV2RouteHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	path := fmt.Sprintf("/v2/apis/%s/routes/%s", parts[0], parts[1])
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return teardownError("DeleteRoute", rec, err)
}

// ── AWS::ApiGateway::Authorizer ─────────────────────────────────────────────
//
// The service has fully implemented CreateAuthorizer/GetAuthorizer(s)/
// DeleteAuthorizer since before this fix; nothing in provisioner.go's
// resourceHandler registry ever dispatched to them, so a method wired to a
// CfnAuthorizer via Fn::GetAtt/Ref provisioned with no authorizer behind it.

type apigwAuthorizerHandler struct{}

func (h *apigwAuthorizerHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	restApiID, _ := props["RestApiId"].(string)

	body := map[string]any{}
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	} else {
		body["name"] = rCtx.generatedName()
	}
	if v, _ := props["Type"].(string); v != "" {
		body["type"] = v
	}
	if v, _ := props["AuthorizerUri"].(string); v != "" {
		body["authorizerUri"] = v
	}
	if v, _ := props["AuthorizerCredentials"].(string); v != "" {
		body["authorizerCredentials"] = v
	}
	if v, _ := props["IdentitySource"].(string); v != "" {
		body["identitySource"] = v
	}
	if v, _ := props["IdentityValidationExpression"].(string); v != "" {
		body["identityValidationExpression"] = v
	}
	if v, ok := props["AuthorizerResultTtlInSeconds"]; ok {
		body["authorizerResultTtlInSeconds"] = v
	}
	if v, ok := props["ProviderARNs"].([]any); ok {
		arns := make([]string, 0, len(v))
		for _, a := range v {
			if s, _ := a.(string); s != "" {
				arns = append(arns, s)
			}
		}
		if len(arns) > 0 {
			body["providerARNs"] = arns
		}
	}

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/restapis/%s/authorizers", restApiID)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateAuthorizer: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateAuthorizer: parse response: %w", err)
	}
	authorizerID, _ := resp["id"].(string)

	physicalID := restApiID + "/" + authorizerID
	attrs := map[string]string{
		"AuthorizerId": authorizerID,
		"Ref":          authorizerID,
	}
	return physicalID, attrs, nil
}

func (h *apigwAuthorizerHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	path := fmt.Sprintf("/restapis/%s/authorizers/%s", parts[0], parts[1])
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return teardownError("DeleteAuthorizer", rec, err)
}

// ── AWS::ApiGateway::Model ───────────────────────────────────────────────────
//
// Same registry gap as Authorizer above: CreateModel/GetModel(s)/DeleteModel
// were already implemented service-side.

type apigwModelHandler struct{}

func (h *apigwModelHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	restApiID, _ := props["RestApiId"].(string)
	name, _ := props["Name"].(string)
	if name == "" {
		name = rCtx.generatedName()
	}

	body := map[string]any{"name": name}
	if v, _ := props["ContentType"].(string); v != "" {
		body["contentType"] = v
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	if v, _ := props["Schema"].(string); v != "" {
		body["schema"] = v
	}

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/restapis/%s/models", restApiID)
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("CreateModel: %w", err)
	}

	// Ref of AWS::ApiGateway::Model is the model name, not a generated ID
	// (GetModel/DeleteModel are also addressed by name, unlike Authorizer).
	physicalID := restApiID + "/" + name
	attrs := map[string]string{
		"Name": name,
		"Ref":  name,
	}
	return physicalID, attrs, nil
}

func (h *apigwModelHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	path := fmt.Sprintf("/restapis/%s/models/%s", parts[0], parts[1])
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return teardownError("DeleteModel", rec, err)
}

// ── AWS::ApiGateway::RequestValidator ───────────────────────────────────────
//
// Same registry gap: CreateRequestValidator/GetRequestValidators/
// DeleteRequestValidator were already implemented service-side. There is no
// GetRequestValidator (singular) or UpdateRequestValidator endpoint, so an
// in-place Update is not offered here — a property change replaces the
// resource, same as the service's own capability surface allows.

type apigwRequestValidatorHandler struct{}

func (h *apigwRequestValidatorHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	restApiID, _ := props["RestApiId"].(string)

	body := map[string]any{}
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	} else {
		body["name"] = rCtx.generatedName()
	}
	if v, ok := props["ValidateRequestBody"]; ok {
		body["validateRequestBody"] = v
	}
	if v, ok := props["ValidateRequestParameters"]; ok {
		body["validateRequestParameters"] = v
	}

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/restapis/%s/requestvalidators", restApiID)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateRequestValidator: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateRequestValidator: parse response: %w", err)
	}
	validatorID, _ := resp["id"].(string)

	physicalID := restApiID + "/" + validatorID
	attrs := map[string]string{
		"RequestValidatorId": validatorID,
		"Ref":                validatorID,
	}
	return physicalID, attrs, nil
}

func (h *apigwRequestValidatorHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	path := fmt.Sprintf("/restapis/%s/requestvalidators/%s", parts[0], parts[1])
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return teardownError("DeleteRequestValidator", rec, err)
}
