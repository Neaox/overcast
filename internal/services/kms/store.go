package kms

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	storeNS     = "kms"
	keyPrefix   = "key:"
	aliasPrefix = "alias:"
)

// Tag represents a KMS resource tag.
type Tag struct {
	TagKey   string `json:"TagKey" cbor:"TagKey"`
	TagValue string `json:"TagValue" cbor:"TagValue"`
}

// Key represents a KMS key.
type Key struct {
	KeyID        string     `json:"KeyID"`
	ARN          string     `json:"ARN"`
	Description  string     `json:"Description"`
	KeySpec      string     `json:"KeySpec"`  // "SYMMETRIC_DEFAULT", "RSA_2048", etc.
	KeyUsage     string     `json:"KeyUsage"` // "ENCRYPT_DECRYPT", "SIGN_VERIFY"
	Enabled      bool       `json:"Enabled"`
	KeyState     string     `json:"KeyState"` // "Enabled", "Disabled", "PendingDeletion"
	DeletionDate *time.Time `json:"DeletionDate,omitempty"`
	CreatedAt    time.Time  `json:"CreatedAt"`
	Tags         []Tag      `json:"Tags,omitempty"`
	Policy       string     `json:"Policy,omitempty"` // JSON key policy document
	// Crypto material (never sent to clients)
	AESKey     []byte `json:"AESKey,omitempty"`     // 32 bytes for SYMMETRIC_DEFAULT
	RSAPrivKey []byte `json:"RSAPrivKey,omitempty"` // PEM-encoded private key for RSA
}

// Alias represents a KMS alias.
type Alias struct {
	AliasName   string    `json:"AliasName"`
	AliasARN    string    `json:"AliasARN"`
	TargetKeyID string    `json:"TargetKeyID"`
	CreatedAt   time.Time `json:"CreatedAt"`
}

// Store wraps state.Store with KMS-specific helpers.
type Store struct {
	s             state.Store
	defaultRegion string
}

func newStore(s state.Store, defaultRegion string) *Store {
	return &Store{s: s, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (st *Store) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, st.defaultRegion)
}

// ── Keys ─────────────────────────────────────────────────────────────────────

// GetKey retrieves a key by ID. Returns nil, nil if not found.
func (st *Store) GetKey(ctx context.Context, keyID string) (*Key, error) {
	raw, found, err := st.s.Get(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), keyPrefix+keyID))
	if err != nil {
		return nil, fmt.Errorf("kms: get key %q: %w", keyID, err)
	}
	if !found {
		return nil, nil
	}
	var k Key
	if err := json.Unmarshal([]byte(raw), &k); err != nil {
		return nil, fmt.Errorf("kms: unmarshal key %q: %w", keyID, err)
	}
	return &k, nil
}

// PutKey saves a key record.
func (st *Store) PutKey(ctx context.Context, k *Key) error {
	raw, err := json.Marshal(k)
	if err != nil {
		return fmt.Errorf("kms: marshal key %q: %w", k.KeyID, err)
	}
	return st.s.Set(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), keyPrefix+k.KeyID), string(raw))
}

// ScanKeys returns all keys.
func (st *Store) ScanKeys(ctx context.Context) ([]*Key, error) {
	pairs, err := st.s.Scan(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), keyPrefix))
	if err != nil {
		return nil, fmt.Errorf("kms: scan keys: %w", err)
	}
	out := make([]*Key, 0, len(pairs))
	for _, kv := range pairs {
		var k Key
		if err := json.Unmarshal([]byte(kv.Value), &k); err != nil {
			continue
		}
		out = append(out, &k)
	}
	return out, nil
}

// ── Aliases ───────────────────────────────────────────────────────────────────

// GetAlias retrieves an alias by name. Returns nil, nil if not found.
func (st *Store) GetAlias(ctx context.Context, aliasName string) (*Alias, error) {
	raw, found, err := st.s.Get(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), aliasPrefix+aliasName))
	if err != nil {
		return nil, fmt.Errorf("kms: get alias %q: %w", aliasName, err)
	}
	if !found {
		return nil, nil
	}
	var a Alias
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, fmt.Errorf("kms: unmarshal alias %q: %w", aliasName, err)
	}
	return &a, nil
}

// PutAlias saves an alias record.
func (st *Store) PutAlias(ctx context.Context, a *Alias) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("kms: marshal alias %q: %w", a.AliasName, err)
	}
	return st.s.Set(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), aliasPrefix+a.AliasName), string(raw))
}

// DeleteAlias removes an alias.
func (st *Store) DeleteAlias(ctx context.Context, aliasName string) error {
	return st.s.Delete(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), aliasPrefix+aliasName))
}

// ScanAliases returns all aliases, optionally filtered to a single keyID.
// If keyID is empty, all aliases are returned.
func (st *Store) ScanAliases(ctx context.Context, keyID string) ([]*Alias, error) {
	pairs, err := st.s.Scan(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), aliasPrefix))
	if err != nil {
		return nil, fmt.Errorf("kms: scan aliases: %w", err)
	}
	out := make([]*Alias, 0, len(pairs))
	for _, kv := range pairs {
		var a Alias
		if err := json.Unmarshal([]byte(kv.Value), &a); err != nil {
			continue
		}
		if keyID == "" || a.TargetKeyID == keyID {
			out = append(out, &a)
		}
	}
	return out, nil
}

// ── Grants ───────────────────────────────────────────────────────────────────

const grantPrefix = "grant:"

// GrantConstraint represents a constraint on a KMS grant.
type GrantConstraint struct {
	EncryptionContextSubset map[string]string `json:"EncryptionContextSubset,omitempty"`
	EncryptionContextEquals map[string]string `json:"EncryptionContextEquals,omitempty"`
}

// Grant represents a KMS grant.
type Grant struct {
	GrantID           string           `json:"GrantID"`
	GrantToken        string           `json:"GrantToken"`
	KeyID             string           `json:"KeyID"`
	GranteePrincipal  string           `json:"GranteePrincipal"`
	RetiringPrincipal string           `json:"RetiringPrincipal,omitempty"`
	Operations        []string         `json:"Operations"`
	Constraints       *GrantConstraint `json:"Constraints,omitempty"`
	Name              string           `json:"Name,omitempty"`
	CreationDate      time.Time        `json:"CreationDate"`
	ExpirationDate    *time.Time       `json:"ExpirationDate,omitempty"`
}

// PutGrant saves a grant record.
func (st *Store) PutGrant(ctx context.Context, g *Grant) error {
	raw, err := json.Marshal(g)
	if err != nil {
		return fmt.Errorf("kms: marshal grant %q: %w", g.GrantID, err)
	}
	return st.s.Set(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), grantPrefix+g.GrantID), string(raw))
}

// GetGrant retrieves a grant by ID. Returns nil, nil if not found.
func (st *Store) GetGrant(ctx context.Context, grantID string) (*Grant, error) {
	raw, found, err := st.s.Get(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), grantPrefix+grantID))
	if err != nil {
		return nil, fmt.Errorf("kms: get grant %q: %w", grantID, err)
	}
	if !found {
		return nil, nil
	}
	var g Grant
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return nil, fmt.Errorf("kms: unmarshal grant %q: %w", grantID, err)
	}
	return &g, nil
}

// DeleteGrant removes a grant.
func (st *Store) DeleteGrant(ctx context.Context, grantID string) error {
	return st.s.Delete(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), grantPrefix+grantID))
}

// ScanGrantsByKey returns all grants for a given key ID.
func (st *Store) ScanGrantsByKey(ctx context.Context, keyID string) ([]*Grant, error) {
	pairs, err := st.s.Scan(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), grantPrefix))
	if err != nil {
		return nil, fmt.Errorf("kms: scan grants: %w", err)
	}
	out := make([]*Grant, 0, len(pairs))
	for _, kv := range pairs {
		var g Grant
		if err := json.Unmarshal([]byte(kv.Value), &g); err != nil {
			continue
		}
		if g.KeyID == keyID {
			out = append(out, &g)
		}
	}
	return out, nil
}

// ScanGrantsByPrincipal returns all grants for a given principal.
func (st *Store) ScanGrantsByPrincipal(ctx context.Context, principal string) ([]*Grant, error) {
	pairs, err := st.s.Scan(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), grantPrefix))
	if err != nil {
		return nil, fmt.Errorf("kms: scan grants: %w", err)
	}
	out := make([]*Grant, 0, len(pairs))
	for _, kv := range pairs {
		var g Grant
		if err := json.Unmarshal([]byte(kv.Value), &g); err != nil {
			continue
		}
		if g.RetiringPrincipal == principal {
			out = append(out, &g)
		}
	}
	return out, nil
}

// ScanAllGrants returns every grant in the store.
func (st *Store) ScanAllGrants(ctx context.Context) ([]*Grant, error) {
	pairs, err := st.s.Scan(ctx, storeNS, serviceutil.RegionKey(st.region(ctx), grantPrefix))
	if err != nil {
		return nil, fmt.Errorf("kms: scan grants: %w", err)
	}
	out := make([]*Grant, 0, len(pairs))
	for _, kv := range pairs {
		var g Grant
		if err := json.Unmarshal([]byte(kv.Value), &g); err != nil {
			continue
		}
		out = append(out, &g)
	}
	return out, nil
}
