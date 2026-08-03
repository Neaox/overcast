package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

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

// generatedECSName mints the physical name CloudFormation generates for a
// resource whose name property the template left out — `<stack>-<logical>-<suffix>`.
//
// CDK leans on this heavily: it emits `AWS::ECS::Cluster` with no properties at
// all and `AWS::ECS::Service` with no `ServiceName`, expecting CloudFormation to
// name them. Without it a cluster falls back to the ECS API's default name of
// "default", so every CDK stack shares one cluster, and a service is rejected
// outright for having no name.
func generatedECSName(rCtx *resolveContext) string {
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	return fmt.Sprintf("%s-%s-%s", rCtx.StackName, rCtx.LogicalID, suffix)
}

// ── AWS::ECS::Cluster ──────────────────────────────────────────────────────

type ecsClusterHandler struct{}

func (h *ecsClusterHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["ClusterName"].(string); v != "" {
		body["clusterName"] = v
	} else {
		body["clusterName"] = generatedECSName(rCtx)
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

// ecsServiceStabilizeTimeout bounds the wait for a new service to reach its
// desired count. CloudFormation itself waits for hours before giving up; an
// emulator placing containers locally either gets there in well under a second
// or is not going to, so the wait is short enough to fail fast and long enough
// to cover a slow image pull.
const (
	ecsServiceStabilizeTimeout  = 60 * time.Second
	ecsServiceStabilizeInterval = 100 * time.Millisecond
)

func (h *ecsServiceHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Convert CF PascalCase properties to ECS API camelCase.
	body := convertCFKeysToAPI(props).(map[string]any)

	// CloudFormation names the service when the template does not, which is
	// what CDK expects — it never emits ServiceName.
	if name, _ := body["serviceName"].(string); name == "" {
		body["serviceName"] = generatedECSName(rCtx)
	}

	// CloudFormation defaults DesiredCount for a new service, and CDK relies on
	// it: since v2 the construct omits the property entirely unless the app
	// sets one. Without this the service is created with the ECS API's own
	// default of zero and sits ACTIVE at 0/0, never starting anything.
	// DAEMON services take their count from the cluster, not from this.
	if _, ok := body["desiredCount"]; !ok {
		if strategy, _ := body["schedulingStrategy"].(string); strategy != "DAEMON" {
			body["desiredCount"] = 1
		}
	}

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
	cluster, _ := body["cluster"].(string)
	if err := waitForServiceStable(ctx, router, rCtx.Region, cluster, arn); err != nil {
		return "", nil, err
	}

	attrs := map[string]string{
		"ServiceArn": arn,
		"Name":       resp.Service.ServiceName,
	}
	return arn, attrs, nil
}

// describedECSService is the DescribeServices projection the stabilization wait
// reads. Nothing else in the response bears on whether the resource is done.
type describedECSService struct {
	ServiceName  string                   `json:"serviceName"`
	DesiredCount int                      `json:"desiredCount"`
	RunningCount int                      `json:"runningCount"`
	Deployments  []describedECSDeployment `json:"deployments"`
	Events       []describedECSEvent      `json:"events"`
}

type describedECSDeployment struct {
	Status             string `json:"status"`
	FailedTasks        int    `json:"failedTasks"`
	RolloutState       string `json:"rolloutState"`
	RolloutStateReason string `json:"rolloutStateReason"`
}

type describedECSEvent struct {
	Message string `json:"message"`
}

// ecsServiceStable reports whether a service has finished rolling out, by the
// same definition the AWS CLI's `ecs wait services-stable` uses: one deployment
// left, running its desired count.
//
// The deployment count is the half that matters on an update. A service being
// updated reports two deployments — the new one placing tasks and the one it
// replaced, still serving — so its running count already equals its desired
// count before the new deployment has started anything. Reading the counts
// alone therefore calls a rollout complete the instant it begins, which is how
// an update to a task definition whose tasks cannot start reported
// UPDATE_COMPLETE around a service that never ran it.
func ecsServiceStable(svc describedECSService) bool {
	return len(svc.Deployments) <= 1 && svc.RunningCount >= svc.DesiredCount
}

// ecsServiceRolloutFailure reports the reason the deployment currently being
// rolled out has failed, if it has. Only the PRIMARY deployment counts: the one
// it superseded may well be the failure that prompted this update, and holding
// its history against the new one would fail every recovery.
func ecsServiceRolloutFailure(svc describedECSService) (string, bool) {
	for _, d := range svc.Deployments {
		if d.Status != "PRIMARY" {
			continue
		}
		if d.RolloutState != "FAILED" && d.FailedTasks == 0 {
			return "", false
		}
		if len(svc.Events) > 0 {
			return svc.Events[0].Message, true
		}
		return d.RolloutStateReason, true
	}
	return "", false
}

// waitForServiceStable blocks until an ECS service's current deployment reaches
// its desired count, so a service that cannot place its tasks fails the stack
// instead of leaving it CREATE_COMPLETE or UPDATE_COMPLETE with nothing running.
// This is what CloudFormation does: the resource is not complete until the
// service stabilizes, and a service that never does fails with the reason its
// own events give.
//
// Create and update both come through here, deliberately: they are the same
// wait on the same definition of done, and the bug this fixes is what happens
// when the two drift apart.
func waitForServiceStable(ctx context.Context, router http.Handler, region, cluster, serviceArn string) error {
	body := map[string]any{"services": []string{serviceArn}}
	if cluster != "" {
		body["cluster"] = cluster
	}

	deadline := time.Now().Add(ecsServiceStabilizeTimeout)
	lastReason := ""
	for {
		rec, err := internalJSON(ctx, router, region, "AmazonEC2ContainerServiceV20141113.DescribeServices", body)
		if err != nil {
			return fmt.Errorf("DescribeServices: %w", err)
		}
		var resp struct {
			Services []describedECSService `json:"services"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			return fmt.Errorf("DescribeServices: parse response: %w", err)
		}
		if len(resp.Services) == 0 {
			return fmt.Errorf("service %s not found while waiting for it to stabilize", serviceArn)
		}
		svc := resp.Services[0]
		if ecsServiceStable(svc) {
			return nil
		}
		if len(svc.Events) > 0 {
			lastReason = svc.Events[0].Message
		}
		// A task the scheduler could not place will not place itself on a
		// retry here, so report it as soon as it is known rather than holding
		// the stack open for the full timeout.
		if reason, failed := ecsServiceRolloutFailure(svc); failed {
			if reason == "" {
				reason = lastReason
			}
			return fmt.Errorf("service %s did not stabilize: %s", svc.ServiceName, reason)
		}
		if time.Now().After(deadline) {
			if lastReason == "" {
				lastReason = fmt.Sprintf("%d of %d tasks running", svc.RunningCount, svc.DesiredCount)
			}
			return fmt.Errorf("service %s did not stabilize within %s: %s", svc.ServiceName, ecsServiceStabilizeTimeout, lastReason)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ecsServiceStabilizeInterval):
		}
	}
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

	// Wait for the new deployment the same way Create does. An update that
	// swaps in a task definition whose tasks cannot start is the single most
	// common way a real deployment goes wrong, and without this the resource
	// reports success while the service sits on a failed rollout — leaving the
	// stack UPDATE_COMPLETE around a service running nothing.
	//
	// The failure is terminal for the resource: the service exists and has the
	// new task definition on it, so replacing it would add a second service
	// running the same broken revision rather than fix anything.
	if err := waitForServiceStable(ctx, router, rCtx.Region, cluster, physicalID); err != nil {
		return "", nil, failUpdate(err)
	}
	return physicalID, nil, nil
}
