package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
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

// ecsServiceStabilizeTimeout bounds the wait for a new service to reach a
// steady state. CloudFormation itself waits for hours before giving up; an
// emulator placing containers locally gets there in seconds — the tasks start
// at once and then have to stay up long enough for ECS to credit them — or is
// not going to, so the wait is short enough to fail fast and long enough to
// cover a slow image pull on top of that.
//
// The interval is what a `services-stable` waiter would call reckless: AWS's
// polls every 15 seconds. Polling this fast is right for a local emulator,
// where the whole deploy is over inside one of those intervals, but it is the
// reason ecsServiceStable cannot read the counts alone — at 100 ms it samples
// moments AWS's waiter would never see.
const (
	ecsServiceStabilizeTimeout  = 60 * time.Second
	ecsServiceStabilizeInterval = 100 * time.Millisecond
)

func (h *ecsServiceHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// Convert CF PascalCase properties to ECS API camelCase.
	body := convertCFKeysToAPI(props).(map[string]any)
	// ForceNewDeployment is a CloudFormation orchestration property, not a
	// CreateService request member. Initial tasks already read launch-time
	// inputs, so there is no existing deployment to replace on create.
	delete(body, "forceNewDeployment")

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
	attrs := map[string]string{
		"ServiceArn": arn,
		"Name":       resp.Service.ServiceName,
	}
	return arn, attrs, nil
}

// Stabilize holds the resource open until the service's current deployment
// reaches a steady state, so a service that cannot place its tasks — or cannot
// keep them alive — fails the stack rather than leaving it complete with nothing
// running. Create and update share it — they are the same wait on the same
// definition of done, and the bug this exists for is what happened when the two
// drifted apart. See resourceStabilizer.
func (h *ecsServiceHandler) Stabilize(ctx context.Context, router http.Handler, _ *config.Config, clk clock.Clock, physicalID string, rCtx *resolveContext) error {
	return waitForServiceStable(ctx, clk, router, rCtx.Region, ecsClusterFromServiceARN(physicalID), physicalID)
}

// ecsClusterFromServiceARN reads the cluster out of a service ARN
// (arn:aws:ecs:region:account:service/cluster/service-name), which is where
// every call that needs one after create gets it: the physical ID is the only
// thing the provisioner hands back.
func ecsClusterFromServiceARN(serviceARN string) string {
	if parts := strings.Split(serviceARN, "/"); len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return ""
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

// ecsServiceStable reports whether a service has finished rolling out: one
// deployment left, running its desired count, and — where the service reports a
// rollout state at all — that deployment saying it reached a steady state.
//
// The deployment count is the half that matters on an update. A service being
// updated reports two deployments — the new one placing tasks and the one it
// replaced, still serving — so its running count already equals its desired
// count before the new deployment has started anything. Reading the counts
// alone therefore calls a rollout complete the instant it begins, which is how
// an update to a task definition whose tasks cannot start reported
// UPDATE_COMPLETE around a service that never ran it.
//
// The rollout state is the half that matters on a create, and it is where this
// deliberately goes further than the AWS CLI's `ecs wait services-stable`,
// which is satisfied by the counts alone. Two things make the counts on their
// own unsafe here. AWS defines the deployment's own state in terms of the one
// this is trying to detect — "when the service reaches a steady state, the
// deployment transitions to a COMPLETED state" (API_Deployment) — so asking the
// deployment is asking the more direct question. And that waiter polls every
// 15 seconds, where this polls every 100 (see ecsServiceStabilizeInterval): it
// samples transient states real AWS would never show a caller, including the
// instant a container that is about to exit is briefly RUNNING. Counting that
// instant is what reported CREATE_COMPLETE on a service that was crash-looping.
//
// A CODE_DEPLOY or EXTERNAL controller reports no rolloutState at all, so for
// those the counts remain the only evidence there is.
func ecsServiceStable(svc describedECSService) bool {
	if len(svc.Deployments) > 1 || svc.RunningCount < svc.DesiredCount {
		return false
	}
	for _, d := range svc.Deployments {
		if d.Status == "PRIMARY" && d.RolloutState != "" {
			return d.RolloutState == "COMPLETED"
		}
	}
	return true
}

// ecsServiceRolloutFailure reports the reason the deployment currently being
// rolled out has failed, if it has. Only the PRIMARY deployment counts: the one
// it superseded may well be the failure that prompted this update, and holding
// its history against the new one would fail every recovery.
//
// The reason it returns is never empty, so the caller has something to report
// without a fallback of its own.
func ecsServiceRolloutFailure(svc describedECSService) (string, bool) {
	for _, d := range svc.Deployments {
		if d.Status != "PRIMARY" {
			continue
		}
		if d.RolloutState != "FAILED" && d.FailedTasks == 0 {
			return "", false
		}
		return ecsRolloutFailureReason(svc, d), true
	}
	return "", false
}

// ecsRolloutFailureReason picks the most useful account of why a deployment
// failed, from what DescribeServices carries.
//
// The order matters, and the middle rung is the one that had to be qualified. A
// deployment's rolloutStateReason only describes a failure once a circuit
// breaker has failed it; until then it still holds the "ECS deployment <id> in
// progress." it was created with. Quoting that as the reason a stack failed
// describes a deploy that is over as though it were still running, and a real
// failed update reported exactly that: "service orders-api did not stabilize:
// ECS deployment e3a3e8a0-… in progress."
//
// The last rung is a sentence built from the deployment's own numbers rather
// than the newest service event. A service that is failing to keep tasks alive
// emits ordinary progress events while it does — "has started 1 tasks." lands
// on top after every replacement attempt — and reporting one of those as the
// failure is worse than reporting no event at all. The deployment's failed-task
// count is at least true.
func ecsRolloutFailureReason(svc describedECSService, d describedECSDeployment) string {
	if reason := ecsServiceFailureEvent(svc.Events); reason != "" {
		return reason
	}
	if d.RolloutState == "FAILED" && d.RolloutStateReason != "" {
		return d.RolloutStateReason
	}
	return fmt.Sprintf("%d task(s) failed to stay running; %d of %d tasks running",
		d.FailedTasks, svc.RunningCount, svc.DesiredCount)
}

// ecsServiceFailureEvent returns the newest scheduler event that reports a
// failure. ECS prepends service events, and a replacement attempt can therefore
// put a successful "has started N tasks" event ahead of the event that explains
// why the deployment failed.
func ecsServiceFailureEvent(events []describedECSEvent) string {
	// Placement events carry the service-provided cause after "Reason:". ECS
	// may prepend broader deployment-failure events afterward, so prefer the
	// reason-bearing observation even when it is not the newest failure event.
	for _, event := range events {
		if strings.Contains(strings.ToLower(event.Message), "reason:") {
			return event.Message
		}
	}
	for _, event := range events {
		message := strings.ToLower(event.Message)
		if strings.Contains(message, " was unable to ") ||
			strings.Contains(message, " is unable to ") ||
			strings.Contains(message, " deployment failed:") {
			return event.Message
		}
	}
	return ""
}

// waitForServiceStable blocks until an ECS service's current deployment reaches
// a steady state, so a service that cannot place its tasks — or cannot keep them
// alive — fails the stack instead of leaving it CREATE_COMPLETE or
// UPDATE_COMPLETE with nothing running. This is what CloudFormation does: the
// resource is not complete until the service stabilizes, and a service that
// never does fails with the reason its own deployment and events give.
//
// Create and update both come through here, deliberately: they are the same
// wait on the same definition of done, and the bug this fixes is what happens
// when the two drift apart.
//
// This is the one wait that does not run on the shared shape in
// provisioner_stabilize.go, because its question is not the one that shape
// answers. That shape asks whether a single documented status string has
// reached a documented value; a service is done only when a predicate over
// three things holds at once — how many deployments it has left, whether the
// surviving one is running its desired count, and what that deployment says
// about its own rollout. Everything else about it is the same, including the
// clock, which is injected rather than read off time.Now() so the timeout can
// be exercised without spending it.
func waitForServiceStable(ctx context.Context, clk clock.Clock, router http.Handler, region, cluster, serviceArn string) error {
	body := map[string]any{"services": []string{serviceArn}}
	if cluster != "" {
		body["cluster"] = cluster
	}

	deadline := clk.Now().Add(ecsServiceStabilizeTimeout)
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
		// Only a failure event is worth remembering for the timeout message. A
		// service that is failing keeps emitting ordinary progress events on
		// top of the one that explains it, so the newest event is more often
		// "has started 1 tasks." than anything a reader can act on — the counts
		// below say more.
		if reason := ecsServiceFailureEvent(svc.Events); reason != "" {
			lastReason = reason
		}
		// A task the scheduler could not place will not place itself on a
		// retry here, so report it as soon as it is known rather than holding
		// the stack open for the full timeout. The reason comes back non-empty,
		// so there is nothing to fall back to.
		if reason, failed := ecsServiceRolloutFailure(svc); failed {
			return fmt.Errorf("service %s did not stabilize: %s", svc.ServiceName, reason)
		}
		if clk.Now().After(deadline) {
			if lastReason == "" {
				lastReason = fmt.Sprintf("%d of %d tasks running", svc.RunningCount, svc.DesiredCount)
			}
			return fmt.Errorf("service %s did not stabilize within %s: %s", svc.ServiceName, ecsServiceStabilizeTimeout, lastReason)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-clk.After(ecsServiceStabilizeInterval):
		}
	}
}

func (h *ecsServiceHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	cluster := ecsClusterFromServiceARN(physicalID)

	// First set desired count to 0, then delete.
	updateBody := map[string]any{
		"service":      physicalID,
		"desiredCount": 0,
	}
	if cluster != "" {
		updateBody["cluster"] = cluster
	}
	// Best-effort: the scale-down only smooths the delete that follows, and
	// DeleteService is the call whose outcome decides whether the service is
	// gone.
	_, _ = internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerServiceV20141113.UpdateService", updateBody)

	deleteBody := map[string]any{
		"service": physicalID,
	}
	if cluster != "" {
		deleteBody["cluster"] = cluster
	}
	rec, err := internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerServiceV20141113.DeleteService", deleteBody)
	return teardownError("DeleteService", rec, err)
}

func (h *ecsServiceHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	cluster := ecsClusterFromServiceARN(physicalID)

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
	if forceNewDeploymentEnabled(props["ForceNewDeployment"]) {
		body["forceNewDeployment"] = true
	}

	if _, err := internalJSON(ctx, router, rCtx.Region, "AmazonEC2ContainerServiceV20141113.UpdateService", body); err != nil {
		return "", nil, fmt.Errorf("UpdateService: %w", err)
	}

	// The provisioner waits for the new deployment here, the same way it does
	// on create — see Stabilize. An update that swaps in a task definition whose
	// tasks cannot start is the single most common way a real deployment goes
	// wrong, and without the wait the resource reports success while the service
	// sits on a failed rollout.
	return physicalID, nil, nil
}

// forceNewDeploymentEnabled translates AWS::ECS::Service's CloudFormation-only
// ForceNewDeployment object to the boolean UpdateService member. CloudFormation
// invokes Update only when the resource properties change; a changed nonce is
// therefore preserved as one forced ECS deployment without storing runtime
// state or inventing a cache.
func forceNewDeploymentEnabled(raw any) bool {
	props, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	enabled, _ := props["EnableForceNewDeployment"].(bool)
	return enabled
}
