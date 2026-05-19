package secretsmanager

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsSecrets = "secretsmanager:secrets"
)

// Tag represents a key-value tag on a secret.
type Tag struct {
	Key   string `json:"Key" cbor:"Key"`
	Value string `json:"Value" cbor:"Value"`
}

// SecretVersion holds the payload for one version of a secret.
type SecretVersion struct {
	VersionId    string   `json:"VersionId" cbor:"VersionId"`
	SecretString string   `json:"SecretString,omitempty" cbor:"SecretString,omitempty"`
	SecretBinary string   `json:"SecretBinary,omitempty" cbor:"SecretBinary,omitempty"` // base64-encoded
	Stages       []string `json:"VersionStages" cbor:"VersionStages"`
	CreatedDate  float64  `json:"CreatedDate" cbor:"CreatedDate"`
}

// RotationRules describes the automatic rotation schedule.
type RotationRules struct {
	AutomaticallyAfterDays int64 `json:"AutomaticallyAfterDays,omitempty" cbor:"AutomaticallyAfterDays,omitempty"`
}

// Secret is the full domain model stored for each secret.
type Secret struct {
	ARN               string          `json:"ARN"`
	Name              string          `json:"Name"`
	Description       string          `json:"Description,omitempty"`
	Tags              []Tag           `json:"Tags,omitempty"`
	Versions          []SecretVersion `json:"Versions"`
	CurrentVersionId  string          `json:"CurrentVersionId"`
	CreatedDate       float64         `json:"CreatedDate"`
	LastChangedDate   float64         `json:"LastChangedDate"`
	RotationEnabled   bool            `json:"RotationEnabled,omitempty"`
	RotationRules     *RotationRules  `json:"RotationRules,omitempty"`
	RotationLambdaARN string          `json:"RotationLambdaARN,omitempty"`
}

// smStore wraps state.Store with Secrets Manager specific access helpers.
type smStore struct {
	store         state.Store
	clk           clock.Clock
	defaultRegion string
}

func newSMStore(store state.Store, clk clock.Clock, defaultRegion string) *smStore {
	return &smStore{store: store, clk: clk, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (s *smStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

func (s *smStore) getSecret(ctx context.Context, name string) (*Secret, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsSecrets, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errResourceNotFound(name)
	}
	var sec Secret
	if err := json.Unmarshal([]byte(raw), &sec); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &sec, nil
}

func (s *smStore) putSecret(ctx context.Context, sec *Secret) *protocol.AWSError {
	raw, err := json.Marshal(sec)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsSecrets, serviceutil.RegionKey(s.region(ctx), sec.Name), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *smStore) deleteSecret(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsSecrets, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *smStore) listSecrets(ctx context.Context) ([]Secret, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsSecrets, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]Secret, 0, len(pairs))
	for _, p := range pairs {
		var sec Secret
		if err := json.Unmarshal([]byte(p.Value), &sec); err != nil {
			continue
		}
		out = append(out, sec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// resolveSecret looks up a secret by name or ARN.
func (s *smStore) resolveSecret(ctx context.Context, secretId string) (*Secret, *protocol.AWSError) {
	// Try direct name lookup first
	sec, aerr := s.getSecret(ctx, secretId)
	if aerr == nil {
		return sec, nil
	}
	// If it looks like an ARN, scan for a matching ARN
	if len(secretId) > 12 && secretId[:4] == "arn:" {
		all, scanErr := s.listSecrets(ctx)
		if scanErr != nil {
			return nil, scanErr
		}
		for i := range all {
			if all[i].ARN == secretId {
				return &all[i], nil
			}
		}
	}
	return nil, errResourceNotFound(secretId)
}

func (s *smStore) now() time.Time {
	return s.clk.Now()
}

// ─── Error constructors ────────────────────────────────────────────────────

func errResourceNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    "Secrets Manager can't find the specified secret: " + name,
		HTTPStatus: 400,
	}
}

func errResourceExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceExistsException",
		Message:    "The operation failed because the secret " + name + " already exists.",
		HTTPStatus: 400,
	}
}

func errInvalidParameter(msg string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidParameterException",
		Message:    msg,
		HTTPStatus: 400,
	}
}
