package appconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

const (
	nsApps     = "appconfig:apps"
	nsEnvs     = "appconfig:envs"
	nsProfiles = "appconfig:profiles"
	nsHCVs     = "appconfig:hcversions"  // hosted configuration versions
	nsHCVCnts  = "appconfig:hcvcounters" // per-profile version counters
	nsTags     = "appconfig:tags"
)

// appConfigStore is every read and write AppConfig makes against state.Store.
//
// Records are keyed by identity alone rather than by region. That is not a
// claim that AppConfig resources are global — they are not — but changing it
// would change the cross-service accessors the appconfigdata package resolves
// sessions through (ResolveApplication and friends take no region), and that
// package is the subject of its own issue (#855). Tag keys follow the records
// for the same reason: keying them by ARN, which carries a region, would make
// tags region-sensitive against resources that are not.
type appConfigStore struct {
	store state.Store
	log   *serviceutil.ServiceLogger
	tags  *serviceutil.NSStore
	// versionLocks serialises the read-increment-write of a profile's version
	// counter, which two concurrent CreateHostedConfigurationVersion calls
	// would otherwise resolve to the same number.
	versionLocks serviceutil.RecordLocks
}

func newAppConfigStore(st state.Store, log *serviceutil.ServiceLogger) *appConfigStore {
	return &appConfigStore{
		store: st,
		log:   log,
		tags:  &serviceutil.NSStore{Store: st, NS: nsTags},
	}
}

// ─── Applications ─────────────────────────────────────────────────────────────

func (s *appConfigStore) putApp(ctx context.Context, a *Application) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("appconfig: marshal application: %w", err)
	}
	return s.store.Set(ctx, nsApps, a.ID, string(raw))
}

// getApp returns the application called id.
//
// A record that cannot be decoded reads as absent rather than as a failure: one
// bad payload must not turn a get into a 500, and ResourceNotFoundException is
// the answer AWS models for an identifier the service cannot produce
// (AGENTS.md § malformed persisted state must be isolated).
func (s *appConfigStore) getApp(ctx context.Context, id string) (*Application, bool) {
	raw, found, err := s.store.Get(ctx, nsApps, id)
	if err != nil || !found {
		return nil, false
	}
	var a Application
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		s.log.Warn("skipping undecodable application record", zap.String("application", id), zap.Error(err))
		return nil, false
	}
	return &a, true
}

// listApps returns every application in ID order, skipping records that cannot
// be decoded so one bad payload does not fail the whole listing. The order is
// stable because ListApplications is paginated by index.
func (s *appConfigStore) listApps(ctx context.Context) ([]Application, error) {
	pairs, err := s.store.Scan(ctx, nsApps, "")
	if err != nil {
		return nil, err
	}
	out := make([]Application, 0, len(pairs))
	for _, kv := range pairs {
		var a Application
		if err := json.Unmarshal([]byte(kv.Value), &a); err != nil {
			s.log.Warn("skipping undecodable application record", zap.String("key", kv.Key), zap.Error(err))
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// deleteApp removes an application and the tags attached to it, so a later
// application cannot inherit them.
func (s *appConfigStore) deleteApp(ctx context.Context, id string) error {
	if err := s.store.Delete(ctx, nsTags, applicationTagKey(id)); err != nil {
		return err
	}
	return s.store.Delete(ctx, nsApps, id)
}

// ─── Environments ─────────────────────────────────────────────────────────────

func envKey(appID, envID string) string { return appID + "/" + envID }

func (s *appConfigStore) putEnv(ctx context.Context, e *Environment) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("appconfig: marshal environment: %w", err)
	}
	return s.store.Set(ctx, nsEnvs, envKey(e.ApplicationID, e.ID), string(raw))
}

func (s *appConfigStore) getEnv(ctx context.Context, appID, envID string) (*Environment, bool) {
	raw, found, err := s.store.Get(ctx, nsEnvs, envKey(appID, envID))
	if err != nil || !found {
		return nil, false
	}
	var e Environment
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		s.log.Warn("skipping undecodable environment record",
			zap.String("application", appID), zap.String("environment", envID), zap.Error(err))
		return nil, false
	}
	return &e, true
}

func (s *appConfigStore) listEnvs(ctx context.Context, appID string) ([]Environment, error) {
	pairs, err := s.store.Scan(ctx, nsEnvs, appID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]Environment, 0, len(pairs))
	for _, kv := range pairs {
		var e Environment
		if err := json.Unmarshal([]byte(kv.Value), &e); err != nil {
			s.log.Warn("skipping undecodable environment record", zap.String("key", kv.Key), zap.Error(err))
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *appConfigStore) deleteEnv(ctx context.Context, appID, envID string) error {
	if err := s.store.Delete(ctx, nsTags, environmentTagKey(appID, envID)); err != nil {
		return err
	}
	return s.store.Delete(ctx, nsEnvs, envKey(appID, envID))
}

// ─── Configuration profiles ───────────────────────────────────────────────────

func profileKey(appID, profID string) string { return appID + "/" + profID }

func (s *appConfigStore) putProfile(ctx context.Context, p *ConfigurationProfile) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("appconfig: marshal configuration profile: %w", err)
	}
	return s.store.Set(ctx, nsProfiles, profileKey(p.ApplicationID, p.ID), string(raw))
}

func (s *appConfigStore) getProfile(ctx context.Context, appID, profID string) (*ConfigurationProfile, bool) {
	raw, found, err := s.store.Get(ctx, nsProfiles, profileKey(appID, profID))
	if err != nil || !found {
		return nil, false
	}
	var p ConfigurationProfile
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		s.log.Warn("skipping undecodable configuration profile record",
			zap.String("application", appID), zap.String("profile", profID), zap.Error(err))
		return nil, false
	}
	return &p, true
}

func (s *appConfigStore) listProfiles(ctx context.Context, appID string) ([]ConfigurationProfile, error) {
	pairs, err := s.store.Scan(ctx, nsProfiles, appID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]ConfigurationProfile, 0, len(pairs))
	for _, kv := range pairs {
		var p ConfigurationProfile
		if err := json.Unmarshal([]byte(kv.Value), &p); err != nil {
			s.log.Warn("skipping undecodable configuration profile record", zap.String("key", kv.Key), zap.Error(err))
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *appConfigStore) deleteProfile(ctx context.Context, appID, profID string) error {
	if err := s.store.Delete(ctx, nsTags, profileTagKey(appID, profID)); err != nil {
		return err
	}
	return s.store.Delete(ctx, nsProfiles, profileKey(appID, profID))
}

// ─── Hosted configuration versions ────────────────────────────────────────────

func hcvKey(appID, profID string, version int) string {
	return fmt.Sprintf("%s/%s/%d", appID, profID, version)
}

func hcvCounterKey(appID, profID string) string { return appID + "/" + profID }

// nextVersionNumber increments and returns the profile's next version number.
//
// expected, when non-zero, is the caller's Latest-Version-Number header: AWS
// models it as optimistic concurrency, so a value that is not the current
// latest is a ConflictException rather than a silent overwrite. The check and
// the increment share the lock; splitting them would be the race the header
// exists to prevent.
func (s *appConfigStore) nextVersionNumber(ctx context.Context, appID, profID string, expected *int) (int, error) {
	key := hcvCounterKey(appID, profID)
	defer s.versionLocks.Lock(key)()

	current, err := s.readVersionCounter(ctx, key)
	if err != nil {
		return 0, err
	}
	if expected != nil && *expected != current {
		return 0, errStaleLatestVersion
	}
	next := current + 1
	if err := s.store.Set(ctx, nsHCVCnts, key, strconv.Itoa(next)); err != nil {
		return 0, err
	}
	return next, nil
}

// latestVersionNumber returns the profile's latest version number, 0 when it
// has none.
func (s *appConfigStore) latestVersionNumber(ctx context.Context, appID, profID string) (int, error) {
	return s.readVersionCounter(ctx, hcvCounterKey(appID, profID))
}

// readVersionCounter reads the stored counter. A counter that is not an
// integer reads as zero and is logged: the alternative is failing every
// subsequent write to that profile on a value no API of this service produces.
func (s *appConfigStore) readVersionCounter(ctx context.Context, key string) (int, error) {
	raw, found, err := s.store.Get(ctx, nsHCVCnts, key)
	if err != nil || !found {
		return 0, err
	}
	n, convErr := strconv.Atoi(raw)
	if convErr != nil {
		s.log.Warn("resetting undecodable hosted configuration version counter",
			zap.String("key", key), zap.Error(convErr))
		return 0, nil
	}
	return n, nil
}

func (s *appConfigStore) putHCV(ctx context.Context, v *HostedConfigurationVersion) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("appconfig: marshal hosted configuration version: %w", err)
	}
	return s.store.Set(ctx, nsHCVs, hcvKey(v.ApplicationID, v.ConfigurationProfileID, v.VersionNumber), string(raw))
}

func (s *appConfigStore) getHCV(ctx context.Context, appID, profID string, version int) (*HostedConfigurationVersion, bool) {
	raw, found, err := s.store.Get(ctx, nsHCVs, hcvKey(appID, profID, version))
	if err != nil || !found {
		return nil, false
	}
	var v HostedConfigurationVersion
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		s.log.Warn("skipping undecodable hosted configuration version record",
			zap.String("key", hcvKey(appID, profID, version)), zap.Error(err))
		return nil, false
	}
	return &v, true
}

// listHCVs returns a profile's versions newest first, which is the order the
// AWS console and CLI present them in.
//
// Sorting is numeric rather than by key: the store returns keys in lexical
// order, where version 10 sorts before version 2.
func (s *appConfigStore) listHCVs(ctx context.Context, appID, profID string) ([]HostedConfigurationVersion, error) {
	pairs, err := s.store.Scan(ctx, nsHCVs, appID+"/"+profID+"/")
	if err != nil {
		return nil, err
	}
	out := make([]HostedConfigurationVersion, 0, len(pairs))
	for _, kv := range pairs {
		var v HostedConfigurationVersion
		if err := json.Unmarshal([]byte(kv.Value), &v); err != nil {
			s.log.Warn("skipping undecodable hosted configuration version record",
				zap.String("key", kv.Key), zap.Error(err))
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VersionNumber > out[j].VersionNumber })
	return out, nil
}

func (s *appConfigStore) deleteHCV(ctx context.Context, appID, profID string, version int) error {
	return s.store.Delete(ctx, nsHCVs, hcvKey(appID, profID, version))
}
