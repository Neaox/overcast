package ecs

// handler_services.go — CreateService, UpdateService, DeleteService,
// DescribeServices, ListServices handlers and the service reconciler.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
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
	h.addServiceEventAt(svc, msg, h.clk.Now())
}

func (h *Handler) addServiceEventAt(svc *ecsService, msg string, occurredAt time.Time) {
	if occurredAt.IsZero() {
		occurredAt = h.clk.Now()
	}
	evt := ServiceEvent{
		ID:        uuid.New().String(),
		CreatedAt: float64(occurredAt.UnixMilli()) / 1000,
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
	var req updateServiceRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	svc, aerr := h.updateServiceRecord(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.publish(r, events.ECSServiceUpdated, events.ResourcePayload{Name: extractServiceName(req.Service)})
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"service": svc}, "application/x-amz-json-1.1")
}

// updateServiceRecord applies an UpdateService to the stored service and then
// drives the service to what it now describes, returning the record as it
// stands afterwards. Both wire paths come through here, so an update means the
// same thing whichever protocol asked for it.
func (h *Handler) updateServiceRecord(ctx context.Context, req *updateServiceRequest) (*ecsService, *protocol.AWSError) {
	if req.Cluster == "" {
		req.Cluster = "default"
	}
	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)

	edited, aerr := h.mutateService(ctx, clusterName, serviceName, func(svc *ecsService) *protocol.AWSError {
		return h.applyServiceUpdate(ctx, serviceName, svc, req)
	})
	if aerr != nil {
		return nil, aerr
	}

	// Reconcile: adjust task count. Outside the lock — reconcile takes it.
	h.reconcile(ctx, clusterName, serviceName)

	// Re-read with updated counts.
	if svc, _ := h.store.getService(ctx, clusterName, serviceName); svc != nil {
		return svc, nil
	}
	return edited, nil
}

// applyServiceUpdate edits a service record to match an UpdateService request:
// its desired count, its task definition — which starts a new deployment and
// demotes the one it replaces — its networking, and its platform version.
//
// It only edits. Placing and retiring tasks is the reconcile the caller runs
// once the record is written and the lock is released.
func (h *Handler) applyServiceUpdate(ctx context.Context, serviceName string, svc *ecsService, req *updateServiceRequest) *protocol.AWSError {
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

	// Updating the task definition or explicitly forcing a deployment creates
	// a new PRIMARY deployment. A forced deployment deliberately reuses the
	// service's current task definition: its purpose is to start fresh tasks so
	// launch-time inputs such as secrets and mutable image tags are read again.
	if req.TaskDefinition != "" || req.ForceNewDeployment {
		taskDefinition := req.TaskDefinition
		if taskDefinition == "" {
			taskDefinition = svc.TaskDefinition
		}
		family, revision, hasRevision := parseTaskDefRef(taskDefinition)
		var td *TaskDefinition
		var aerr *protocol.AWSError
		if hasRevision {
			td, aerr = h.store.getTaskDefinition(ctx, family, revision)
		} else {
			td, aerr = h.store.getLatestTaskDefinition(ctx, family)
		}
		if aerr != nil {
			return aerr
		}

		if td.TaskDefinitionArn != svc.TaskDefinition || req.ForceNewDeployment {
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
			if _, placementErr := h.resolveAwsvpcPlacement(ctx, newNetCfg, "awsvpc services"); placementErr != nil {
				return placementErr
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
			if req.ForceNewDeployment && req.TaskDefinition == "" {
				h.addServiceEvent(svc, fmt.Sprintf("(service %s) has begun a forced deployment.", serviceName))
			} else {
				h.addServiceEvent(svc, fmt.Sprintf("(service %s) was updated to use task definition %s.", serviceName, td.TaskDefinitionArn))
			}
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

	return nil
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

	svc, aerr := h.drainServiceRecord(r.Context(), req.Cluster, req.Service)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.publish(r, events.ECSServiceDeleted, events.ResourcePayload{Name: extractServiceName(req.Service)})
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"service": svc}, "application/x-amz-json-1.1")
}

// drainServiceRecord puts a service into DRAINING at a desired count of zero
// and reconciles it, which is what stops its tasks. Both wire paths come
// through here.
func (h *Handler) drainServiceRecord(ctx context.Context, cluster, service string) (*ecsService, *protocol.AWSError) {
	if cluster == "" {
		cluster = "default"
	}
	clusterName := extractClusterName(cluster)
	serviceName := extractServiceName(service)

	edited, aerr := h.mutateService(ctx, clusterName, serviceName, func(svc *ecsService) *protocol.AWSError {
		svc.Status = "DRAINING"
		svc.DesiredCount = 0
		for i := range svc.Deployments {
			if svc.Deployments[i].Status == "PRIMARY" {
				svc.Deployments[i].DesiredCount = 0
				svc.Deployments[i].UpdatedAt = h.clk.Now().Unix()
			}
		}
		h.addServiceEvent(svc, fmt.Sprintf("(service %s) is draining.", serviceName))
		return nil
	})
	if aerr != nil {
		return nil, aerr
	}

	// Reconcile: stop all tasks. Outside the lock — reconcile takes it.
	h.reconcile(ctx, clusterName, serviceName)

	// Re-read with updated counts.
	if svc, _ := h.store.getService(ctx, clusterName, serviceName); svc != nil {
		return svc, nil
	}
	return edited, nil
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

	// Headers must be set before the body is written; late mutation only
	// reaches the wire while DrainBody happens to buffer the response.
	docker.SetBackingHeaders(w, h.dockerReady.Load(), docker.ContainerHealthUnknown)
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

// lockService serialises the read-modify-write of one service's record,
// returning the function that releases it. A service is stored as one JSON
// blob, so every writer reads the whole record, edits it and writes the whole
// record back: two of them overlapping means the second silently discards the
// first's edit.
//
// That is not hypothetical here. A crash loop drives both writers at once — the
// scheduler reconciling the service while the Docker event stream reports the
// container that just died — and the edit that gets discarded is the failure
// count, which is the one thing a crash loop is supposed to make visible.
//
// Held across a whole reconcile, container launches included, because the
// placement decisions are derived from the reads: releasing between the read
// and the write is the race. Two reconciles of one service overlapping would
// also both see the same shortfall and each place a task for it.
func (h *Handler) lockService(ctx context.Context, clusterName, serviceName string) func() {
	key := h.store.region(ctx) + "/" + clusterName + "/" + serviceName
	h.serviceLocksMu.Lock()
	if h.serviceLocks == nil {
		h.serviceLocks = make(map[string]*sync.Mutex)
	}
	mu, ok := h.serviceLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		h.serviceLocks[key] = mu
	}
	h.serviceLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// mutateService applies edit to a service's stored record under lockService,
// returning the record it wrote. The read, the edit and the write are one step,
// so a writer that read the record earlier — a reconcile on a scheduler
// goroutine, the Docker exit notifier — cannot land its own copy in between and
// discard the edit.
//
// An API call is as much a writer as they are, and the edit it loses that way
// is the caller's instruction. A scale-down overwritten by a reconcile that was
// already running leaves the service stored at the count it had, so the
// reconcile the caller then runs sees nothing to retire and the surplus tasks
// keep running — a scale-down that returned 200 and did nothing. Only the
// stored record shows it: DescribeServices recounts from the tasks on its own
// copy, so the service reports whatever it is actually running.
//
// edit returning an error leaves the record untouched. The lock is released
// before the caller reconciles, because reconcile takes it itself — lock
// ordering here is one service lock at a time, then task locks beneath it.
func (h *Handler) mutateService(ctx context.Context, clusterName, serviceName string, edit func(svc *ecsService) *protocol.AWSError) (*ecsService, *protocol.AWSError) {
	defer h.lockService(ctx, clusterName, serviceName)()

	svc, aerr := h.store.getService(ctx, clusterName, serviceName)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := edit(svc); aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.putService(ctx, clusterName, svc); aerr != nil {
		return nil, aerr
	}
	return svc, nil
}

// deploymentCounts is one deployment's share of a service's tasks. settled is
// the subset of running tasks that have been up for deploymentRecoveryWindow,
// which is what a deployment with failures behind it is judged on.
type deploymentCounts struct {
	running int
	pending int
	settled int
}

// refreshServiceCounts recounts running/pending tasks for a service from the
// store and derives the primary deployment's rollout state from them. It
// reports whether the deployment reached steady state on this call, which is
// the edge the steady-state service event is emitted on — by callers that
// persist the service, since this is also called on the read path.
//
// Counts are attributed per deployment, by the deployment that started each
// task. A deployment's own tasks are the only ones that say whether it has
// rolled out: crediting it with the tasks of the deployment it replaced makes
// a service that has not started anything new look like it reached steady
// state the instant the task definition changed.
func (h *Handler) refreshServiceCounts(ctx context.Context, clusterName string, svc *ecsService) bool {
	tasks, aerr := h.store.listTasks(ctx, clusterName)
	if aerr != nil {
		return false
	}
	now := h.clk.Now().Unix()
	recoverAfter := int64(deploymentRecoveryWindow / time.Second)
	running, pending := 0, 0
	byDeployment := make(map[string]*deploymentCounts, len(svc.Deployments))
	serviceGroup := serviceGroupPrefix + svc.ServiceName
	for _, t := range tasks {
		if t.Group != serviceGroup {
			continue
		}
		c := byDeployment[t.StartedBy]
		if c == nil {
			c = &deploymentCounts{}
			byDeployment[t.StartedBy] = c
		}
		switch t.LastStatus {
		case "RUNNING":
			running++
			c.running++
			if t.StartedAt == nil || now-*t.StartedAt >= recoverAfter {
				c.settled++
			}
		case "PROVISIONING", "PENDING":
			pending++
			c.pending++
		}
	}
	// The service's own counts span every deployment, as AWS reports them:
	// during a rollout it is running the new tasks and the old ones together.
	svc.RunningCount = running
	svc.PendingCount = pending
	for i := range svc.Deployments {
		c := byDeployment[svc.Deployments[i].ID]
		if c == nil {
			c = &deploymentCounts{}
		}
		svc.Deployments[i].RunningCount = c.running
		svc.Deployments[i].PendingCount = c.pending
	}

	d := primaryDeployment(svc)
	if d == nil {
		return false
	}
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
	// One that has failed nothing settles the moment its tasks run. Either way
	// the deployment is credited only with its own tasks: the ones it inherited
	// from the deployment it replaced say nothing about whether it rolled out.
	credited := d.RunningCount
	if d.FailedTasks > 0 {
		credited = 0
		if c := byDeployment[d.ID]; c != nil {
			credited = c.settled
		}
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

// scheduleRecoveryCheck re-reconciles a service whose current deployment has
// placed all of its tasks but is still waiting out deploymentRecoveryWindow
// after a failure. Nothing else would look again: the tasks are placed, so no
// replacement is pending, and without this the recovery — and the steady-state
// event announcing it — would only ever surface on a read.
//
// The deployment is judged on its own running count, not the service's: during
// a rollout the service is also running the tasks of the deployment being
// replaced, and those are not what has to prove it stays up.
func (h *Handler) scheduleRecoveryCheck(ctx context.Context, clusterName string, svc *ecsService) {
	d := primaryDeployment(svc)
	if d == nil || d.FailedTasks == 0 || d.RolloutState != rolloutInProgress {
		return
	}
	if d.RunningCount < d.DesiredCount {
		return
	}
	serviceName := svc.ServiceName
	h.scheduler.AfterScoped(h.store.region(ctx), serviceName, "recover", deploymentRecoveryWindow,
		func(bgCtx context.Context) {
			h.reconcile(bgCtx, clusterName, serviceName)
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
	log := h.log.WithRecorder(ctx)
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

	started, failed := 0, 0
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
			log.Warn("ecs: reconcile: failed to persist new task",
				zap.String("cluster", clusterName),
				zap.String("service", svc.ServiceName),
				zap.String("error", aerr.Message))
			h.recordPlacementFailure(svc, aerr.Message, 1)
			failed++
		case startErr != nil:
			h.recordPlacementFailure(svc, task.StoppedReason, 1)
			failed++
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

	// A task whose containers never started is retried, on the same backoff as
	// one that started and then exited. The scheduler is what keeps trying on
	// ECS, and a service that stops trying after the first failed launch never
	// recovers from a transient one — nor does anything waiting on it ever see
	// the count move again.
	if failed > 0 {
		h.scheduleServiceReplacement(ctx, h.store.region(ctx), clusterName, svc.ServiceName)
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
	log := h.log.WithRecorder(ctx)
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
			log.Warn("ecs: could not register task with target group",
				zap.String("service", svc.ServiceName),
				zap.String("target_group", lb.TargetGroupArn),
				zap.String("address", address), zap.Error(err))
		}
	}
}

// deregisterTaskTargets removes a stopped task from its service's target
// groups, so the load balancer stops forwarding to an address that is gone.
func (h *Handler) deregisterTaskTargets(ctx context.Context, svc *ecsService, task *Task) {
	log := h.log.WithRecorder(ctx)
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
			log.Warn("ecs: could not deregister task from target group",
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
	h.recordDeploymentFailureAt(svc, n, h.clk.Now())
}

func (h *Handler) recordDeploymentFailureAt(svc *ecsService, n int, occurredAt time.Time) {
	d := primaryDeployment(svc)
	if d == nil || n <= 0 {
		return
	}
	before := d.FailedTasks
	d.FailedTasks += n
	// A deployment that is still trying says so through its timestamp; a frozen
	// updatedAt is how a service that has given up looks.
	if occurredAt.IsZero() {
		occurredAt = h.clk.Now()
	}
	d.UpdatedAt = occurredAt.Unix()

	if before < consistentFailureThreshold && d.FailedTasks >= consistentFailureThreshold {
		h.addServiceEventAt(svc, fmt.Sprintf(
			"(service %s) is unable to consistently start tasks successfully. For more information, see the Troubleshooting section.",
			svc.ServiceName), occurredAt)
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
	h.addServiceEventAt(svc, fmt.Sprintf("(service %s) (deployment %s) deployment failed: tasks failed to start.",
		svc.ServiceName, d.ID), occurredAt)
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
func (h *Handler) recordTaskStopFailureAt(svc *ecsService, task *Task, occurredAt time.Time) {
	d := primaryDeployment(svc)
	if d == nil || (task.StartedBy != "" && task.StartedBy != d.ID) {
		return
	}
	h.recordDeploymentFailureAt(svc, 1, occurredAt)
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

// What the service scheduler records on a task it stops itself. The stop code
// is AWS's; the two reasons are ours — AWS's exact wording for them has not
// been pinned against a captured response, and callers switch on the code.
const (
	stopReasonScaling        = "Service scaling adjustment"
	stopReasonSuperseded     = "Task stopped by a newer service deployment"
	stopCodeServiceScheduler = "ServiceSchedulerInitiated"
)

// stopServiceTasks stops tasks the service scheduler has decided to retire —
// scaled-down surplus or the tasks of a deployment that has been replaced —
// taking them out of load balancer rotation and recording the stop before
// tearing their containers down.
func (h *Handler) stopServiceTasks(ctx context.Context, clusterName string, svc *ecsService, tasks []Task, reason string) {
	stoppedCount := 0
	log := h.log.WithRecorder(ctx)
	for i := range tasks {
		t := tasks[i]
		taskID := extractTaskID(t.TaskArn)

		// Cancel pending scheduler transitions.
		h.scheduler.CancelScoped(h.store.region(ctx), taskID, "pending")

		// Take the task out of rotation before stopping it, so the load
		// balancer is not still forwarding to a container being torn down.
		h.deregisterTaskTargets(ctx, svc, &t)

		// Record the stop *before* touching the containers. Killing them
		// raises a Docker die event, and the exit notifier handling it races
		// this write: if it wins, a task the scheduler retired on purpose is
		// reported as one that died on its own — the wrong stop code, and a
		// failure counted against the deployment.
		//
		// The task is re-read under its own lock rather than written back from
		// the copy the reconcile listed: that copy is older than the cancel
		// above, so it can be a task whose transition to RUNNING is mid-flight.
		// See lockTask.
		stopped, changed, aerr := h.stopTaskRecord(ctx, clusterName, taskID, taskStop{
			reason: reason, code: stopCodeServiceScheduler,
		})
		if aerr != nil {
			log.Warn("ecs: reconcile: failed to persist stopped task",
				zap.String("cluster", clusterName),
				zap.String("service", svc.ServiceName),
				zap.String("task", taskID),
				zap.String("error", aerr.Message))
			continue
		}
		if stopped == nil {
			continue
		}

		h.retireTaskContainers(ctx, stopped)

		// Only what this pass actually retired is reported. A task that had
		// already stopped on its own is not the scheduler's doing.
		if changed {
			stoppedCount++
		}
	}
	if stoppedCount > 0 {
		h.addServiceEvent(svc, fmt.Sprintf("(service %s) has stopped %d tasks.", svc.ServiceName, stoppedCount))
	}
}

// retireTaskContainers tears down the Docker containers of a task the service
// scheduler has stopped: each one is stopped, its final output captured, and
// then it is removed.
//
// The capture sits between the stop and the remove, and that ordering is the
// point. Retention used to ride the Docker die event alone, which the worker
// pool runs asynchronously — so on this path it raced the RemoveContainerForce
// below and lost often enough to matter. The case it loses is the one that
// matters most: a CloudFormation rollback deleting a service is exactly where
// somebody goes looking for why the tasks would not start, and the miss
// surfaced as nothing more than a Debug line about a container Docker no longer
// had. Capturing before the stop would not do either — a container still gets
// to write while it shuts down, and its last words are usually the useful ones.
//
// The die event still fires and still captures; captureContainerLogs keeps the
// first success rather than letting the two orderings overwrite each other.
func (h *Handler) retireTaskContainers(ctx context.Context, task *Task) {
	if !h.dockerReady.Load() {
		return
	}
	log := h.log.WithRecorder(ctx)
	for _, c := range task.Containers {
		if c.DockerID == "" {
			continue
		}
		if err := h.docker.StopContainer(ctx, c.DockerID, 5); err != nil {
			log.Warn("ecs: reconcile: failed to stop container",
				zap.String("container", c.DockerID), zap.Error(err))
		}
		h.captureContainerLogs(ctx, task, c.DockerID)
		if !h.cfg.ECSKeepContainers {
			if err := h.docker.RemoveContainerForce(c.DockerID); err != nil {
				log.Warn("ecs: reconcile: failed to remove container",
					zap.String("container", c.DockerID), zap.Error(err))
			}
		}
	}
}

// reconcile drives a service toward the state its current deployment describes:
// that deployment's tasks placed up to the desired count, and the tasks of any
// deployment it superseded retired as the replacements come up.
//
// The deployment split is what makes an update a rollout rather than a rename.
// Treating every task in the service's group as interchangeable — as this did —
// means a service already at its desired count has nothing to do when the task
// definition changes, so the new deployment places nothing, the old containers
// keep serving, and the service reports steady state on a deployment that never
// happened.
func (h *Handler) reconcile(ctx context.Context, clusterName, serviceName string) {
	log := h.log.WithRecorder(ctx)
	defer h.lockService(ctx, clusterName, serviceName)()

	svc, aerr := h.store.getService(ctx, clusterName, serviceName)
	if aerr != nil || svc == nil {
		return
	}

	// List tasks belonging to this service.
	allTasks, aerr := h.store.listTasks(ctx, clusterName)
	if aerr != nil {
		return
	}

	primaryID := ""
	if d := primaryDeployment(svc); d != nil {
		primaryID = d.ID
	}

	serviceGroup := serviceGroupPrefix + serviceName
	current := make([]Task, 0, len(allTasks))    // the deployment being rolled out
	superseded := make([]Task, 0, len(allTasks)) // deployments it replaces
	runningCurrent := 0
	for _, t := range allTasks {
		if t.Group != serviceGroup || t.LastStatus == "STOPPED" {
			continue
		}
		if primaryID != "" && t.StartedBy != primaryID {
			superseded = append(superseded, t)
			continue
		}
		current = append(current, t)
		if t.LastStatus == "RUNNING" {
			runningCurrent++
		}
	}

	desired := svc.DesiredCount

	// Scale up: place tasks to reach desired count.
	if len(current) < desired {
		h.scaleUp(ctx, clusterName, svc, desired-len(current))
	}

	// Scale down: stop excess tasks.
	if len(current) > desired {
		h.stopServiceTasks(ctx, clusterName, svc, current[desired:], stopReasonScaling)
	}

	// Retire the superseded deployment's tasks as the new deployment takes
	// over, keeping enough of them to hold the desired count in the meantime.
	// A new deployment that never starts anything therefore leaves the old
	// tasks serving, which is what ECS does with a rollout that fails.
	if len(superseded) > 0 {
		retain := min(max(desired-runningCurrent, 0), len(superseded))
		h.stopServiceTasks(ctx, clusterName, svc, superseded[retain:], stopReasonSuperseded)
	}

	// Recount from store and update service.
	reachedSteadyState := h.refreshServiceCounts(ctx, clusterName, svc)
	retireDrainedDeployments(svc)
	if reachedSteadyState {
		h.addServiceEvent(svc, fmt.Sprintf("(service %s) has reached a steady state.", serviceName))
	}
	h.scheduleRecoveryCheck(ctx, clusterName, svc)
	if aerr := h.store.putService(ctx, clusterName, svc); aerr != nil {
		log.Warn("ecs: reconcile: failed to persist service counts",
			zap.String("cluster", clusterName),
			zap.String("service", serviceName),
			zap.String("error", aerr.Message))
	}
}

// retireDrainedDeployments drops the superseded deployments that have no tasks
// left. AWS reports a service mid-rollout with both deployments and settles
// back to one, which is how a caller waiting on a rollout — the `services-stable`
// waiter, CloudFormation — knows the old revision is gone. Counts must be
// refreshed first: this reads them.
func retireDrainedDeployments(svc *ecsService) {
	kept := svc.Deployments[:0]
	for _, d := range svc.Deployments {
		if d.Status != "PRIMARY" && d.RunningCount == 0 && d.PendingCount == 0 {
			continue
		}
		kept = append(kept, d)
	}
	svc.Deployments = kept
}
