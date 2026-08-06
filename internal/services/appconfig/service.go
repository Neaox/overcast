// Package appconfig provides a basic emulation of AWS AppConfig.
//
// Control plane implemented operations: CreateApplication, GetApplication,
// ListApplications, DeleteApplication, CreateEnvironment, GetEnvironment,
// ListEnvironments, DeleteEnvironment, CreateConfigurationProfile,
// GetConfigurationProfile, ListConfigurationProfiles,
// DeleteConfigurationProfile, CreateHostedConfigurationVersion,
// GetHostedConfigurationVersion, ListHostedConfigurationVersions,
// DeleteHostedConfigurationVersion.
//
// Data-plane operations (StartConfigurationSession, GetLatestConfiguration)
// are implemented in the sibling appconfigdata package.
package appconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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

const serviceName = "appconfig"

// ─── Types ────────────────────────────────────────────────────

// Application represents an AppConfig application.
type Application struct {
	ID          string            `json:"Id"`
	Name        string            `json:"Name"`
	Description string            `json:"Description,omitempty"`
	Tags        map[string]string `json:"Tags,omitempty"`
	ARN         string            `json:"Arn,omitempty"`
}

func appARN(region, accountID, appID string) string {
	return fmt.Sprintf("arn:aws:appconfig:%s:%s:application/%s", region, accountID, appID)
}

func envARN(region, accountID, appID, envID string) string {
	return fmt.Sprintf("arn:aws:appconfig:%s:%s:application/%s/environment/%s", region, accountID, appID, envID)
}

func profileARN(region, accountID, appID, profID string) string {
	return fmt.Sprintf("arn:aws:appconfig:%s:%s:application/%s/configurationprofile/%s", region, accountID, appID, profID)
}

// Environment represents an AppConfig environment.
type Environment struct {
	ApplicationId string            `json:"ApplicationId"`
	ID            string            `json:"Id"`
	Name          string            `json:"Name"`
	Description   string            `json:"Description,omitempty"`
	State         string            `json:"State"`
	Tags          map[string]string `json:"Tags,omitempty"`
	ARN           string            `json:"Arn,omitempty"`
}

// ConfigurationProfile represents an AppConfig configuration profile.
type ConfigurationProfile struct {
	ApplicationId string            `json:"ApplicationId"`
	ID            string            `json:"Id"`
	Name          string            `json:"Name"`
	LocationUri   string            `json:"LocationUri,omitempty"`
	Type          string            `json:"Type,omitempty"`
	Tags          map[string]string `json:"Tags,omitempty"`
	ARN           string            `json:"Arn,omitempty"`
}

// HostedConfigurationVersion represents a stored configuration payload.
type HostedConfigurationVersion struct {
	ApplicationId          string `json:"ApplicationId"`
	ConfigurationProfileId string `json:"ConfigurationProfileId"`
	VersionNumber          int    `json:"VersionNumber"`
	ContentType            string `json:"ContentType"`
	Description            string `json:"Description,omitempty"`
	Content                string `json:"Content"` // raw bytes stored as a string
}

// ─── Store ────────────────────────────────────────────────────

type appConfigStore struct {
	mu    sync.Mutex
	store state.Store
}

func newAppConfigStore(s state.Store) *appConfigStore {
	return &appConfigStore{store: s}
}

const (
	nsApps     = "appconfig:apps"
	nsEnvs     = "appconfig:envs"
	nsProfiles = "appconfig:profiles"
	nsHCVs     = "appconfig:hcversions"  // hosted configuration versions
	nsHCVCnts  = "appconfig:hcvcounters" // per-profile version counters
)

func (s *appConfigStore) putApp(ctx context.Context, a *Application) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("appconfig: marshal application: %w", err)
	}
	return s.store.Set(ctx, nsApps, a.ID, string(raw))
}

func (s *appConfigStore) getApp(ctx context.Context, id string) (*Application, bool) {
	raw, found, err := s.store.Get(ctx, nsApps, id)
	if err != nil || !found {
		return nil, false
	}
	var a Application
	if json.Unmarshal([]byte(raw), &a) != nil {
		return nil, false
	}
	return &a, true
}

func (s *appConfigStore) listApps(ctx context.Context) ([]*Application, error) {
	pairs, err := s.store.Scan(ctx, nsApps, "")
	if err != nil {
		return nil, err
	}
	out := make([]*Application, 0, len(pairs))
	for _, kv := range pairs {
		var a Application
		if json.Unmarshal([]byte(kv.Value), &a) == nil {
			out = append(out, &a)
		}
	}
	return out, nil
}

func (s *appConfigStore) deleteApp(ctx context.Context, id string) error {
	return s.store.Delete(ctx, nsApps, id)
}

func envKey(appID, envID string) string { return appID + "/" + envID }

func (s *appConfigStore) putEnv(ctx context.Context, e *Environment) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("appconfig: marshal environment: %w", err)
	}
	return s.store.Set(ctx, nsEnvs, envKey(e.ApplicationId, e.ID), string(raw))
}

func (s *appConfigStore) getEnv(ctx context.Context, appID, envID string) (*Environment, bool) {
	raw, found, err := s.store.Get(ctx, nsEnvs, envKey(appID, envID))
	if err != nil || !found {
		return nil, false
	}
	var e Environment
	if json.Unmarshal([]byte(raw), &e) != nil {
		return nil, false
	}
	return &e, true
}

func (s *appConfigStore) listEnvs(ctx context.Context, appID string) ([]*Environment, error) {
	pairs, err := s.store.Scan(ctx, nsEnvs, "")
	if err != nil {
		return nil, err
	}
	out := make([]*Environment, 0, len(pairs))
	for _, kv := range pairs {
		var e Environment
		if json.Unmarshal([]byte(kv.Value), &e) == nil && e.ApplicationId == appID {
			out = append(out, &e)
		}
	}
	return out, nil
}

func (s *appConfigStore) deleteEnv(ctx context.Context, appID, envID string) error {
	return s.store.Delete(ctx, nsEnvs, envKey(appID, envID))
}

func profileKey(appID, profID string) string { return appID + "/" + profID }

func (s *appConfigStore) putProfile(ctx context.Context, p *ConfigurationProfile) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("appconfig: marshal configuration profile: %w", err)
	}
	return s.store.Set(ctx, nsProfiles, profileKey(p.ApplicationId, p.ID), string(raw))
}

func (s *appConfigStore) getProfile(ctx context.Context, appID, profID string) (*ConfigurationProfile, bool) {
	raw, found, err := s.store.Get(ctx, nsProfiles, profileKey(appID, profID))
	if err != nil || !found {
		return nil, false
	}
	var p ConfigurationProfile
	if json.Unmarshal([]byte(raw), &p) != nil {
		return nil, false
	}
	return &p, true
}

func (s *appConfigStore) listProfiles(ctx context.Context, appID string) ([]*ConfigurationProfile, error) {
	pairs, err := s.store.Scan(ctx, nsProfiles, "")
	if err != nil {
		return nil, err
	}
	out := make([]*ConfigurationProfile, 0, len(pairs))
	for _, kv := range pairs {
		var p ConfigurationProfile
		if json.Unmarshal([]byte(kv.Value), &p) == nil && p.ApplicationId == appID {
			out = append(out, &p)
		}
	}
	return out, nil
}

func (s *appConfigStore) deleteProfile(ctx context.Context, appID, profID string) error {
	return s.store.Delete(ctx, nsProfiles, profileKey(appID, profID))
}

// ─── Hosted Configuration Versions ────────────────────────────────────────────

func hcvKey(appID, profID string, version int) string {
	return fmt.Sprintf("%s/%s/%d", appID, profID, version)
}

func hcvCounterKey(appID, profID string) string { return appID + "/" + profID }

// nextVersionNumber atomically increments and returns the next version number.
func (s *appConfigStore) nextVersionNumber(ctx context.Context, appID, profID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, found, err := s.store.Get(ctx, nsHCVCnts, hcvCounterKey(appID, profID))
	if err != nil {
		return 0, err
	}
	n := 0
	if found {
		n, _ = strconv.Atoi(raw)
	}
	n++
	if err := s.store.Set(ctx, nsHCVCnts, hcvCounterKey(appID, profID), strconv.Itoa(n)); err != nil {
		return 0, err
	}
	return n, nil
}

// latestVersionNumber returns the latest version number for a profile (0 if none).
func (s *appConfigStore) latestVersionNumber(ctx context.Context, appID, profID string) (int, error) {
	raw, found, err := s.store.Get(ctx, nsHCVCnts, hcvCounterKey(appID, profID))
	if err != nil || !found {
		return 0, err
	}
	n, _ := strconv.Atoi(raw)
	return n, nil
}

func (s *appConfigStore) putHCV(ctx context.Context, v *HostedConfigurationVersion) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("appconfig: marshal hcv: %w", err)
	}
	return s.store.Set(ctx, nsHCVs, hcvKey(v.ApplicationId, v.ConfigurationProfileId, v.VersionNumber), string(raw))
}

func (s *appConfigStore) getHCV(ctx context.Context, appID, profID string, version int) (*HostedConfigurationVersion, bool) {
	raw, found, err := s.store.Get(ctx, nsHCVs, hcvKey(appID, profID, version))
	if err != nil || !found {
		return nil, false
	}
	var v HostedConfigurationVersion
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil, false
	}
	return &v, true
}

func (s *appConfigStore) listHCVs(ctx context.Context, appID, profID string) ([]*HostedConfigurationVersion, error) {
	pairs, err := s.store.Scan(ctx, nsHCVs, "")
	if err != nil {
		return nil, err
	}
	prefix := appID + "/" + profID + "/"
	out := make([]*HostedConfigurationVersion, 0)
	for _, kv := range pairs {
		if len(kv.Key) <= len(prefix) || kv.Key[:len(prefix)] != prefix {
			continue
		}
		var v HostedConfigurationVersion
		if json.Unmarshal([]byte(kv.Value), &v) == nil {
			out = append(out, &v)
		}
	}
	return out, nil
}

func (s *appConfigStore) deleteHCV(ctx context.Context, appID, profID string, version int) error {
	return s.store.Delete(ctx, nsHCVs, hcvKey(appID, profID, version))
}

// ─── Service ──────────────────────────────────────────────────

// Service implements router.Service for AppConfig.
type Service struct {
	log     *serviceutil.ServiceLogger
	store   *appConfigStore
	cfg     *config.Config
	typedOp map[string]op.Operation
}

// New returns a configured AppConfig Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, _ clock.Clock) *Service {
	s := &Service{
		log:   serviceutil.NewServiceLogger(logger, serviceName),
		store: newAppConfigStore(st),
		cfg:   cfg,
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string { return serviceName }

func (s *Service) TargetPrefix() string { return "AppConfig." }

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

// ─── Cross-service accessors (used by appconfigdata) ──────────────────────────

// ResolveApplication finds an application by ID or name. Returns (app, true) or (nil, false).
func (s *Service) ResolveApplication(ctx context.Context, identifier string) (*Application, bool) {
	// try by ID first
	if a, ok := s.store.getApp(ctx, identifier); ok {
		return a, true
	}
	// fall back to name scan
	apps, _ := s.store.listApps(ctx)
	for _, a := range apps {
		if a.Name == identifier {
			return a, true
		}
	}
	return nil, false
}

// ResolveEnvironment finds an environment by ID or name within the given app.
func (s *Service) ResolveEnvironment(ctx context.Context, appID, identifier string) (*Environment, bool) {
	envs, _ := s.store.listEnvs(ctx, appID)
	for _, e := range envs {
		if e.ID == identifier || e.Name == identifier {
			return e, true
		}
	}
	return nil, false
}

// ResolveProfile finds a configuration profile by ID or name within the given app.
func (s *Service) ResolveProfile(ctx context.Context, appID, identifier string) (*ConfigurationProfile, bool) {
	profiles, _ := s.store.listProfiles(ctx, appID)
	for _, p := range profiles {
		if p.ID == identifier || p.Name == identifier {
			return p, true
		}
	}
	return nil, false
}

// LatestVersionNumber returns the latest hosted config version number for a profile (0 = none).
func (s *Service) LatestVersionNumber(ctx context.Context, appID, profID string) (int, error) {
	return s.store.latestVersionNumber(ctx, appID, profID)
}

// GetHostedConfigVersionByNum retrieves a specific hosted config version.
func (s *Service) GetHostedConfigVersionByNum(ctx context.Context, appID, profID string, version int) (*HostedConfigurationVersion, bool) {
	return s.store.getHCV(ctx, appID, profID, version)
}

// RegisterRoutes registers the REST endpoints for AppConfig.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Route("/_appconfig", func(r chi.Router) {
		// Applications
		r.Post("/applications", s.createApplication)
		r.Get("/applications", s.listApplications)
		r.Get("/applications/{appId}", s.getApplication)
		r.Delete("/applications/{appId}", s.deleteApplication)
		// Environments
		r.Post("/applications/{appId}/environments", s.createEnvironment)
		r.Get("/applications/{appId}/environments", s.listEnvironments)
		r.Get("/applications/{appId}/environments/{envId}", s.getEnvironment)
		r.Delete("/applications/{appId}/environments/{envId}", s.deleteEnvironment)
		// Configuration Profiles
		r.Post("/applications/{appId}/configurationprofiles", s.createConfigurationProfile)
		r.Get("/applications/{appId}/configurationprofiles", s.listConfigurationProfiles)
		r.Get("/applications/{appId}/configurationprofiles/{profId}", s.getConfigurationProfile)
		r.Delete("/applications/{appId}/configurationprofiles/{profId}", s.deleteConfigurationProfile)
		// Hosted Configuration Versions
		r.Post("/applications/{appId}/configurationprofiles/{profId}/hostedconfigurationversions", s.createHostedConfigurationVersion)
		r.Get("/applications/{appId}/configurationprofiles/{profId}/hostedconfigurationversions", s.listHostedConfigurationVersions)
		r.Get("/applications/{appId}/configurationprofiles/{profId}/hostedconfigurationversions/{version}", s.getHostedConfigurationVersion)
		r.Delete("/applications/{appId}/configurationprofiles/{profId}/hostedconfigurationversions/{version}", s.deleteHostedConfigurationVersion)
		// Tags
		r.Post("/tags/*", s.tagResource)
		r.Delete("/tags/*", s.untagResource)
		r.Get("/tags/*", s.listTagsForResource)
	})
}

// ─── Handlers ─────────────────────────────────────────────────

func (s *Service) createApplication(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"Name"`
		Description string `json:"Description"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "BadRequestException", Message: "Name is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)
	id := shortID()
	app := &Application{
		ID: id, Name: req.Name, Description: req.Description,
		Tags: make(map[string]string),
		ARN:  appARN(region, s.cfg.AccountID, id),
	}
	if err := s.store.putApp(r.Context(), app); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusCreated, app)
}

func (s *Service) getApplication(w http.ResponseWriter, r *http.Request) {
	app, found := s.store.getApp(r.Context(), chi.URLParam(r, "appId"))
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Application not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, app)
}

func (s *Service) listApplications(w http.ResponseWriter, r *http.Request) {
	apps, err := s.store.listApps(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Items": apps})
}

func (s *Service) deleteApplication(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "appId")
	if _, found := s.store.getApp(r.Context(), id); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Application not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	_ = s.store.deleteApp(r.Context(), id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) createEnvironment(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	var req struct {
		Name        string `json:"Name"`
		Description string `json:"Description"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)
	id := shortID()
	env := &Environment{
		ApplicationId: appID,
		ID:            id,
		Name:          req.Name,
		Description:   req.Description,
		State:         "READY_FOR_DEPLOYMENT",
		Tags:          make(map[string]string),
		ARN:           envARN(region, s.cfg.AccountID, appID, id),
	}
	if err := s.store.putEnv(r.Context(), env); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusCreated, env)
}

func (s *Service) getEnvironment(w http.ResponseWriter, r *http.Request) {
	env, found := s.store.getEnv(r.Context(), chi.URLParam(r, "appId"), chi.URLParam(r, "envId"))
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Environment not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, env)
}

func (s *Service) listEnvironments(w http.ResponseWriter, r *http.Request) {
	envs, err := s.store.listEnvs(r.Context(), chi.URLParam(r, "appId"))
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Items": envs})
}

func (s *Service) deleteEnvironment(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	envID := chi.URLParam(r, "envId")
	if _, found := s.store.getEnv(r.Context(), appID, envID); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Environment not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	_ = s.store.deleteEnv(r.Context(), appID, envID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) createConfigurationProfile(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	var req struct {
		Name        string `json:"Name"`
		LocationUri string `json:"LocationUri"`
		Type        string `json:"Type"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	region := middleware.RegionFromContext(r.Context(), s.cfg.Region)
	id := shortID()
	prof := &ConfigurationProfile{
		ApplicationId: appID,
		ID:            id,
		Name:          req.Name,
		LocationUri:   req.LocationUri,
		Type:          req.Type,
		Tags:          make(map[string]string),
		ARN:           profileARN(region, s.cfg.AccountID, appID, id),
	}
	if err := s.store.putProfile(r.Context(), prof); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusCreated, prof)
}

func (s *Service) getConfigurationProfile(w http.ResponseWriter, r *http.Request) {
	prof, found := s.store.getProfile(r.Context(), chi.URLParam(r, "appId"), chi.URLParam(r, "profId"))
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Configuration profile not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, prof)
}

func (s *Service) listConfigurationProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.store.listProfiles(r.Context(), chi.URLParam(r, "appId"))
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Items": profiles})
}

func (s *Service) deleteConfigurationProfile(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	profID := chi.URLParam(r, "profId")
	if _, found := s.store.getProfile(r.Context(), appID, profID); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Configuration profile not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	_ = s.store.deleteProfile(r.Context(), appID, profID)
	w.WriteHeader(http.StatusNoContent)
}

// shortID generates a 7-character hex ID similar to AppConfig's format.
func shortID() string {
	return uuid.NewString()[:7]
}

// ─── Hosted Configuration Version handlers ─────────────────────────────────────

// createHostedConfigurationVersion stores raw config content.
// Request body: raw configuration bytes.
// Content-Type header: content type of the configuration.
// Optional Description header.
func (s *Service) createHostedConfigurationVersion(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	profID := chi.URLParam(r, "profId")

	// validate profile exists
	if _, found := s.store.getProfile(r.Context(), appID, profID); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Configuration profile not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	description := r.Header.Get("Description")

	const maxContentLength = 1 << 20 // 1 MB
	body, err := io.ReadAll(io.LimitReader(r.Body, maxContentLength+1))
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if len(body) > maxContentLength {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "BadRequestException", Message: "Configuration content exceeds the maximum size of 1 MB.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	versionNum, err := s.store.nextVersionNumber(r.Context(), appID, profID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	hcv := &HostedConfigurationVersion{
		ApplicationId:          appID,
		ConfigurationProfileId: profID,
		VersionNumber:          versionNum,
		ContentType:            contentType,
		Description:            description,
		Content:                string(body),
	}
	if err := s.store.putHCV(r.Context(), hcv); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	// Return metadata (not the content) in the body, content-type as a header.
	w.Header().Set("AppConfig-Configuration-Version", strconv.Itoa(versionNum))
	w.Header().Set("AppConfig-Application-Id", appID)
	w.Header().Set("AppConfig-Configuration-Profile-Id", profID)
	protocol.WriteJSON(w, r, http.StatusCreated, hcv)
}

func (s *Service) getHostedConfigurationVersion(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	profID := chi.URLParam(r, "profId")
	versionStr := chi.URLParam(r, "version")
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "BadRequestException", Message: "Invalid version number",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	hcv, found := s.store.getHCV(r.Context(), appID, profID, version)
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Hosted configuration version not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	// Return raw configuration content with appropriate headers.
	w.Header().Set("Content-Type", hcv.ContentType)
	w.Header().Set("AppConfig-Configuration-Version", strconv.Itoa(hcv.VersionNumber))
	w.Header().Set("AppConfig-Application-Id", appID)
	w.Header().Set("AppConfig-Configuration-Profile-Id", profID)
	if hcv.Description != "" {
		w.Header().Set("AppConfig-Description", hcv.Description)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(hcv.Content))
}

func (s *Service) listHostedConfigurationVersions(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	profID := chi.URLParam(r, "profId")

	versions, err := s.store.listHCVs(r.Context(), appID, profID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	// Return metadata without content (consistent with AWS).
	type versionMeta struct {
		ApplicationId          string `json:"ApplicationId"`
		ConfigurationProfileId string `json:"ConfigurationProfileId"`
		VersionNumber          int    `json:"VersionNumber"`
		ContentType            string `json:"ContentType"`
		Description            string `json:"Description,omitempty"`
	}
	items := make([]versionMeta, 0, len(versions))
	for _, v := range versions {
		items = append(items, versionMeta{
			ApplicationId:          v.ApplicationId,
			ConfigurationProfileId: v.ConfigurationProfileId,
			VersionNumber:          v.VersionNumber,
			ContentType:            v.ContentType,
			Description:            v.Description,
		})
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Items": items})
}

func (s *Service) deleteHostedConfigurationVersion(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	profID := chi.URLParam(r, "profId")
	versionStr := chi.URLParam(r, "version")
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "BadRequestException", Message: "Invalid version number",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if _, found := s.store.getHCV(r.Context(), appID, profID, version); !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Hosted configuration version not found",
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	_ = s.store.deleteHCV(r.Context(), appID, profID, version)
	w.WriteHeader(http.StatusNoContent)
}

// ─── Tag helpers ─────────────────────────────────────────────────

// appTagCfg tunes tag-validation error codes to match AppConfig.
var appConfigTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "BadRequestException",
	InvalidCode:     "BadRequestException",
	ExceededMessage: "Too many tags.",
}

type arnParts struct {
	ResourceType string
	AppID        string
	EnvID        string
	ProfID       string
	Version      string
}

// parseAppConfigARN splits an AppConfig resource ARN into its parts.
func parseAppConfigARN(arn string) *arnParts {
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return nil
	}
	resource := parts[5]
	segs := strings.Split(resource, "/")
	if len(segs) < 2 {
		return nil
	}
	if segs[0] != "application" {
		return nil
	}
	p := &arnParts{ResourceType: "application", AppID: segs[1]}
	if len(segs) < 3 {
		return p
	}
	switch segs[2] {
	case "environment":
		p.ResourceType = "environment"
		if len(segs) >= 4 {
			p.EnvID = segs[3]
		}
	case "configurationprofile":
		p.ResourceType = "configurationprofile"
		if len(segs) >= 4 {
			p.ProfID = segs[3]
		}
		if len(segs) >= 5 && segs[4] == "hostedconfigurationversion" && len(segs) >= 6 {
			p.ResourceType = "hostedconfigurationversion"
			p.Version = segs[5]
		}
	}
	return p
}

// resolveTagTarget loads the resource identified by an ARN and returns its
// tags map (nil when the resource is not found).
func (s *Service) resolveTagTarget(ctx context.Context, arn string) (*arnParts, map[string]string, *protocol.AWSError) {
	p := parseAppConfigARN(arn)
	if p == nil || p.AppID == "" {
		return nil, nil, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Resource not found",
			HTTPStatus: http.StatusNotFound,
		}
	}
	switch p.ResourceType {
	case "application":
		if app, ok := s.store.getApp(ctx, p.AppID); ok {
			return p, app.Tags, nil
		}
	case "environment":
		if p.EnvID == "" {
			return nil, nil, &protocol.AWSError{
				Code: "ResourceNotFoundException", Message: "Resource not found",
				HTTPStatus: http.StatusNotFound,
			}
		}
		if env, ok := s.store.getEnv(ctx, p.AppID, p.EnvID); ok {
			return p, env.Tags, nil
		}
	case "configurationprofile":
		if p.ProfID == "" {
			return nil, nil, &protocol.AWSError{
				Code: "ResourceNotFoundException", Message: "Resource not found",
				HTTPStatus: http.StatusNotFound,
			}
		}
		if prof, ok := s.store.getProfile(ctx, p.AppID, p.ProfID); ok {
			return p, prof.Tags, nil
		}
	case "hostedconfigurationversion":
		return nil, nil, &protocol.AWSError{
			Code: "BadRequestException", Message: "Tagging hosted configuration versions is not supported",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return nil, nil, &protocol.AWSError{
		Code: "ResourceNotFoundException", Message: "Resource not found",
		HTTPStatus: http.StatusNotFound,
	}
}

// persistTags writes updated tags back to the identified resource.
func (s *Service) persistTags(ctx context.Context, p *arnParts, tags map[string]string) *protocol.AWSError {
	switch p.ResourceType {
	case "application":
		app, ok := s.store.getApp(ctx, p.AppID)
		if !ok {
			return &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Resource not found", HTTPStatus: http.StatusNotFound}
		}
		app.Tags = tags
		if err := s.store.putApp(ctx, app); err != nil {
			return protocol.ErrInternalError
		}
	case "environment":
		env, ok := s.store.getEnv(ctx, p.AppID, p.EnvID)
		if !ok {
			return &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Resource not found", HTTPStatus: http.StatusNotFound}
		}
		env.Tags = tags
		if err := s.store.putEnv(ctx, env); err != nil {
			return protocol.ErrInternalError
		}
	case "configurationprofile":
		prof, ok := s.store.getProfile(ctx, p.AppID, p.ProfID)
		if !ok {
			return &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Resource not found", HTTPStatus: http.StatusNotFound}
		}
		prof.Tags = tags
		if err := s.store.putProfile(ctx, prof); err != nil {
			return protocol.ErrInternalError
		}
	}
	return nil
}

// ─── REST tag handlers ─────────────────────────────────────────

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")
	var req struct {
		Tags map[string]string `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	p, existing, aerr := s.resolveTagTarget(r.Context(), arn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing == nil {
		existing = make(map[string]string)
	}
	for k, v := range req.Tags {
		existing[k] = v
	}
	if aerr := serviceutil.ValidateTags(appConfigTagCfg, existing); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := s.persistTags(r.Context(), p, existing); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")
	keys := r.URL.Query()["tagKeys"]
	p, existing, aerr := s.resolveTagTarget(r.Context(), arn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	for _, k := range keys {
		delete(existing, k)
	}
	if aerr := s.persistTags(r.Context(), p, existing); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn := chi.URLParam(r, "*")
	_, existing, aerr := s.resolveTagTarget(r.Context(), arn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if existing == nil {
		existing = make(map[string]string)
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]map[string]string{"Tags": existing})
}
