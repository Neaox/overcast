package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/config"
)

// ── ECS property helpers ─────────────────────────────────────────────────

// toLowerCamelCase converts PascalCase to camelCase for the first level.
func toLowerCamelCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// convertCFKeysToAPI recursively converts PascalCase keys in maps to camelCase.
// This handles the CF→API property translation for ECS and similar services.
func convertCFKeysToAPI(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, v := range val {
			out[toLowerCamelCase(k)] = convertCFKeysToAPI(v)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, v := range val {
			out[i] = convertCFKeysToAPI(v)
		}
		return out
	default:
		return v
	}
}

// ── AWS::ECS::Cluster ──────────────────────────────────────────────────────

type ecsClusterHandler struct{}

func (h *ecsClusterHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["ClusterName"].(string); v != "" {
		body["clusterName"] = v
	}
	if v, ok := props["CapacityProviders"]; ok {
		body["capacityProviders"] = v
	}
	if v, ok := props["DefaultCapacityProviderStrategy"]; ok {
		body["defaultCapacityProviderStrategy"] = convertCFKeysToAPI(v)
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerServiceV20141113.CreateCluster", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateCluster: %w", err)
	}

	var resp struct {
		Cluster struct {
			ClusterArn  string `json:"clusterArn"`
			ClusterName string `json:"clusterName"`
		} `json:"cluster"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateCluster: parse response: %w", err)
	}

	attrs := map[string]string{
		"Arn": resp.Cluster.ClusterArn,
	}
	return resp.Cluster.ClusterArn, attrs, nil
}

func (h *ecsClusterHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"cluster": physicalID}
	_, err := internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerServiceV20141113.DeleteCluster", body)
	return err
}

func (h *ecsClusterHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::ECS::TaskDefinition ───────────────────────────────────────────────

type ecsTaskDefinitionHandler struct{}

func (h *ecsTaskDefinitionHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Convert CF PascalCase properties to ECS API camelCase.
	body := convertCFKeysToAPI(props).(map[string]any)

	rec, err := internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerServiceV20141113.RegisterTaskDefinition", body)
	if err != nil {
		return "", nil, fmt.Errorf("RegisterTaskDefinition: %w", err)
	}

	var resp struct {
		TaskDefinition struct {
			TaskDefinitionArn string `json:"taskDefinitionArn"`
		} `json:"taskDefinition"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("RegisterTaskDefinition: parse response: %w", err)
	}

	arn := resp.TaskDefinition.TaskDefinitionArn
	attrs := map[string]string{
		"TaskDefinitionArn": arn,
	}
	return arn, attrs, nil
}

func (h *ecsTaskDefinitionHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"taskDefinition": physicalID}
	_, err := internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerServiceV20141113.DeregisterTaskDefinition", body)
	return err
}

// ── AWS::ECS::Service ──────────────────────────────────────────────────────

type ecsServiceHandler struct{}

func (h *ecsServiceHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Convert CF PascalCase properties to ECS API camelCase.
	body := convertCFKeysToAPI(props).(map[string]any)

	rec, err := internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerServiceV20141113.CreateService", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateService: %w", err)
	}

	var resp struct {
		Service struct {
			ServiceArn  string `json:"serviceArn"`
			ServiceName string `json:"serviceName"`
			ClusterArn  string `json:"clusterArn"`
		} `json:"service"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateService: parse response: %w", err)
	}

	arn := resp.Service.ServiceArn
	attrs := map[string]string{
		"ServiceArn": arn,
		"Name":       resp.Service.ServiceName,
	}
	return arn, attrs, nil
}

func (h *ecsServiceHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	// Extract cluster from ARN if possible.
	// Service ARN: arn:aws:ecs:region:account:service/cluster/service-name
	cluster := ""
	if parts := strings.Split(physicalID, "/"); len(parts) >= 2 {
		cluster = parts[len(parts)-2]
	}

	// First set desired count to 0, then delete.
	updateBody := map[string]any{
		"service":      physicalID,
		"desiredCount": 0,
	}
	if cluster != "" {
		updateBody["cluster"] = cluster
	}
	// Best-effort update; ignore errors.
	_, _ = internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerServiceV20141113.UpdateService", updateBody)

	deleteBody := map[string]any{
		"service": physicalID,
	}
	if cluster != "" {
		deleteBody["cluster"] = cluster
	}
	_, err := internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerServiceV20141113.DeleteService", deleteBody)
	return err
}

func (h *ecsServiceHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	cluster := ""
	if parts := strings.Split(physicalID, "/"); len(parts) >= 2 {
		cluster = parts[len(parts)-2]
	}

	if oldProps != nil {
		if newName, _ := props["ServiceName"].(string); newName != "" {
			if oldName, _ := oldProps["ServiceName"].(string); oldName != "" && newName != oldName {
				return "", nil, errReplacementRequired
			}
		}
		if newCluster, _ := props["Cluster"].(string); newCluster != "" {
			if oldCluster, _ := oldProps["Cluster"].(string); oldCluster != "" && newCluster != oldCluster {
				return "", nil, errReplacementRequired
			}
		}
		if newLaunchType, _ := props["LaunchType"].(string); newLaunchType != "" {
			if oldLaunchType, _ := oldProps["LaunchType"].(string); oldLaunchType != "" && newLaunchType != oldLaunchType {
				return "", nil, errReplacementRequired
			}
		}
	}

	body := map[string]any{"service": physicalID}
	if cluster != "" {
		body["cluster"] = cluster
	}
	if v, ok := props["DesiredCount"]; ok {
		body["desiredCount"] = v
	}
	if v, _ := props["TaskDefinition"].(string); v != "" {
		body["taskDefinition"] = v
	}
	if v, ok := props["NetworkConfiguration"]; ok {
		body["networkConfiguration"] = convertCFKeysToAPI(v)
	}
	if v, _ := props["PlatformVersion"].(string); v != "" {
		body["platformVersion"] = v
	}

	if _, err := internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerServiceV20141113.UpdateService", body); err != nil {
		return "", nil, fmt.Errorf("UpdateService: %w", err)
	}
	return physicalID, nil, nil
}
