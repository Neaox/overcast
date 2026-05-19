package cloudformation

// CloudFormation provisioners for AWS::ApiGateway::ApiKey,
// AWS::ApiGateway::UsagePlan, and AWS::ApiGateway::UsagePlanKey.
//
// These wire CDK / template authors through to the API Gateway service so
// `cdk deploy` for stacks that use API key authentication produces the same
// runtime state as a direct SDK call.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/config"
)

// ── AWS::ApiGateway::ApiKey ────────────────────────────────────────────────

type apigwApiKeyHandler struct{}

func (h *apigwApiKeyHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{
		// CloudFormation defaults Enabled to false but most templates set it
		// to true; preserve the property if present, otherwise default true
		// to match the typical CDK-generated behaviour.
		"enabled": true,
	}
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	if v, ok := props["Enabled"]; ok {
		body["enabled"] = v
	}
	if v, _ := props["Value"].(string); v != "" {
		body["value"] = v
	}
	if stages, ok := props["StageKeys"].([]any); ok && len(stages) > 0 {
		stageKeys := make([]string, 0, len(stages))
		for _, s := range stages {
			sm, ok := s.(map[string]any)
			if !ok {
				continue
			}
			restAPIID, _ := sm["RestApiId"].(string)
			stageName, _ := sm["StageName"].(string)
			if restAPIID != "" && stageName != "" {
				stageKeys = append(stageKeys, restAPIID+"/"+stageName)
			}
		}
		if len(stageKeys) > 0 {
			body["stageKeys"] = stageKeys
		}
	}

	data, _ := json.Marshal(body)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/apikeys", "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateApiKey: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateApiKey: parse response: %w", err)
	}
	keyID, _ := resp["id"].(string)
	keyValue, _ := resp["value"].(string)

	attrs := map[string]string{
		"APIKeyId": keyID,
		"Ref":      keyID,
	}
	// Surface the generated/known value so other stack resources can read it
	// via Fn::GetAtt in the unlikely case it's used.
	if keyValue != "" {
		attrs["Value"] = keyValue
	}
	return keyID, attrs, nil
}

func (h *apigwApiKeyHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	if physicalID == "" {
		return nil
	}
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/apikeys/"+physicalID, "", nil)
	return err
}

// ── AWS::ApiGateway::UsagePlan ─────────────────────────────────────────────

type apigwUsagePlanHandler struct{}

func (h *apigwUsagePlanHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["UsagePlanName"].(string); v != "" {
		body["name"] = v
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}

	if stages, ok := props["ApiStages"].([]any); ok && len(stages) > 0 {
		out := make([]map[string]any, 0, len(stages))
		for _, s := range stages {
			sm, ok := s.(map[string]any)
			if !ok {
				continue
			}
			apiID, _ := sm["ApiId"].(string)
			stageName, _ := sm["Stage"].(string)
			if apiID == "" || stageName == "" {
				continue
			}
			out = append(out, map[string]any{
				"apiId": apiID,
				"stage": stageName,
			})
		}
		if len(out) > 0 {
			body["apiStages"] = out
		}
	}

	if t, ok := props["Throttle"].(map[string]any); ok {
		throttle := map[string]any{}
		if v, ok := t["BurstLimit"]; ok {
			throttle["burstLimit"] = v
		}
		if v, ok := t["RateLimit"]; ok {
			throttle["rateLimit"] = v
		}
		if len(throttle) > 0 {
			body["throttle"] = throttle
		}
	}

	if q, ok := props["Quota"].(map[string]any); ok {
		quota := map[string]any{}
		if v, ok := q["Limit"]; ok {
			quota["limit"] = v
		}
		if v, ok := q["Offset"]; ok {
			quota["offset"] = v
		}
		if v, _ := q["Period"].(string); v != "" {
			quota["period"] = v
		}
		if len(quota) > 0 {
			body["quota"] = quota
		}
	}

	data, _ := json.Marshal(body)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/usageplans", "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateUsagePlan: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateUsagePlan: parse response: %w", err)
	}
	planID, _ := resp["id"].(string)

	attrs := map[string]string{
		"Id":  planID,
		"Ref": planID,
	}
	return planID, attrs, nil
}

func (h *apigwUsagePlanHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	if physicalID == "" {
		return nil
	}
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/usageplans/"+physicalID, "", nil)
	return err
}

func (h *apigwUsagePlanHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	usagePlanID := physicalID

	ops := buildPatchOps(oldProps, props, map[string]string{
		"Description": "/description",
	})

	oldThrottle, _ := oldProps["Throttle"].(map[string]any)
	newThrottle, _ := props["Throttle"].(map[string]any)
	ops = append(ops, buildPatchOps(oldThrottle, newThrottle, map[string]string{
		"BurstLimit": "/throttle/burstLimit",
		"RateLimit":  "/throttle/rateLimit",
	})...)

	oldQuota, _ := oldProps["Quota"].(map[string]any)
	newQuota, _ := props["Quota"].(map[string]any)
	ops = append(ops, buildPatchOps(oldQuota, newQuota, map[string]string{
		"Limit":  "/quota/limit",
		"Offset": "/quota/offset",
		"Period": "/quota/period",
	})...)

	if len(ops) == 0 {
		return physicalID, nil, nil
	}
	body := map[string]any{"patchOperations": ops}
	data, _ := json.Marshal(body)
	path := "/usageplans/" + usagePlanID
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPatch, path, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("UpdateUsagePlan: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::ApiGateway::UsagePlanKey ──────────────────────────────────────────

type apigwUsagePlanKeyHandler struct{}

func (h *apigwUsagePlanKeyHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	planID, _ := props["UsagePlanId"].(string)
	keyID, _ := props["KeyId"].(string)
	keyType, _ := props["KeyType"].(string)
	if keyType == "" {
		keyType = "API_KEY"
	}
	if planID == "" || keyID == "" {
		return "", nil, fmt.Errorf("CreateUsagePlanKey: UsagePlanId and KeyId are required")
	}

	body := map[string]any{
		"keyId":   keyID,
		"keyType": keyType,
	}
	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/usageplans/%s/keys", planID)
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("CreateUsagePlanKey: %w", err)
	}

	physicalID := planID + "/" + keyID
	attrs := map[string]string{
		"Id":  keyID,
		"Ref": keyID,
	}
	return physicalID, attrs, nil
}

func (h *apigwUsagePlanKeyHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	if physicalID == "" {
		return nil
	}
	// physicalID = "{usagePlanId}/{keyId}"
	for i := 0; i < len(physicalID); i++ {
		if physicalID[i] == '/' {
			planID := physicalID[:i]
			keyID := physicalID[i+1:]
			path := fmt.Sprintf("/usageplans/%s/keys/%s", planID, keyID)
			_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
			return err
		}
	}
	return nil
}
