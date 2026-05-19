package eks

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func (s *Service) createAddon(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	var req struct {
		AddonName             string            `json:"addonName"`
		AddonVersion          string            `json:"addonVersion"`
		ConfigurationValues   string            `json:"configurationValues"`
		ServiceAccountRoleArn string            `json:"serviceAccountRoleArn"`
		Tags                  map[string]string `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AddonName, "addonName") {
		return
	}

	a := &Addon{
		ClusterName:           clusterName,
		AddonName:             req.AddonName,
		AddonArn:              s.addonARN(region, clusterName, req.AddonName),
		AddonVersion:          req.AddonVersion,
		ConfigurationValues:   req.ConfigurationValues,
		ServiceAccountRoleArn: req.ServiceAccountRoleArn,
		CreatedAt:             s.clk.Now(),
		Status:                "ACTIVE",
	}
	if err := s.putAddon(ctx, region, a); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := s.putInlineTags(ctx, a.AddonArn, req.Tags); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	a.Tags = s.readTagsForARN(ctx, a.AddonArn)
	protocol.WriteJSON(w, r, http.StatusCreated, map[string]any{"addon": a})
}

func (s *Service) listAddons(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	addons, err := s.listAddonsForCluster(ctx, region, clusterName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	names := make([]string, 0, len(addons))
	for _, a := range addons {
		names = append(names, a.AddonName)
	}
	sort.Strings(names)
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"addons": names})
}

func (s *Service) describeAddon(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	addonName := chi.URLParam(r, "addonName")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	a, found, err := s.getAddon(ctx, region, clusterName, addonName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No addon found for name: %s", addonName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	a.Tags = s.readTagsForARN(ctx, a.AddonArn)
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"addon": a})
}

func (s *Service) deleteAddon(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	addonName := chi.URLParam(r, "addonName")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	a, found, err := s.getAddon(ctx, region, clusterName, addonName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No addon found for name: %s", addonName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if err := s.store.Delete(ctx, nsAddons, addonKey(region, clusterName, addonName)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := s.store.Delete(ctx, nsTags, tagKey(a.AddonArn)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	a.Status = "DELETING"
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"addon": a})
}

func (s *Service) updateAddon(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	addonName := chi.URLParam(r, "addonName")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	a, found, err := s.getAddon(ctx, region, clusterName, addonName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No addon found for name: %s", addonName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req struct {
		AddonVersion          string `json:"addonVersion"`
		ConfigurationValues   string `json:"configurationValues"`
		ServiceAccountRoleArn string `json:"serviceAccountRoleArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.AddonVersion == "" && req.ConfigurationValues == "" && req.ServiceAccountRoleArn == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "No addon changes requested",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	params := make([]map[string]any, 0, 4)
	if req.AddonVersion != "" {
		a.AddonVersion = req.AddonVersion
		params = append(params, map[string]any{"type": "AddonVersion", "value": req.AddonVersion})
	}
	if req.ConfigurationValues != "" {
		a.ConfigurationValues = req.ConfigurationValues
		params = append(params, map[string]any{"type": "ConfigurationValues", "value": "updated"})
	}
	if req.ServiceAccountRoleArn != "" {
		a.ServiceAccountRoleArn = req.ServiceAccountRoleArn
		params = append(params, map[string]any{"type": "ServiceAccountRoleArn", "value": req.ServiceAccountRoleArn})
	}
	params = append(params, map[string]any{"type": "AddonName", "value": addonName})

	if err := s.putAddon(ctx, region, a); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	update := &Update{
		ID:        fmt.Sprintf("upd-addon-%s-%d", addonName, s.clk.Now().UnixNano()),
		Status:    "Successful",
		Type:      "AddonUpdate",
		CreatedAt: s.clk.Now(),
		Params:    params,
	}
	if err := s.putUpdate(ctx, region, clusterName, update); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"update": update})
}

// addonVersionCatalog holds curated mock versions for the core EKS add-ons.
// Unknown add-ons receive an empty list — callers must handle this gracefully.
var addonVersionCatalog = map[string][]string{
	"vpc-cni":            {"v1.18.3-eksbuild.3", "v1.18.2-eksbuild.1", "v1.17.1-eksbuild.1"},
	"coredns":            {"v1.11.3-eksbuild.1", "v1.11.1-eksbuild.11", "v1.10.1-eksbuild.11"},
	"kube-proxy":         {"v1.30.3-eksbuild.5", "v1.29.7-eksbuild.9", "v1.28.13-eksbuild.2"},
	"aws-ebs-csi-driver": {"v1.35.0-eksbuild.1", "v1.34.0-eksbuild.1"},
}

var addonConfigurationCatalog = map[string]struct {
	Version string
	Schema  string
}{
	"vpc-cni": {
		Version: "v1.18.3-eksbuild.3",
		Schema:  `{"$schema":"http://json-schema.org/draft-06/schema#","type":"object","properties":{"env":{"type":"object","properties":{"AWS_VPC_K8S_CNI_LOGLEVEL":{"type":"string"}}}}}`,
	},
	"coredns": {
		Version: "v1.11.3-eksbuild.1",
		Schema:  `{"$schema":"http://json-schema.org/draft-06/schema#","type":"object","properties":{"replicaCount":{"type":"integer"}}}`,
	},
	"kube-proxy": {
		Version: "v1.30.3-eksbuild.5",
		Schema:  `{"$schema":"http://json-schema.org/draft-06/schema#","type":"object","properties":{"mode":{"type":"string"}}}`,
	},
	"aws-ebs-csi-driver": {
		Version: "v1.35.0-eksbuild.1",
		Schema:  `{"$schema":"http://json-schema.org/draft-06/schema#","type":"object","properties":{"controller":{"type":"object"}}}`,
	},
}

func (s *Service) describeAddonVersions(w http.ResponseWriter, r *http.Request) {
	addonName := chi.URLParam(r, "addonName")

	versions := addonVersionCatalog[addonName]
	if len(versions) == 0 {
		protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"addons": []any{}})
		return
	}

	versionEntries := make([]map[string]any, 0, len(versions))
	for _, v := range versions {
		versionEntries = append(versionEntries, map[string]any{
			"addonVersion": v,
			"compatibilities": []map[string]any{
				{"clusterVersion": "1.30", "defaultVersion": v == versions[0]},
				{"clusterVersion": "1.29", "defaultVersion": false},
			},
		})
	}

	entry := map[string]any{
		"addonName":     addonName,
		"addonVersions": versionEntries,
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"addons": []any{entry}})
}

func (s *Service) describeAddonConfiguration(w http.ResponseWriter, r *http.Request) {
	addonName := chi.URLParam(r, "addonName")
	cfg, ok := addonConfigurationCatalog[addonName]
	if !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No addon configuration found for name: %s", addonName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"addonName":           addonName,
		"addonVersion":        cfg.Version,
		"configurationSchema": cfg.Schema,
	})
}
