package ecs

// handler_containerinstances.go — RegisterContainerInstance, DeregisterContainerInstance,
// ListContainerInstances, DescribeContainerInstances handlers.
//
// Container instances are metadata-only. No actual EC2 instance or ECS agent
// is started. Instances are stored per cluster and returned in list/describe calls.
// Deregistered instances are removed from the store (status set to INACTIVE in the response).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/protocol"
)

func (h *Handler) containerInstanceARN(ctx context.Context, cluster, id string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:container-instance/%s/%s", h.region(ctx), h.cfg.AccountID, cluster, id)
}

// extractContainerInstanceID extracts the instance UUID from an ARN or returns the input as-is.
func extractContainerInstanceID(input string) string {
	if strings.HasPrefix(input, "arn:") {
		parts := strings.Split(input, "/")
		if len(parts) >= 2 {
			return parts[len(parts)-1]
		}
	}
	return input
}

// RegisterContainerInstance handles AmazonEC2ContainerServiceV20141113.RegisterContainerInstance.
func (h *Handler) RegisterContainerInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster       string `json:"cluster"`
		Ec2InstanceId string `json:"ec2InstanceId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	clusterName := req.Cluster
	if clusterName == "" {
		clusterName = "default"
	}
	clusterName = extractClusterName(clusterName)

	id := uuid.New().String()
	arn := h.containerInstanceARN(r.Context(), clusterName, id)

	ci := &ContainerInstance{
		ContainerInstanceArn: arn,
		Ec2InstanceId:        req.Ec2InstanceId,
		Status:               "ACTIVE",
		AgentConnected:       true,
		RegisteredAt:         h.clk.Now().Unix(),
		ClusterName:          clusterName,
	}
	if aerr := h.store.putContainerInstance(r.Context(), ci); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"containerInstance": ci,
	})
}

// DeregisterContainerInstance handles AmazonEC2ContainerServiceV20141113.DeregisterContainerInstance.
func (h *Handler) DeregisterContainerInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster           string `json:"cluster"`
		ContainerInstance string `json:"containerInstance"`
		Force             bool   `json:"force"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ContainerInstance == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("containerInstance"))
		return
	}

	clusterName := req.Cluster
	if clusterName == "" {
		clusterName = "default"
	}
	clusterName = extractClusterName(clusterName)

	ciID := extractContainerInstanceID(req.ContainerInstance)

	ci, aerr := h.store.getContainerInstance(r.Context(), clusterName, req.ContainerInstance)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if ci == nil {
		// Try by short ID
		ci, aerr = h.store.getContainerInstance(r.Context(), clusterName, h.containerInstanceARN(r.Context(), clusterName, ciID))
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}
	if ci == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Container instance not found: " + req.ContainerInstance,
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	if aerr := h.store.deleteContainerInstance(r.Context(), clusterName, ci.ContainerInstanceArn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	ci.Status = "INACTIVE"
	ci.AgentConnected = false

	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"containerInstance": ci,
	})
}

// ListContainerInstances handles AmazonEC2ContainerServiceV20141113.ListContainerInstances.
func (h *Handler) ListContainerInstances(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster    string `json:"cluster"`
		Filter     string `json:"filter"`
		MaxResults *int   `json:"maxResults"`
		NextToken  string `json:"nextToken"`
		Status     string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	clusterName := req.Cluster
	if clusterName == "" {
		clusterName = "default"
	}
	clusterName = extractClusterName(clusterName)

	instances, aerr := h.store.listContainerInstances(r.Context(), clusterName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	arns := make([]string, 0, len(instances))
	for _, ci := range instances {
		if req.Status == "" || strings.EqualFold(ci.Status, req.Status) {
			arns = append(arns, ci.ContainerInstanceArn)
		}
	}

	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"containerInstanceArns": arns,
	})
}

// DescribeContainerInstances handles AmazonEC2ContainerServiceV20141113.DescribeContainerInstances.
func (h *Handler) DescribeContainerInstances(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster            string   `json:"cluster"`
		ContainerInstances []string `json:"containerInstances"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	clusterName := req.Cluster
	if clusterName == "" {
		clusterName = "default"
	}
	clusterName = extractClusterName(clusterName)

	type failure struct {
		Arn    string `json:"arn"`
		Reason string `json:"reason"`
	}
	var found []ContainerInstance
	var failures []failure

	for _, ref := range req.ContainerInstances {
		ci, aerr := h.store.getContainerInstance(r.Context(), clusterName, ref)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		if ci == nil {
			// Try resolving short ID to full ARN
			fullARN := h.containerInstanceARN(r.Context(), clusterName, extractContainerInstanceID(ref))
			ci, aerr = h.store.getContainerInstance(r.Context(), clusterName, fullARN)
			if aerr != nil {
				protocol.WriteJSONError(w, r, aerr)
				return
			}
		}
		if ci == nil {
			failures = append(failures, failure{Arn: ref, Reason: "MISSING"})
		} else {
			found = append(found, *ci)
		}
	}

	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"containerInstances": found,
		"failures":           failures,
	})
}
