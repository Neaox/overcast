package eks

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
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

// updateAddon serves POST /clusters/{clusterName}/addons/{addonName}/update —
// "update", not "updates", which is the whole of the #858 fault here.
func (s *Service) updateAddon(w http.ResponseWriter, r *http.Request) {
	req := &updateAddonRequest{}
	if !serviceutil.DecodeJSON(w, r, req) {
		return
	}
	// The labels are applied after the body so a body member of the same name
	// cannot displace the path the request was routed on.
	req.ClusterName = chi.URLParam(r, "name")
	req.AddonName = chi.URLParam(r, "addonName")
	out, aerr := s.updateAddonTyped(r.Context(), req)
	writeResult(w, r, out, aerr)
}

// addonCatalogEntry is one entry in the synthetic catalog of AWS-managed
// add-ons that DescribeAddonVersions lists and DescribeAddonConfiguration
// answers from.
//
// The two operations used to read separate tables, and the configuration table
// carried a single version per add-on — so a caller asking for the schema of a
// version DescribeAddonVersions had just advertised was answered with whatever
// version that other table held. One table means the two can no longer
// disagree.
//
// Type, Publisher and Owner are AWS's documented values for its own managed
// add-ons; the pinned model describes the members, not their contents, so they
// could not be read from it. They are here so the modeled `types`, `publishers`
// and `owners` filters can be honoured rather than silently ignored.
type addonCatalogEntry struct {
	Type      string
	Publisher string
	Owner     string
	// Versions is newest-first. The first entry is the default version.
	Versions []string
	// Schema is the configuration schema, which is synthetic and therefore the
	// same for every version of an add-on.
	Schema string
}

// addonCatalogClusterVersions are the Kubernetes versions every catalogued
// add-on declares compatibility with. They gate the modeled kubernetesVersion
// query filter.
var addonCatalogClusterVersions = []string{"1.30", "1.29"}

var addonCatalog = map[string]addonCatalogEntry{
	"vpc-cni": {
		Type: "networking", Publisher: "eks", Owner: "aws",
		Versions: []string{"v1.18.3-eksbuild.3", "v1.18.2-eksbuild.1", "v1.17.1-eksbuild.1"},
		Schema:   `{"$schema":"http://json-schema.org/draft-06/schema#","type":"object","properties":{"env":{"type":"object","properties":{"AWS_VPC_K8S_CNI_LOGLEVEL":{"type":"string"}}}}}`,
	},
	"coredns": {
		Type: "networking", Publisher: "eks", Owner: "aws",
		Versions: []string{"v1.11.3-eksbuild.1", "v1.11.1-eksbuild.11", "v1.10.1-eksbuild.11"},
		Schema:   `{"$schema":"http://json-schema.org/draft-06/schema#","type":"object","properties":{"replicaCount":{"type":"integer"}}}`,
	},
	"kube-proxy": {
		Type: "networking", Publisher: "eks", Owner: "aws",
		Versions: []string{"v1.30.3-eksbuild.5", "v1.29.7-eksbuild.9", "v1.28.13-eksbuild.2"},
		Schema:   `{"$schema":"http://json-schema.org/draft-06/schema#","type":"object","properties":{"mode":{"type":"string"}}}`,
	},
	"aws-ebs-csi-driver": {
		Type: "storage", Publisher: "eks", Owner: "aws",
		Versions: []string{"v1.35.0-eksbuild.1", "v1.34.0-eksbuild.1"},
		Schema:   `{"$schema":"http://json-schema.org/draft-06/schema#","type":"object","properties":{"controller":{"type":"object"}}}`,
	},
}

// describeAddonVersions serves GET /addons/supported-versions. Every input is
// an httpQuery member on a fixed path — the add-on name included, which is why
// there is no chi label to read.
func (s *Service) describeAddonVersions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	out, aerr := s.describeAddonVersionsTyped(r.Context(), &describeAddonVersionsRequest{
		AddonName:         query.Get("addonName"),
		KubernetesVersion: query.Get("kubernetesVersion"),
		Types:             query["types"],
		Publishers:        query["publishers"],
		Owners:            query["owners"],
		MaxResults:        serviceutil.QueryInt(r, "maxResults", 0),
		NextToken:         query.Get("nextToken"),
	})
	writeResult(w, r, out, aerr)
}

// describeAddonConfiguration serves GET /addons/configuration-schemas.
// addonName and addonVersion are both @required httpQuery members.
func (s *Service) describeAddonConfiguration(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	out, aerr := s.describeAddonConfigurationTyped(r.Context(), &describeAddonConfigurationRequest{
		AddonName:    query.Get("addonName"),
		AddonVersion: query.Get("addonVersion"),
	})
	writeResult(w, r, out, aerr)
}
