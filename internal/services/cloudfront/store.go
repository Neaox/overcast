package cloudfront

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	storeNS = "cloudfront"

	// Key prefixes — each resource type gets its own prefix.
	prefixDist = "dist:"

	// Future resource prefixes — types defined in types.go, stubs return 501.
	prefixInv             = "inv:"
	prefixOAC             = "oac:"
	prefixTag             = "tag:"
	prefixCachePolicy     = "cpol:"
	prefixOriginReqPolicy = "opol:"
	prefixRespHdrPolicy   = "rpol:"
	prefixOAI             = "oai:"
	prefixFunc            = "fn:"
	prefixKeyGrp          = "keygrp:"
	prefixPubKey          = "pubkey:"
	prefixMonSub          = "monsub:"
	prefixRtLog           = "rtlog:"
	prefixFLE             = "fle:"
	prefixFLEProf         = "fleprof:"
	prefixContDeploy      = "cdp:"
)

// Store wraps state.Store with CloudFront-specific helpers.
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

// rk builds a region-scoped store key.
func (st *Store) rk(ctx context.Context, key string) string {
	return serviceutil.RegionKey(st.region(ctx), key)
}

// ─── Generic helpers ─────────────────────────────────────────────────────────

// getRecord loads a single JSON record. Returns nil, nil when not found.
func getRecord[T any](st *Store, ctx context.Context, key string) (*T, error) {
	raw, found, err := st.s.Get(ctx, storeNS, st.rk(ctx, key))
	if err != nil {
		return nil, fmt.Errorf("cloudfront: get %q: %w", key, err)
	}
	if !found {
		return nil, nil
	}
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("cloudfront: unmarshal %q: %w", key, err)
	}
	return &v, nil
}

// putRecord saves a single JSON record.
func putRecord[T any](st *Store, ctx context.Context, key string, v *T) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("cloudfront: marshal %q: %w", key, err)
	}
	return st.s.Set(ctx, storeNS, st.rk(ctx, key), string(raw))
}

// del deletes a single record.
func (st *Store) del(ctx context.Context, key string) error {
	return st.s.Delete(ctx, storeNS, st.rk(ctx, key))
}

// scanRecords lists all records matching a key prefix.
func scanRecords[T any](st *Store, ctx context.Context, prefix string) ([]*T, error) {
	pairs, err := st.s.Scan(ctx, storeNS, st.rk(ctx, prefix))
	if err != nil {
		return nil, fmt.Errorf("cloudfront: scan %q: %w", prefix, err)
	}
	items := make([]*T, 0, len(pairs))
	for _, p := range pairs {
		var v T
		if err := json.Unmarshal([]byte(p.Value), &v); err != nil {
			return nil, fmt.Errorf("cloudfront: unmarshal scan result: %w", err)
		}
		items = append(items, &v)
	}
	return items, nil
}

// ─── Distribution ────────────────────────────────────────────────────────────

// GetDistribution retrieves a distribution by ID.
func (st *Store) GetDistribution(ctx context.Context, id string) (*Distribution, error) {
	return getRecord[Distribution](st, ctx, prefixDist+id)
}

// PutDistribution persists a distribution.
func (st *Store) PutDistribution(ctx context.Context, d *Distribution) error {
	return putRecord(st, ctx, prefixDist+d.ID, d)
}

// DeleteDistribution removes a distribution by ID.
func (st *Store) DeleteDistribution(ctx context.Context, id string) error {
	return st.del(ctx, prefixDist+id)
}

// ListDistributions returns all distributions.
func (st *Store) ListDistributions(ctx context.Context) ([]*Distribution, error) {
	return scanRecords[Distribution](st, ctx, prefixDist)
}

// FindByCallerRef scans distributions to find one with a matching CallerReference.
// Returns nil, nil if no match is found.
func (st *Store) FindByCallerRef(ctx context.Context, callerRef string) (*Distribution, error) {
	all, err := st.ListDistributions(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		if d.DistributionConfig.CallerReference == callerRef {
			return d, nil
		}
	}
	return nil, nil
}

// ─── Invalidation ───────────────────────────────────────────────────────────

// GetInvalidation retrieves an invalidation by distribution ID and invalidation ID.
func (st *Store) GetInvalidation(ctx context.Context, distID, invID string) (*Invalidation, error) {
	return getRecord[Invalidation](st, ctx, prefixInv+distID+":"+invID)
}

// PutInvalidation persists an invalidation scoped to a distribution.
func (st *Store) PutInvalidation(ctx context.Context, distID string, inv *Invalidation) error {
	return putRecord(st, ctx, prefixInv+distID+":"+inv.ID, inv)
}

// ListInvalidations returns all invalidations for a distribution.
func (st *Store) ListInvalidations(ctx context.Context, distID string) ([]*Invalidation, error) {
	return scanRecords[Invalidation](st, ctx, prefixInv+distID+":")
}

// DeleteAllInvalidations removes every invalidation record for a distribution.
func (st *Store) DeleteAllInvalidations(ctx context.Context, distID string) error {
	invs, err := st.ListInvalidations(ctx, distID)
	if err != nil {
		return err
	}
	for _, inv := range invs {
		if err := st.del(ctx, prefixInv+distID+":"+inv.ID); err != nil {
			return err
		}
	}
	return nil
}

// ─── Origin Access Control ──────────────────────────────────────────────────

// GetOAC retrieves an origin access control by ID.
func (st *Store) GetOAC(ctx context.Context, id string) (*OriginAccessControl, error) {
	return getRecord[OriginAccessControl](st, ctx, prefixOAC+id)
}

// PutOAC persists an origin access control.
func (st *Store) PutOAC(ctx context.Context, oac *OriginAccessControl) error {
	return putRecord(st, ctx, prefixOAC+oac.ID, oac)
}

// DeleteOAC removes an origin access control by ID.
func (st *Store) DeleteOAC(ctx context.Context, id string) error {
	return st.del(ctx, prefixOAC+id)
}

// ListOACs returns all origin access controls.
func (st *Store) ListOACs(ctx context.Context) ([]*OriginAccessControl, error) {
	return scanRecords[OriginAccessControl](st, ctx, prefixOAC)
}

// ─── Tags ───────────────────────────────────────────────────────────────────

// GetTags retrieves tags for a resource ARN.
func (st *Store) GetTags(ctx context.Context, arn string) (*Tags, error) {
	return getRecord[Tags](st, ctx, prefixTag+arn)
}

// PutTags persists tags for a resource ARN.
func (st *Store) PutTags(ctx context.Context, arn string, tags *Tags) error {
	return putRecord(st, ctx, prefixTag+arn, tags)
}

// DeleteTags removes all tags for a resource ARN.
func (st *Store) DeleteTags(ctx context.Context, arn string) error {
	return st.del(ctx, prefixTag+arn)
}

// ─── Cache Policy ───────────────────────────────────────────────────────────

// GetCachePolicy retrieves a cache policy by ID.
func (st *Store) GetCachePolicy(ctx context.Context, id string) (*CachePolicy, error) {
	return getRecord[CachePolicy](st, ctx, prefixCachePolicy+id)
}

// PutCachePolicy persists a cache policy.
func (st *Store) PutCachePolicy(ctx context.Context, cp *CachePolicy) error {
	return putRecord(st, ctx, prefixCachePolicy+cp.ID, cp)
}

// DeleteCachePolicy removes a cache policy by ID.
func (st *Store) DeleteCachePolicy(ctx context.Context, id string) error {
	return st.del(ctx, prefixCachePolicy+id)
}

// ListCachePolicies returns all cache policies.
func (st *Store) ListCachePolicies(ctx context.Context) ([]*CachePolicy, error) {
	return scanRecords[CachePolicy](st, ctx, prefixCachePolicy)
}

// ─── Origin Request Policy ──────────────────────────────────────────────────

// GetOriginRequestPolicy retrieves an origin request policy by ID.
func (st *Store) GetOriginRequestPolicy(ctx context.Context, id string) (*OriginRequestPolicy, error) {
	return getRecord[OriginRequestPolicy](st, ctx, prefixOriginReqPolicy+id)
}

// PutOriginRequestPolicy persists an origin request policy.
func (st *Store) PutOriginRequestPolicy(ctx context.Context, p *OriginRequestPolicy) error {
	return putRecord(st, ctx, prefixOriginReqPolicy+p.ID, p)
}

// DeleteOriginRequestPolicy removes an origin request policy by ID.
func (st *Store) DeleteOriginRequestPolicy(ctx context.Context, id string) error {
	return st.del(ctx, prefixOriginReqPolicy+id)
}

// ListOriginRequestPolicies returns all origin request policies.
func (st *Store) ListOriginRequestPolicies(ctx context.Context) ([]*OriginRequestPolicy, error) {
	return scanRecords[OriginRequestPolicy](st, ctx, prefixOriginReqPolicy)
}

// ─── Response Headers Policy ────────────────────────────────────────────────

// GetResponseHeadersPolicy retrieves a response headers policy by ID.
func (st *Store) GetResponseHeadersPolicy(ctx context.Context, id string) (*ResponseHeadersPolicy, error) {
	return getRecord[ResponseHeadersPolicy](st, ctx, prefixRespHdrPolicy+id)
}

// PutResponseHeadersPolicy persists a response headers policy.
func (st *Store) PutResponseHeadersPolicy(ctx context.Context, p *ResponseHeadersPolicy) error {
	return putRecord(st, ctx, prefixRespHdrPolicy+p.ID, p)
}

// DeleteResponseHeadersPolicy removes a response headers policy by ID.
func (st *Store) DeleteResponseHeadersPolicy(ctx context.Context, id string) error {
	return st.del(ctx, prefixRespHdrPolicy+id)
}

// ListResponseHeadersPolicies returns all response headers policies.
func (st *Store) ListResponseHeadersPolicies(ctx context.Context) ([]*ResponseHeadersPolicy, error) {
	return scanRecords[ResponseHeadersPolicy](st, ctx, prefixRespHdrPolicy)
}

// ─── Origin Access Identity (legacy) ────────────────────────────────────────

// GetOAI retrieves a legacy origin access identity by ID.
func (st *Store) GetOAI(ctx context.Context, id string) (*CloudFrontOriginAccessIdentity, error) {
	return getRecord[CloudFrontOriginAccessIdentity](st, ctx, prefixOAI+id)
}

// PutOAI persists a legacy origin access identity.
func (st *Store) PutOAI(ctx context.Context, oai *CloudFrontOriginAccessIdentity) error {
	return putRecord(st, ctx, prefixOAI+oai.ID, oai)
}

// DeleteOAI removes a legacy origin access identity by ID.
func (st *Store) DeleteOAI(ctx context.Context, id string) error {
	return st.del(ctx, prefixOAI+id)
}

// ListOAIs returns all legacy origin access identities.
func (st *Store) ListOAIs(ctx context.Context) ([]*CloudFrontOriginAccessIdentity, error) {
	return scanRecords[CloudFrontOriginAccessIdentity](st, ctx, prefixOAI)
}

// ─── CloudFront Functions (future P6) ───────────────────────────────────────

// GetFunction retrieves a CloudFront Function by name.
func (st *Store) GetFunction(ctx context.Context, name string) (*CloudFrontFunction, error) {
	return getRecord[CloudFrontFunction](st, ctx, prefixFunc+name)
}

// PutFunction persists a CloudFront Function.
func (st *Store) PutFunction(ctx context.Context, f *CloudFrontFunction) error {
	return putRecord(st, ctx, prefixFunc+f.Name, f)
}

// DeleteFunction removes a CloudFront Function by name.
func (st *Store) DeleteFunction(ctx context.Context, name string) error {
	return st.del(ctx, prefixFunc+name)
}

// ListFunctions returns all CloudFront Functions.
func (st *Store) ListFunctions(ctx context.Context) ([]*CloudFrontFunction, error) {
	return scanRecords[CloudFrontFunction](st, ctx, prefixFunc)
}

// GetFunctionByARN returns the function with the given ARN, or nil if not found.
// ARN format: arn:aws:cloudfront::000000000000:function/{name}.
func (st *Store) GetFunctionByARN(ctx context.Context, arn string) (*CloudFrontFunction, error) {
	parts := strings.Split(arn, "/")
	if len(parts) < 2 {
		return nil, nil
	}
	name := parts[len(parts)-1]
	return st.GetFunction(ctx, name)
}

// ─── Key Groups ─────────────────────────────────────────────────────────────

// GetKeyGroup retrieves a key group by ID.
func (st *Store) GetKeyGroup(ctx context.Context, id string) (*KeyGroup, error) {
	return getRecord[KeyGroup](st, ctx, prefixKeyGrp+id)
}

// PutKeyGroup persists a key group.
func (st *Store) PutKeyGroup(ctx context.Context, kg *KeyGroup) error {
	return putRecord(st, ctx, prefixKeyGrp+kg.ID, kg)
}

// DeleteKeyGroup removes a key group by ID.
func (st *Store) DeleteKeyGroup(ctx context.Context, id string) error {
	return st.del(ctx, prefixKeyGrp+id)
}

// ListKeyGroups returns all key groups.
func (st *Store) ListKeyGroups(ctx context.Context) ([]*KeyGroup, error) {
	return scanRecords[KeyGroup](st, ctx, prefixKeyGrp)
}

// ─── Public Keys ────────────────────────────────────────────────────────────

// GetPublicKey retrieves a public key by ID.
func (st *Store) GetPublicKey(ctx context.Context, id string) (*PublicKey, error) {
	return getRecord[PublicKey](st, ctx, prefixPubKey+id)
}

// PutPublicKey persists a public key.
func (st *Store) PutPublicKey(ctx context.Context, pk *PublicKey) error {
	return putRecord(st, ctx, prefixPubKey+pk.ID, pk)
}

// DeletePublicKey removes a public key by ID.
func (st *Store) DeletePublicKey(ctx context.Context, id string) error {
	return st.del(ctx, prefixPubKey+id)
}

// ListPublicKeys returns all public keys.
func (st *Store) ListPublicKeys(ctx context.Context) ([]*PublicKey, error) {
	return scanRecords[PublicKey](st, ctx, prefixPubKey)
}

// ─── Monitoring Subscriptions ───────────────────────────────────────────────

// GetMonitoringSubscription retrieves a monitoring subscription by distribution ID.
func (st *Store) GetMonitoringSubscription(ctx context.Context, distID string) (*MonitoringSubscription, error) {
	return getRecord[MonitoringSubscription](st, ctx, prefixMonSub+distID)
}

// PutMonitoringSubscription persists a monitoring subscription.
func (st *Store) PutMonitoringSubscription(ctx context.Context, distID string, ms *MonitoringSubscription) error {
	return putRecord(st, ctx, prefixMonSub+distID, ms)
}

// DeleteMonitoringSubscription removes a monitoring subscription by distribution ID.
func (st *Store) DeleteMonitoringSubscription(ctx context.Context, distID string) error {
	return st.del(ctx, prefixMonSub+distID)
}

// ─── Realtime Log Configs ───────────────────────────────────────────────────

// GetRealtimeLogConfig retrieves a realtime log config by name.
func (st *Store) GetRealtimeLogConfig(ctx context.Context, name string) (*RealtimeLogConfig, error) {
	return getRecord[RealtimeLogConfig](st, ctx, prefixRtLog+name)
}

// PutRealtimeLogConfig persists a realtime log config.
func (st *Store) PutRealtimeLogConfig(ctx context.Context, rlc *RealtimeLogConfig) error {
	return putRecord(st, ctx, prefixRtLog+rlc.Name, rlc)
}

// DeleteRealtimeLogConfig removes a realtime log config by name.
func (st *Store) DeleteRealtimeLogConfig(ctx context.Context, name string) error {
	return st.del(ctx, prefixRtLog+name)
}

// ListRealtimeLogConfigs returns all realtime log configs.
func (st *Store) ListRealtimeLogConfigs(ctx context.Context) ([]*RealtimeLogConfig, error) {
	return scanRecords[RealtimeLogConfig](st, ctx, prefixRtLog)
}

// ─── Field-Level Encryption Configs ─────────────────────────────────────────

// GetFLEConfig retrieves a field-level encryption config by ID.
func (st *Store) GetFLEConfig(ctx context.Context, id string) (*FLEConfig, error) {
	return getRecord[FLEConfig](st, ctx, prefixFLE+id)
}

// PutFLEConfig persists a field-level encryption config.
func (st *Store) PutFLEConfig(ctx context.Context, c *FLEConfig) error {
	return putRecord(st, ctx, prefixFLE+c.ID, c)
}

// DeleteFLEConfig removes a field-level encryption config by ID.
func (st *Store) DeleteFLEConfig(ctx context.Context, id string) error {
	return st.del(ctx, prefixFLE+id)
}

// ListFLEConfigs returns all field-level encryption configs.
func (st *Store) ListFLEConfigs(ctx context.Context) ([]*FLEConfig, error) {
	return scanRecords[FLEConfig](st, ctx, prefixFLE)
}

// ─── Field-Level Encryption Profiles ────────────────────────────────────────

// GetFLEProfile retrieves a field-level encryption profile by ID.
func (st *Store) GetFLEProfile(ctx context.Context, id string) (*FLEProfile, error) {
	return getRecord[FLEProfile](st, ctx, prefixFLEProf+id)
}

// PutFLEProfile persists a field-level encryption profile.
func (st *Store) PutFLEProfile(ctx context.Context, p *FLEProfile) error {
	return putRecord(st, ctx, prefixFLEProf+p.ID, p)
}

// DeleteFLEProfile removes a field-level encryption profile by ID.
func (st *Store) DeleteFLEProfile(ctx context.Context, id string) error {
	return st.del(ctx, prefixFLEProf+id)
}

// ListFLEProfiles returns all field-level encryption profiles.
func (st *Store) ListFLEProfiles(ctx context.Context) ([]*FLEProfile, error) {
	return scanRecords[FLEProfile](st, ctx, prefixFLEProf)
}

// ─── Continuous Deployment Policies ─────────────────────────────────────────

// GetContinuousDeploymentPolicy retrieves a continuous deployment policy by ID.
func (st *Store) GetContinuousDeploymentPolicy(ctx context.Context, id string) (*ContinuousDeploymentPolicy, error) {
	return getRecord[ContinuousDeploymentPolicy](st, ctx, prefixContDeploy+id)
}

// PutContinuousDeploymentPolicy persists a continuous deployment policy.
func (st *Store) PutContinuousDeploymentPolicy(ctx context.Context, p *ContinuousDeploymentPolicy) error {
	return putRecord(st, ctx, prefixContDeploy+p.ID, p)
}

// DeleteContinuousDeploymentPolicy removes a continuous deployment policy by ID.
func (st *Store) DeleteContinuousDeploymentPolicy(ctx context.Context, id string) error {
	return st.del(ctx, prefixContDeploy+id)
}

// ListContinuousDeploymentPolicies returns all continuous deployment policies.
func (st *Store) ListContinuousDeploymentPolicies(ctx context.Context) ([]*ContinuousDeploymentPolicy, error) {
	return scanRecords[ContinuousDeploymentPolicy](st, ctx, prefixContDeploy)
}
