package eks

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func (s *Service) listIdentityProviderConfigs(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	configs, err := s.listIdentityProviderConfigsForCluster(ctx, region, clusterName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	items := make([]map[string]any, 0, len(configs))
	for _, cfg := range configs {
		items = append(items, map[string]any{
			"type": cfg.Type,
			"name": cfg.Name,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i]["type"] == items[j]["type"] {
			return fmt.Sprint(items[i]["name"]) < fmt.Sprint(items[j]["name"])
		}
		return fmt.Sprint(items[i]["type"]) < fmt.Sprint(items[j]["type"])
	})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"identityProviderConfigs": items})
}

func (s *Service) listPodIdentityAssociations(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	associations, err := s.listPodIdentityAssociationsForCluster(ctx, region, clusterName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	sort.Slice(associations, func(i, j int) bool {
		return associations[i].AssociationID < associations[j].AssociationID
	})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"associations": associations})
}

func (s *Service) createPodIdentityAssociation(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	var req struct {
		Namespace      string            `json:"namespace"`
		ServiceAccount string            `json:"serviceAccount"`
		RoleArn        string            `json:"roleArn"`
		Tags           map[string]string `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.Namespace, "namespace") ||
		!serviceutil.RequireString(w, r, req.ServiceAccount, "serviceAccount") ||
		!serviceutil.RequireString(w, r, req.RoleArn, "roleArn") {
		return
	}

	existing, err := s.listPodIdentityAssociationsForCluster(ctx, region, clusterName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	for _, assoc := range existing {
		if assoc.Namespace == req.Namespace && assoc.ServiceAccount == req.ServiceAccount {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "ResourceInUseException",
				Message:    fmt.Sprintf("Pod identity association for namespace %q service account %q already exists", req.Namespace, req.ServiceAccount),
				HTTPStatus: http.StatusConflict,
			})
			return
		}
	}

	associationID := fmt.Sprintf("pia-%d", s.clk.Now().UnixNano())
	assoc := &PodIdentityAssociation{
		ClusterName:    clusterName,
		AssociationID:  associationID,
		AssociationArn: s.podIdentityAssociationARN(region, clusterName, associationID),
		Namespace:      req.Namespace,
		ServiceAccount: req.ServiceAccount,
		RoleArn:        req.RoleArn,
		CreatedAt:      s.clk.Now(),
		ModifiedAt:     s.clk.Now(),
	}

	if err := s.putPodIdentityAssociation(ctx, region, assoc); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := s.putInlineTags(ctx, assoc.AssociationArn, req.Tags); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	assoc.Tags = s.readTagsForARN(ctx, assoc.AssociationArn)
	protocol.WriteJSON(w, r, http.StatusCreated, map[string]any{"association": assoc})
}

func (s *Service) describePodIdentityAssociation(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	associationID := chi.URLParam(r, "associationId")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	assoc, found, err := s.getPodIdentityAssociation(ctx, region, clusterName, associationID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No pod identity association found for id: %s", associationID),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	assoc.Tags = s.readTagsForARN(ctx, assoc.AssociationArn)
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"association": assoc})
}

func (s *Service) updatePodIdentityAssociation(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	associationID := chi.URLParam(r, "associationId")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	assoc, found, err := s.getPodIdentityAssociation(ctx, region, clusterName, associationID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No pod identity association found for id: %s", associationID),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req struct {
		RoleArn string `json:"roleArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.RoleArn, "roleArn") {
		return
	}

	assoc.RoleArn = req.RoleArn
	assoc.ModifiedAt = s.clk.Now()

	if err := s.putPodIdentityAssociation(ctx, region, assoc); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"association": assoc})
}

func (s *Service) deletePodIdentityAssociation(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	associationID := chi.URLParam(r, "associationId")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	assoc, found, err := s.getPodIdentityAssociation(ctx, region, clusterName, associationID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No pod identity association found for id: %s", associationID),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if err := s.store.Delete(ctx, nsPodIDAssoc, podIdentityAssociationKey(region, clusterName, associationID)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := s.store.Delete(ctx, nsTags, tagKey(assoc.AssociationArn)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"association": assoc})
}

func (s *Service) describeIdentityProviderConfig(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	configType := chi.URLParam(r, "configType")
	configName := chi.URLParam(r, "configName")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	cfg, found, err := s.getIdentityProviderConfig(ctx, region, clusterName, configType, configName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No identity provider config found for %s/%s", configType, configName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	cfg.Tags = s.readTagsForARN(ctx, s.identityProviderConfigARN(region, clusterName, configType, configName))
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"identityProviderConfig": cfg})
}

func (s *Service) updateIdentityProviderConfig(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	configType := chi.URLParam(r, "configType")
	configName := chi.URLParam(r, "configName")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	cfg, found, err := s.getIdentityProviderConfig(ctx, region, clusterName, configType, configName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No identity provider config found for %s/%s", configType, configName),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req struct {
		OIDC map[string]any    `json:"oidc"`
		Tags map[string]string `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if configType == "oidc" {
		if req.OIDC == nil {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "InvalidParameterException",
				Message:    "oidc update payload is required",
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
		if cfg.OIDC == nil {
			cfg.OIDC = map[string]any{}
		}
		for k, v := range req.OIDC {
			cfg.OIDC[k] = v
		}
	}

	if err := s.putIdentityProviderConfig(ctx, region, cfg); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	update := &Update{
		ID:        fmt.Sprintf("upd-idp-update-%s-%d", configName, s.clk.Now().UnixNano()),
		Status:    "Successful",
		Type:      "UpdateIdentityProviderConfig",
		CreatedAt: s.clk.Now(),
		Params: []map[string]any{
			{"type": "IdentityProviderConfigType", "value": configType},
			{"type": "IdentityProviderConfigName", "value": configName},
		},
	}
	if err := s.putUpdate(ctx, region, clusterName, update); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"update": update})
}

func (s *Service) associateIdentityProviderConfig(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	var req struct {
		OIDC map[string]any    `json:"oidc"`
		Tags map[string]string `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.OIDC == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "oidc configuration is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	name, _ := req.OIDC["identityProviderConfigName"].(string)
	if !serviceutil.RequireString(w, r, name, "oidc.identityProviderConfigName") {
		return
	}

	cfg := &IdentityProviderConfig{
		ClusterName: clusterName,
		Type:        "oidc",
		Name:        name,
		OIDC:        req.OIDC,
		CreatedAt:   s.clk.Now(),
	}
	if err := s.putIdentityProviderConfig(ctx, region, cfg); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	idpARN := s.identityProviderConfigARN(region, clusterName, cfg.Type, cfg.Name)
	if err := s.putInlineTags(ctx, idpARN, req.Tags); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	update := &Update{
		ID:        fmt.Sprintf("upd-idp-%s-%d", name, s.clk.Now().UnixNano()),
		Status:    "Successful",
		Type:      "AssociateIdentityProviderConfig",
		CreatedAt: s.clk.Now(),
		Params: []map[string]any{
			{"type": "IdentityProviderConfigType", "value": "oidc"},
			{"type": "IdentityProviderConfigName", "value": name},
		},
	}
	if err := s.putUpdate(ctx, region, clusterName, update); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"update": update})
}

func (s *Service) disassociateIdentityProviderConfig(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	var req struct {
		IdentityProviderConfig struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"identityProviderConfig"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.IdentityProviderConfig.Type, "identityProviderConfig.type") ||
		!serviceutil.RequireString(w, r, req.IdentityProviderConfig.Name, "identityProviderConfig.name") {
		return
	}

	if err := s.store.Delete(ctx, nsIDPConfigs, idpConfigKey(region, clusterName, req.IdentityProviderConfig.Type, req.IdentityProviderConfig.Name)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := s.store.Delete(ctx, nsTags, tagKey(s.identityProviderConfigARN(region, clusterName, req.IdentityProviderConfig.Type, req.IdentityProviderConfig.Name))); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	update := &Update{
		ID:        fmt.Sprintf("upd-idp-disassoc-%s-%d", req.IdentityProviderConfig.Name, s.clk.Now().UnixNano()),
		Status:    "Successful",
		Type:      "DisassociateIdentityProviderConfig",
		CreatedAt: s.clk.Now(),
		Params: []map[string]any{
			{"type": "IdentityProviderConfigType", "value": req.IdentityProviderConfig.Type},
			{"type": "IdentityProviderConfigName", "value": req.IdentityProviderConfig.Name},
		},
	}
	if err := s.putUpdate(ctx, region, clusterName, update); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"update": update})
}
