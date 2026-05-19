// Package appconfigdata provides the AWS AppConfig data plane emulation.
//
// Implemented operations: StartConfigurationSession, GetLatestConfiguration.
//
// The data plane retrieves configuration content stored via AppConfig's
// CreateHostedConfigurationVersion control-plane API. Configuration is
// delivered on the first poll after a new version is published; subsequent
// polls return an empty body until the version changes (matching AWS
// behaviour to avoid hot-loops in well-behaved SDKs).
//
// Routes: POST /_appconfigdata/configurationsessions
//
//	GET  /_appconfigdata/configuration/{token}
package appconfigdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/services/appconfig"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "appconfigdata"

// ─── Types ────────────────────────────────────────────────────────────────────

// session holds the state for one configuration polling session.
type session struct {
	Token           string `json:"Token"`
	AppID           string `json:"AppID"`
	EnvID           string `json:"EnvID"`
	ProfileID       string `json:"ProfileID"`
	LastVersionSeen int    `json:"LastVersionSeen"` // 0 = never delivered
}

// ─── Store ────────────────────────────────────────────────────────────────────

const nsSessions = "appconfigdata:sessions"

type dataStore struct {
	store state.Store
}

func (d *dataStore) putSession(ctx context.Context, s *session) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("appconfigdata: marshal session: %w", err)
	}
	return d.store.Set(ctx, nsSessions, s.Token, string(raw))
}

func (d *dataStore) getSession(ctx context.Context, token string) (*session, bool) {
	raw, found, err := d.store.Get(ctx, nsSessions, token)
	if err != nil || !found {
		return nil, false
	}
	var s session
	if json.Unmarshal([]byte(raw), &s) != nil {
		return nil, false
	}
	return &s, true
}

// ─── AppConfig accessor interface ────────────────────────────────────────────

// appConfigReader is the subset of appconfig.Service the data plane needs.
// Using an interface keeps the dependency testable and avoids import cycles.
type appConfigReader interface {
	ResolveApplication(ctx context.Context, identifier string) (*appconfig.Application, bool)
	ResolveEnvironment(ctx context.Context, appID, identifier string) (*appconfig.Environment, bool)
	ResolveProfile(ctx context.Context, appID, identifier string) (*appconfig.ConfigurationProfile, bool)
	LatestVersionNumber(ctx context.Context, appID, profID string) (int, error)
	GetHostedConfigVersionByNum(ctx context.Context, appID, profID string, version int) (*appconfig.HostedConfigurationVersion, bool)
}

// ─── Service ──────────────────────────────────────────────────────────────────

// Service implements router.Service for AppConfigData.
type Service struct {
	log     *serviceutil.ServiceLogger
	store   *dataStore
	ac      appConfigReader
	typedOp map[string]op.Operation
}

// New returns a configured AppConfigData Service.
// ac must be the AppConfig Service instance already registered in the router.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, _ clock.Clock, ac appConfigReader) *Service {
	s := &Service{
		log:   serviceutil.NewServiceLogger(logger, serviceName),
		store: &dataStore{store: st},
		ac:    ac,
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string { return serviceName }

func (s *Service) TargetPrefix() string { return "AppConfigData." }

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

// RegisterRoutes registers the AppConfigData data-plane endpoints.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Route("/_appconfigdata", func(r chi.Router) {
		r.Post("/configurationsessions", s.startConfigurationSession)
		r.Get("/configuration/{token}", s.getLatestConfiguration)
	})
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (s *Service) startConfigurationSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ApplicationIdentifier          string `json:"ApplicationIdentifier"`
		EnvironmentIdentifier          string `json:"EnvironmentIdentifier"`
		ConfigurationProfileIdentifier string `json:"ConfigurationProfileIdentifier"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()

	// Resolve application.
	app, found := s.ac.ResolveApplication(ctx, req.ApplicationIdentifier)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Application not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	// Resolve environment.
	if _, found := s.ac.ResolveEnvironment(ctx, app.ID, req.EnvironmentIdentifier); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Environment not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	// Resolve configuration profile.
	prof, found := s.ac.ResolveProfile(ctx, app.ID, req.ConfigurationProfileIdentifier)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Configuration profile not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	token := newToken()
	sess := &session{
		Token:           token,
		AppID:           app.ID,
		EnvID:           req.EnvironmentIdentifier,
		ProfileID:       prof.ID,
		LastVersionSeen: 0,
	}
	if err := s.store.putSession(ctx, sess); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, map[string]any{
		"InitialConfigurationToken": token,
	})
}

func (s *Service) getLatestConfiguration(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	ctx := r.Context()

	sess, found := s.store.getSession(ctx, token)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "BadRequestException", Message: "Invalid or expired configuration token",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// What is the latest version available?
	latestVersion, err := s.ac.LatestVersionNumber(ctx, sess.AppID, sess.ProfileID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	// Rotate the token — each call must use the returned NextPollConfigurationToken.
	nextToken := newToken()
	updatedSess := &session{
		Token:           nextToken,
		AppID:           sess.AppID,
		EnvID:           sess.EnvID,
		ProfileID:       sess.ProfileID,
		LastVersionSeen: latestVersion,
	}
	if err := s.store.putSession(ctx, updatedSess); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	// Invalidate the old token so it cannot be replayed.
	_ = s.store.store.Delete(ctx, nsSessions, token)

	w.Header().Set("Next-Poll-Configuration-Token", nextToken)
	w.Header().Set("Next-Poll-Interval-In-Seconds", "60")

	// If no version exists yet or version hasn't changed, return empty body.
	if latestVersion == 0 || latestVersion == sess.LastVersionSeen {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Fetch the latest version content.
	hcv, found := s.ac.GetHostedConfigVersionByNum(ctx, sess.AppID, sess.ProfileID, latestVersion)
	if !found {
		// Version counter exists but content was deleted — return empty.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", hcv.ContentType)
	w.Header().Set("AppConfig-Configuration-Version", strconv.Itoa(hcv.VersionNumber))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(hcv.Content))
}

// newToken generates an opaque random session token.
func newToken() string {
	return uuid.NewString()
}
