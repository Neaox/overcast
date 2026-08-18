// Package opensearch provides a basic emulation of Amazon OpenSearch Service.
//
// Implemented operations: CreateDomain, DescribeDomain, DescribeDomains,
// ListDomainNames, DeleteDomain, AddTags, ListTags, RemoveTags.
//
// Routes are AWS's own restJson1 bindings from the pinned model
// (opensearch-2021-01-01) rather than an emulator invention: the domain
// surface hangs off /2021-01-01/opensearch, while ListDomainNames and the
// three tag operations sit directly under /2021-01-01. See RegisterRoutes.
// The service answers on no other wire protocol — the model gives every
// operation an empty target prefix and restJson1 alone.
//
// Domains are created instantly and reported as active. Nothing runs behind
// the control plane, which is what router.TierInert records for this service:
// there is no search cluster, so DomainStatus.Endpoint names a host that
// serves nothing.
package opensearch

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	serviceName = "opensearch"

	// apiPrefix is the API-version prefix every OpenSearch binding carries.
	// It is OpenSearch's alone: no other service in the pinned models binds a
	// /2021-01-01 URI.
	apiPrefix = "/2021-01-01"

	// defaultEngineVersion is what a CreateDomain that omits EngineVersion
	// gets, standing in for AWS's current OpenSearch release.
	defaultEngineVersion = "OpenSearch_2.11"

	engineTypeOpenSearch    = "OpenSearch"
	engineTypeElasticsearch = "Elasticsearch"

	// elasticsearchVersionPrefix is how AWS spells a legacy Elasticsearch
	// engine version. CreateDomain's documented EngineVersion pattern admits
	// only "Elasticsearch_X.Y" and "OpenSearch_X.Y", so the prefix is what
	// decides a domain's EngineType.
	elasticsearchVersionPrefix = "Elasticsearch_"
)

// tagValidation gives the shared tag validator OpenSearch's own error codes:
// AddTags models LimitExceededException for exceeding the tag limit and
// ValidationException for a malformed key or value.
var tagValidation = serviceutil.TagValidationConfig{
	ExceededCode:    "LimitExceededException",
	ExceededMessage: "The request exceeded the maximum number of tags for this resource.",
	InvalidCode:     "ValidationException",
}

// ─── Wire shapes ──────────────────────────────────────────────

// DomainStatus is the subset of AWS's DomainStatus an inert domain can
// populate honestly. The modeled shape carries ~40 more members, all of them
// describing a cluster Overcast does not run.
type DomainStatus struct {
	DomainId      string `json:"DomainId"`
	DomainName    string `json:"DomainName"`
	ARN           string `json:"ARN"`
	EngineVersion string `json:"EngineVersion,omitempty"`
	Created       bool   `json:"Created"`
	Deleted       bool   `json:"Deleted"`
	Processing    bool   `json:"Processing"`
	Endpoint      string `json:"Endpoint,omitempty"`
}

// createDomainRequest is AWS's CreateDomainRequest. Every member is bound to
// the body; the operation takes no URI parameters. The model carries ~25 more
// members (ClusterConfig, EBSOptions, VPCOptions, …), all of which configure a
// cluster that does not exist here, so they are accepted and ignored.
type createDomainRequest struct {
	DomainName    string                `json:"DomainName"`
	EngineVersion string                `json:"EngineVersion"`
	TagList       []serviceutil.TagPair `json:"TagList"`
}

// domainStatusResponse is the body CreateDomain, DescribeDomain and
// DeleteDomain share.
type domainStatusResponse struct {
	DomainStatus *DomainStatus `json:"DomainStatus"`
}

type describeDomainsRequest struct {
	DomainNames []string `json:"DomainNames"`
}

type describeDomainsResponse struct {
	DomainStatusList []DomainStatus `json:"DomainStatusList"`
}

// domainInfo is AWS's DomainInfo, the element ListDomainNames returns.
type domainInfo struct {
	DomainName string `json:"DomainName"`
	EngineType string `json:"EngineType"`
}

type listDomainNamesResponse struct {
	DomainNames []domainInfo `json:"DomainNames"`
}

// addTagsRequest is AWS's AddTagsRequest: ARN and TagList are both body
// members, unlike ListTags, which carries its ARN in the query string.
type addTagsRequest struct {
	ARN     string                `json:"ARN"`
	TagList []serviceutil.TagPair `json:"TagList"`
}

type listTagsResponse struct {
	TagList []serviceutil.TagPair `json:"TagList"`
}

type removeTagsRequest struct {
	ARN     string   `json:"ARN"`
	TagKeys []string `json:"TagKeys"`
}

// ─── Errors ───────────────────────────────────────────────────

// notFoundError is ResourceNotFoundException. OpenSearch answers it with HTTP
// 409, not 404 — see the Errors section of the operation pages in the AWS
// OpenSearch Service API reference, which document 409 for
// ResourceNotFoundException, ResourceAlreadyExistsException and
// LimitExceededException, and 400 for ValidationException.
func notFoundError(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("Domain not found: %s", name),
		HTTPStatus: http.StatusConflict,
	}
}

func validationError(message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationException",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

// ─── Service ──────────────────────────────────────────────────

// Service implements router.Service for OpenSearch.
type Service struct {
	cfg   *config.Config
	log   *serviceutil.ServiceLogger
	store *osStore
}

// New returns a configured OpenSearch Service. Pure field assignment — no
// store reads or I/O (startup-budget rule).
func New(cfg *config.Config, st state.Store, logger *zap.Logger, _ clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:   cfg,
		log:   log,
		store: newOSStore(st, log),
	}
}

func (s *Service) Name() string { return serviceName }

// PathPrefixes satisfies router.PathPrefixService.
func (s *Service) PathPrefixes() []string { return []string{apiPrefix} }

// RegisterRoutes registers OpenSearch's modeled REST bindings.
//
// The paths are absolute rather than nested under a chi sub-router for
// apiPrefix, and that is deliberate. A sub-router owns its whole subtree, so
// every /2021-01-01 path it does not match would hit its own NotFound instead
// of the parent's generated fallback. OpenSearch declares eight of the ~100
// operations AWS binds under this prefix; the other ninety-odd must keep
// falling through to a protocol-correct 501.
//
// ListDomainNames and the tag operations sit directly under the version
// prefix while the rest of the domain surface sits under /opensearch. That
// asymmetry is AWS's, not a tidying opportunity: collapsing GET
// /2021-01-01/domain (list) onto POST /2021-01-01/opensearch/domain (create)
// is exactly the mistake #856 warns about.
//
// ListTags is registered twice, with and without a trailing slash, and both
// spellings are load-bearing. AWS binds it to GET /2021-01-01/tags/ — the only
// trailing-slash URI in the whole OpenSearch model — and that is what an
// unmodified AWS client sends. Registering the slash-less form alone left the
// operation unreachable: a signed client got a 501, and an unsigned one fell
// past OpenSearch into S3's wildcard object route and came back HTTP 404 with
// <Error><Code>NoSuchKey</Code>…</Error>, an S3 error for an OpenSearch call.
// That is #963's fault in a second service, fixed the way #966 fixed it in
// Backup. The slash-less spelling stays because callers were reaching it
// before the trailing-slash form existed, and dropping it now would trade one
// break for another. AddTags and RemoveTags are POSTs to different URIs and
// carry no slash in the model, so they need only one registration each.
func (s *Service) RegisterRoutes(r chi.Router) {
	// Domains
	r.Get(apiPrefix+"/domain", s.listDomainNames)
	r.Post(apiPrefix+"/opensearch/domain", s.createDomain)
	r.Get(apiPrefix+"/opensearch/domain/{DomainName}", s.describeDomain)
	r.Delete(apiPrefix+"/opensearch/domain/{DomainName}", s.deleteDomain)
	r.Post(apiPrefix+"/opensearch/domain-info", s.describeDomains)

	// Tags
	r.Post(apiPrefix+"/tags", s.addTags)
	r.Get(apiPrefix+"/tags", s.listTags)
	r.Get(apiPrefix+"/tags/", s.listTags)
	r.Post(apiPrefix+"/tags-removal", s.removeTags)
}

// ─── Handlers ─────────────────────────────────────────────────

func (s *Service) createDomain(w http.ResponseWriter, r *http.Request) {
	var req createDomainRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.DomainName == "" {
		protocol.WriteJSONError(w, r, validationError("DomainName is required"))
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	_, found, err := s.store.getDomain(r.Context(), region, req.DomainName)
	if err != nil {
		s.internalError(w, r, "CreateDomain", err)
		return
	}
	if found {
		// Domain names are unique per account per region, so a repeat create
		// is a conflict rather than an overwrite. AWS models
		// ResourceAlreadyExistsException for exactly this and answers 409.
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceAlreadyExistsException",
			Message:    fmt.Sprintf("Domain already exists: %s", req.DomainName),
			HTTPStatus: http.StatusConflict,
		})
		return
	}

	engineVersion := req.EngineVersion
	if engineVersion == "" {
		engineVersion = defaultEngineVersion
	}
	domain := &DomainStatus{
		DomainId:      fmt.Sprintf("%s/%s", s.cfg.AccountID, req.DomainName),
		DomainName:    req.DomainName,
		ARN:           domainARN(region, s.cfg.AccountID, req.DomainName),
		EngineVersion: engineVersion,
		Created:       true,
		Endpoint:      fmt.Sprintf("search-%s.%s.es.%s", req.DomainName, region, s.cfg.ExternalHostname()),
	}
	if err := s.store.putDomain(r.Context(), region, domain); err != nil {
		s.internalError(w, r, "CreateDomain", err)
		return
	}
	// TagList is applied at creation, as it is on AWS, so a ListTags that
	// follows a tagged CreateDomain sees the tags without a second call.
	if len(req.TagList) > 0 {
		if _, aerr := serviceutil.ApplyStoreTags(r.Context(), s.store.tags, domain.ARN,
			serviceutil.TagsFromList(req.TagList), tagValidation); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}
	protocol.WriteJSON(w, r, http.StatusOK, domainStatusResponse{DomainStatus: domain})
}

func (s *Service) describeDomain(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "DomainName")
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	domain, found, err := s.store.getDomain(r.Context(), region, name)
	if err != nil {
		s.internalError(w, r, "DescribeDomain", err)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, notFoundError(name))
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, domainStatusResponse{DomainStatus: domain})
}

func (s *Service) deleteDomain(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "DomainName")
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	domain, found, err := s.store.getDomain(r.Context(), region, name)
	if err != nil {
		s.internalError(w, r, "DeleteDomain", err)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, notFoundError(name))
		return
	}
	if err := s.store.deleteDomain(r.Context(), region, domain); err != nil {
		s.internalError(w, r, "DeleteDomain", err)
		return
	}
	// The response describes the domain as it is now: on its way out.
	domain.Deleted = true
	protocol.WriteJSON(w, r, http.StatusOK, domainStatusResponse{DomainStatus: domain})
}

func (s *Service) listDomainNames(w http.ResponseWriter, r *http.Request) {
	// EngineType is an httpQuery member named "engineType" — ListDomainNames
	// is a GET and has no body to carry it in.
	engineType := r.URL.Query().Get("engineType")
	switch engineType {
	case "", engineTypeOpenSearch, engineTypeElasticsearch:
	default:
		protocol.WriteJSONError(w, r, validationError(
			"engineType must be one of OpenSearch, Elasticsearch"))
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	domains, err := s.store.listDomains(r.Context(), region)
	if err != nil {
		s.internalError(w, r, "ListDomainNames", err)
		return
	}
	names := make([]domainInfo, 0, len(domains))
	for _, d := range domains {
		info := domainInfo{DomainName: d.DomainName, EngineType: engineTypeFor(d.EngineVersion)}
		if engineType != "" && info.EngineType != engineType {
			continue
		}
		names = append(names, info)
	}
	protocol.WriteJSON(w, r, http.StatusOK, listDomainNamesResponse{DomainNames: names})
}

func (s *Service) describeDomains(w http.ResponseWriter, r *http.Request) {
	var req describeDomainsRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if len(req.DomainNames) == 0 {
		protocol.WriteJSONError(w, r, validationError("DomainNames must specify at least one domain name"))
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)

	// A name that names no domain is omitted rather than raised: AWS models no
	// not-found error on this operation, and DomainStatusList is a required
	// response member, so the answer is always a list.
	list := make([]DomainStatus, 0, len(req.DomainNames))
	for _, name := range req.DomainNames {
		domain, found, err := s.store.getDomain(r.Context(), region, name)
		if err != nil {
			s.internalError(w, r, "DescribeDomains", err)
			return
		}
		if found {
			list = append(list, *domain)
		}
	}
	protocol.WriteJSON(w, r, http.StatusOK, describeDomainsResponse{DomainStatusList: list})
}

func (s *Service) addTags(w http.ResponseWriter, r *http.Request) {
	var req addTagsRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ARN == "" {
		protocol.WriteJSONError(w, r, validationError("ARN is required"))
		return
	}
	if _, aerr := serviceutil.ApplyStoreTags(r.Context(), s.store.tags, req.ARN,
		serviceutil.TagsFromList(req.TagList), tagValidation); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

func (s *Service) listTags(w http.ResponseWriter, r *http.Request) {
	// ARN is an httpQuery member named "arn", not a path segment and not a
	// body member: ListTags is GET /2021-01-01/tags?arn=… and has no body.
	arn := r.URL.Query().Get("arn")
	if arn == "" {
		protocol.WriteJSONError(w, r, validationError("arn is required"))
		return
	}
	tags, aerr := s.store.tags.Load(r.Context(), arn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, listTagsResponse{TagList: serviceutil.TagsToList(tags)})
}

func (s *Service) removeTags(w http.ResponseWriter, r *http.Request) {
	var req removeTagsRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ARN == "" {
		protocol.WriteJSONError(w, r, validationError("ARN is required"))
		return
	}
	if _, aerr := serviceutil.RemoveStoreTags(r.Context(), s.store.tags, req.ARN, req.TagKeys); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// ─── Helpers ──────────────────────────────────────────────────

// domainARN builds the ARN AWS gives a domain. The partition service name is
// "es", the same name OpenSearch requests are SigV4-signed for.
func domainARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:es:%s:%s:domain/%s", region, accountID, name)
}

// engineTypeFor derives DomainInfo.EngineType from a domain's engine version.
// AWS's EngineVersion is "Elasticsearch_X.Y" or "OpenSearch_X.Y", and
// EngineType says which of the two engines the domain runs — reporting
// OpenSearch for every domain made ListDomainNames' engineType filter answer
// with domains it was asked to exclude.
func engineTypeFor(engineVersion string) string {
	if strings.HasPrefix(engineVersion, elasticsearchVersionPrefix) {
		return engineTypeElasticsearch
	}
	return engineTypeOpenSearch
}

// internalError logs a store failure and answers with InternalException, the
// error AWS models for one, rather than the emulator's generic InternalError.
func (s *Service) internalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	s.log.Error("state store failure", zap.String("operation", operation), zap.Error(err))
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "InternalException",
		Message:    "Request processing failed because of an unknown error, exception, or internal failure.",
		HTTPStatus: http.StatusInternalServerError,
	})
}
