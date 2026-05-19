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

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "shield"

// Protection represents a Shield protection resource.
type Protection struct {
	ID          string `json:"Id"`
	Name        string `json:"Name"`
	ResourceArn string `json:"ResourceArn"`
}

// shieldStore provides state access for the Shield service.
type shieldStore struct {
	store state.Store
}

func newShieldStore(s state.Store) *shieldStore {
	return &shieldStore{store: s}
}

const nsProtections = "shield:protections"

func (s *shieldStore) putProtection(ctx context.Context, p *Protection) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsProtections, p.ID, string(raw))
}

func (s *shieldStore) getProtection(ctx context.Context, id string) (*Protection, bool) {
	raw, found, err := s.store.Get(ctx, nsProtections, id)
	if err != nil || !found {
		return nil, false
	}
	var p Protection
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, false
	}
	return &p, true
}

func (s *shieldStore) listProtections(ctx context.Context) ([]*Protection, error) {
	pairs, err := s.store.Scan(ctx, nsProtections, "")
	if err != nil {
		return nil, err
	}
	out := make([]*Protection, 0, len(pairs))
	for _, kv := range pairs {
		var p Protection
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
