package backup

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsVaults = "backup:vaults"
	nsPlans  = "backup:plans"
)

// vaultRecord is a backup vault as it is persisted. It is deliberately not the
// wire shape: CreationDate is an RFC 3339 string here (what time.Time
// marshals to, and what every already-stored record holds) while restJson1
// puts epoch seconds on the wire. Keeping the two apart is what lets the wire
// encoding be corrected without a migration.
type vaultRecord struct {
	BackupVaultName  string    `json:"BackupVaultName"`
	BackupVaultArn   string    `json:"BackupVaultArn"`
	CreationDate     time.Time `json:"CreationDate"`
	EncryptionKeyArn string    `json:"EncryptionKeyArn,omitempty"`
	CreatorRequestID string    `json:"CreatorRequestId,omitempty"`
}

// planRecord is a backup plan as it is persisted.
//
// Version is the plan's version counter, rendered on the wire as an opaque
// VersionId. Records written before #815 carry a "VersionId" string instead
// and decode with Version 0, which versionID reads as version 1 — the value
// those records were written with.
type planRecord struct {
	BackupPlanID   string           `json:"BackupPlanId"`
	BackupPlanArn  string           `json:"BackupPlanArn"`
	BackupPlanName string           `json:"BackupPlanName"`
	Rules          []map[string]any `json:"Rules,omitempty"`
	Version        int              `json:"Version"`
	CreationDate   time.Time        `json:"CreationDate"`
}

// backupStore is every read and write Backup makes against state.Store.
//
// Keys are region-scoped. AWS states the constraint on CreateBackupVault:
// vault names are unique "per account per region", and a plan's ARN names the
// region it was created in — so two regions may legitimately hold the same
// name and a request must only ever see its own region's. Records written
// before #815 are keyed by bare name and are therefore not read: state that
// predates a key reshape is lost on upgrade, which CONTRIBUTING.md
// § JSON compatibility permits for a local dev tool with no durability
// guarantee, and which costs nothing here because no SDK could ever have
// written one.
type backupStore struct {
	store state.Store
	log   *serviceutil.ServiceLogger
}

func newBackupStore(st state.Store, log *serviceutil.ServiceLogger) *backupStore {
	return &backupStore{store: st, log: log}
}

// ─── Vaults ───────────────────────────────────────────────────

func (s *backupStore) putVault(ctx context.Context, region string, v *vaultRecord) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsVaults, serviceutil.RegionKey(region, v.BackupVaultName), string(raw))
}

// getVault returns the vault called name in region.
//
// A record that cannot be decoded reads as absent rather than as a failure:
// one bad payload must not turn a describe into a 500, and
// ResourceNotFoundException is the answer AWS models for a name the service
// cannot produce (AGENTS.md § malformed persisted state must be isolated).
func (s *backupStore) getVault(ctx context.Context, region, name string) (*vaultRecord, bool, error) {
	raw, found, err := s.store.Get(ctx, nsVaults, serviceutil.RegionKey(region, name))
	if err != nil || !found {
		return nil, false, err
	}
	var v vaultRecord
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		s.log.Warn("skipping undecodable vault record", zap.String("vault", name), zap.Error(err))
		return nil, false, nil
	}
	return &v, true, nil
}

// listVaults returns every vault in region, skipping records that cannot be
// decoded so that one bad payload does not fail the whole listing.
func (s *backupStore) listVaults(ctx context.Context, region string) ([]vaultRecord, error) {
	pairs, err := s.store.Scan(ctx, nsVaults, serviceutil.RegionKey(region, ""))
	if err != nil {
		return nil, err
	}
	out := make([]vaultRecord, 0, len(pairs))
	for _, kv := range pairs {
		var v vaultRecord
		if err := json.Unmarshal([]byte(kv.Value), &v); err != nil {
			s.log.Warn("skipping undecodable vault record", zap.String("key", kv.Key), zap.Error(err))
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *backupStore) deleteVault(ctx context.Context, region, name string) error {
	return s.store.Delete(ctx, nsVaults, serviceutil.RegionKey(region, name))
}

// ─── Plans ────────────────────────────────────────────────────

func (s *backupStore) putPlan(ctx context.Context, region string, p *planRecord) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsPlans, serviceutil.RegionKey(region, p.BackupPlanID), string(raw))
}

func (s *backupStore) getPlan(ctx context.Context, region, id string) (*planRecord, bool, error) {
	raw, found, err := s.store.Get(ctx, nsPlans, serviceutil.RegionKey(region, id))
	if err != nil || !found {
		return nil, false, err
	}
	var p planRecord
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		s.log.Warn("skipping undecodable plan record", zap.String("plan", id), zap.Error(err))
		return nil, false, nil
	}
	return &p, true, nil
}

func (s *backupStore) listPlans(ctx context.Context, region string) ([]planRecord, error) {
	pairs, err := s.store.Scan(ctx, nsPlans, serviceutil.RegionKey(region, ""))
	if err != nil {
		return nil, err
	}
	out := make([]planRecord, 0, len(pairs))
	for _, kv := range pairs {
		var p planRecord
		if err := json.Unmarshal([]byte(kv.Value), &p); err != nil {
			s.log.Warn("skipping undecodable plan record", zap.String("key", kv.Key), zap.Error(err))
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *backupStore) deletePlan(ctx context.Context, region, id string) error {
	return s.store.Delete(ctx, nsPlans, serviceutil.RegionKey(region, id))
}
