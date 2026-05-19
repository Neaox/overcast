package ecs

// handler_services.go — CreateService, UpdateService, DeleteService,
// DescribeServices, ListServices handlers and the service reconciler.

import (
	"context"
	"fmt"
	"net/http"
	"strings"

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
		CapacityProviderStrategy []CapacityProviderStrategyItem `json:"capacityProviderStrategy"`
		PlatformVersion          string                         `json:"platformVersion"`
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

	// Fargate requires networkConfiguration.
	if req.LaunchType == "FARGATE" && req.NetworkConfiguration == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Network Configuration must be provided when networkMode is 'awsvpc'.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	// Validate VPC launchability early before expensive lookups (cluster/task-def).
	if _, _, _, _, placementErr := h.resolveAwsvpcPlacement(r.Context(), req.NetworkConfiguration, "awsvpc services"); placementErr != nil {
		protocol.WriteJSONError(w, r, placementErr)
		return
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
		CapacityProviderStrategy: req.CapacityProviderStrategy,
		PlatformVersion:          platformVersion,
		Events:                   make([]ServiceEvent, 0),
		Deployments: []Deployment{{
			ID:                   uuid.New().String(),
			Status:               "PRIMARY",
			TaskDefinition:       td.TaskDefinitionArn,
			DesiredCount:         desired,
			RunningCount:         0,
			PendingCount:         0,
			CreatedAt:            now,
			UpdatedAt:            now,
			NetworkConfiguration: req.NetworkConfiguration,
			PlatformVersion:      platformVersion,
		}},
	}
	h.addServiceEvent(svc, fmt.Sprintf("(service %s) has reached a steady state.", req.ServiceName))

	if aerr := h.store.putService(r.Context(), clusterName, svc); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Reconcile: start tasks to match desired count.
	h.reconcile(r.Context(), clusterName, req.ServiceName)

	// Re-read service with updated counts.
	svc, _ = h.store.getService(r.Context(), clusterName, req.ServiceName)

	h.publish(r, events.ECSServiceCreated, events.ResourcePayload{Name: req.ServiceName})
	writeJSON(w, r, http.StatusOK, map[string]any{"service": svc})
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
			if _, _, _, _, placementErr := h.resolveAwsvpcPlacement(r.Context(), newNetCfg, "awsvpc services"); placementErr != nil {
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
	writeJSON(w, r, http.StatusOK, map[string]any{"service": svc})
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
	writeJSON(w, r, http.StatusOK, map[string]any{"service": svc})
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

	writeJSON(w, r, http.StatusOK, map[string]any{
		"services": found,
		"failures": failures,
	})
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

	writeJSON(w, r, http.StatusOK, map[string]any{"serviceArns": arns})
}

// refreshServiceCounts recounts running/pending tasks for a service from the store.
func (h *Handler) refreshServiceCounts(ctx context.Context, clusterName string, svc *ecsService) {
	tasks, aerr := h.store.listTasks(ctx, clusterName)
	if aerr != nil {
		return
	}
	running, pending := 0, 0
	serviceGroup := "service:" + svc.ServiceName
	for _, t := range tasks {
		if t.Group != serviceGroup {
			continue
		}
		switch t.LastStatus {
		case "RUNNING":
			running++
		case "PROVISIONING", "PENDING":
			pending++
		}
	}
	svc.RunningCount = running
	svc.PendingCount = pending
	for i := range svc.Deployments {
		if svc.Deployments[i].Status == "PRIMARY" {
			svc.Deployments[i].RunningCount = running
			svc.Deployments[i].PendingCount = pending
		}
	}
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

	// Scale up: create tasks to reach desired count.
	if activeCount < desired {
		deficit := desired - activeCount

		// Resolve task definition.
		family, revision, hasRevision := parseTaskDefRef(svc.TaskDefinition)
		var td *TaskDefinition
		if hasRevision {
			td, aerr = h.store.getTaskDefinition(ctx, family, revision)
		} else {
			td, aerr = h.store.getLatestTaskDefinition(ctx, family)
		}
		if aerr != nil || td == nil {
			return
		}

		now := h.clk.Now().Unix()
		for i := 0; i < deficit; i++ {
			taskID := uuid.New().String()
			taskArn := h.taskARN(ctx, clusterName, taskID)

			containers := make([]Container, 0, len(td.ContainerDefinitions))
			for _, cd := range td.ContainerDefinitions {
				containers = append(containers, Container{
					ContainerArn: h.containerARN(ctx, uuid.New().String()),
					Name:         cd.Name,
					Image:        cd.Image,
					LastStatus:   "PENDING",
				})
			}

			task := Task{
				TaskArn:           taskArn,
				TaskDefinitionArn: td.TaskDefinitionArn,
				ClusterArn:        svc.ClusterArn,
				LastStatus:        "PROVISIONING",
				DesiredStatus:     "RUNNING",
				LaunchType:        svc.LaunchType,
				Cpu:               td.Cpu,
				Memory:            td.Memory,
				CreatedAt:         now,
				Group:             serviceGroup,
				Containers:        containers,
			}
			if aerr := h.store.putTask(ctx, &task); aerr != nil {
				h.log.Warn("ecs: reconcile: failed to persist new task",
					zap.String("cluster", clusterName),
					zap.String("service", serviceName),
					zap.String("task", taskID),
					zap.String("error", aerr.Message))
				h.addServiceEvent(svc, fmt.Sprintf("(service %s) failed to start task: %s.", serviceName, aerr.Message))
				continue
			}

			// Schedule PROVISIONING → RUNNING transition.
			h.scheduleMetadataTransition(clusterName, taskID)
		}

		h.addServiceEvent(svc, fmt.Sprintf("(service %s) has started %d tasks.", serviceName, deficit))
	}

	// Scale down: stop excess tasks.
	if activeCount > desired {
		excess := activeCount - desired
		stopped := 0
		for i := len(activeTasks) - 1; i >= 0 && stopped < excess; i-- {
			t := activeTasks[i]
			taskID := extractTaskID(t.TaskArn)

			// Cancel pending scheduler transitions.
			h.scheduler.Cancel(taskID + ":pending")

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

			t.LastStatus = "STOPPED"
			t.DesiredStatus = "STOPPED"
			t.StoppedReason = "Service scaling adjustment"
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
			stopped++
		}

		if stopped > 0 {
			h.addServiceEvent(svc, fmt.Sprintf("(service %s) has stopped %d tasks.", serviceName, stopped))
		}
	}

	// Recount from store and update service.
	h.refreshServiceCounts(ctx, clusterName, svc)
	if aerr := h.store.putService(ctx, clusterName, svc); aerr != nil {
		h.log.Warn("ecs: reconcile: failed to persist service counts",
			zap.String("cluster", clusterName),
			zap.String("service", serviceName),
			zap.String("error", aerr.Message))
	}
}
