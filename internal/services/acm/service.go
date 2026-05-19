// Package acm provides a basic emulation of AWS Certificate Manager (ACM).
//
// Implemented operations: RequestCertificate, DescribeCertificate,
// ListCertificates, DeleteCertificate, ListTagsForCertificate,
// AddTagsToCertificate, RemoveTagsFromCertificate.
//
// Certificates are issued immediately in ISSUED state — no validation required.
package acm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "acm"

// Certificate represents an ACM certificate.
type Certificate struct {
	CertificateArn          string   `json:"CertificateArn"`
	DomainName              string   `json:"DomainName"`
	SubjectAlternativeNames []string `json:"SubjectAlternativeNames,omitempty"`
	Status                  string   `json:"Status"`
	Type                    string   `json:"Type"`
	CreatedAt               float64  `json:"CreatedAt"`
	IssuedAt                float64  `json:"IssuedAt,omitempty"`
}

// Tag is an ACM resource tag.
type Tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value,omitempty"`
}

// ─── Store ────────────────────────────────────────────────────

type acmStore struct {
	store state.Store
	cfg   *config.Config
	clk   clock.Clock
}

func newACMStore(s state.Store, cfg *config.Config, clk clock.Clock) *acmStore {
	return &acmStore{store: s, cfg: cfg, clk: clk}
}

const (
	nsCerts = "acm:certs"
	nsTags  = "acm:tags"
)

func (s *acmStore) putCert(ctx context.Context, c *Certificate) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsCerts, c.CertificateArn, string(raw))
}

func (s *acmStore) getCert(ctx context.Context, arn string) (*Certificate, bool) {
	raw, found, err := s.store.Get(ctx, nsCerts, arn)
	if err != nil || !found {
		return nil, false
	}
	var c Certificate
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, false
	}
	return &c, true
}

func (s *acmStore) listCerts(ctx context.Context) ([]*Certificate, error) {
	pairs, err := s.store.Scan(ctx, nsCerts, "")
	if err != nil {
		return nil, err
	}
	out := make([]*Certificate, 0, len(pairs))
	for _, kv := range pairs {
		var c Certificate
		if err := json.Unmarshal([]byte(kv.Value), &c); err != nil {
			continue
		}
		out = append(out, &c)
	}
	return out, nil
}

func (s *acmStore) deleteCert(ctx context.Context, arn string) error {
	_ = s.store.Delete(ctx, nsTags, arn)
	return s.store.Delete(ctx, nsCerts, arn)
}

func (s *acmStore) setTags(ctx context.Context, arn string, tags []Tag) error {
	raw, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsTags, arn, string(raw))
}

func (s *acmStore) getTags(ctx context.Context, arn string) ([]Tag, error) {
	raw, found, err := s.store.Get(ctx, nsTags, arn)
	if err != nil {
		return nil, err
	}
	if !found {
		return []Tag{}, nil
	}
	var tags []Tag
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

// ─── Service ──────────────────────────────────────────────────

// Service implements router.Service and router.TargetDispatcher for ACM.
type Service struct {
	handler *Handler
	log     *serviceutil.ServiceLogger
	store   *acmStore
	cfg     *config.Config
	clk     clock.Clock
}

// New returns a configured ACM Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	store := newACMStore(st, cfg, clk)
	return &Service{
		log:     serviceutil.NewServiceLogger(logger, serviceName),
		handler: newHandler(cfg, store, clk),
		store:   store,
		cfg:     cfg,
		clk:     clk,
	}
}

func (s *Service) Name() string                { return serviceName }
func (s *Service) RegisterRoutes(_ chi.Router) {}
func (s *Service) TargetPrefix() string        { return "CertificateManager." }

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "ACM does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if c.Name() != codec.NameRPCv2CBOR {
			if fn, ok := s.handler.ops[opName]; ok {
				fn(w, r)
				return
			}
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, &protocol.AWSError{
			Code:       "UnknownOperationException",
			Message:    "Unknown ACM operation: " + opName,
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	target := r.Header.Get("X-Amz-Target")
	op := target
	if idx := strings.LastIndex(target, "."); idx >= 0 {
		op = target[idx+1:]
	}
	if fn, ok := s.handler.ops[op]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

// mergeTags returns existing plus overrides, with overrides winning on key collisions.
func mergeTags(existing, overrides []Tag) []Tag {
	m := make(map[string]string, len(existing)+len(overrides))
	for _, t := range existing {
		m[t.Key] = t.Value
	}
	for _, t := range overrides {
		m[t.Key] = t.Value
	}
	out := make([]Tag, 0, len(m))
	for k, v := range m {
		out = append(out, Tag{Key: k, Value: v})
	}
	return out
}

func removeTagKeys(existing []Tag, keys []string) []Tag {
	if len(keys) == 0 {
		return existing
	}
	remove := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		remove[k] = struct{}{}
	}
	kept := make([]Tag, 0, len(existing))
	for _, t := range existing {
		if _, drop := remove[t.Key]; !drop {
			kept = append(kept, t)
		}
	}
	return kept
}
