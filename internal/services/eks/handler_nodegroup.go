package eks

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func (s *Service) createNodegroup(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()
	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	var req struct {
		NodegroupName  string            `json:"nodegroupName"`
		NodeRole       string            `json:"nodeRole"`
		Version        string            `json:"version"`
		ReleaseVersion string            `json:"releaseVersion"`
		Subnets        []string          `json:"subnets"`
		InstanceTypes  []string          `json:"instanceTypes"`
		AmiType        string            `json:"amiType"`
		CapacityType   string            `json:"capacityType"`
		DiskSize       int               `json:"diskSize"`
		Labels         map[string]string `json:"labels"`
		Taints         []map[string]any  `json:"taints"`
		ScalingConfig  map[string]any    `json:"scalingConfig"`
		UpdateConfig   map[string]any    `json:"updateConfig"`
		LaunchTemplate map[string]any    `json:"launchTemplate"`
		Tags           map[string]string `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.NodegroupName, "nodegroupName") || !serviceutil.RequireString(w, r, req.NodeRole, "nodeRole") {
		return
	}
	key := nodegroupKey(region, clusterName, req.NodegroupName)
	if _, found, err := s.store.Get(ctx, nsNodegroups, key); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	} else if found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceInUseException",
			Message:    fmt.Sprintf("Nodegroup %q already exists", req.NodegroupName),
			HTTPStatus: http.StatusConflict,
		})
		return
	}

	ng := &Nodegroup{
		ClusterName:    clusterName,
		NodegroupName:  req.NodegroupName,
		NodegroupArn:   s.nodegroupARN(region, clusterName, req.NodegroupName),
		CreatedAt:      s.clk.Now(),
		Status:         "ACTIVE",
		Version:        req.Version,
		ReleaseVersion: req.ReleaseVersion,
		NodeRole:       req.NodeRole,
		Subnets:        req.Subnets,
		InstanceTypes:  req.InstanceTypes,
		AmiType:        req.AmiType,
		CapacityType:   req.CapacityType,
		DiskSize:       req.DiskSize,
		Labels:         req.Labels,
		Taints:         req.Taints,
		ScalingConfig:  req.ScalingConfig,
		UpdateConfig:   req.UpdateConfig,
		LaunchTemplate: req.LaunchTemplate,
	}
	if err := s.putNodegroup(ctx, region, ng); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := s.putInlineTags(ctx, ng.NodegroupArn, req.Tags); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	ng.Tags = s.readTagsForARN(ctx, ng.NodegroupArn)
	protocol.WriteJSON(w, r, http.StatusCreated, map[string]any{"nodegroup": ng})
}

func (s *Service) listNodegroups(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()
	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	nodegroups, err := s.listNodegroupsForCluster(ctx, region, clusterName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	names := make([]string, 0, len(nodegroups))
	for _, ng := range nodegroups {
		names = append(names, ng.NodegroupName)
	}
	sort.Strings(names)
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"nodegroups": names})
}

func (s *Service) describeNodegroup(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	nodegroupName := chi.URLParam(r, "nodegroupName")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	nodegroup, found, err := s.getNodegroup(ctx, region, clusterName, nodegroupName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No nodegroup found for name: %s", nodegroupName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	nodegroup.Tags = s.readTagsForARN(ctx, nodegroup.NodegroupArn)
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"nodegroup": nodegroup})
}

func (s *Service) updateNodegroupVersion(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	nodegroupName := chi.URLParam(r, "nodegroupName")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	nodegroup, found, err := s.getNodegroup(ctx, region, clusterName, nodegroupName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No nodegroup found for name: %s", nodegroupName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req struct {
		Version string `json:"version"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.Version, "version") {
		return
	}

	nodegroup.Version = req.Version
	if err := s.putNodegroup(ctx, region, nodegroup); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	update := &Update{
		ID:        fmt.Sprintf("upd-ng-%s-%d", nodegroupName, s.clk.Now().UnixNano()),
		Status:    "Successful",
		Type:      "VersionUpdate",
		CreatedAt: s.clk.Now(),
		Params: []map[string]any{
			{"type": "Version", "value": req.Version},
			{"type": "NodegroupName", "value": nodegroupName},
		},
	}
	if err := s.putUpdate(ctx, region, clusterName, update); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"update": update,
	})
}

func (s *Service) updateNodegroupConfig(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	nodegroupName := chi.URLParam(r, "nodegroupName")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	nodegroup, found, err := s.getNodegroup(ctx, region, clusterName, nodegroupName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No nodegroup found for name: %s", nodegroupName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req struct {
		Labels        map[string]string `json:"labels"`
		Taints        []map[string]any  `json:"taints"`
		ScalingConfig map[string]any    `json:"scalingConfig"`
		UpdateConfig  map[string]any    `json:"updateConfig"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Labels == nil && req.Taints == nil && req.ScalingConfig == nil && req.UpdateConfig == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "No nodegroup configuration changes requested",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	params := make([]map[string]any, 0, 5)
	if req.Labels != nil {
		nodegroup.Labels = req.Labels
		params = append(params, map[string]any{"type": "Labels", "value": "updated"})
	}
	if req.Taints != nil {
		nodegroup.Taints = req.Taints
		params = append(params, map[string]any{"type": "Taints", "value": "updated"})
	}
	if req.ScalingConfig != nil {
		nodegroup.ScalingConfig = req.ScalingConfig
		params = append(params, map[string]any{"type": "ScalingConfig", "value": "updated"})
	}
	if req.UpdateConfig != nil {
		nodegroup.UpdateConfig = req.UpdateConfig
		params = append(params, map[string]any{"type": "UpdateConfig", "value": "updated"})
	}
	params = append(params, map[string]any{"type": "NodegroupName", "value": nodegroupName})

	if err := s.putNodegroup(ctx, region, nodegroup); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	update := &Update{
		ID:        fmt.Sprintf("upd-ngcfg-%s-%d", nodegroupName, s.clk.Now().UnixNano()),
		Status:    "Successful",
		Type:      "ConfigUpdate",
		CreatedAt: s.clk.Now(),
		Params:    params,
	}
	if err := s.putUpdate(ctx, region, clusterName, update); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"update": update})
}

func (s *Service) deleteNodegroup(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	nodegroupName := chi.URLParam(r, "nodegroupName")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	ng, found, err := s.getNodegroup(ctx, region, clusterName, nodegroupName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No nodegroup found for name: %s", nodegroupName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if err := s.store.Delete(ctx, nsNodegroups, nodegroupKey(region, clusterName, nodegroupName)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := s.store.Delete(ctx, nsTags, tagKey(ng.NodegroupArn)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	ng.Status = "DELETING"
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"nodegroup": ng})
}
