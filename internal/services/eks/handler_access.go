package eks

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func (s *Service) listAccessEntries(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	entries, err := s.listAccessEntriesForCluster(ctx, region, clusterName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	arns := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.PrincipalArn != "" {
			arns = append(arns, e.PrincipalArn)
		}
	}
	sort.Strings(arns)

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"accessEntries": arns})
}

func (s *Service) createAccessEntry(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	var req struct {
		PrincipalArn     string            `json:"principalArn"`
		Type             string            `json:"type"`
		Username         string            `json:"username"`
		KubernetesGroups []string          `json:"kubernetesGroups"`
		Tags             map[string]string `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.PrincipalArn, "principalArn") {
		return
	}

	key := accessEntryKey(region, clusterName, req.PrincipalArn)
	if _, found, err := s.store.Get(ctx, nsAccess, key); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	} else if found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceInUseException",
			Message:    fmt.Sprintf("Access entry for principal %q already exists", req.PrincipalArn),
			HTTPStatus: http.StatusConflict,
		})
		return
	}

	entryType := req.Type
	if strings.TrimSpace(entryType) == "" {
		entryType = "STANDARD"
	}

	entry := &AccessEntry{
		ClusterName:      clusterName,
		PrincipalArn:     req.PrincipalArn,
		Type:             entryType,
		Username:         req.Username,
		KubernetesGroups: req.KubernetesGroups,
		CreatedAt:        s.clk.Now(),
		ModifiedAt:       s.clk.Now(),
	}
	if err := s.putAccessEntry(ctx, region, entry); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	entryARN := s.accessEntryARN(region, clusterName, req.PrincipalArn)
	if err := s.putInlineTags(ctx, entryARN, req.Tags); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	entry.Tags = s.readTagsForARN(ctx, entryARN)
	protocol.WriteJSON(w, r, http.StatusCreated, map[string]any{"accessEntry": entry})
}

func (s *Service) describeAccessEntry(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	principalArnParam := chi.URLParam(r, "principalArn")
	principalArn, err := url.PathUnescape(principalArnParam)
	if err != nil {
		principalArn = principalArnParam
	}

	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	entry, found, err := s.getAccessEntry(ctx, region, clusterName, principalArn)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No access entry found for principalArn: %s", principalArn),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	entry.Tags = s.readTagsForARN(ctx, s.accessEntryARN(region, clusterName, principalArn))
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"accessEntry": entry})
}

func (s *Service) updateAccessEntry(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	principalArnParam := chi.URLParam(r, "principalArn")
	principalArn, err := url.PathUnescape(principalArnParam)
	if err != nil {
		principalArn = principalArnParam
	}

	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	entry, found, err := s.getAccessEntry(ctx, region, clusterName, principalArn)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No access entry found for principalArn: %s", principalArn),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req struct {
		Username         string   `json:"username"`
		KubernetesGroups []string `json:"kubernetesGroups"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.Username != "" {
		entry.Username = req.Username
	}
	if req.KubernetesGroups != nil {
		entry.KubernetesGroups = req.KubernetesGroups
	}
	entry.ModifiedAt = s.clk.Now()

	if err := s.putAccessEntry(ctx, region, entry); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"accessEntry": entry})
}

func (s *Service) deleteAccessEntry(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	principalArnParam := chi.URLParam(r, "principalArn")
	principalArn, err := url.PathUnescape(principalArnParam)
	if err != nil {
		principalArn = principalArnParam
	}

	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	if _, found, err := s.getAccessEntry(ctx, region, clusterName, principalArn); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	} else if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No access entry found for principalArn: %s", principalArn),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	associatedPolicies, err := s.listAssociatedAccessPoliciesForEntry(ctx, region, clusterName, principalArn)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	for _, p := range associatedPolicies {
		if err := s.store.Delete(ctx, nsAccessPol, associatedAccessPolicyKey(region, clusterName, principalArn, p.PolicyArn)); err != nil {
			protocol.WriteJSONError(w, r, protocol.ErrInternalError)
			return
		}
	}

	if err := s.store.Delete(ctx, nsAccess, accessEntryKey(region, clusterName, principalArn)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := s.store.Delete(ctx, nsTags, tagKey(s.accessEntryARN(region, clusterName, principalArn))); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) associateAccessPolicy(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	principalArnParam := chi.URLParam(r, "principalArn")
	principalArn, err := url.PathUnescape(principalArnParam)
	if err != nil {
		principalArn = principalArnParam
	}

	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	if _, found, err := s.getAccessEntry(ctx, region, clusterName, principalArn); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	} else if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No access entry found for principalArn: %s", principalArn),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req struct {
		PolicyArn   string         `json:"policyArn"`
		AccessScope map[string]any `json:"accessScope"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.PolicyArn, "policyArn") {
		return
	}

	if _, found, err := s.getAssociatedAccessPolicy(ctx, region, clusterName, principalArn, req.PolicyArn); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	} else if found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceInUseException",
			Message:    fmt.Sprintf("Access policy %q is already associated", req.PolicyArn),
			HTTPStatus: http.StatusConflict,
		})
		return
	}

	scope := req.AccessScope
	if scope == nil {
		scope = map[string]any{"type": "cluster"}
	}

	assoc := &AssociatedAccessPolicy{
		ClusterName:  clusterName,
		PrincipalArn: principalArn,
		PolicyArn:    req.PolicyArn,
		AccessScope:  scope,
		AssociatedAt: s.clk.Now(),
		ModifiedAt:   s.clk.Now(),
	}
	if err := s.putAssociatedAccessPolicy(ctx, region, assoc); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, map[string]any{"associatedAccessPolicy": assoc})
}

func (s *Service) listAssociatedAccessPolicies(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	principalArnParam := chi.URLParam(r, "principalArn")
	principalArn, err := url.PathUnescape(principalArnParam)
	if err != nil {
		principalArn = principalArnParam
	}

	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	if _, found, err := s.getAccessEntry(ctx, region, clusterName, principalArn); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	} else if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No access entry found for principalArn: %s", principalArn),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	items, err := s.listAssociatedAccessPoliciesForEntry(ctx, region, clusterName, principalArn)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].PolicyArn < items[j].PolicyArn
	})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"associatedAccessPolicies": items})
}

func (s *Service) disassociateAccessPolicy(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	principalArnParam := chi.URLParam(r, "principalArn")
	principalArn, err := url.PathUnescape(principalArnParam)
	if err != nil {
		principalArn = principalArnParam
	}
	policyArnParam := chi.URLParam(r, "policyArn")
	policyArn, err := url.PathUnescape(policyArnParam)
	if err != nil {
		policyArn = policyArnParam
	}

	region := s.region(r)
	ctx := r.Context()

	if _, ok := s.requireAccessibleCluster(w, r, region, clusterName); !ok {
		return
	}

	if _, found, err := s.getAccessEntry(ctx, region, clusterName, principalArn); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	} else if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No access entry found for principalArn: %s", principalArn),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if _, found, err := s.getAssociatedAccessPolicy(ctx, region, clusterName, principalArn, policyArn); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	} else if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("No associated access policy found for policyArn: %s", policyArn),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if err := s.store.Delete(ctx, nsAccessPol, associatedAccessPolicyKey(region, clusterName, principalArn, policyArn)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) listAccessPolicies(w http.ResponseWriter, r *http.Request) {
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"accessPolicies": managedAccessPolicies(),
	})
}

func (s *Service) describeAccessPolicy(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	for _, policy := range managedAccessPolicies() {
		if policy["name"] == name {
			protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"accessPolicy": policy})
			return
		}
	}

	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("No access policy found for name: %s", name),
		HTTPStatus: http.StatusNotFound,
	})
}

func managedAccessPolicies() []map[string]any {
	return []map[string]any{
		{
			"name": "AmazonEKSAdminPolicy",
			"arn":  "arn:aws:eks::aws:cluster-access-policy/AmazonEKSAdminPolicy",
		},
		{
			"name": "AmazonEKSClusterAdminPolicy",
			"arn":  "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy",
		},
		{
			"name": "AmazonEKSEditPolicy",
			"arn":  "arn:aws:eks::aws:cluster-access-policy/AmazonEKSEditPolicy",
		},
		{
			"name": "AmazonEKSViewPolicy",
			"arn":  "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy",
		},
	}
}
