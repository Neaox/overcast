package lambda

// store.go — Lambda state access helpers.
//
// All Lambda state flows through lambdaStore, never through direct state.Store calls
// in handlers or the invoker. JSON serialisation lives here.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	// nsFunctions is the state store namespace for Lambda function definitions.
	// Keys are function names.
	nsFunctions = "lambda:functions"

	// nsInvocations is the state store namespace for invocation records.
	// Keys are "{functionName}:{timestamp_ns}" so each invocation is distinct.
	nsInvocations = "lambda:invocations"

	// nsTestEvents is the state store namespace for saved test events.
	// Keys are "{functionName}:{eventName}".
	nsTestEvents = "lambda:test-events"
)

// lambdaStore wraps state.Store with Lambda-specific helpers.
type lambdaStore struct {
	mu            sync.Mutex
	store         state.Store
	defaultRegion string
	clk           clock.Clock
}

func newLambdaStore(store state.Store, defaultRegion string, clk clock.Clock) *lambdaStore {
	return &lambdaStore{store: store, defaultRegion: defaultRegion, clk: clk}
}

// region extracts the per-request region from context, falling back to the default.
func (s *lambdaStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

// getFunction retrieves a function by name. Returns nil, nil when not found.
func (s *lambdaStore) getFunction(ctx context.Context, name string) (*Function, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsFunctions, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var fn Function
	if err := json.Unmarshal([]byte(raw), &fn); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("lambda: decode function %q: %w", name, err))
	}
	return &fn, nil
}

// putFunction stores a function definition.
func (s *lambdaStore) putFunction(ctx context.Context, fn *Function) *protocol.AWSError {
	raw, err := json.Marshal(fn)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsFunctions, serviceutil.RegionKey(s.region(ctx), fn.Name), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// deleteFunction removes a function from the store.
func (s *lambdaStore) deleteFunction(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsFunctions, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// listFunctions returns all stored functions.
func (s *lambdaStore) listFunctions(ctx context.Context) ([]*Function, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsFunctions, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*Function, 0, len(kvs))
	for _, kv := range kvs {
		var fn Function
		if err := json.Unmarshal([]byte(kv.Value), &fn); err != nil {
			continue // skip corrupt entries
		}
		out = append(out, &fn)
	}
	return out, nil
}

// listAllFunctions returns all stored functions across all regions.
// Used by startup reconciliation which must not be scoped to a single region.
func (s *lambdaStore) listAllFunctions(ctx context.Context) ([]*Function, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsFunctions, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*Function, 0, len(kvs))
	for _, kv := range kvs {
		var fn Function
		if err := json.Unmarshal([]byte(kv.Value), &fn); err != nil {
			continue // skip corrupt entries
		}
		out = append(out, &fn)
	}
	return out, nil
}

// invocationRecord captures metadata written each time InvokeAsync runs.
type invocationRecord struct {
	FunctionName string `json:"function_name"`
	FunctionARN  string `json:"function_arn"`
	PayloadSize  int    `json:"payload_size"`
}

// addInvocation writes an invocation record, keyed by "{name}:{nano_timestamp}".
// It is used to make notification delivery observable in tests.
func (s *lambdaStore) addInvocation(ctx context.Context, fn *Function, payload []byte) error {
	rec := invocationRecord{
		FunctionName: fn.Name,
		FunctionARN:  fn.ARN,
		PayloadSize:  len(payload),
	}
	raw, _ := json.Marshal(rec)
	// Use a unique key per invocation so multiple calls are all preserved.
	key := fn.Name + ":" + uniqueSuffix(s.clk)
	return s.store.Set(ctx, nsInvocations, serviceutil.RegionKey(s.region(ctx), key), string(raw))
}

// invocationSeq is a monotonic counter used to ensure unique invocation keys
// even when two invocations happen within the same nanosecond.
var invocationSeq atomic.Int64

// uniqueSuffix returns a string that is unique per process run, combining
// the current nanosecond timestamp and a monotonic counter.
func uniqueSuffix(clk clock.Clock) string {
	seq := invocationSeq.Add(1)
	return strconv.FormatInt(clk.Now().UnixNano(), 10) + "-" + strconv.FormatInt(seq, 10)
}

// functionNameFromARN parses the function name from a full Lambda ARN.
// ARN format: arn:aws:lambda:<region>:<account>:function:<name>
// Also handles unqualified names and partial ARNs gracefully.
func functionNameFromARN(arn string) string {
	if !strings.HasPrefix(arn, "arn:") {
		// Treat bare names as-is.
		return arn
	}
	parts := strings.Split(arn, ":")
	// arn:aws:lambda:region:account:function:name → index 6
	if len(parts) >= 7 && parts[5] == "function" {
		return parts[6]
	}
	return arn
}

// regionFromFunctionARN extracts the region segment from a full Lambda ARN.
// Returns "" for unqualified names or malformed ARNs so callers can fall back
// to the emulator's default region.
func regionFromFunctionARN(arn string) string {
	if !strings.HasPrefix(arn, "arn:") {
		return ""
	}
	parts := strings.Split(arn, ":")
	// arn:aws:lambda:region:account:function:name → region at index 3
	if len(parts) >= 4 {
		return parts[3]
	}
	return ""
}

// ─── Saved test events ─────────────────────────────────────────────────────

// TestEvent is a saved test event payload associated with a Lambda function.
type TestEvent struct {
	Name         string `json:"name"`
	FunctionName string `json:"function_name"`
	Body         string `json:"body"` // JSON event payload
}

// testEventKey builds the store key for a test event: "{functionName}:{eventName}".
func testEventKey(functionName, eventName string) string {
	return functionName + ":" + eventName
}

// listTestEvents returns all saved test events for a function.
func (s *lambdaStore) listTestEvents(ctx context.Context, functionName string) ([]TestEvent, error) {
	kvs, err := s.store.Scan(ctx, nsTestEvents, serviceutil.RegionKey(s.region(ctx), functionName+":"))
	if err != nil {
		return nil, err
	}
	out := make([]TestEvent, 0, len(kvs))
	for _, kv := range kvs {
		var te TestEvent
		if err := json.Unmarshal([]byte(kv.Value), &te); err != nil {
			continue
		}
		out = append(out, te)
	}
	return out, nil
}

// putTestEvent saves or updates a test event.
func (s *lambdaStore) putTestEvent(ctx context.Context, te *TestEvent) error {
	raw, err := json.Marshal(te)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, nsTestEvents, serviceutil.RegionKey(s.region(ctx), testEventKey(te.FunctionName, te.Name)), string(raw))
}

// deleteTestEvent removes a saved test event.
func (s *lambdaStore) deleteTestEvent(ctx context.Context, functionName, eventName string) error {
	return s.store.Delete(ctx, nsTestEvents, serviceutil.RegionKey(s.region(ctx), testEventKey(functionName, eventName)))
}

// ─── Versions ──────────────────────────────────────────────────────────────

const (
	// nsVersions stores immutable function version snapshots.
	// Keys are "{functionName}:{versionNumber:010d}" (zero-padded so Scan returns
	// them in numeric order).
	nsVersions = "lambda:versions"

	// nsVersionCounters stores the next version number per function.
	// Keys are "{functionName}", values are decimal integers.
	nsVersionCounters = "lambda:version-counters"
)

// FunctionVersion is an immutable snapshot of a function configuration published
// via PublishVersion. It mirrors the AWS FunctionConfiguration wire shape with the
// additional Version and CodeSha256 fields.
type FunctionVersion struct {
	// Embed the full function config — all fields are frozen at publish time.
	Function
	// Version is the numeric version identifier (1, 2, 3, …).
	Version int `json:"version"`
	// Description overrides the function description for this specific version.
	Description string `json:"version_description,omitempty"`
	// CodeSha256 is the SHA-256 of the deployment package at publish time.
	CodeSha256 string `json:"code_sha256,omitempty"`
}

// versionKey returns the store key for a version, zero-padded so Scan returns
// versions in numeric order.
func versionKey(functionName string, version int) string {
	return fmt.Sprintf("%s:%010d", functionName, version)
}

// nextVersion atomically increments and returns the next version number for a
// function. Version numbers start at 1.
func (s *lambdaStore) nextVersion(ctx context.Context, functionName string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := serviceutil.RegionKey(s.region(ctx), functionName)
	raw, found, err := s.store.Get(ctx, nsVersionCounters, key)
	if err != nil {
		return 0, fmt.Errorf("lambda: read version counter %q: %w", functionName, err)
	}
	next := 1
	if found && raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("lambda: parse version counter %q: %w", functionName, err)
		}
		next = n + 1
	}
	if err := s.store.Set(ctx, nsVersionCounters, key, strconv.Itoa(next)); err != nil {
		return 0, fmt.Errorf("lambda: save version counter %q: %w", functionName, err)
	}
	return next, nil
}

// putVersion stores an immutable function version.
func (s *lambdaStore) putVersion(ctx context.Context, v *FunctionVersion) *protocol.AWSError {
	raw, err := json.Marshal(v)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsVersions, serviceutil.RegionKey(s.region(ctx), versionKey(v.Name, v.Version)), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// listVersions returns all published versions for a function in ascending order.
func (s *lambdaStore) listVersions(ctx context.Context, functionName string) ([]*FunctionVersion, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsVersions, serviceutil.RegionKey(s.region(ctx), functionName+":"))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*FunctionVersion, 0, len(kvs))
	for _, kv := range kvs {
		var v FunctionVersion
		if err := json.Unmarshal([]byte(kv.Value), &v); err != nil {
			continue
		}
		out = append(out, &v)
	}
	return out, nil
}

// ─── Aliases ───────────────────────────────────────────────────────────────

const (
	// nsAliases stores function aliases.
	// Keys are "{functionName}:{aliasName}".
	nsAliases = "lambda:aliases"
)

// FunctionAlias is a named pointer to a specific function version.
type FunctionAlias struct {
	FunctionName    string `json:"function_name"`
	Name            string `json:"name"`
	FunctionVersion string `json:"function_version"` // e.g. "3" or "$LATEST"
	Description     string `json:"description,omitempty"`
	AliasARN        string `json:"alias_arn"`
	RevisionId      string `json:"revision_id"`
}

// aliasKey returns the store key for an alias.
func aliasKey(functionName, aliasName string) string {
	return functionName + ":" + aliasName
}

// putAlias stores or updates an alias.
func (s *lambdaStore) putAlias(ctx context.Context, a *FunctionAlias) *protocol.AWSError {
	raw, err := json.Marshal(a)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsAliases, serviceutil.RegionKey(s.region(ctx), aliasKey(a.FunctionName, a.Name)), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// getAlias retrieves an alias by function name + alias name. Returns nil, nil when not found.
func (s *lambdaStore) getAlias(ctx context.Context, functionName, aliasName string) (*FunctionAlias, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsAliases, serviceutil.RegionKey(s.region(ctx), aliasKey(functionName, aliasName)))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var a FunctionAlias
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("lambda: decode alias %q/%q: %w", functionName, aliasName, err))
	}
	return &a, nil
}

// listAliases returns all aliases for a function.
func (s *lambdaStore) listAliases(ctx context.Context, functionName string) ([]*FunctionAlias, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsAliases, serviceutil.RegionKey(s.region(ctx), functionName+":"))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*FunctionAlias, 0, len(kvs))
	for _, kv := range kvs {
		var a FunctionAlias
		if err := json.Unmarshal([]byte(kv.Value), &a); err != nil {
			continue
		}
		out = append(out, &a)
	}
	return out, nil
}

// deleteAlias removes an alias.
func (s *lambdaStore) deleteAlias(ctx context.Context, functionName, aliasName string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsAliases, serviceutil.RegionKey(s.region(ctx), aliasKey(functionName, aliasName))); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ─── Layers ────────────────────────────────────────────────────────────────

const (
	// nsLayers stores layer version records.
	// Keys are "{layerName}:{version:010d}" — zero-padded so Scan returns versions
	// in numeric order.
	nsLayers = "lambda:layers"

	// nsLayerCounters stores the next version number per layer name.
	// Keys are "{layerName}", values are decimal integers.
	nsLayerCounters = "lambda:layer-counters"
)

// LayerVersion is the domain model for a published Lambda layer version.
type LayerVersion struct {
	LayerName               string   `json:"layer_name"`
	LayerARN                string   `json:"layer_arn"`
	LayerVersionARN         string   `json:"layer_version_arn"`
	Version                 int64    `json:"version"`
	Description             string   `json:"description,omitempty"`
	CreatedDate             string   `json:"created_date"`
	CompatibleRuntimes      []string `json:"compatible_runtimes,omitempty"`
	CompatibleArchitectures []string `json:"compatible_architectures,omitempty"`
	// Content stores the raw zip bytes.
	Content []byte `json:"content,omitempty"`
	// CodeSize is the byte length of Content.
	CodeSize int64 `json:"code_size"`
}

// layerVersionKey returns the store key for a layer version, zero-padded for
// ordered Scan results.
func layerVersionKey(layerName string, version int64) string {
	return fmt.Sprintf("%s:%010d", layerName, version)
}

// nextLayerVersion atomically increments and returns the next version number
// for a layer. Version numbers start at 1.
func (s *lambdaStore) nextLayerVersion(ctx context.Context, layerName string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := serviceutil.RegionKey(s.region(ctx), layerName)
	raw, found, err := s.store.Get(ctx, nsLayerCounters, key)
	if err != nil {
		return 0, fmt.Errorf("lambda: read layer version counter %q: %w", layerName, err)
	}
	var next int64 = 1
	if found && raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("lambda: parse layer version counter %q: %w", layerName, err)
		}
		next = n + 1
	}
	if err := s.store.Set(ctx, nsLayerCounters, key, strconv.FormatInt(next, 10)); err != nil {
		return 0, fmt.Errorf("lambda: save layer version counter %q: %w", layerName, err)
	}
	return next, nil
}

// putLayerVersion stores a layer version.
func (s *lambdaStore) putLayerVersion(ctx context.Context, lv *LayerVersion) *protocol.AWSError {
	raw, err := json.Marshal(lv)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsLayers, serviceutil.RegionKey(s.region(ctx), layerVersionKey(lv.LayerName, lv.Version)), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// getLayerVersion retrieves a specific layer version. Returns nil, nil when not found.
func (s *lambdaStore) getLayerVersion(ctx context.Context, layerName string, version int64) (*LayerVersion, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsLayers, serviceutil.RegionKey(s.region(ctx), layerVersionKey(layerName, version)))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var lv LayerVersion
	if err := json.Unmarshal([]byte(raw), &lv); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("lambda: decode layer %q v%d: %w", layerName, version, err))
	}
	return &lv, nil
}

// listLayerVersions returns all versions for a layer in ascending order.
func (s *lambdaStore) listLayerVersions(ctx context.Context, layerName string) ([]*LayerVersion, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsLayers, serviceutil.RegionKey(s.region(ctx), layerName+":"))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]*LayerVersion, 0, len(kvs))
	for _, kv := range kvs {
		var lv LayerVersion
		if err := json.Unmarshal([]byte(kv.Value), &lv); err != nil {
			continue
		}
		out = append(out, &lv)
	}
	return out, nil
}

// listAllLayerNames returns the distinct layer names that have at least one version.
// It scans the counters namespace which has one entry per layer name.
func (s *lambdaStore) listAllLayerNames(ctx context.Context) ([]string, *protocol.AWSError) {
	kvs, err := s.store.Scan(ctx, nsLayerCounters, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	names := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		_, rest := serviceutil.SplitRegionKey(kv.Key)
		names = append(names, rest)
	}
	return names, nil
}

// deleteLayerVersion removes a specific layer version.
func (s *lambdaStore) deleteLayerVersion(ctx context.Context, layerName string, version int64) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsLayers, serviceutil.RegionKey(s.region(ctx), layerVersionKey(layerName, version))); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ─── Provisioned concurrency ───────────────────────────────────────────────

const (
	// nsProvisionedConcurrency stores provisioned concurrency configs.
	// Keys are "{functionName}:{qualifier}".
	nsProvisionedConcurrency = "lambda:provisioned-concurrency"
)

// ProvisionedConcurrencyConfig is the domain model for a provisioned concurrency setting.
type ProvisionedConcurrencyConfig struct {
	FunctionName                             string `json:"function_name"`
	Qualifier                                string `json:"qualifier"`
	RequestedProvisionedConcurrentExecutions int    `json:"requested"`
	LastModified                             string `json:"last_modified"`
}

// provisionedConcurrencyKey builds the store key for a provisioned concurrency config.
func provisionedConcurrencyKey(functionName, qualifier string) string {
	return functionName + ":" + qualifier
}

// putProvisionedConcurrencyConfig stores a provisioned concurrency config.
func (s *lambdaStore) putProvisionedConcurrencyConfig(ctx context.Context, cfg *ProvisionedConcurrencyConfig) *protocol.AWSError {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), provisionedConcurrencyKey(cfg.FunctionName, cfg.Qualifier))
	if err := s.store.Set(ctx, nsProvisionedConcurrency, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// getProvisionedConcurrencyConfig retrieves a provisioned concurrency config. Returns nil, nil when not found.
func (s *lambdaStore) getProvisionedConcurrencyConfig(ctx context.Context, functionName, qualifier string) (*ProvisionedConcurrencyConfig, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), provisionedConcurrencyKey(functionName, qualifier))
	raw, found, err := s.store.Get(ctx, nsProvisionedConcurrency, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil
	}
	var cfg ProvisionedConcurrencyConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("lambda: decode provisioned concurrency %q/%q: %w", functionName, qualifier, err))
	}
	return &cfg, nil
}

// getLayerVersionByARN parses a layer version ARN of the form
// arn:aws:lambda:{region}:{account}:layer:{name}:{version} and delegates to
// getLayerVersion. Returns nil, nil when the ARN has an unexpected shape or the
// layer version does not exist.
func (s *lambdaStore) getLayerVersionByARN(ctx context.Context, arn string) (*LayerVersion, *protocol.AWSError) {
	// Minimum expected ARN has 8 colon-separated segments.
	parts := strings.Split(arn, ":")
	if len(parts) < 8 {
		return nil, nil
	}
	layerName := parts[6]
	versionStr := parts[7]
	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil {
		return nil, nil
	}
	return s.getLayerVersion(ctx, layerName, version)
}
