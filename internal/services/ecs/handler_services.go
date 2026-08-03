package ecs

// handler_services.go — CreateService, UpdateService, DeleteService,
// DescribeServices, ListServices handlers and the service reconciler.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

const maxServiceEvents = 100

// serviceARN builds an ECS service ARN.
func (h *Handler) serviceARN(ctx context.Context, cluster, name string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:service/%s/%s", h.region(ctx), h.cfg.AccountID, cluster, name)
}

// extractServiceName extracts the service name from an ARN or returns the input as-is.
func extractServiceName(input string) string {
	if strings.HasPrefix(input, "arn:") {
		parts := strings.Split(input, "/")
		if len(parts) >= 2 {
			return parts[len(parts)-1]
		}
	}
	return input
}

// addServiceEvent prepends an event to the service's event list, capping at maxServiceEvents.
func (h *Handler) addServiceEvent(svc *ecsService, msg string) {
	evt := ServiceEvent{
		ID:        uuid.New().String(),
		CreatedAt: h.clk.Now().Unix(),
		Message:   msg,
	}
	svc.Events = append([]ServiceEvent{evt}, svc.Events...)
	if len(svc.Events) > maxServiceEvents {
		svc.Events = svc.Events[:maxServiceEvents]
	}
}

// CreateService handles AmazonEC2ContainerServiceV20141113.CreateService.
func (h *Handler) CreateService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster                  string                         `json:"cluster"`
		ServiceName              string                         `json:"serviceName"`
		TaskDefinition           string                         `json:"taskDefinition"`
		DesiredCount             *int                           `json:"desiredCount"`
		LaunchType               string                         `json:"launchType"`
		SchedulingStrategy       string                         `json:"schedulingStrategy"`
		NetworkConfiguration     *NetworkConfiguration          `json:"networkConfiguration"`
		DeploymentController     *DeploymentController          `json:"deploymentController"`
		DeploymentConfiguration  *DeploymentConfiguration       `json:"deploymentConfiguration"`
		CapacityProviderStrategy []CapacityProviderStrategyItem `json:"capacityProviderStrategy"`
		LoadBalancers            []ServiceLoadBalancer          `json:"loadBalancers"`
		PlatformVersion          string                         `json:"platformVersion"`

		HealthCheckGracePeriodSeconds *int              `json:"healthCheckGracePeriodSeconds"`
		EnableExecuteCommand          bool              `json:"enableExecuteCommand"`
		PropagateTags                 string            `json:"propagateTags"`
		ServiceRegistries             []ServiceRegistry `json:"serviceRegistries"`
		PlacementStrategy             []PlacementItem   `json:"placementStrategy"`
		PlacementConstraints          []PlacementItem   `json:"placementConstraints"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}
	if req.SchedulingStrategy == "" {
		req.SchedulingStrategy = "REPLICA"
	}
	if req.DeploymentController == nil {
		req.DeploymentController = &DeploymentController{Type: "ECS"}
	}

	clusterName := extractClusterName(req.Cluster)

	// Validate cluster exists.
	cluster, aerr := h.store.getCluster(r.Context(), clusterName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Validate service name.
	if req.ServiceName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "1 validation error detected: Value at 'serviceName' failed to satisfy constraint: Member must not be null",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Validate task definition.
	if req.TaskDefinition == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "taskDefinition must be specified when creating a service.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	family, revision, hasRevision := parseTaskDefRef(req.TaskDefinition)
	var td *TaskDefinition
	if hasRevision {
		td, aerr = h.store.getTaskDefinition(r.Context(), family, revision)
	} else {
		td, aerr = h.store.getLatestTaskDefinition(r.Context(), family)
	}
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// awsvpc networking is required by the task definition's networkMode, so
	// this can only be checked once the task definition has been resolved.
	if aerr := validateAwsvpcNetworkConfiguration(td, req.LaunchType, req.NetworkConfiguration); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if _, placementErr := h.resolveAwsvpcPlacement(r.Context(), req.NetworkConfiguration, "awsvpc services"); placementErr != nil {
		protocol.WriteJSONError(w, r, placementErr)
		return
	}

	// Check for duplicate.
	existing, _ := h.store.getService(r.Context(), clusterName, req.ServiceName)
	if existing != nil && existing.Status == "ACTIVE" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Creation of service was not idempotent.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	desired := 0
	if req.DesiredCount != nil {
		desired = *req.DesiredCount
	}

	// Determine platform version.
	platformVersion := req.PlatformVersion
	if platformVersion == "" && req.LaunchType == "FARGATE" {
		platformVersion = "LATEST"
	}

	now := h.clk.Now().Unix()

	svc := &ecsService{
		ServiceName:              req.ServiceName,
		ServiceArn:               h.serviceARN(r.Context(), clusterName, req.ServiceName),
		ClusterArn:               cluster.ClusterArn,
		TaskDefinition:           td.TaskDefinitionArn,
		DesiredCount:             desired,
		RunningCount:             0,
		PendingCount:             0,
		Status:                   "ACTIVE",
		LaunchType:               req.LaunchType,
		CreatedAt:                now,
		SchedulingStrategy:       req.SchedulingStrategy,
		NetworkConfiguration:     req.NetworkConfiguration,
		DeploymentController:     req.DeploymentController,
		DeploymentConfiguration:  req.DeploymentConfiguration,
		LoadBalancers:            req.LoadBalancers,
		CapacityProviderStrategy: req.CapacityProviderStrategy,

		HealthCheckGracePeriodSeconds: req.HealthCheckGracePeriodSeconds,
		EnableExecuteCommand:          req.EnableExecuteCommand,
		PropagateTags:                 req.PropagateTags,
		ServiceRegistries:             req.ServiceRegistries,
		PlacementStrategy:             req.PlacementStrategy,
		PlacementConstraints:          req.PlacementConstraints,
		PlatformVersion:               platformVersion,
		Events:                        make([]ServiceEvent, 0),
		Deployments:                   []Deployment{newPrimaryDeployment(td.TaskDefinitionArn, desired, now, platformVersion, req.NetworkConfiguration, req.DeploymentController)},
	}

	if aerr := h.store.putService(r.Context(), clusterName, svc); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Reconcile: start tasks to match desired count.
	h.reconcile(r.Context(), clusterName, req.ServiceName)

	// Re-read service with updated counts.
	svc, _ = h.store.getService(r.Context(), clusterName, req.ServiceName)

	h.publish(r, events.ECSServiceCreated, events.ResourcePayload{Name: req.ServiceName})
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"service": svc}, "application/x-amz-json-1.1")
}

// UpdateService handles AmazonEC2ContainerServiceV20141113.UpdateService.
func (h *Handler) UpdateService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster              string                `json:"cluster"`
		Service              string                `json:"service"`
		TaskDefinition       string                `json:"taskDefinition"`
		DesiredCount         *int                  `json:"desiredCount"`
		NetworkConfiguration *NetworkConfiguration `json:"networkConfiguration"`
		PlatformVersion      string                `json:"platformVersion"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}

	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)

	svc, aerr := h.store.getService(r.Context(), clusterName, serviceName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	now := h.clk.Now().Unix()

	// Update desired count.
	if req.DesiredCount != nil {
		svc.DesiredCount = *req.DesiredCount
		for i := range svc.Deployments {
			if svc.Deployments[i].Status == "PRIMARY" {
				svc.Deployments[i].DesiredCount = *req.DesiredCount
				svc.Deployments[i].UpdatedAt = now
			}
		}
		h.addServiceEvent(svc, fmt.Sprintf("(service %s) has begun draining connections on %d tasks.", serviceName, 0))
	}

	// Update task definition if changed.
	if req.TaskDefinition != "" {
		family, revision, hasRevision := parseTaskDefRef(req.TaskDefinition)
		var td *TaskDefinition
		if hasRevision {
			td, aerr = h.store.getTaskDefinition(r.Context(), family, revision)
		} else {
			td, aerr = h.store.getLatestTaskDefinition(r.Context(), family)
		}
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}

		if td.TaskDefinitionArn != svc.TaskDefinition {
			// Demote current PRIMARY to ACTIVE.
			for i := range svc.Deployments {
				if svc.Deployments[i].Status == "PRIMARY" {
					svc.Deployments[i].Status = "ACTIVE"
					svc.Deployments[i].UpdatedAt = now
				}
			}
			// Create new PRIMARY deployment.
			newNetCfg := req.NetworkConfiguration
			if newNetCfg == nil {
				newNetCfg = svc.NetworkConfiguration
			}
			// Re-validate with any updated NetworkConfiguration during deployment creation.
			if _, placementErr := h.resolveAwsvpcPlacement(r.Context(), newNetCfg, "awsvpc services"); placementErr != nil {
				protocol.WriteJSONError(w, r, placementErr)
				return
			}
			newPlatformVersion := req.PlatformVersion
			if newPlatformVersion == "" {
				newPlatformVersion = svc.PlatformVersion
			}
			svc.Deployments = append([]Deployment{{
				ID:                   uuid.New().String(),
				Status:               "PRIMARY",
				TaskDefinition:       td.TaskDefinitionArn,
				DesiredCount:         svc.DesiredCount,
				RunningCount:         0,
				PendingCount:         0,
				CreatedAt:            now,
				UpdatedAt:            now,
				NetworkConfiguration: newNetCfg,
				PlatformVersion:      newPlatformVersion,
			}}, svc.Deployments...)
			svc.TaskDefinition = td.TaskDefinitionArn
			h.addServiceEvent(svc, fmt.Sprintf("(service %s) was updated to use task definition %s.", serviceName, td.TaskDefinitionArn))
		}
	}

	// Update networkConfiguration if provided.
	if req.NetworkConfiguration != nil {
		svc.NetworkConfiguration = req.NetworkConfiguration
		for i := range svc.Deployments {
			if svc.Deployments[i].Status == "PRIMARY" {
				svc.Deployments[i].NetworkConfiguration = req.NetworkConfiguration
				svc.Deployments[i].UpdatedAt = now
			}
		}
	}

	// Update platformVersion if provided.
	if req.PlatformVersion != "" {
		svc.PlatformVersion = req.PlatformVersion
	}

	if aerr := h.store.putService(r.Context(), clusterName, svc); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Reconcile: adjust task count.
	h.reconcile(r.Context(), clusterName, serviceName)

	// Re-read with updated counts.
	svc, _ = h.store.getService(r.Context(), clusterName, serviceName)

	h.publish(r, events.ECSServiceUpdated, events.ResourcePayload{Name: serviceName})
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"service": svc}, "application/x-amz-json-1.1")
}

// DeleteService handles AmazonEC2ContainerServiceV20141113.DeleteService.
func (h *Handler) DeleteService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
		Service string `json:"service"`
		Force   bool   `json:"force"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}

	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)

	svc, aerr := h.store.getService(r.Context(), clusterName, serviceName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	svc.Status = "DRAINING"
	svc.DesiredCount = 0
	for i := range svc.Deployments {
		if svc.Deployments[i].Status == "PRIMARY" {
			svc.Deployments[i].DesiredCount = 0
			svc.Deployments[i].UpdatedAt = h.clk.Now().Unix()
		}
	}
	h.addServiceEvent(svc, fmt.Sprintf("(service %s) is draining.", serviceName))

	if aerr := h.store.putService(r.Context(), clusterName, svc); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Reconcile: stop all tasks.
	h.reconcile(r.Context(), clusterName, serviceName)

	// Re-read with updated counts.
	svc, _ = h.store.getService(r.Context(), clusterName, serviceName)

	h.publish(r, events.ECSServiceDeleted, events.ResourcePayload{Name: serviceName})
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"service": svc}, "application/x-amz-json-1.1")
}

// DescribeServices handles AmazonEC2ContainerServiceV20141113.DescribeServices.
func (h *Handler) DescribeServices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster  string   `json:"cluster"`
		Services []string `json:"services"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)

	type failure struct {
		Arn    string `json:"arn"`
		Reason string `json:"reason"`
	}
	found := make([]ecsService, 0, len(req.Services))
	failures := make([]failure, 0)

	for _, ref := range req.Services {
		name := extractServiceName(ref)
		svc, aerr := h.store.getService(r.Context(), clusterName, name)
		if aerr != nil {
			arn := ref
			if !strings.HasPrefix(arn, "arn:") {
				arn = h.serviceARN(r.Context(), clusterName, name)
			}
			failures = append(failures, failure{Arn: arn, Reason: "MISSING"})
			continue
		}

		// Recount from actual tasks for accuracy.
		h.refreshServiceCounts(r.Context(), clusterName, svc)
		found = append(found, *svc)
	}

	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{
		"services": found,
		"failures": failures,
	}, "application/x-amz-json-1.1")
}

// ListServices handles AmazonEC2ContainerServiceV20141113.ListServices.
func (h *Handler) ListServices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster    string `json:"cluster"`
		LaunchType string `json:"launchType"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)

	services, aerr := h.store.listServices(r.Context(), clusterName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	arns := make([]string, 0, len(services))
	for _, s := range services {
		if req.LaunchType != "" && s.LaunchType != req.LaunchType {
			continue
		}
		arns = append(arns, s.ServiceArn)
	}

	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"serviceArns": arns}, "application/x-amz-json-1.1")
}

// Rollout states of an ECS deployment. A deployment starts IN_PROGRESS, reaches
// COMPLETED at steady state, and only ever reaches FAILED through the
// deployment circuit breaker.
const (
	rolloutInProgress = "IN_PROGRESS"
	rolloutCompleted  = "COMPLETED"
	rolloutFailed     = "FAILED"
)

// serviceGroupPrefix is the task group ECS gives tasks a service owns.
const serviceGroupPrefix = "service:"

// serviceNameFromGroup returns the service a task belongs to, if any.
func serviceNameFromGroup(group string) (string, bool) {
	name, ok := strings.CutPrefix(group, serviceGroupPrefix)
	return name, ok && name != ""
}

// newPrimaryDeployment builds the PRIMARY deployment a new service or a new
// task definition starts with. It begins IN_PROGRESS — a deployment is only
// COMPLETED once its tasks are actually running, which is what steady state
// means — and carries no rollout state at all under a non-ECS controller,
// matching what AWS reports.
func newPrimaryDeployment(taskDefArn string, desired int, now int64, platformVersion string, netCfg *NetworkConfiguration, controller *DeploymentController) Deployment {
	d := Deployment{
		ID:                   uuid.New().String(),
		Status:               "PRIMARY",
		TaskDefinition:       taskDefArn,
		DesiredCount:         desired,
		CreatedAt:            now,
		UpdatedAt:            now,
		NetworkConfiguration: netCfg,
		PlatformVersion:      platformVersion,
	}
	if controller == nil || controller.Type == "" || controller.Type == "ECS" {
		d.RolloutState = rolloutInProgress
		d.RolloutStateReason = fmt.Sprintf("ECS deployment %s in progress.", d.ID)
	}
	return d
}

// primaryDeployment returns the service's PRIMARY deployment, or nil.
func primaryDeployment(svc *ecsService) *Deployment {
	for i := range svc.Deployments {
		if svc.Deployments[i].Status == "PRIMARY" {
			return &svc.Deployments[i]
		}
	}
	return nil
}

// usesECSController reports whether the service uses the rolling-update (ECS)
// deployment controller. AWS reports rolloutState only for those, omitting it
// for CODE_DEPLOY and EXTERNAL.
func usesECSController(svc *ecsService) bool {
	return svc.DeploymentController == nil || svc.DeploymentController.Type == "" || svc.DeploymentController.Type == "ECS"
}

// refreshServiceCounts recounts running/pending tasks for a service from the
// store and derives the primary deployment's rollout state from them. It
// reports whether the deployment reached steady state on this call, which is
// the edge the steady-state service event is emitted on — by callers that
// persist the service, since this is also called on the read path.
func (h *Handler) refreshServiceCounts(ctx context.Context, clusterName string, svc *ecsService) bool {
	tasks, aerr := h.store.listTasks(ctx, clusterName)
	if aerr != nil {
		return false
	}
	now := h.clk.Now().Unix()
	recoverAfter := int64(deploymentRecoveryWindow / time.Second)
	running, pending, settled := 0, 0, 0
	serviceGroup := serviceGroupPrefix + svc.ServiceName
	for _, t := range tasks {
		if t.Group != serviceGroup {
			continue
		}
		switch t.LastStatus {
		case "RUNNING":
			running++
			if t.StartedAt == nil || now-*t.StartedAt >= recoverAfter {
				settled++
			}
		case "PROVISIONING", "PENDING":
			pending++
		}
	}
	svc.RunningCount = running
	svc.PendingCount = pending

	d := primaryDeployment(svc)
	if d == nil {
		return false
	}
	d.RunningCount = running
	d.PendingCount = pending
	if !usesECSController(svc) {
		return false
	}
	// FAILED is terminal: a deployment the circuit breaker stopped does not
	// recover on its own, it is replaced by the next deployment.
	if d.RolloutState == rolloutFailed {
		return false
	}
	// A deployment that has already failed tasks has to prove a replacement
	// stays up before it counts as recovered — see deploymentRecoveryWindow.
	// One that has failed nothing settles the moment its tasks run.
	credited := running
	if d.FailedTasks > 0 {
		credited = settled
	}
	if credited < d.DesiredCount {
		d.RolloutState = rolloutInProgress
		d.RolloutStateReason = fmt.Sprintf("ECS deployment %s in progress.", d.ID)
		return false
	}
	reached := d.RolloutState != rolloutCompleted
	d.RolloutState = rolloutCompleted
	d.RolloutStateReason = fmt.Sprintf("ECS deployment %s completed.", d.ID)
	// failedTasks is AWS's count of *consecutive* failures, so reaching a
	// steady state clears it. Nothing else does: a replacement merely starting
	// is what a crash loop does on every cycle.
	d.FailedTasks = 0
	return reached
}

// settleService recomputes a service's counts after one of its tasks changed
// state, persisting the result and recording the steady-state event on the edge
// into it. Called from the task's own RUNNING transition, because that is what
// moves a service to its desired count once the tasks have been placed.
func (h *Handler) settleService(ctx context.Context, clusterName, serviceName string) {
	svc, aerr := h.store.getService(ctx, clusterName, serviceName)
	if aerr != nil || svc == nil {
		return
	}
	if h.refreshServiceCounts(ctx, clusterName, svc) {
		h.addServiceEvent(svc, fmt.Sprintf("(service %s) has reached a steady state.", serviceName))
	}
	h.scheduleRecoveryCheck(ctx, clusterName, svc)
	if aerr := h.store.putService(ctx, clusterName, svc); aerr != nil {
		h.log.Warn("ecs: failed to persist service after task transition",
			zap.String("cluster", clusterName),
			zap.String("service", serviceName),
			zap.String("error", aerr.Message))
	}
}

// scheduleRecoveryCheck re-settles a service whose tasks are all running but
// whose deployment is still waiting out deploymentRecoveryWindow after a
// failure. Nothing else would look again: the tasks are placed, so no
// replacement is pending, and without this the recovery — and the steady-state
// event announcing it — would only ever surface on a read.
func (h *Handler) scheduleRecoveryCheck(ctx context.Context, clusterName string, svc *ecsService) {
	d := primaryDeployment(svc)
	if d == nil || d.FailedTasks == 0 || d.RolloutState != rolloutInProgress {
		return
	}
	if svc.RunningCount < d.DesiredCount {
		return
	}
	serviceName := svc.ServiceName
	h.scheduler.AfterScoped(h.store.region(ctx), serviceName, "recover", deploymentRecoveryWindow,
		func(bgCtx context.Context) {
			h.settleService(bgCtx, clusterName, serviceName)
		})
}

// circuitBreakerThreshold is the number of consecutive task launch failures
// that trips the deployment circuit breaker: half the desired count, clamped to
// [3, 200], as AWS documents the calculation.
func circuitBreakerThreshold(desired int) int {
	t := desired / 2
	if t < 3 {
		return 3
	}
	if t > 200 {
		return 200
	}
	return t
}

// scaleUp places n new tasks for a service, recording each outcome the way AWS
// does: a service event, the primary deployment's consecutive failure count,
// and — only when the service asked for a circuit breaker — a FAILED rollout.
func (h *Handler) scaleUp(ctx context.Context, clusterName string, svc *ecsService, n int) {
	family, revision, hasRevision := parseTaskDefRef(svc.TaskDefinition)
	var td *TaskDefinition
	var aerr *protocol.AWSError
	if hasRevision {
		td, aerr = h.store.getTaskDefinition(ctx, family, revision)
	} else {
		td, aerr = h.store.getLatestTaskDefinition(ctx, family)
	}
	if aerr != nil || td == nil {
		return
	}

	// A deployment the circuit breaker has failed launches no further tasks.
	if d := primaryDeployment(svc); d != nil && d.RolloutState == rolloutFailed {
		return
	}

	placement, placementErr := h.resolveAwsvpcPlacement(ctx, svc.NetworkConfiguration, "awsvpc services")
	if placementErr != nil {
		h.recordPlacementFailure(svc, placementErr.Message, n)
		return
	}

	startedBy := ""
	if d := primaryDeployment(svc); d != nil {
		startedBy = d.ID
	}

	started := 0
	for i := 0; i < n; i++ {
		task, aerr, startErr := h.launchTask(ctx, taskLaunchSpec{
			clusterName:     clusterName,
			clusterArn:      svc.ClusterArn,
			td:              td,
			launchType:      svc.LaunchType,
			platformVersion: svc.PlatformVersion,
			group:           serviceGroupPrefix + svc.ServiceName,
			startedBy:       startedBy,
			netCfg:          svc.NetworkConfiguration,
			placement:       placement,
			ordinal:         i,
		})
		switch {
		case aerr != nil:
			h.log.Warn("ecs: reconcile: failed to persist new task",
				zap.String("cluster", clusterName),
				zap.String("service", svc.ServiceName),
				zap.String("error", aerr.Message))
			h.recordPlacementFailure(svc, aerr.Message, 1)
		case startErr != nil:
			h.recordPlacementFailure(svc, task.StoppedReason, 1)
		default:
			h.registerTaskTargets(ctx, svc, td, task)
			started++
		}
	}

	if started > 0 {
		// Only the event: the consecutive-failure count is cleared at steady
		// state, not here. A task merely being placed proves nothing — a
		// crash-looping service places one on every cycle, and clearing the
		// count here is what stopped the circuit breaker ever tripping.
		h.addServiceEvent(svc, fmt.Sprintf("(service %s) has started %d tasks.", svc.ServiceName, started))
	}
}

// taskTargetAddress is the address a load balancer reaches a task on: the
// private IP of its awsvpc ENI, which is what ECS registers for an ip-type
// target group and what DescribeTasks reports.
func taskTargetAddress(task *Task) string {
	for _, a := range task.Attachments {
		if a.Type != "ElasticNetworkInterface" {
			continue
		}
		for _, d := range a.Details {
			if d.Name == "privateIPv4Address" && d.Value != "" {
				return d.Value
			}
		}
	}
	return ""
}

// registerTaskTargets adds a task to the target groups its service is attached
// to, so the load balancer starts forwarding to it. The port comes from the
// service's loadBalancers entry, as on ECS, falling back to the container's
// first port mapping when the entry omits it.
func (h *Handler) registerTaskTargets(ctx context.Context, svc *ecsService, td *TaskDefinition, task *Task) {
	if h.targets == nil || len(svc.LoadBalancers) == 0 {
		return
	}
	address := taskTargetAddress(task)
	if address == "" {
		return
	}
	for _, lb := range svc.LoadBalancers {
		if lb.TargetGroupArn == "" {
			continue
		}
		port := lb.ContainerPort
		if port == 0 {
			port = firstContainerPort(td, lb.ContainerName)
		}
		if port == 0 {
			continue
		}
		if err := h.targets.RegisterTarget(ctx, lb.TargetGroupArn, address, port); err != nil {
			h.log.Warn("ecs: could not register task with target group",
				zap.String("service", svc.ServiceName),
				zap.String("target_group", lb.TargetGroupArn),
				zap.String("address", address), zap.Error(err))
		}
	}
}

// deregisterTaskTargets removes a stopped task from its service's target
// groups, so the load balancer stops forwarding to an address that is gone.
func (h *Handler) deregisterTaskTargets(ctx context.Context, svc *ecsService, task *Task) {
	if h.targets == nil || len(svc.LoadBalancers) == 0 {
		return
	}
	address := taskTargetAddress(task)
	if address == "" {
		return
	}
	for _, lb := range svc.LoadBalancers {
		if lb.TargetGroupArn == "" {
			continue
		}
		if err := h.targets.DeregisterTarget(ctx, lb.TargetGroupArn, address); err != nil {
			h.log.Warn("ecs: could not deregister task from target group",
				zap.String("service", svc.ServiceName),
				zap.String("target_group", lb.TargetGroupArn),
				zap.String("address", address), zap.Error(err))
		}
	}
}

// firstContainerPort returns the first mapped port of the named container, or
// of the first container when the name is empty or unmatched.
func firstContainerPort(td *TaskDefinition, containerName string) int {
	if td == nil {
		return 0
	}
	for _, cd := range td.ContainerDefinitions {
		if containerName != "" && cd.Name != containerName {
			continue
		}
		if len(cd.PortMappings) > 0 {
			return cd.PortMappings[0].ContainerPort
		}
	}
	return 0
}

// consistentFailureThreshold is how many consecutive failures a deployment
// takes before AWS says the service cannot consistently start tasks. AWS does
// not document the count; three is where its own circuit breaker threshold
// bottoms out, so the two agree at the low end.
const consistentFailureThreshold = 3

// recordDeploymentFailure counts n tasks of the primary deployment that failed
// and reports the count the way AWS does: the "unable to consistently start
// tasks" event once as the count crosses the threshold rather than once per
// failure, and — only when the service asked for a circuit breaker — a FAILED
// rollout once the count reaches the breaker's threshold.
//
// Both placement failures and tasks that die on their own come through here,
// because AWS's failedTasks counts a task that never reached a running state
// and one that did not stay there alike.
func (h *Handler) recordDeploymentFailure(svc *ecsService, n int) {
	d := primaryDeployment(svc)
	if d == nil || n <= 0 {
		return
	}
	before := d.FailedTasks
	d.FailedTasks += n

	if before < consistentFailureThreshold && d.FailedTasks >= consistentFailureThreshold {
		h.addServiceEvent(svc, fmt.Sprintf(
			"(service %s) is unable to consistently start tasks successfully. For more information, see the Troubleshooting section.",
			svc.ServiceName))
	}

	// Without a circuit breaker the deployment stays IN_PROGRESS and the
	// scheduler keeps retrying, which is what AWS does — the failure is
	// reported through service events alone.
	if !usesECSController(svc) || svc.DeploymentConfiguration == nil ||
		svc.DeploymentConfiguration.DeploymentCircuitBreaker == nil ||
		!svc.DeploymentConfiguration.DeploymentCircuitBreaker.Enable {
		return
	}
	// FAILED is terminal, so the breaker announces itself once.
	if d.RolloutState == rolloutFailed || d.FailedTasks < circuitBreakerThreshold(d.DesiredCount) {
		return
	}
	d.RolloutState = rolloutFailed
	d.RolloutStateReason = "ECS deployment circuit breaker: task failed to start."
	h.addServiceEvent(svc, fmt.Sprintf("(service %s) (deployment %s) deployment failed: tasks failed to start.",
		svc.ServiceName, d.ID))
}

// recordPlacementFailure records n tasks the scheduler could not place.
func (h *Handler) recordPlacementFailure(svc *ecsService, reason string, n int) {
	h.addServiceEvent(svc, fmt.Sprintf("(service %s) was unable to place a task. Reason: %s", svc.ServiceName, reason))
	h.recordDeploymentFailure(svc, n)
}

// recordTaskStopFailure records a task of the service whose containers exited
// without the scheduler or the caller asking them to — a crash-looping
// container is exactly this, repeated. It counts against the deployment that
// placed the task; one belonging to a superseded deployment is draining, not
// failing.
func (h *Handler) recordTaskStopFailure(svc *ecsService, task *Task) {
	d := primaryDeployment(svc)
	if d == nil || (task.StartedBy != "" && task.StartedBy != d.ID) {
		return
	}
	h.recordDeploymentFailure(svc, 1)
}

// deploymentRecoveryWindow is how long a replacement task must stay RUNNING
// before a deployment that has already failed tasks counts as recovered.
//
// It applies only after a failure. A deployment that has not failed anything
// reaches its steady state the moment its tasks run, as it does today — the
// window is not an artificial delay on the healthy path. Once tasks have
// started dying, though, "a task is RUNNING right now" stops being evidence of
// anything: a container that exits on startup is RUNNING for a moment on every
// replacement, and without a window each of those moments looks like a
// recovery. AWS applies the same idea over minutes; the emulator's job is only
// to outlast the crash loop's own cycle.
const deploymentRecoveryWindow = 10 * time.Second

// Bounds on how fast a service replaces tasks that keep dying.
const (
	// replacementChurnWindow is how far back a stopped task still counts as
	// churn when deciding how long to wait before replacing the next one.
	replacementChurnWindow = 5 * time.Minute
	replacementMinDelay    = 500 * time.Millisecond
	replacementMaxDelay    = 30 * time.Second
)

// replacementDelay backs off as a service's tasks keep stopping, so a container
// that exits the moment it starts produces a crash loop that slows down instead
// of a hot loop. AWS throttles the same way, and reports it with the "unable to
// consistently start tasks successfully" event.
func replacementDelay(recentStops int) time.Duration {
	d := replacementMinDelay
	for i := 1; i < recentStops && d < replacementMaxDelay; i++ {
		d *= 2
	}
	if d > replacementMaxDelay {
		return replacementMaxDelay
	}
	return d
}

// scheduleServiceReplacement re-runs a service's reconcile after one of its
// tasks stopped, which is what places the replacement. The delay grows with how
// many of the service's tasks have stopped recently; the schedule is scoped to
// the service, so several tasks dying together collapse into one pass.
func (h *Handler) scheduleServiceReplacement(ctx context.Context, region, clusterName, serviceName string) {
	recent := 0
	if tasks, aerr := h.store.listTasks(ctx, clusterName); aerr == nil {
		cutoff := h.clk.Now().Add(-replacementChurnWindow).Unix()
		group := serviceGroupPrefix + serviceName
		for _, t := range tasks {
			if t.Group == group && t.StoppedAt != nil && *t.StoppedAt >= cutoff {
				recent++
			}
		}
	}
	h.scheduler.AfterScoped(region, serviceName, "replace", replacementDelay(recent), func(bgCtx context.Context) {
		h.reconcile(bgCtx, clusterName, serviceName)
	})
}

// reconcile adjusts task count to match a service's desired count.
func (h *Handler) reconcile(ctx context.Context, clusterName, serviceName string) {
	svc, aerr := h.store.getService(ctx, clusterName, serviceName)
	if aerr != nil || svc == nil {
		return
	}

	// List tasks belonging to this service.
	allTasks, aerr := h.store.listTasks(ctx, clusterName)
	if aerr != nil {
		return
	}

	serviceGroup := "service:" + serviceName
	activeTasks := make([]Task, 0)
	for _, t := range allTasks {
		if t.Group != serviceGroup {
			continue
		}
		if t.LastStatus != "STOPPED" {
			activeTasks = append(activeTasks, t)
		}
	}

	activeCount := len(activeTasks)
	desired := svc.DesiredCount

	// Scale up: place tasks to reach desired count.
	if activeCount < desired {
		h.scaleUp(ctx, clusterName, svc, desired-activeCount)
	}

	// Scale down: stop excess tasks.
	if activeCount > desired {
		excess := activeCount - desired
		stopped := 0
		for i := len(activeTasks) - 1; i >= 0 && stopped < excess; i-- {
			t := activeTasks[i]
			taskID := extractTaskID(t.TaskArn)

			// Cancel pending scheduler transitions.
			h.scheduler.CancelScoped(h.store.region(ctx), taskID, "pending")

			// Take the task out of rotation before stopping it, so the load
			// balancer is not still forwarding to a container being torn down.
			h.deregisterTaskTargets(ctx, svc, &t)

			// Record the stop *before* touching the containers. Killing them
			// raises a Docker die event, and the exit notifier handling it
			// races this write: if it wins, a task the scheduler stopped on
			// purpose is reported as one that died on its own — the wrong stop
			// code, and a failure counted against the deployment.
			t.LastStatus = "STOPPED"
			t.DesiredStatus = "STOPPED"
			t.StoppedReason = "Service scaling adjustment"
			t.StopCode = "ServiceSchedulerInitiated"
			stoppedAt := h.clk.Now().Unix()
			t.StoppedAt = &stoppedAt
			for j := range t.Containers {
				t.Containers[j].LastStatus = "STOPPED"
			}
			if aerr := h.store.putTask(ctx, &t); aerr != nil {
				h.log.Warn("ecs: reconcile: failed to persist stopped task",
					zap.String("cluster", clusterName),
					zap.String("service", serviceName),
					zap.String("task", taskID),
					zap.String("error", aerr.Message))
				continue
			}

			// Stop Docker containers if available.
			if h.dockerReady.Load() {
				for _, c := range t.Containers {
					if c.DockerID == "" {
						continue
					}
					if err := h.docker.StopContainer(ctx, c.DockerID, 5); err != nil {
						h.log.Warn("ecs: reconcile: failed to stop container",
							zap.String("container", c.DockerID), zap.Error(err))
					}
					if !h.cfg.ECSKeepContainers {
						if err := h.docker.RemoveContainerForce(c.DockerID); err != nil {
							h.log.Warn("ecs: reconcile: failed to remove container",
								zap.String("container", c.DockerID), zap.Error(err))
						}
					}
				}
			}
			stopped++
		}

		if stopped > 0 {
			h.addServiceEvent(svc, fmt.Sprintf("(service %s) has stopped %d tasks.", serviceName, stopped))
		}
	}

	// Recount from store and update service.
	if h.refreshServiceCounts(ctx, clusterName, svc) {
		h.addServiceEvent(svc, fmt.Sprintf("(service %s) has reached a steady state.", serviceName))
	}
	if aerr := h.store.putService(ctx, clusterName, svc); aerr != nil {
		h.log.Warn("ecs: reconcile: failed to persist service counts",
			zap.String("cluster", clusterName),
			zap.String("service", serviceName),
			zap.String("error", aerr.Message))
	}
}
