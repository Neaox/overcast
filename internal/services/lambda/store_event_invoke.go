package lambda

// store_event_invoke.go — FunctionEventInvokeConfig persistence.
//
// A config is keyed by function name + qualifier (qualifier "" means
// unqualified / $LATEST), the same convention as function URL configs
// (store_url.go) and aliases (store.go). AWS treats a function, a version and
// an alias as three separately configurable things, so the qualifier is part of
// the identity rather than a filter applied afterwards.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// nsEventInvokeConfigs stores per-function asynchronous invocation settings.
// Keys are "{functionName}:{qualifier}" (qualifier "" for unqualified).
const nsEventInvokeConfigs = "lambda:event-invoke-configs"

// EventInvokeConfig is the domain model behind
// Put/Get/Update/Delete/ListFunctionEventInvokeConfigs.
//
// The two limits are pointers because "unset" and "zero" are different
// settings and AWS reports them differently: an absent MaximumRetryAttempts
// means the default of two, while an explicit 0 means do not retry at all.
// Collapsing them would silently turn "do not retry" into "retry twice", which
// is the opposite of what the caller asked for.
type EventInvokeConfig struct {
	FunctionName string `json:"function_name"`
	// Qualifier is "" for the unqualified function, otherwise a version number
	// or alias name. It is part of the key, not just data.
	Qualifier string `json:"qualifier,omitempty"`
	// FunctionArn is the qualified ARN when Qualifier is set, so a response
	// names the exact thing that was configured.
	FunctionArn              string             `json:"function_arn"`
	MaximumRetryAttempts     *int               `json:"maximum_retry_attempts,omitempty"`
	MaximumEventAgeInSeconds *int               `json:"maximum_event_age_in_seconds,omitempty"`
	DestinationConfig        *DestinationConfig `json:"destination_config,omitempty"`
	// LastModifiedUnix is Unix seconds, because that is what AWS returns for
	// this resource — unlike FunctionConfiguration.LastModified, which is an
	// RFC 3339 string. Storing it in the wire's own units keeps the conversion
	// out of the handler.
	LastModifiedUnix int64 `json:"last_modified_unix"`
}

// onSuccessARN and onFailureARN read a side of the destination config without
// the caller re-deriving the nil checks each time.
func (c *EventInvokeConfig) onSuccessARN() string {
	if c == nil || c.DestinationConfig == nil || c.DestinationConfig.OnSuccess == nil {
		return ""
	}
	return c.DestinationConfig.OnSuccess.Destination
}

func (c *EventInvokeConfig) onFailureARN() string {
	if c == nil || c.DestinationConfig == nil || c.DestinationConfig.OnFailure == nil {
		return ""
	}
	return c.DestinationConfig.OnFailure.Destination
}

// eventInvokeKey builds the store key for a function + qualifier pair.
func eventInvokeKey(functionName, qualifier string) string {
	return functionName + ":" + qualifier
}

func (s *lambdaStore) putEventInvokeConfig(ctx context.Context, c *EventInvokeConfig) *protocol.AWSError {
	raw, err := json.Marshal(c)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), eventInvokeKey(c.FunctionName, c.Qualifier))
	if err := s.store.Set(ctx, nsEventInvokeConfigs, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// getEventInvokeConfig returns nil (and no error) when the function has no
// configuration, which is the common case and not a failure.
func (s *lambdaStore) getEventInvokeConfig(ctx context.Context, functionName, qualifier string) (*EventInvokeConfig, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), eventInvokeKey(functionName, qualifier))
	raw, found, err := s.store.Get(ctx, nsEventInvokeConfigs, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var c EventInvokeConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError,
			fmt.Errorf("lambda: decode event invoke config %q/%q: %w", functionName, qualifier, err))
	}
	return &c, nil
}

// listEventInvokeConfigs returns every configuration belonging to a function,
// across qualifiers. A record that cannot be decoded is skipped rather than
// failing the list — one bad row must not hide the others.
func (s *lambdaStore) listEventInvokeConfigs(ctx context.Context, functionName string) ([]*EventInvokeConfig, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), functionName+":")
	kvs, err := s.store.Scan(ctx, nsEventInvokeConfigs, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*EventInvokeConfig, 0, len(kvs))
	for _, kv := range kvs {
		var c EventInvokeConfig
		if err := json.Unmarshal([]byte(kv.Value), &c); err != nil {
			continue
		}
		out = append(out, &c)
	}
	return out, nil
}

func (s *lambdaStore) deleteEventInvokeConfig(ctx context.Context, functionName, qualifier string) *protocol.AWSError {
	key := serviceutil.RegionKey(s.region(ctx), eventInvokeKey(functionName, qualifier))
	if err := s.store.Delete(ctx, nsEventInvokeConfigs, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// deleteEventInvokeConfigsForFunction removes every qualifier's configuration,
// so a deleted function does not leave settings behind for a later function of
// the same name to inherit.
func (s *lambdaStore) deleteEventInvokeConfigsForFunction(ctx context.Context, functionName string) {
	configs, aerr := s.listEventInvokeConfigs(ctx, functionName)
	if aerr != nil {
		return
	}
	for _, c := range configs {
		_ = s.deleteEventInvokeConfig(ctx, functionName, c.Qualifier)
	}
}
