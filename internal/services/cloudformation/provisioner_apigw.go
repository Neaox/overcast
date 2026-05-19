package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/Neaox/overcast/internal/config"
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
			ops = append(ops, map[string]any{"op": "add", "path": jsonPath, "value": newVal})
		} else if oldExists && !newExists {
			ops = append(ops, map[string]any{"op": "remove", "path": jsonPath})
		} else if !reflect.DeepEqual(oldVal, newVal) {
			ops = append(ops, map[string]any{"op": "replace", "path": jsonPath, "value": newVal})
		}
	}
	return ops
}

// ── AWS::ApiGateway::RestApi ───────────────────────────────────────────────

type apigwRestApiHandler struct{}

func (h *apigwRestApiHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	if ec, ok := props["EndpointConfiguration"].(map[string]any); ok {
		body["endpointConfiguration"] = ec
	}

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
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/restapis/"+physicalID, "", nil)
	return err
}

func (h *apigwRestApiHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID := physicalID
	ops := buildPatchOps(oldProps, props, map[string]string{
		"Name":        "/name",
		"Description": "/description",
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
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
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
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
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
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
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
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/v2/apis/"+physicalID, "", nil)
	return err
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
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
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
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
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
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}
