package lambda

// store_code_signing.go — persistence for code signing configurations.
//
// These are standalone resources, not properties of a function: a
// configuration is created once and then referenced by any number of
// functions through their CodeSigningConfigArn. That is why they live in their
// own namespace keyed by the configuration id rather than hanging off the
// function record.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// nsCodeSigningConfigs is the state store namespace for code signing
// configurations. Keys are the configuration id ("csc-…").
const nsCodeSigningConfigs = "lambda:code-signing-configs"

// AllowedPublishers lists the signing profiles whose signatures are accepted.
// AWS requires between 1 and 20.
type AllowedPublishers struct {
	SigningProfileVersionArns []string `json:"SigningProfileVersionArns"`
}

// CodeSigningPolicies decides what happens when a signature check fails at
// deploy time. Overcast never runs those checks, but the setting round-trips
// so configuration-as-code reads back what it wrote.
type CodeSigningPolicies struct {
	UntrustedArtifactOnDeployment string `json:"UntrustedArtifactOnDeployment,omitempty"`
}

// CodeSigningConfig is the domain model for a code signing configuration. The
// JSON tags are the wire names, so it serialises straight into responses.
type CodeSigningConfig struct {
	CodeSigningConfigID  string               `json:"CodeSigningConfigId"`
	CodeSigningConfigArn string               `json:"CodeSigningConfigArn"`
	Description          string               `json:"Description,omitempty"`
	AllowedPublishers    AllowedPublishers    `json:"AllowedPublishers"`
	CodeSigningPolicies  *CodeSigningPolicies `json:"CodeSigningPolicies,omitempty"`
	LastModified         string               `json:"LastModified"`
}

func (s *lambdaStore) putCodeSigningConfig(ctx context.Context, csc *CodeSigningConfig) *protocol.AWSError {
	raw, err := json.Marshal(csc)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsCodeSigningConfigs,
		serviceutil.RegionKey(s.region(ctx), csc.CodeSigningConfigID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// getCodeSigningConfig returns nil, nil when no configuration has that id.
func (s *lambdaStore) getCodeSigningConfig(ctx context.Context, id string) (*CodeSigningConfig, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsCodeSigningConfigs, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var csc CodeSigningConfig
	if err := json.Unmarshal([]byte(raw), &csc); err != nil {
		// A single corrupt record must read as "not found" for this one
		// resource rather than failing the caller with a 500.
		return nil, protocol.Wrap(protocol.ErrInternalError,
			fmt.Errorf("lambda: decode code signing config %q: %w", id, err))
	}
	return &csc, nil
}

// listCodeSigningConfigs returns every configuration in the caller's region.
// Malformed records are skipped so one bad payload cannot break the listing.
func (s *lambdaStore) listCodeSigningConfigs(ctx context.Context) ([]*CodeSigningConfig, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsCodeSigningConfigs, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*CodeSigningConfig, 0, len(kvs))
	for _, kv := range kvs {
		var csc CodeSigningConfig
		if err := json.Unmarshal([]byte(kv.Value), &csc); err != nil {
			continue
		}
		out = append(out, &csc)
	}
	return out, nil
}

func (s *lambdaStore) deleteCodeSigningConfig(ctx context.Context, id string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsCodeSigningConfigs, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}
