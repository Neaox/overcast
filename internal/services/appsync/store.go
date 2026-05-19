package appsync

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	storeNS = "appsync"

	// Key prefixes — each sub-resource type gets its own prefix within the
	// "appsync" namespace. The API ID is embedded in the key to scope
	// children to their parent API.
	prefixAPI      = "api:"
	prefixSchema   = "schema:"
	prefixKey      = "key:"
	prefixDS       = "ds:"
	prefixFunction = "fn:"
	prefixResolver = "resolver:"

	// Future resource prefixes — types defined, store methods ready, stubs return 501.
	prefixDomain   = "domain:"
	prefixAssoc    = "assoc:"
	prefixCache    = "cache:"
	prefixSrcAssoc = "srcassoc:"
	prefixEnvVars  = "envvars:"
	prefixType     = "type:"
	prefixEventApi = "eventapi:"
	prefixChanNS   = "channs:"
)

// Store wraps state.Store with AppSync-specific helpers.
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
		return nil, fmt.Errorf("appsync: get %q: %w", key, err)
	}
	if !found {
		return nil, nil
	}
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("appsync: unmarshal %q: %w", key, err)
	}
	return &v, nil
}

// putRecord saves a single JSON record.
func putRecord[T any](st *Store, ctx context.Context, key string, v *T) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("appsync: marshal %q: %w", key, err)
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
		return nil, fmt.Errorf("appsync: scan %q: %w", prefix, err)
	}
	items := make([]*T, 0, len(pairs))
	for _, p := range pairs {
		var v T
		if err := json.Unmarshal([]byte(p.Value), &v); err != nil {
			return nil, fmt.Errorf("appsync: unmarshal scan result: %w", err)
		}
		items = append(items, &v)
	}
	return items, nil
}

// ─── GraphQL API ─────────────────────────────────────────────────────────────

func (st *Store) GetAPI(ctx context.Context, apiID string) (*GraphqlAPI, error) {
	return getRecord[GraphqlAPI](st, ctx, prefixAPI+apiID)
}

func (st *Store) PutAPI(ctx context.Context, api *GraphqlAPI) error {
	return putRecord(st, ctx, prefixAPI+api.ApiId, api)
}

func (st *Store) DeleteAPI(ctx context.Context, apiID string) error {
	return st.del(ctx, prefixAPI+apiID)
}

func (st *Store) ListAPIs(ctx context.Context) ([]*GraphqlAPI, error) {
	return scanRecords[GraphqlAPI](st, ctx, prefixAPI)
}

// DeleteAPIAndChildren removes an API and all of its sub-resources (schema,
// keys, data sources, functions, resolvers).
func (st *Store) DeleteAPIAndChildren(ctx context.Context, apiID string) error {
	// Delete children — each type has keys prefixed with apiID.
	for _, prefix := range []string{prefixKey, prefixDS, prefixFunction, prefixResolver} {
		pairs, err := st.s.Scan(ctx, storeNS, st.rk(ctx, prefix+apiID+":"))
		if err != nil {
			return fmt.Errorf("appsync: cascade scan %s%s: %w", prefix, apiID, err)
		}
		for _, p := range pairs {
			if err := st.s.Delete(ctx, storeNS, p.Key); err != nil {
				return fmt.Errorf("appsync: cascade delete %q: %w", p.Key, err)
			}
		}
	}
	// Delete the schema (single key per API, not colon-separated).
	_ = st.del(ctx, prefixSchema+apiID)
	// Delete the API itself.
	return st.del(ctx, prefixAPI+apiID)
}

// ─── Schema ──────────────────────────────────────────────────────────────────

func (st *Store) GetSchema(ctx context.Context, apiID string) (*Schema, error) {
	return getRecord[Schema](st, ctx, prefixSchema+apiID)
}

func (st *Store) PutSchema(ctx context.Context, schema *Schema) error {
	return putRecord(st, ctx, prefixSchema+schema.ApiId, schema)
}

// ─── API Keys ────────────────────────────────────────────────────────────────

func apiKeyKey(apiID, keyID string) string { return prefixKey + apiID + ":" + keyID }

func (st *Store) GetApiKey(ctx context.Context, apiID, keyID string) (*ApiKey, error) {
	return getRecord[ApiKey](st, ctx, apiKeyKey(apiID, keyID))
}

func (st *Store) PutApiKey(ctx context.Context, apiID string, key *ApiKey) error {
	return putRecord(st, ctx, apiKeyKey(apiID, key.Id), key)
}

func (st *Store) DeleteApiKey(ctx context.Context, apiID, keyID string) error {
	return st.del(ctx, apiKeyKey(apiID, keyID))
}

func (st *Store) ListApiKeys(ctx context.Context, apiID string) ([]*ApiKey, error) {
	return scanRecords[ApiKey](st, ctx, prefixKey+apiID+":")
}

// ─── Data Sources ────────────────────────────────────────────────────────────

func dsKey(apiID, name string) string { return prefixDS + apiID + ":" + name }

func (st *Store) GetDataSource(ctx context.Context, apiID, name string) (*DataSource, error) {
	return getRecord[DataSource](st, ctx, dsKey(apiID, name))
}

func (st *Store) PutDataSource(ctx context.Context, apiID string, ds *DataSource) error {
	return putRecord(st, ctx, dsKey(apiID, ds.Name), ds)
}

func (st *Store) DeleteDataSource(ctx context.Context, apiID, name string) error {
	return st.del(ctx, dsKey(apiID, name))
}

func (st *Store) ListDataSources(ctx context.Context, apiID string) ([]*DataSource, error) {
	return scanRecords[DataSource](st, ctx, prefixDS+apiID+":")
}

// ─── Functions ───────────────────────────────────────────────────────────────

func fnKey(apiID, fnID string) string { return prefixFunction + apiID + ":" + fnID }

func (st *Store) GetFunction(ctx context.Context, apiID, fnID string) (*FunctionConfiguration, error) {
	return getRecord[FunctionConfiguration](st, ctx, fnKey(apiID, fnID))
}

func (st *Store) PutFunction(ctx context.Context, apiID string, fn *FunctionConfiguration) error {
	return putRecord(st, ctx, fnKey(apiID, fn.FunctionId), fn)
}

func (st *Store) DeleteFunction(ctx context.Context, apiID, fnID string) error {
	return st.del(ctx, fnKey(apiID, fnID))
}

func (st *Store) ListFunctions(ctx context.Context, apiID string) ([]*FunctionConfiguration, error) {
	return scanRecords[FunctionConfiguration](st, ctx, prefixFunction+apiID+":")
}

// ─── Resolvers ───────────────────────────────────────────────────────────────

func resolverKey(apiID, typeName, fieldName string) string {
	return prefixResolver + apiID + ":" + typeName + "." + fieldName
}

func (st *Store) GetResolver(ctx context.Context, apiID, typeName, fieldName string) (*Resolver, error) {
	return getRecord[Resolver](st, ctx, resolverKey(apiID, typeName, fieldName))
}

func (st *Store) PutResolver(ctx context.Context, apiID string, res *Resolver) error {
	return putRecord(st, ctx, resolverKey(apiID, res.TypeName, res.FieldName), res)
}

func (st *Store) DeleteResolver(ctx context.Context, apiID, typeName, fieldName string) error {
	return st.del(ctx, resolverKey(apiID, typeName, fieldName))
}

func (st *Store) ListResolvers(ctx context.Context, apiID, typeName string) ([]*Resolver, error) {
	prefix := prefixResolver + apiID + ":"
	if typeName != "" {
		prefix += typeName + "."
	}
	return scanRecords[Resolver](st, ctx, prefix)
}

// ─── Domain Names ────────────────────────────────────────────────────────────

func (st *Store) GetDomainName(ctx context.Context, domainName string) (*DomainNameConfig, error) {
	return getRecord[DomainNameConfig](st, ctx, prefixDomain+domainName)
}

func (st *Store) PutDomainName(ctx context.Context, dn *DomainNameConfig) error {
	return putRecord(st, ctx, prefixDomain+dn.DomainName, dn)
}

func (st *Store) DeleteDomainName(ctx context.Context, domainName string) error {
	return st.del(ctx, prefixDomain+domainName)
}

func (st *Store) ListDomainNames(ctx context.Context) ([]*DomainNameConfig, error) {
	return scanRecords[DomainNameConfig](st, ctx, prefixDomain)
}

// ─── API Associations ────────────────────────────────────────────────────────

func (st *Store) GetApiAssociation(ctx context.Context, domainName string) (*ApiAssociation, error) {
	return getRecord[ApiAssociation](st, ctx, prefixAssoc+domainName)
}

func (st *Store) PutApiAssociation(ctx context.Context, assoc *ApiAssociation) error {
	return putRecord(st, ctx, prefixAssoc+assoc.DomainName, assoc)
}

func (st *Store) DeleteApiAssociation(ctx context.Context, domainName string) error {
	return st.del(ctx, prefixAssoc+domainName)
}

// ─── API Cache ───────────────────────────────────────────────────────────────

func (st *Store) GetApiCache(ctx context.Context, apiID string) (*ApiCacheConfig, error) {
	return getRecord[ApiCacheConfig](st, ctx, prefixCache+apiID)
}

func (st *Store) PutApiCache(ctx context.Context, apiID string, cache *ApiCacheConfig) error {
	return putRecord(st, ctx, prefixCache+apiID, cache)
}

func (st *Store) DeleteApiCache(ctx context.Context, apiID string) error {
	return st.del(ctx, prefixCache+apiID)
}

// ─── Source API Associations ─────────────────────────────────────────────────

func srcAssocKey(mergedApiID, assocID string) string {
	return prefixSrcAssoc + mergedApiID + ":" + assocID
}

func (st *Store) GetSourceApiAssociation(ctx context.Context, mergedApiID, assocID string) (*SourceApiAssociation, error) {
	return getRecord[SourceApiAssociation](st, ctx, srcAssocKey(mergedApiID, assocID))
}

func (st *Store) PutSourceApiAssociation(ctx context.Context, mergedApiID string, assoc *SourceApiAssociation) error {
	return putRecord(st, ctx, srcAssocKey(mergedApiID, assoc.AssociationId), assoc)
}

func (st *Store) DeleteSourceApiAssociation(ctx context.Context, mergedApiID, assocID string) error {
	return st.del(ctx, srcAssocKey(mergedApiID, assocID))
}

func (st *Store) ListSourceApiAssociations(ctx context.Context, mergedApiID string) ([]*SourceApiAssociation, error) {
	return scanRecords[SourceApiAssociation](st, ctx, prefixSrcAssoc+mergedApiID+":")
}

// ─── Environment Variables ───────────────────────────────────────────────────

func (st *Store) GetEnvironmentVariables(ctx context.Context, apiID string) (*EnvironmentVariables, error) {
	return getRecord[EnvironmentVariables](st, ctx, prefixEnvVars+apiID)
}

func (st *Store) PutEnvironmentVariables(ctx context.Context, apiID string, ev *EnvironmentVariables) error {
	return putRecord(st, ctx, prefixEnvVars+apiID, ev)
}

// ─── Type Definitions ────────────────────────────────────────────────────────

func typeKey(apiID, typeName string) string {
	return prefixType + apiID + ":" + typeName
}

func (st *Store) GetType(ctx context.Context, apiID, typeName string) (*TypeDefinition, error) {
	return getRecord[TypeDefinition](st, ctx, typeKey(apiID, typeName))
}

func (st *Store) PutType(ctx context.Context, apiID string, td *TypeDefinition) error {
	return putRecord(st, ctx, typeKey(apiID, td.Name), td)
}

func (st *Store) DeleteType(ctx context.Context, apiID, typeName string) error {
	return st.del(ctx, typeKey(apiID, typeName))
}

func (st *Store) ListTypes(ctx context.Context, apiID string) ([]*TypeDefinition, error) {
	return scanRecords[TypeDefinition](st, ctx, prefixType+apiID+":")
}

// ─── Event APIs ──────────────────────────────────────────────────────────────

func (st *Store) GetEventApi(ctx context.Context, apiID string) (*EventApi, error) {
	return getRecord[EventApi](st, ctx, prefixEventApi+apiID)
}

func (st *Store) PutEventApi(ctx context.Context, api *EventApi) error {
	return putRecord(st, ctx, prefixEventApi+api.ApiId, api)
}

func (st *Store) DeleteEventApi(ctx context.Context, apiID string) error {
	// Delete channel namespaces first.
	pairs, err := st.s.Scan(ctx, storeNS, st.rk(ctx, prefixChanNS+apiID+":"))
	if err != nil {
		return fmt.Errorf("appsync: cascade scan channel namespaces for %s: %w", apiID, err)
	}
	for _, p := range pairs {
		if err := st.s.Delete(ctx, storeNS, p.Key); err != nil {
			return fmt.Errorf("appsync: cascade delete channel namespace %q: %w", p.Key, err)
		}
	}
	return st.del(ctx, prefixEventApi+apiID)
}

func (st *Store) ListEventApis(ctx context.Context) ([]*EventApi, error) {
	return scanRecords[EventApi](st, ctx, prefixEventApi)
}

// ─── Channel Namespaces ─────────────────────────────────────────────────────

func chanNSKey(apiID, name string) string { return prefixChanNS + apiID + ":" + name }

func (st *Store) GetChannelNamespace(ctx context.Context, apiID, name string) (*ChannelNamespace, error) {
	return getRecord[ChannelNamespace](st, ctx, chanNSKey(apiID, name))
}

func (st *Store) PutChannelNamespace(ctx context.Context, apiID string, ns *ChannelNamespace) error {
	return putRecord(st, ctx, chanNSKey(apiID, ns.Name), ns)
}

func (st *Store) DeleteChannelNamespace(ctx context.Context, apiID, name string) error {
	return st.del(ctx, chanNSKey(apiID, name))
}

func (st *Store) ListChannelNamespaces(ctx context.Context, apiID string) ([]*ChannelNamespace, error) {
	return scanRecords[ChannelNamespace](st, ctx, prefixChanNS+apiID+":")
}
