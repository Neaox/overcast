package ecs

// handler.go — HTTP handlers for the ECS emulator.
// Contains the Handler struct, operation registry, shared helpers, and all
// implemented operation handlers. Stubs live in handler_stubs.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/lifecycle"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// Handler holds ECS handler dependencies.
type Handler struct {
	cfg         *config.Config
	store       *ecsStore
	log         *serviceutil.ServiceLogger
	clk         clock.Clock
	bus         *events.Bus
	scheduler   *lifecycle.Scheduler
	ops         map[string]http.HandlerFunc
	typedOp     map[string]op.Operation
	docker      *docker.Client
	dockerReady atomic.Bool
	puller      *docker.ImagePuller
	gc          *docker.GC
	vpcResolver VPCNetworkResolver
	seedOnce    sync.Once // guards ensureBuiltinProviders
}

// VPCNetworkResolver resolves subnet-backed ECS awsvpc placement against EC2.
type VPCNetworkResolver interface {
	VpcIDForSubnet(ctx context.Context, subnetID string) string
	VPCNetworkStatus(ctx context.Context, vpcID string) string
	DockerNetworkForVpc(ctx context.Context, vpcID string) string
	AllocatePrivateIPForSubnet(ctx context.Context, subnetID string) string
}

// ensureBuiltinProviders lazily seeds FARGATE and FARGATE_SPOT capacity
// providers so they are discoverable without explicit creation, matching
// real AWS behaviour. Called on first capacity-provider-touching request.
func (h *Handler) ensureBuiltinProviders() {
	h.seedOnce.Do(func() {
		ctx := context.Background()
		for _, name := range []string{"FARGATE", "FARGATE_SPOT"} {
			existing, err := h.store.getCapacityProvider(ctx, name)
			if err != nil || existing != nil {
				continue
			}
			cp := &CapacityProvider{
				CapacityProviderArn: h.capacityProviderARN(ctx, name),
				Name:                name,
				Status:              "ACTIVE",
			}
			_ = h.store.putCapacityProvider(ctx, cp)
		}
	})
}

func newHandler(cfg *config.Config, store *ecsStore, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: store, log: log, clk: clk, scheduler: lifecycle.NewScheduler(clk)}
	h.initOps()
	return h
}

// decodeJSON decodes the request body into v. Returns false and writes an error if it fails.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "SerializationException",
			Message:    "Failed to deserialize the message: " + err.Error(),
			HTTPStatus: http.StatusBadRequest,
		})
		return false
	}
	return true
}

// publish emits an event if the bus is wired.
func (h *Handler) publish(r *http.Request, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{Type: t, Payload: payload})
	}
}

// region returns the request's region (from SigV4 / X-Overcast-Region header),
// falling back to the configured default. Used for ARN minting so returned
// ARNs carry the deployment region the SDK actually called.
func (h *Handler) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, h.cfg.Region)
}

// clusterARN builds an ECS cluster ARN.
func (h *Handler) clusterARN(ctx context.Context, name string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/%s", h.region(ctx), h.cfg.AccountID, name)
}

// taskDefinitionARN builds an ECS task definition ARN.
func (h *Handler) taskDefinitionARN(ctx context.Context, family string, revision int) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:task-definition/%s:%d", h.region(ctx), h.cfg.AccountID, family, revision)
}

// extractClusterName extracts the cluster name from an ARN or returns the input as-is.
func extractClusterName(input string) string {
	if strings.HasPrefix(input, "arn:") {
		parts := strings.Split(input, "/")
		if len(parts) >= 2 {
			return parts[len(parts)-1]
		}
	}
	return input
}

// taskARN builds an ECS task ARN.
func (h *Handler) taskARN(ctx context.Context, cluster, taskID string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:task/%s/%s", h.region(ctx), h.cfg.AccountID, cluster, taskID)
}

// containerARN builds an ECS container ARN.
func (h *Handler) containerARN(ctx context.Context, id string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:container/%s", h.region(ctx), h.cfg.AccountID, id)
}

// extractTaskID extracts the task UUID from an ARN or returns the input as-is.
func extractTaskID(input string) string {
	if strings.HasPrefix(input, "arn:") {
		parts := strings.Split(input, "/")
		if len(parts) >= 2 {
			return parts[len(parts)-1]
		}
	}
	return input
}

/*
parseTaskDefRef parses a task definition reference which can be:
- family (latest)
- family:revision
- full ARN (arn:aws:ecs:...:task-definition/family:revision).
*/
func parseTaskDefRef(ref string) (family string, revision int, hasRevision bool) {
	// Strip ARN prefix if present
	if strings.HasPrefix(ref, "arn:") {
		parts := strings.Split(ref, "/")
		if len(parts) >= 2 {
			ref = parts[len(parts)-1]
		}
	}
	// Split family:revision
	if idx := strings.LastIndex(ref, ":"); idx >= 0 {
		fam := ref[:idx]
		if rev, err := strconv.Atoi(ref[idx+1:]); err == nil {
			return fam, rev, true
		}
	}
	return ref, 0, false
}

// validFargateCPUMemory defines valid CPU (millicores string) → allowed memory ranges (MiB).
// https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-cpu-memory-error.html
var validFargateCPUMemory = map[string][2]int{
	"256":   {512, 2048},
	"512":   {1024, 4096},
	"1024":  {2048, 8192},
	"2048":  {4096, 16384},
	"4096":  {8192, 30720},
	"8192":  {16384, 61440},
	"16384": {32768, 122880},
}

// isFargate returns true if the requiresCompatibilities list includes "FARGATE".
func isFargate(compat []string) bool {
	for _, c := range compat {
		if strings.EqualFold(c, "FARGATE") {
			return true
		}
	}
	return false
}

// validateFargateCPUMemory checks that the cpu/memory combination is valid for Fargate.
func validateFargateCPUMemory(cpu, memory string) error {
	memMiB, err := strconv.Atoi(memory)
	if err != nil {
		return fmt.Errorf("invalid memory value %q: must be a number in MiB", memory)
	}
	allowed, ok := validFargateCPUMemory[cpu]
	if !ok {
		return fmt.Errorf("invalid cpu value %q for FARGATE; valid values: 256, 512, 1024, 2048, 4096, 8192, 16384", cpu)
	}
	if memMiB < allowed[0] || memMiB > allowed[1] {
		return fmt.Errorf("invalid memory value %d MiB for cpu=%s; valid range: %d–%d MiB", memMiB, cpu, allowed[0], allowed[1])
	}
	return nil
}

// ---- Cluster handlers --------------------------------------------------------

// CreateCluster handles AmazonEC2ContainerServiceV20141113.CreateCluster.
func (h *Handler) CreateCluster(w http.ResponseWriter, r *http.Request) {
	h.ensureBuiltinProviders()
	var req struct {
		ClusterName                     string                         `json:"clusterName"`
		CapacityProviders               []string                       `json:"capacityProviders"`
		DefaultCapacityProviderStrategy []CapacityProviderStrategyItem `json:"defaultCapacityProviderStrategy"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ClusterName == "" {
		req.ClusterName = "default"
	}
	c := &Cluster{
		ClusterName:                       req.ClusterName,
		ClusterArn:                        h.clusterARN(r.Context(), req.ClusterName),
		Status:                            "ACTIVE",
		RegisteredContainerInstancesCount: 0,
		RunningTasksCount:                 0,
		PendingTasksCount:                 0,
		ActiveServicesCount:               0,
		CapacityProviders:                 req.CapacityProviders,
		DefaultCapacityProviderStrategy:   req.DefaultCapacityProviderStrategy,
	}
	if aerr := h.store.putCluster(r.Context(), c); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	h.publish(r, events.ECSClusterCreated, events.ResourcePayload{Name: c.ClusterName})
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"cluster": c}, "application/x-amz-json-1.1")
}

// DescribeClusters handles AmazonEC2ContainerServiceV20141113.DescribeClusters.
func (h *Handler) DescribeClusters(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Clusters []string `json:"clusters"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	type failure struct {
		Arn    string `json:"arn"`
		Reason string `json:"reason"`
	}
	found := make([]Cluster, 0, len(req.Clusters))
	failures := make([]failure, 0)
	for _, ref := range req.Clusters {
		name := extractClusterName(ref)
		c, aerr := h.store.getCluster(r.Context(), name)
		if aerr != nil {
			failures = append(failures, failure{
				Arn:    h.clusterARN(r.Context(), name),
				Reason: "MISSING",
			})
			continue
		}
		found = append(found, *c)
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{
		"clusters": found,
		"failures": failures,
	}, "application/x-amz-json-1.1")
}

// ListClusters handles AmazonEC2ContainerServiceV20141113.ListClusters.
func (h *Handler) ListClusters(w http.ResponseWriter, r *http.Request) {
	// Consume request body (may be empty or {})
	var req json.RawMessage
	_ = json.NewDecoder(r.Body).Decode(&req)

	clusters, aerr := h.store.listClusters(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	arns := make([]string, 0, len(clusters))
	for _, c := range clusters {
		arns = append(arns, c.ClusterArn)
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"clusterArns": arns}, "application/x-amz-json-1.1")
}

// DeleteCluster handles AmazonEC2ContainerServiceV20141113.DeleteCluster.
func (h *Handler) DeleteCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := extractClusterName(req.Cluster)
	c, aerr := h.store.getCluster(r.Context(), name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	c.Status = "INACTIVE"
	if aerr := h.store.deleteCluster(r.Context(), name); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	h.publish(r, events.ECSClusterDeleted, events.ResourcePayload{Name: name})
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"cluster": c}, "application/x-amz-json-1.1")
}

// ---- Task definition handlers -------------------------------------------------

// RegisterTaskDefinition handles AmazonEC2ContainerServiceV20141113.RegisterTaskDefinition.
func (h *Handler) RegisterTaskDefinition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Family                  string                `json:"family"`
		ContainerDefinitions    []ContainerDefinition `json:"containerDefinitions"`
		NetworkMode             string                `json:"networkMode"`
		RequiresCompatibilities []string              `json:"requiresCompatibilities"`
		Cpu                     string                `json:"cpu"`
		Memory                  string                `json:"memory"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Family == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ClientException",
			Message:    "Family is required.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Validate Fargate constraints.
	if isFargate(req.RequiresCompatibilities) {
		if req.NetworkMode != "" && req.NetworkMode != "awsvpc" {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "ClientException",
				Message:    "FARGATE requires networkMode to be 'awsvpc'.",
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
		if req.Cpu == "" || req.Memory == "" {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "ClientException",
				Message:    "FARGATE requires both cpu and memory to be specified at the task level.",
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
		if err := validateFargateCPUMemory(req.Cpu, req.Memory); err != nil {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "ClientException",
				Message:    err.Error(),
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
		// Default networkMode to awsvpc when not specified for Fargate.
		if req.NetworkMode == "" {
			req.NetworkMode = "awsvpc"
		}
	}

	rev, aerr := h.store.nextRevision(r.Context(), req.Family)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	td := &TaskDefinition{
		TaskDefinitionArn:       h.taskDefinitionARN(r.Context(), req.Family, rev),
		Family:                  req.Family,
		Revision:                rev,
		Status:                  "ACTIVE",
		NetworkMode:             req.NetworkMode,
		RequiresCompatibilities: req.RequiresCompatibilities,
		Cpu:                     req.Cpu,
		Memory:                  req.Memory,
		ContainerDefinitions:    req.ContainerDefinitions,
	}
	if aerr := h.store.putTaskDefinition(r.Context(), td); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	// Pre-warm container images in the background so the first RunTask does
	// not pay the cold-pull cost on the request path. The sync.Once inside
	// the puller coalesces with any concurrent RunTask pull.
	if h.puller != nil {
		for _, c := range td.ContainerDefinitions {
			h.puller.Prewarm(c.Image)
		}
	}
	h.publish(r, events.ECSTaskDefinitionRegistered, events.ResourcePayload{Name: req.Family})
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"taskDefinition": td}, "application/x-amz-json-1.1")
}

// DescribeTaskDefinition handles AmazonEC2ContainerServiceV20141113.DescribeTaskDefinition.
func (h *Handler) DescribeTaskDefinition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskDefinition string `json:"taskDefinition"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TaskDefinition == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ClientException",
			Message:    "taskDefinition is required.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	family, revision, hasRevision := parseTaskDefRef(req.TaskDefinition)
	var td *TaskDefinition
	var aerr *protocol.AWSError
	if hasRevision {
		td, aerr = h.store.getTaskDefinition(r.Context(), family, revision)
	} else {
		td, aerr = h.store.getLatestTaskDefinition(r.Context(), family)
	}
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"taskDefinition": td}, "application/x-amz-json-1.1")
}

// ListTaskDefinitions handles AmazonEC2ContainerServiceV20141113.ListTaskDefinitions.
func (h *Handler) ListTaskDefinitions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FamilyPrefix string `json:"familyPrefix"`
	}
	// Body may be empty
	_ = json.NewDecoder(r.Body).Decode(&req)

	var defs []TaskDefinition
	var aerr *protocol.AWSError
	if req.FamilyPrefix != "" {
		defs, aerr = h.store.listTaskDefinitionsByFamily(r.Context(), req.FamilyPrefix)
	} else {
		defs, aerr = h.store.listTaskDefinitions(r.Context())
	}
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	arns := make([]string, 0, len(defs))
	for _, td := range defs {
		arns = append(arns, td.TaskDefinitionArn)
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"taskDefinitionArns": arns}, "application/x-amz-json-1.1")
}

// DeregisterTaskDefinition handles AmazonEC2ContainerServiceV20141113.DeregisterTaskDefinition.
func (h *Handler) DeregisterTaskDefinition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskDefinition string `json:"taskDefinition"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TaskDefinition == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ClientException",
			Message:    "taskDefinition is required.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	family, revision, hasRevision := parseTaskDefRef(req.TaskDefinition)
	if !hasRevision {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ClientException",
			Message:    "taskDefinition must include a revision (family:revision).",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	td, aerr := h.store.getTaskDefinition(r.Context(), family, revision)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	td.Status = "INACTIVE"
	if aerr := h.store.putTaskDefinition(r.Context(), td); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	h.publish(r, events.ECSTaskDefinitionDeregistered, events.ResourcePayload{Name: family})
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"taskDefinition": td}, "application/x-amz-json-1.1")
}

// ListTaskDefinitionFamilies handles AmazonEC2ContainerServiceV20141113.ListTaskDefinitionFamilies.
func (h *Handler) ListTaskDefinitionFamilies(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FamilyPrefix string `json:"familyPrefix"`
		Status       string `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	families, aerr := h.store.listTaskDefinitionFamilies(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	filtered := make([]string, 0, len(families))
	for _, f := range families {
		if req.FamilyPrefix != "" && !strings.HasPrefix(f, req.FamilyPrefix) {
			continue
		}
		filtered = append(filtered, f)
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"families": filtered}, "application/x-amz-json-1.1")
}

// ---- Cluster update handlers --------------------------------------------------

// UpdateCluster handles AmazonEC2ContainerServiceV20141113.UpdateCluster.
func (h *Handler) UpdateCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster       string `json:"cluster"`
		Configuration *struct {
			ExecuteCommandConfiguration *struct {
				Logging string `json:"logging"`
			} `json:"executeCommandConfiguration"`
		} `json:"configuration"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := extractClusterName(req.Cluster)
	c, aerr := h.store.getCluster(r.Context(), name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	// Configuration is metadata-only — store it and return the cluster.
	if aerr := h.store.putCluster(r.Context(), c); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"cluster": c}, "application/x-amz-json-1.1")
}

// UpdateClusterSettings handles AmazonEC2ContainerServiceV20141113.UpdateClusterSettings.
func (h *Handler) UpdateClusterSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster  string `json:"cluster"`
		Settings []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"settings"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := extractClusterName(req.Cluster)
	c, aerr := h.store.getCluster(r.Context(), name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	// Settings are metadata-only — acknowledge and return the cluster.
	if aerr := h.store.putCluster(r.Context(), c); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteAWSJSON(w, r, http.StatusOK, map[string]any{"cluster": c}, "application/x-amz-json-1.1")
}
