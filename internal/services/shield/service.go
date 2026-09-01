// Package shield provides a basic emulation of AWS Shield (DDoS protection).
//
// Implemented operations: DescribeSubscription, CreateProtection,
// ListProtections, DeleteProtection, DescribeProtection.
package shield

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

const serviceName = "shield"

// Protection represents a Shield protection resource. This is the wire
// shape — the AWS model's Protection carries no Tags member, so tags must
// never be embedded here. See protectionRecord for how tags are persisted.
type Protection struct {
	ID          string `json:"Id"`
	Name        string `json:"Name"`
	ResourceArn string `json:"ResourceArn"`
}

// protectionRecord is a Protection as persisted: the wire shape plus its
// tags. Tags are kept off ListProtections/DescribeProtection (the model
// keeps them off Protection) and exposed only through TagResource,
// UntagResource and ListTagsForResource.
type protectionRecord struct {
	Protection
	Tags map[string]string `json:"overcastTags,omitempty"`
}

func (p *protectionRecord) GetTags() map[string]string  { return p.Tags }
func (p *protectionRecord) SetTags(t map[string]string) { p.Tags = t }

// shieldStore provides state access for the Shield service.
type shieldStore struct {
	store state.Store
}

func newShieldStore(s state.Store) *shieldStore {
	return &shieldStore{store: s}
}

const nsProtections = "shield:protections"

func (s *shieldStore) putProtection(ctx context.Context, p *protectionRecord) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsProtections, p.ID, string(raw))
}

func (s *shieldStore) getProtection(ctx context.Context, id string) (*protectionRecord, bool) {
	raw, found, err := s.store.Get(ctx, nsProtections, id)
	if err != nil || !found {
		return nil, false
	}
	var p protectionRecord
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, false
	}
	return &p, true
}

func (s *shieldStore) listProtections(ctx context.Context) ([]*protectionRecord, error) {
	pairs, err := s.store.Scan(ctx, nsProtections, "")
	if err != nil {
		return nil, err
	}
	out := make([]*protectionRecord, 0, len(pairs))
	for _, kv := range pairs {
		var p protectionRecord
		if err := json.Unmarshal([]byte(kv.Value), &p); err != nil {
			continue
		}
		out = append(out, &p)
	}
	return out, nil
}

func (s *shieldStore) deleteProtection(ctx context.Context, id string) error {
	return s.store.Delete(ctx, nsProtections, id)
}

// Service implements router.Service and router.TargetDispatcher for Shield.
type Service struct {
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured Shield Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, _ clock.Clock) *Service {
	return &Service{
		log:     serviceutil.NewServiceLogger(logger, serviceName),
		handler: newHandler(st),
	}
}

func (s *Service) Name() string                { return serviceName }
func (s *Service) RegisterRoutes(_ chi.Router) {}
func (s *Service) TargetPrefix() string        { return "AWSShield_20160616." }

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "Shield does not support wire protocol " + c.Name() + ".",
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
			Message:    "Unknown Shield operation: " + opName,
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
