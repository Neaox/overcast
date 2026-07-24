package ecs

// handler_tasksets.go — CreateTaskSet, UpdateTaskSet, DeleteTaskSet,
// DescribeTaskSets, UpdateServicePrimaryTaskSet handlers.
//
// Task sets provide CODE_DEPLOY and EXTERNAL controller support for blue/green
// deployments. Scaling is metadata-only; no actual traffic shifting occurs.

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/protocol"
)

// taskSetARN builds an ECS task set ARN.
func (h *Handler) taskSetARN(ctx context.Context, cluster, service, id string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:task-set/%s/%s/%s", h.region(ctx), h.cfg.AccountID, cluster, service, id)
}

// extractTaskSetID extracts the task set short ID from an ARN or returns the input as-is.
func extractTaskSetID(input string) string {
	if strings.HasPrefix(input, "arn:") {
		parts := strings.Split(input, "/")
		if len(parts) >= 2 {
			return parts[len(parts)-1]
		}
	}
	return input
}

// computeDesiredCount calculates tasks from a percentage scale.
func computeDesiredCount(scale Scale, serviceDesired int) int {
	if scale.Unit != "PERCENT" {
		return serviceDesired
	}
	return int(math.Round(scale.Value / 100.0 * float64(serviceDesired)))
}

// CreateTaskSet handles AmazonEC2ContainerServiceV20141113.CreateTaskSet.
func (h *Handler) CreateTaskSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster              string                `json:"cluster"`
		Service              string                `json:"service"`
		TaskDefinition       string                `json:"taskDefinition"`
		LaunchType           string                `json:"launchType"`
		PlatformVersion      string                `json:"platformVersion"`
		NetworkConfiguration *NetworkConfiguration `json:"networkConfiguration"`
		Scale                *Scale                `json:"scale"`
		ExternalId           string                `json:"externalId"`
		Tags                 []Tag                 `json:"tags"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}

	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)

	// Validate cluster and service exist.
	cluster, aerr := h.store.getCluster(r.Context(), clusterName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	svc, aerr := h.store.getService(r.Context(), clusterName, serviceName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Task sets require CODE_DEPLOY or EXTERNAL controller.
	if svc.DeploymentController == nil || svc.DeploymentController.Type == "ECS" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Task sets can only be created for services using the CODE_DEPLOY or EXTERNAL deployment controller.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Resolve task definition.
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

	scale := Scale{Unit: "PERCENT", Value: 100}
	if req.Scale != nil {
		scale = *req.Scale
	}

	platformVersion := req.PlatformVersion
	if platformVersion == "" {
		platformVersion = "LATEST"
	}

	now := h.clk.Now().Unix()
	tsID := uuid.New().String()

	ts := &TaskSet{
		Id:                   tsID,
		TaskSetArn:           h.taskSetARN(r.Context(), clusterName, serviceName, tsID),
		ServiceArn:           svc.ServiceArn,
		ClusterArn:           cluster.ClusterArn,
		TaskDefinition:       td.TaskDefinitionArn,
		Status:               "ACTIVE",
		LaunchType:           req.LaunchType,
		PlatformVersion:      platformVersion,
		NetworkConfiguration: req.NetworkConfiguration,
		Scale:                scale,
		StabilityStatus:      "STABILIZING",
		CreatedAt:            now,
		UpdatedAt:            now,
		ExternalId:           req.ExternalId,
		Tags:                 req.Tags,
		ComputedDesiredCount: computeDesiredCount(scale, svc.DesiredCount),
	}

	if aerr := h.store.putTaskSet(r.Context(), clusterName, serviceName, ts); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Register task set ARN on the service.
	svc.TaskSets = append(svc.TaskSets, ts.TaskSetArn)
	_ = h.store.putService(r.Context(), clusterName, svc)

	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"taskSet": ts}, "application/x-amz-json-1.1")
}

// UpdateTaskSet handles AmazonEC2ContainerServiceV20141113.UpdateTaskSet.
func (h *Handler) UpdateTaskSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
		Service string `json:"service"`
		TaskSet string `json:"taskSet"`
		Scale   Scale  `json:"scale"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}

	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)
	taskSetID := extractTaskSetID(req.TaskSet)

	svc, aerr := h.store.getService(r.Context(), clusterName, serviceName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	ts, aerr := h.store.getTaskSet(r.Context(), clusterName, serviceName, taskSetID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if ts == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    fmt.Sprintf("The task set %s was not found.", req.TaskSet),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	ts.Scale = req.Scale
	ts.ComputedDesiredCount = computeDesiredCount(req.Scale, svc.DesiredCount)
	ts.UpdatedAt = h.clk.Now().Unix()

	if aerr := h.store.putTaskSet(r.Context(), clusterName, serviceName, ts); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"taskSet": ts}, "application/x-amz-json-1.1")
}

// DeleteTaskSet handles AmazonEC2ContainerServiceV20141113.DeleteTaskSet.
func (h *Handler) DeleteTaskSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
		Service string `json:"service"`
		TaskSet string `json:"taskSet"`
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
	taskSetID := extractTaskSetID(req.TaskSet)

	svc, aerr := h.store.getService(r.Context(), clusterName, serviceName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	ts, aerr := h.store.getTaskSet(r.Context(), clusterName, serviceName, taskSetID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if ts == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    fmt.Sprintf("The task set %s was not found.", req.TaskSet),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	if aerr := h.store.deleteTaskSet(r.Context(), clusterName, serviceName, taskSetID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Remove ARN from service task set list.
	updated := svc.TaskSets[:0]
	for _, arn := range svc.TaskSets {
		if extractTaskSetID(arn) != taskSetID {
			updated = append(updated, arn)
		}
	}
	svc.TaskSets = updated
	_ = h.store.putService(r.Context(), clusterName, svc)

	ts.Status = "DRAINING"
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"taskSet": ts}, "application/x-amz-json-1.1")
}

// DescribeTaskSets handles AmazonEC2ContainerServiceV20141113.DescribeTaskSets.
func (h *Handler) DescribeTaskSets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster  string   `json:"cluster"`
		Service  string   `json:"service"`
		TaskSets []string `json:"taskSets"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}

	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)

	type failure struct {
		Arn    string `json:"arn"`
		Reason string `json:"reason"`
	}

	var result []TaskSet
	var failures []failure

	if len(req.TaskSets) == 0 {
		// Return all task sets for the service.
		all, aerr := h.store.listTaskSets(r.Context(), clusterName, serviceName)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		result = all
		failures = []failure{}
	} else {
		result = make([]TaskSet, 0, len(req.TaskSets))
		failures = make([]failure, 0)
		for _, ref := range req.TaskSets {
			tsID := extractTaskSetID(ref)
			ts, aerr := h.store.getTaskSet(r.Context(), clusterName, serviceName, tsID)
			if aerr != nil {
				protocol.WriteJSONError(w, r, aerr)
				return
			}
			if ts == nil {
				arn := ref
				if !strings.HasPrefix(arn, "arn:") {
					arn = h.taskSetARN(r.Context(), clusterName, serviceName, tsID)
				}
				failures = append(failures, failure{Arn: arn, Reason: "MISSING"})
				continue
			}
			result = append(result, *ts)
		}
	}

	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{
		"taskSets": result,
		"failures": failures,
	}, "application/x-amz-json-1.1")
}

// UpdateServicePrimaryTaskSet handles AmazonEC2ContainerServiceV20141113.UpdateServicePrimaryTaskSet.
func (h *Handler) UpdateServicePrimaryTaskSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster        string `json:"cluster"`
		Service        string `json:"service"`
		PrimaryTaskSet string `json:"primaryTaskSet"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Cluster == "" {
		req.Cluster = "default"
	}

	clusterName := extractClusterName(req.Cluster)
	serviceName := extractServiceName(req.Service)
	primaryID := extractTaskSetID(req.PrimaryTaskSet)

	// Verify the target task set exists.
	primary, aerr := h.store.getTaskSet(r.Context(), clusterName, serviceName, primaryID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if primary == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    fmt.Sprintf("The task set %s was not found.", req.PrimaryTaskSet),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Demote all other task sets to ACTIVE; promote the named one to PRIMARY.
	all, aerr := h.store.listTaskSets(r.Context(), clusterName, serviceName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	now := h.clk.Now().Unix()
	for i := range all {
		if all[i].Id == primaryID {
			all[i].Status = "PRIMARY"
		} else if all[i].Status == "PRIMARY" {
			all[i].Status = "ACTIVE"
		}
		all[i].UpdatedAt = now
		_ = h.store.putTaskSet(r.Context(), clusterName, serviceName, &all[i])
	}

	// Re-read the updated primary.
	primary, _ = h.store.getTaskSet(r.Context(), clusterName, serviceName, primaryID)
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"taskSet": primary}, "application/x-amz-json-1.1")
}
