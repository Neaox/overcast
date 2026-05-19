// Package secretsmanager provides emulation of AWS Secrets Manager.
// See docs/services/secretsmanager.md for the support matrix.
package secretsmanager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "secretsmanager"

// Service implements router.Service for Secrets Manager.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured Secrets Manager Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:     cfg,
		store:   store,
		log:     log,
		handler: newHandler(cfg, store, log, clk),
	}
}

// InitBus wires the event bus for secret lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// Name returns the service identifier.
func (s *Service) Name() string { return serviceName }

// TargetPrefix returns the X-Amz-Target prefix for Secrets Manager dispatch.
func (s *Service) TargetPrefix() string { return "secretsmanager." }

// RegisterRoutes mounts admin endpoints for the web console.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Get("/_overcast/secretsmanager/secrets", s.adminListSecrets)
	r.Post("/_overcast/secretsmanager/secrets", s.adminCreateSecret)
	r.Get("/_overcast/secretsmanager/secrets/{secretId}/value", s.adminGetSecretValue)
	r.Put("/_overcast/secretsmanager/secrets/{secretId}/value", s.adminUpdateSecretValue)
	r.Delete("/_overcast/secretsmanager/secrets/{secretId}", s.adminDeleteSecret)
}

// Dispatch routes to the correct Secrets Manager handler based on X-Amz-Target.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "Secrets Manager does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		// Preserve AWS JSON 1.1 on the existing handler path until JSON
		// wire-byte goldens cover Secrets Manager. CBOR uses typed ops.
		if c.Name() != codec.NameRPCv2CBOR {
			s.dispatchLegacy(w, r, opName)
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, &protocol.AWSError{
			Code:       "UnknownOperationException",
			Message:    "Unknown operation: " + opName,
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	target := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "secretsmanager.")
	s.dispatchLegacy(w, r, target)
}

func (s *Service) dispatchLegacy(w http.ResponseWriter, r *http.Request, target string) {
	if fn, ok := s.handler.ops[target]; ok {
		fn(w, r)
		return
	}
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "UnknownOperationException",
		Message:    "Unknown operation: " + target,
		HTTPStatus: http.StatusBadRequest,
	})
}

// ─── Admin handlers (web console) ──────────────────────────────────────────

func (s *Service) adminListSecrets(w http.ResponseWriter, r *http.Request) {
	secrets, aerr := s.handler.store.listSecrets(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	type secretOut struct {
		Name            string  `json:"name"`
		ARN             string  `json:"arn"`
		Description     string  `json:"description,omitempty"`
		CreatedDate     float64 `json:"createdDate"`
		LastChangedDate float64 `json:"lastChangedDate"`
		RotationEnabled bool    `json:"rotationEnabled"`
	}
	out := make([]secretOut, 0, len(secrets))
	for _, sec := range secrets {
		out = append(out, secretOut{
			Name:            sec.Name,
			ARN:             sec.ARN,
			Description:     sec.Description,
			CreatedDate:     sec.CreatedDate,
			LastChangedDate: sec.LastChangedDate,
			RotationEnabled: sec.RotationEnabled,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"secrets": out})
}

func (s *Service) adminCreateSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		SecretString string `json:"secretString"`
		Description  string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("name is required"))
		return
	}
	ctx := r.Context()
	if _, aerr := s.handler.store.getSecret(ctx, req.Name); aerr == nil {
		protocol.WriteJSONError(w, r, errResourceExists(req.Name))
		return
	}

	now := s.handler.store.now()
	versionId := uuid.New().String()
	arn := protocol.ARN(middleware.RegionFromContext(r.Context(), s.cfg.Region), s.cfg.AccountID, "secretsmanager", fmt.Sprintf("secret:%s", req.Name))

	version := SecretVersion{
		VersionId:    versionId,
		SecretString: req.SecretString,
		Stages:       []string{"AWSCURRENT"},
		CreatedDate:  float64(now.Unix()),
	}
	sec := &Secret{
		ARN:              arn,
		Name:             req.Name,
		Description:      req.Description,
		Versions:         []SecretVersion{version},
		CurrentVersionId: versionId,
		CreatedDate:      float64(now.Unix()),
		LastChangedDate:  float64(now.Unix()),
	}
	if aerr := s.handler.store.putSecret(ctx, sec); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.log.Info("secret created (admin)", zap.String("name", req.Name))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name": req.Name, "arn": arn, "versionId": versionId,
	})
}

func (s *Service) adminGetSecretValue(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "secretId")
	secretId, err := url.PathUnescape(raw)
	if err != nil {
		secretId = raw
	}
	sec, aerr := s.handler.store.resolveSecret(r.Context(), secretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	for _, v := range sec.Versions {
		for _, st := range v.Stages {
			if st == "AWSCURRENT" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"secretId":     sec.Name,
					"versionId":    v.VersionId,
					"secretString": v.SecretString,
					"secretBinary": v.SecretBinary,
					"stages":       v.Stages,
				})
				return
			}
		}
	}
	protocol.WriteJSONError(w, r, errResourceNotFound(secretId))
}

func (s *Service) adminUpdateSecretValue(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "secretId")
	secretId, err := url.PathUnescape(raw)
	if err != nil {
		secretId = raw
	}
	var req struct {
		SecretString string `json:"secretString"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid JSON body"))
		return
	}
	ctx := r.Context()
	sec, aerr := s.handler.store.resolveSecret(ctx, secretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	now := s.handler.store.now()
	versionId := uuid.New().String()
	for i := range sec.Versions {
		for j, st := range sec.Versions[i].Stages {
			if st == "AWSCURRENT" {
				sec.Versions[i].Stages[j] = "AWSPREVIOUS"
			}
		}
	}
	sec.Versions = append(sec.Versions, SecretVersion{
		VersionId:    versionId,
		SecretString: req.SecretString,
		Stages:       []string{"AWSCURRENT"},
		CreatedDate:  float64(now.Unix()),
	})
	sec.CurrentVersionId = versionId
	sec.LastChangedDate = float64(now.Unix())
	if aerr := s.handler.store.putSecret(ctx, sec); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"secretId":  sec.Name,
		"versionId": versionId,
	})
}

func (s *Service) adminDeleteSecret(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "secretId")
	secretId, err := url.PathUnescape(raw)
	if err != nil {
		secretId = raw
	}
	sec, aerr := s.handler.store.resolveSecret(r.Context(), secretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := s.handler.store.deleteSecret(r.Context(), sec.Name); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.log.Info("secret deleted (admin)", zap.String("name", sec.Name))
	w.WriteHeader(http.StatusNoContent)
}
