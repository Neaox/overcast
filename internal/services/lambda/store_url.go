package lambda

// store_url.go — Lambda function URL config persistence.
//
// A function URL config is keyed by function name + qualifier (qualifier ""
// means unqualified / $LATEST), same convention as aliases (store.go). The
// generated UrlID is looked up in reverse (Host-routed invocation only
// carries the UrlID, not the function name) by scanning this namespace —
// fine for an emulator's function counts; see getFunctionURLConfigByURLID.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const (
	// nsFunctionURLConfigs stores Lambda function URL configs.
	// Keys are "{functionName}:{qualifier}" (qualifier "" for unqualified).
	nsFunctionURLConfigs = "lambda:function-urls"
)

// FunctionURLConfig is a Lambda function URL configuration (CreateFunctionUrlConfig).
type FunctionURLConfig struct {
	FunctionName     string      `json:"function_name"`
	FunctionArn      string      `json:"function_arn"`
	Qualifier        string      `json:"qualifier,omitempty"`
	AuthType         string      `json:"auth_type"` // "NONE" or "AWS_IAM" — accepted but never enforced, see docs.
	Cors             *CorsConfig `json:"cors,omitempty"`
	InvokeMode       string      `json:"invoke_mode"` // "BUFFERED" (only mode supported) or "RESPONSE_STREAM"
	UrlID            string      `json:"url_id"`
	Region           string      `json:"region"`
	CreationTime     string      `json:"creation_time"`
	LastModifiedTime string      `json:"last_modified_time"`
}

// CorsConfig mirrors AWS's Cors shape for a function URL config. Overcast
// stores and returns it faithfully but does not enforce it — see
// docs/networking.md "Host-based addressing" AuthType/CORS caveat.
type CorsConfig struct {
	AllowCredentials *bool    `json:"allow_credentials,omitempty"`
	AllowHeaders     []string `json:"allow_headers,omitempty"`
	AllowMethods     []string `json:"allow_methods,omitempty"`
	AllowOrigins     []string `json:"allow_origins,omitempty"`
	ExposeHeaders    []string `json:"expose_headers,omitempty"`
	MaxAge           *int     `json:"max_age,omitempty"`
}

// functionURLKey returns the store key for a function URL config.
func functionURLKey(functionName, qualifier string) string {
	return functionName + ":" + qualifier
}

// putFunctionURLConfig stores or updates a function URL config.
func (s *lambdaStore) putFunctionURLConfig(ctx context.Context, c *FunctionURLConfig) *protocol.AWSError {
	raw, err := json.Marshal(c)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsFunctionURLConfigs, serviceutil.RegionKey(s.region(ctx), functionURLKey(c.FunctionName, c.Qualifier)), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// getFunctionURLConfig retrieves a function URL config by function name +
// qualifier. Returns nil, nil when not found.
func (s *lambdaStore) getFunctionURLConfig(ctx context.Context, functionName, qualifier string) (*FunctionURLConfig, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsFunctionURLConfigs, serviceutil.RegionKey(s.region(ctx), functionURLKey(functionName, qualifier)))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var c FunctionURLConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("lambda: decode function url config %q/%q: %w", functionName, qualifier, err))
	}
	return &c, nil
}

// listFunctionURLConfigs returns every function URL config for a function
// (one per qualifier).
func (s *lambdaStore) listFunctionURLConfigs(ctx context.Context, functionName string) ([]*FunctionURLConfig, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsFunctionURLConfigs, serviceutil.RegionKey(s.region(ctx), functionName+":"))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*FunctionURLConfig, 0, len(kvs))
	for _, kv := range kvs {
		var c FunctionURLConfig
		if err := json.Unmarshal([]byte(kv.Value), &c); err != nil {
			continue // skip corrupt entries
		}
		out = append(out, &c)
	}
	return out, nil
}

// deleteFunctionURLConfig removes a function URL config.
func (s *lambdaStore) deleteFunctionURLConfig(ctx context.Context, functionName, qualifier string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsFunctionURLConfigs, serviceutil.RegionKey(s.region(ctx), functionURLKey(functionName, qualifier))); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// getFunctionURLConfigByURLID resolves a config by its generated UrlID — the
// identifier a Host-routed invocation ({urlId}.lambda-url.{region}.{base})
// carries. Scans every region: a UrlID is a random ~32-char token with no
// meaningful collision risk, so this lookup does not need the caller's region
// (unlike API Gateway, where {apiId} alone is not always unique enough).
// Returns nil, nil when not found.
//
// Being region-agnostic HERE does not make the invocation region-agnostic.
// InvokeFunctionURL follows this with getFunction, which is region-scoped, so
// the region hint middleware.Region reads off the Host is still load-bearing —
// see TestInvokeFunctionURL_hostRoutedInNonDefaultRegion, which fails with a
// 404 "Function not found" when it is dropped.
func (s *lambdaStore) getFunctionURLConfigByURLID(ctx context.Context, urlID string) (*FunctionURLConfig, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsFunctionURLConfigs, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	for _, kv := range kvs {
		var c FunctionURLConfig
		if jsonErr := json.Unmarshal([]byte(kv.Value), &c); jsonErr != nil {
			continue // skip corrupt entries
		}
		if c.UrlID == urlID {
			return &c, nil
		}
	}
	return nil, nil
}
