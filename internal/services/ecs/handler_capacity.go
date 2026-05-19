package ecs

// handler_capacity.go — CreateCapacityProvider, DescribeCapacityProviders,
// UpdateCapacityProvider, PutClusterCapacityProviders handlers.
//
// Capacity providers are metadata-only. FARGATE and FARGATE_SPOT are lazily
// seeded on first access via ensureBuiltinProviders (sync.Once).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
)

// CreateCapacityProvider handles AmazonEC2ContainerServiceV20141113.CreateCapacityProvider.
func (h *Handler) CreateCapacityProvider(w http.ResponseWriter, r *http.Request) {
	h.ensureBuiltinProviders()
	var req struct {
		Name string `json:"name"`
		Tags []Tag  `json:"tags"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "The name must not be null or empty.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if strings.HasPrefix(req.Name, "FARGATE") {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "The capacity provider name prefix 'FARGATE' is reserved.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Check for duplicate.
	existing, aerr := h.store.getCapacityProvider(r.Context(), req.Name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    fmt.Sprintf("A capacity provider with the name '%s' already exists.", req.Name),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	cp := &CapacityProvider{
		CapacityProviderArn: h.capacityProviderARN(r.Context(), req.Name),
		Name:                req.Name,
		Status:              "ACTIVE",
		Tags:                req.Tags,
	}
	if aerr := h.store.putCapacityProvider(r.Context(), cp); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"capacityProvider": cp})
}

// DescribeCapacityProviders handles AmazonEC2ContainerServiceV20141113.DescribeCapacityProviders.
func (h *Handler) DescribeCapacityProviders(w http.ResponseWriter, r *http.Request) {
	h.ensureBuiltinProviders()
	var req struct {
		CapacityProviders []string `json:"capacityProviders"`
	}
	// Body may be empty (returns all providers).
	_ = decodeJSONBody(r, &req)

	type failure struct {
		Arn    string `json:"arn"`
		Reason string `json:"reason"`
	}
	var cps []CapacityProvider
	var failures []failure

	if len(req.CapacityProviders) == 0 {
		// Return all capacity providers including built-ins.
		var aerr *protocol.AWSError
		cps, aerr = h.store.listCapacityProviders(r.Context())
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		failures = []failure{}
	} else {
		cps = make([]CapacityProvider, 0, len(req.CapacityProviders))
		failures = make([]failure, 0)
		for _, name := range req.CapacityProviders {
			cp, aerr := h.store.getCapacityProvider(r.Context(), name)
			if aerr != nil {
				protocol.WriteJSONError(w, r, aerr)
				return
			}
			if cp == nil {
				failures = append(failures, failure{
					Arn:    h.capacityProviderARN(r.Context(), name),
					Reason: "MISSING",
				})
				continue
			}
			cps = append(cps, *cp)
		}
	}

	writeJSON(w, r, http.StatusOK, map[string]any{
		"capacityProviders": cps,
		"failures":          failures,
	})
}

// UpdateCapacityProvider handles AmazonEC2ContainerServiceV20141113.UpdateCapacityProvider.
func (h *Handler) UpdateCapacityProvider(w http.ResponseWriter, r *http.Request) {
	h.ensureBuiltinProviders()
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "The name must not be null or empty.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	cp, aerr := h.store.getCapacityProvider(r.Context(), req.Name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if cp == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Capacity provider not found: %s", req.Name),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Built-in providers cannot be updated.
	if cp.Name == "FARGATE" || cp.Name == "FARGATE_SPOT" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ClientException",
			Message:    fmt.Sprintf("The capacity provider '%s' is a managed capacity provider and cannot be updated.", cp.Name),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	if aerr := h.store.putCapacityProvider(r.Context(), cp); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"capacityProvider": cp})
}

// PutClusterCapacityProviders handles AmazonEC2ContainerServiceV20141113.PutClusterCapacityProviders.
func (h *Handler) PutClusterCapacityProviders(w http.ResponseWriter, r *http.Request) {
	h.ensureBuiltinProviders()
	var req struct {
		Cluster                         string                         `json:"cluster"`
		CapacityProviders               []string                       `json:"capacityProviders"`
		DefaultCapacityProviderStrategy []CapacityProviderStrategyItem `json:"defaultCapacityProviderStrategy"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	clusterName := extractClusterName(req.Cluster)
	cluster, aerr := h.store.getCluster(r.Context(), clusterName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	cluster.CapacityProviders = req.CapacityProviders
	cluster.DefaultCapacityProviderStrategy = req.DefaultCapacityProviderStrategy

	if aerr := h.store.putCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"cluster": cluster})
}

// capacityProviderARN builds an ECS capacity provider ARN.
func (h *Handler) capacityProviderARN(ctx context.Context, name string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:capacity-provider/%s", h.region(ctx), h.cfg.AccountID, name)
}

// decodeJSONBody tries to decode a JSON body — ignores EOF (empty body) but returns other errors.
func decodeJSONBody(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
