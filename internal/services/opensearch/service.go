// Package opensearch provides a basic emulation of Amazon OpenSearch Service.
//
// Implemented operations: CreateDomain, DescribeDomain, ListDomainNames,
// DeleteDomain, DescribeDomains (batch), ListTags, AddTags, RemoveTags.
//
// Domains are created instantly in "Active" state.
package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "opensearch"

// ─── Types ────────────────────────────────────────────────────

// DomainStatus represents an OpenSearch domain.
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

// ─── Store ────────────────────────────────────────────────────

type osStore struct {
	store state.Store
	cfg   *config.Config
	clk   clock.Clock
}

func newOSStore(s state.Store, cfg *config.Config, clk clock.Clock) *osStore {
	return &osStore{store: s, cfg: cfg, clk: clk}
}

const (
	nsDomains = "opensearch:domains"
	nsTags    = "opensearch:tags"
)

func (s *osStore) putDomain(ctx context.Context, d *DomainStatus) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsDomains, d.DomainName, string(raw))
}

func (s *osStore) getDomain(ctx context.Context, name string) (*DomainStatus, bool) {
	raw, found, err := s.store.Get(ctx, nsDomains, name)
	if err != nil || !found {
		return nil, false
	}
	var d DomainStatus
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, false
	}
	return &d, true
}

func (s *osStore) listDomains(ctx context.Context) ([]*DomainStatus, error) {
	pairs, err := s.store.Scan(ctx, nsDomains, "")
	if err != nil {
		return nil, err
	}
	out := make([]*DomainStatus, 0, len(pairs))
	for _, kv := range pairs {
		var d DomainStatus
		if err := json.Unmarshal([]byte(kv.Value), &d); err != nil {
			continue
		}
		out = append(out, &d)
	}
	return out, nil
}

func (s *osStore) deleteDomain(ctx context.Context, name string) error {
	_ = s.store.Delete(ctx, nsTags, name)
	return s.store.Delete(ctx, nsDomains, name)
}

func (s *osStore) setTags(ctx context.Context, arn string, tags map[string]string) error {
	raw, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsTags, arn, string(raw))
}

func (s *osStore) getTags(ctx context.Context, arn string) (map[string]string, error) {
	raw, found, err := s.store.Get(ctx, nsTags, arn)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]string{}, nil
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

// ─── Service ──────────────────────────────────────────────────

// Service implements router.Service for OpenSearch.
type Service struct {
	log     *serviceutil.ServiceLogger
	store   *osStore
	cfg     *config.Config
	clk     clock.Clock
	typedOp map[string]op.Operation
}

// New returns a configured OpenSearch Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		log:   serviceutil.NewServiceLogger(logger, serviceName),
		store: newOSStore(st, cfg, clk),
		cfg:   cfg,
		clk:   clk,
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string { return serviceName }

// TargetPrefix satisfies router.TargetDispatcher.
func (s *Service) TargetPrefix() string { return "OpenSearch." }

// Dispatch satisfies router.TargetDispatcher.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if codec.Supports(s.SupportedProtocols(), c) {
			if typed, ok := s.typedOp[opName]; ok {
				typed.Invoke(w, r, c)
				return
			}
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

// RegisterRoutes registers the REST endpoints for OpenSearch.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Route("/_opensearch", func(r chi.Router) {
		// Domain CRUD
		r.Post("/domain", s.createDomain)
		r.Get("/domain/{domainName}", s.describeDomain)
		r.Delete("/domain/{domainName}", s.deleteDomain)
		r.Get("/domain", s.listDomainNames)
		r.Post("/domain/describe", s.describeDomains)

		// Tags
		r.Post("/tags", s.addTags)
		r.Get("/tags", s.listTags)
		r.Post("/tags-removal", s.removeTags)
	})
}

// ─── Handlers ─────────────────────────────────────────────────

func (s *Service) createDomain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainName    string `json:"DomainName"`
		EngineVersion string `json:"EngineVersion"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.DomainName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ValidationException", Message: "DomainName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)
	arn := fmt.Sprintf("arn:aws:es:%s:%s:domain/%s", region, s.cfg.AccountID, req.DomainName)
	domainID := fmt.Sprintf("%s/%s", s.cfg.AccountID, req.DomainName)

	ev := req.EngineVersion
	if ev == "" {
		ev = "OpenSearch_2.11"
	}

	domain := &DomainStatus{
		DomainId:      domainID,
		DomainName:    req.DomainName,
		ARN:           arn,
		EngineVersion: ev,
		Created:       true,
		Deleted:       false,
		Processing:    false,
		Endpoint:      fmt.Sprintf("search-%s.%s.es.%s", req.DomainName, region, s.cfg.ExternalHostname()),
	}
	if err := s.store.putDomain(r.Context(), domain); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"DomainStatus": domain})
}

func (s *Service) describeDomain(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "domainName")
	domain, found := s.store.getDomain(r.Context(), name)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Domain %s not found", name),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"DomainStatus": domain})
}

func (s *Service) deleteDomain(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "domainName")
	domain, found := s.store.getDomain(r.Context(), name)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Domain %s not found", name),
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	domain.Deleted = true
	if err := s.store.deleteDomain(r.Context(), name); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"DomainStatus": domain})
}

func (s *Service) listDomainNames(w http.ResponseWriter, r *http.Request) {
	domains, err := s.store.listDomains(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	names := make([]map[string]string, 0, len(domains))
	for _, d := range domains {
		names = append(names, map[string]string{
			"DomainName": d.DomainName,
			"EngineType": "OpenSearch",
		})
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"DomainNames": names})
}

func (s *Service) describeDomains(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainNames []string `json:"DomainNames"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	var results []*DomainStatus
	for _, name := range req.DomainNames {
		if d, found := s.store.getDomain(r.Context(), name); found {
			results = append(results, d)
		}
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"DomainStatusList": results})
}

func (s *Service) addTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ARN     string            `json:"ARN"`
		TagList []json.RawMessage `json:"TagList"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	tags, err := s.store.getTags(r.Context(), req.ARN)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	for _, raw := range req.TagList {
		var tag struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		}
		if json.Unmarshal(raw, &tag) == nil {
			tags[tag.Key] = tag.Value
		}
	}
	if err := s.store.setTags(r.Context(), req.ARN, tags); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) listTags(w http.ResponseWriter, r *http.Request) {
	arn := r.URL.Query().Get("arn")
	tags, err := s.store.getTags(r.Context(), arn)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	tagList := make([]map[string]string, 0, len(tags))
	for k, v := range tags {
		tagList = append(tagList, map[string]string{"Key": k, "Value": v})
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"TagList": tagList})
}

func (s *Service) removeTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ARN     string   `json:"ARN"`
		TagKeys []string `json:"TagKeys"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	tags, err := s.store.getTags(r.Context(), req.ARN)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	for _, k := range req.TagKeys {
		delete(tags, k)
	}
	if err := s.store.setTags(r.Context(), req.ARN, tags); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}
