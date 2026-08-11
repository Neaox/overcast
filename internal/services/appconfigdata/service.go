// Package appconfigdata provides the AWS AppConfig data plane emulation.
//
// Implemented operations: StartConfigurationSession, GetLatestConfiguration.
//
// Routes are AWS's own REST-JSON bindings from the pinned Smithy model
// (appconfigdata-2021-11-11): POST /configurationsessions and
// GET /configuration. The session token is a query member on the second, not a
// path segment — see RegisterRoutes and getLatestConfiguration.
//
// The data plane serves configuration content stored through AppConfig's
// CreateHostedConfigurationVersion control-plane API. Content is delivered on
// the first poll after a new version is published; later polls answer with an
// empty payload until the version changes, which is what AWS does and what
// keeps a well-behaved polling SDK from re-applying unchanged configuration.
package appconfigdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/services/appconfig"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	serviceName = "appconfigdata"

	// configurationTokenParam is the name AWS binds GetLatestConfiguration's
	// ConfigurationToken member to (@httpQuery("configuration_token")).
	configurationTokenParam = "configuration_token"

	// defaultPollIntervalSeconds is reported in Next-Poll-Interval-In-Seconds
	// for a session that set no RequiredMinimumPollIntervalInSeconds. The model
	// gives the member no default, so this is Overcast's own choice and matches
	// what the service returned before the operation was reachable.
	defaultPollIntervalSeconds = 60

	// minPollIntervalSeconds and maxPollIntervalSeconds are the bounds the
	// model puts on RequiredMinimumPollIntervalInSeconds (OptionalPollSeconds
	// carries @range(min: 15, max: 86400)).
	minPollIntervalSeconds = 15
	maxPollIntervalSeconds = 86400

	// maxIdentifierLength is the model's Identifier @length max, which every
	// StartConfigurationSession member is typed as.
	maxIdentifierLength = 128

	// tokenLifetime is how long a configuration token stays usable. AWS
	// documents 24 hours on both InitialConfigurationToken and
	// NextPollConfigurationToken, and answers BadRequestException past it.
	tokenLifetime = 24 * time.Hour
)

// ─── Types ────────────────────────────────────────────────────────────────────

// session holds the state behind one configuration token. Each token is
// single-use: GetLatestConfiguration mints a successor and drops the one it
// was given.
type session struct {
	Token           string `json:"Token"`
	AppID           string `json:"AppID"`
	EnvID           string `json:"EnvID"`
	ProfileID       string `json:"ProfileID"`
	LastVersionSeen int    `json:"LastVersionSeen"` // 0 = never delivered
	// MinimumPollSeconds is the session's RequiredMinimumPollIntervalInSeconds,
	// 0 when the caller did not ask for one. It both constrains how soon the
	// next poll may come and is what the response reports.
	MinimumPollSeconds int `json:"MinimumPollSeconds,omitempty"`
	// IssuedAt is when this token was minted, for the 24-hour lifetime and the
	// minimum-poll-interval check.
	IssuedAt time.Time `json:"IssuedAt"`
	// FromPoll marks a token minted by GetLatestConfiguration rather than by
	// StartConfigurationSession. Only those are subject to MinimumPollSeconds:
	// the constraint is on the gap between polls, so the first one is free.
	FromPoll bool `json:"FromPoll,omitempty"`
}

// pollIntervalSeconds is the value reported in Next-Poll-Interval-In-Seconds.
func (s *session) pollIntervalSeconds() int {
	if s.MinimumPollSeconds > 0 {
		return s.MinimumPollSeconds
	}
	return defaultPollIntervalSeconds
}

// ─── Store ────────────────────────────────────────────────────────────────────

const nsSessions = "appconfigdata:sessions"

type dataStore struct {
	store state.Store
	log   *serviceutil.ServiceLogger
}

func (d *dataStore) put(ctx context.Context, s *session) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("appconfigdata: marshal session: %w", err)
	}
	return d.store.Set(ctx, nsSessions, s.Token, string(raw))
}

// get returns the session a token names.
//
// A store failure is returned as an error so the caller can answer
// InternalServerException; one undecodable record is reported as absent and
// logged, which reaches the client as the same BadRequestException an unknown
// token gets rather than a 500 (AGENTS.md § State).
func (d *dataStore) get(ctx context.Context, token string) (*session, bool, error) {
	raw, found, err := d.store.Get(ctx, nsSessions, token)
	if err != nil {
		return nil, false, fmt.Errorf("appconfigdata: read session: %w", err)
	}
	if !found {
		return nil, false, nil
	}
	var s session
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		d.log.Warn("skipping malformed session record", zap.Error(err))
		return nil, false, nil
	}
	return &s, true, nil
}

func (d *dataStore) delete(ctx context.Context, token string) error {
	return d.store.Delete(ctx, nsSessions, token)
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
	log   *serviceutil.ServiceLogger
	clk   clock.Clock
	store *dataStore
	ac    appConfigReader
}

// New returns a configured AppConfigData Service.
// ac must be the AppConfig Service instance already registered in the router.
func New(_ *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock, ac appConfigReader) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		log:   log,
		clk:   clk,
		store: &dataStore{store: st, log: log},
		ac:    ac,
	}
}

func (s *Service) Name() string { return serviceName }

// RegisterRoutes registers the AppConfigData data plane at the bindings the
// pinned model gives it. Both are root-level paths, which is why
// detectService claims them explicitly — see internal/middleware/logger.go.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Post("/configurationsessions", s.startConfigurationSession)
	r.Get("/configuration", s.getLatestConfiguration)
}

// PathPrefixes satisfies router.PathPrefixService.
func (s *Service) PathPrefixes() []string {
	return []string{"/configuration", "/configurationsessions"}
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

// startConfigurationSession serves POST /configurationsessions. The model
// binds every member of StartConfigurationSessionRequest to the JSON body —
// there is no path, query or header member — and answers 201.
func (s *Service) startConfigurationSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ApplicationIdentifier          string `json:"ApplicationIdentifier"`
		EnvironmentIdentifier          string `json:"EnvironmentIdentifier"`
		ConfigurationProfileIdentifier string `json:"ConfigurationProfileIdentifier"`
		// A pointer so an omitted member is distinguishable from an explicit
		// zero, which is out of range and has to be rejected.
		RequiredMinimumPollIntervalInSeconds *int `json:"RequiredMinimumPollIntervalInSeconds"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	for _, member := range []struct{ name, value string }{
		{"ApplicationIdentifier", req.ApplicationIdentifier},
		{"EnvironmentIdentifier", req.EnvironmentIdentifier},
		{"ConfigurationProfileIdentifier", req.ConfigurationProfileIdentifier},
	} {
		if aerr := validateIdentifier(member.name, member.value); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}

	minimumPoll := 0
	if req.RequiredMinimumPollIntervalInSeconds != nil {
		minimumPoll = *req.RequiredMinimumPollIntervalInSeconds
		if minimumPoll < minPollIntervalSeconds || minimumPoll > maxPollIntervalSeconds {
			protocol.WriteJSONError(w, r, badRequest(fmt.Sprintf(
				"RequiredMinimumPollIntervalInSeconds must be between %d and %d.",
				minPollIntervalSeconds, maxPollIntervalSeconds)))
			return
		}
	}

	ctx := r.Context()
	app, found := s.ac.ResolveApplication(ctx, req.ApplicationIdentifier)
	if !found {
		protocol.WriteJSONError(w, r, notFound("Application"))
		return
	}
	if _, found := s.ac.ResolveEnvironment(ctx, app.ID, req.EnvironmentIdentifier); !found {
		protocol.WriteJSONError(w, r, notFound("Environment"))
		return
	}
	prof, found := s.ac.ResolveProfile(ctx, app.ID, req.ConfigurationProfileIdentifier)
	if !found {
		protocol.WriteJSONError(w, r, notFound("ConfigurationProfile"))
		return
	}

	sess := &session{
		Token:              newToken(),
		AppID:              app.ID,
		EnvID:              req.EnvironmentIdentifier,
		ProfileID:          prof.ID,
		MinimumPollSeconds: minimumPoll,
		IssuedAt:           s.clk.Now(),
	}
	if err := s.store.put(ctx, sess); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, map[string]any{
		"InitialConfigurationToken": sess.Token,
	})
}

// getLatestConfiguration serves GET /configuration.
//
// ConfigurationToken is the operation's only request member and the model
// binds it to the query string as configuration_token — not to a path segment,
// which is the shape Overcast invented, and not to a header. The response is
// equally unlike a JSON envelope: the configuration is the httpPayload and
// every other output member is a header.
func (s *Service) getLatestConfiguration(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get(configurationTokenParam)
	if token == "" {
		protocol.WriteJSONError(w, r, badRequest("ConfigurationToken is required."))
		return
	}

	ctx := r.Context()
	sess, found, err := s.store.get(ctx, token)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, badRequest("Invalid or expired configuration token."))
		return
	}

	now := s.clk.Now()
	// A record written before IssuedAt existed carries the zero time; treating
	// that as infinitely old would expire every session an upgrade inherits.
	if !sess.IssuedAt.IsZero() && now.Sub(sess.IssuedAt) > tokenLifetime {
		if err := s.store.delete(ctx, token); err != nil {
			s.log.Warn("dropping expired session failed", zap.Error(err))
		}
		protocol.WriteJSONError(w, r, badRequest("Invalid or expired configuration token."))
		return
	}
	// AWS models this refusal as InvalidParameterProblem.PollIntervalNotSatisfied:
	// "The client called the service before the time specified in the poll
	// interval." The token stays valid so the caller can retry once the
	// interval has passed.
	if sess.FromPoll && sess.MinimumPollSeconds > 0 &&
		now.Sub(sess.IssuedAt) < time.Duration(sess.MinimumPollSeconds)*time.Second {
		protocol.WriteJSONError(w, r, badRequest(fmt.Sprintf(
			"This session requires at least %d seconds between calls to GetLatestConfiguration.",
			sess.MinimumPollSeconds)))
		return
	}

	latestVersion, err := s.ac.LatestVersionNumber(ctx, sess.AppID, sess.ProfileID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	next := &session{
		Token:              newToken(),
		AppID:              sess.AppID,
		EnvID:              sess.EnvID,
		ProfileID:          sess.ProfileID,
		LastVersionSeen:    latestVersion,
		MinimumPollSeconds: sess.MinimumPollSeconds,
		IssuedAt:           now,
		FromPoll:           true,
	}
	if err := s.store.put(ctx, next); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	// The spent token cannot be replayed: the model says a configuration token
	// is only valid for one call.
	if err := s.store.delete(ctx, token); err != nil {
		s.log.Warn("dropping spent configuration token failed", zap.Error(err))
	}

	w.Header().Set("Next-Poll-Configuration-Token", next.Token)
	w.Header().Set("Next-Poll-Interval-In-Seconds", strconv.Itoa(sess.pollIntervalSeconds()))

	// An unchanged version, or a version counter whose content has since been
	// deleted, is an empty payload — the model's Configuration member is
	// optional and AWS leaves it out when the client already has the latest
	// data. Content-Type goes with it, so nothing is claimed about a body that
	// is not there.
	if latestVersion == 0 || latestVersion == sess.LastVersionSeen {
		w.WriteHeader(http.StatusOK)
		return
	}
	hcv, found := s.ac.GetHostedConfigVersionByNum(ctx, sess.AppID, sess.ProfileID, latestVersion)
	if !found {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Version-Label is modeled here too, and is deliberately not sent: it
	// carries the hosted configuration version's user-defined label, which
	// Overcast's AppConfig control plane does not store. AWS also omits it when
	// there is none.
	if hcv.ContentType != "" {
		w.Header().Set("Content-Type", hcv.ContentType)
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(hcv.Content)); err != nil {
		s.log.Debug("writing configuration payload failed", zap.Error(err))
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// badRequest builds the client error every invalid AppConfigData request gets.
// BadRequestException is one of the four errors both operations model; a
// generic MissingParameter would reach the SDK as an unmodeled error type.
func badRequest(message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "BadRequestException",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

// notFound names the missing resource with one of the model's ResourceType
// values. AWS carries that value in a ResourceType member of the error
// payload, which protocol.AWSError has no room for, so it goes in the message.
func notFound(resourceType string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    resourceType + " not found",
		HTTPStatus: http.StatusNotFound,
	}
}

// validateIdentifier applies the model's @required and Identifier @length
// constraints to a StartConfigurationSession member.
func validateIdentifier(member, value string) *protocol.AWSError {
	switch {
	case value == "":
		return badRequest(member + " is required.")
	case len(value) > maxIdentifierLength:
		return badRequest(fmt.Sprintf("%s must be at most %d characters.", member, maxIdentifierLength))
	}
	return nil
}

// newToken generates an opaque configuration token. The model's Token shape is
// ^\S{1,8192}$, which a UUID satisfies.
func newToken() string {
	return uuid.NewString()
}
