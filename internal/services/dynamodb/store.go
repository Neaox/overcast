package dynamodb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const nsTables = "dynamodb:tables"

// StreamSpecification describes the DynamoDB Streams configuration for a table.
type StreamSpecification struct {
	StreamEnabled  bool   `json:"StreamEnabled"`
	StreamViewType string `json:"StreamViewType,omitempty"`
}

// TimeToLiveSpecification describes the TTL configuration for a table.
type TimeToLiveSpecification struct {
	Enabled       bool   `json:"Enabled"`
	AttributeName string `json:"AttributeName"`
}

// TimeToLiveDescription matches the AWS DescribeTimeToLive response shape.
type TimeToLiveDescription struct {
	TimeToLiveStatus string `json:"TimeToLiveStatus"`
	AttributeName    string `json:"AttributeName,omitempty"`
}

// BillingModeSummary contains the billing mode and last transition time.
type BillingModeSummary struct {
	BillingMode               string  `json:"BillingMode"`
	LastUpdateToPayPerRequest float64 `json:"LastUpdateToPayPerRequestDateTime,omitempty"`
}

// ProvisionedThroughput holds the read/write capacity units for a table or GSI.
type ProvisionedThroughput struct {
	ReadCapacityUnits  int64 `json:"ReadCapacityUnits"`
	WriteCapacityUnits int64 `json:"WriteCapacityUnits"`
}

// Table represents a DynamoDB table definition.
type Table struct {
	TableName              string                   `json:"TableName"`
	KeySchema              []KeySchemaElement       `json:"KeySchema"`
	AttributeDefinitions   []AttributeDef           `json:"AttributeDefinitions"`
	TableStatus            string                   `json:"TableStatus"`
	BillingMode            string                   `json:"BillingMode,omitempty"`
	BillingModeSummary     *BillingModeSummary      `json:"BillingModeSummary,omitempty"`
	ProvisionedThroughput  *ProvisionedThroughput   `json:"ProvisionedThroughput,omitempty"`
	TableARN               string                   `json:"TableArn"`
	CreationDateTime       float64                  `json:"CreationDateTime"`
	ItemCount              int64                    `json:"ItemCount"`
	StreamSpecification    *StreamSpecification     `json:"StreamSpecification,omitempty"`
	LatestStreamArn        string                   `json:"LatestStreamArn,omitempty"`
	LatestStreamLabel      string                   `json:"LatestStreamLabel,omitempty"`
	TTL                    *TimeToLiveSpecification `json:"TTL,omitempty"`
	GlobalSecondaryIndexes []SecondaryIndex         `json:"GlobalSecondaryIndexes,omitempty"`
	LocalSecondaryIndexes  []SecondaryIndex         `json:"LocalSecondaryIndexes,omitempty"`
}

// Projection describes which attributes are projected into a secondary index.
type Projection struct {
	ProjectionType   string   `json:"ProjectionType"` // ALL, KEYS_ONLY, INCLUDE
	NonKeyAttributes []string `json:"NonKeyAttributes,omitempty"`
}

// SecondaryIndex represents a GSI or LSI definition.
type SecondaryIndex struct {
	IndexName             string                 `json:"IndexName"`
	KeySchema             []KeySchemaElement     `json:"KeySchema"`
	Projection            Projection             `json:"Projection"`
	IndexArn              string                 `json:"IndexArn,omitempty"`
	IndexStatus           string                 `json:"IndexStatus,omitempty"`
	IndexSizeBytes        int64                  `json:"IndexSizeBytes"`
	ItemCount             int64                  `json:"ItemCount"`
	ProvisionedThroughput *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
}

// streamEnabled reports whether this table has an active stream.
func (t *Table) streamEnabled() bool {
	return t.StreamSpecification != nil && t.StreamSpecification.StreamEnabled
}

// ttlEnabled reports whether this table has TTL enabled.
func (t *Table) ttlEnabled() bool {
	return t.TTL != nil && t.TTL.Enabled
}

// ttlDescription returns the TTL description for DescribeTimeToLive responses.
func (t *Table) ttlDescription() *TimeToLiveDescription {
	if t.TTL == nil || !t.TTL.Enabled {
		return &TimeToLiveDescription{TimeToLiveStatus: "DISABLED"}
	}
	return &TimeToLiveDescription{
		TimeToLiveStatus: "ENABLED",
		AttributeName:    t.TTL.AttributeName,
	}
}

// streamViewType returns the configured view type, or "" when streams are off.
func (t *Table) streamViewType() string {
	if t.StreamSpecification == nil {
		return ""
	}
	return t.StreamSpecification.StreamViewType
}

// KeySchemaElement is a hash or range key definition.
type KeySchemaElement struct {
	AttributeName string `json:"AttributeName"`
	KeyType       string `json:"KeyType"` // "HASH" or "RANGE"
}

// AttributeDef defines an attribute type for key schema elements.
type AttributeDef struct {
	AttributeName string `json:"AttributeName"`
	AttributeType string `json:"AttributeType"` // "S", "N", "B"
}

// hashKeyName returns the partition key name for the table.
func (t *Table) hashKeyName() string {
	for _, k := range t.KeySchema {
		if k.KeyType == "HASH" {
			return k.AttributeName
		}
	}
	return ""
}

// sortKeyName returns the sort key name for the table, or "" if none.
func (t *Table) sortKeyName() string {
	for _, k := range t.KeySchema {
		if k.KeyType == "RANGE" {
			return k.AttributeName
		}
	}
	return ""
}

// findIndex looks up a GSI or LSI by name. Returns nil if not found.
func (t *Table) findIndex(name string) *SecondaryIndex {
	for i := range t.GlobalSecondaryIndexes {
		if t.GlobalSecondaryIndexes[i].IndexName == name {
			return &t.GlobalSecondaryIndexes[i]
		}
	}
	for i := range t.LocalSecondaryIndexes {
		if t.LocalSecondaryIndexes[i].IndexName == name {
			return &t.LocalSecondaryIndexes[i]
		}
	}
	return nil
}

// isGSI reports whether name identifies one of table's
// GlobalSecondaryIndexes, as opposed to a LocalSecondaryIndex or no index at
// all. The read path uses this to route GSI Query/Scan onto the ordered
// index structure (dynamodb-gsi-design.md §4) while LSI Query/Scan keeps the
// existing full-scan fallback unchanged — LSI routing is called out in §5 as
// separable follow-up work, not part of this flip, because LSIs have no
// equivalent index storage (index_maintenance.go's diffIndexMutations only
// ever iterates table.GlobalSecondaryIndexes, never LocalSecondaryIndexes).
func (t *Table) isGSI(name string) bool {
	for i := range t.GlobalSecondaryIndexes {
		if t.GlobalSecondaryIndexes[i].IndexName == name {
			return true
		}
	}
	return false
}

// indexHashKeyName returns the partition key name for a secondary index.
func indexHashKeyName(idx *SecondaryIndex) string {
	for _, k := range idx.KeySchema {
		if k.KeyType == "HASH" {
			return k.AttributeName
		}
	}
	return ""
}

// indexSortKeyName returns the sort key name for a secondary index, or "" if none.
func indexSortKeyName(idx *SecondaryIndex) string {
	for _, k := range idx.KeySchema {
		if k.KeyType == "RANGE" {
			return k.AttributeName
		}
	}
	return ""
}

// DynamoDB attribute value: {"S": "foo"} or {"N": "123"} etc.
type attrValue = map[string]any

// Item is a DynamoDB item represented in DynamoDB JSON format.
type Item = map[string]attrValue

// dynamoStore wraps state.Store (for table metadata), an itemBackend
// (for item data), and a streamBackend (for stream records).
type dynamoStore struct {
	tables        state.Store   // table descriptors
	items         itemBackend   // item data — memItemBackend or sqlItemBackend
	streams       streamBackend // stream records — memStreamBackend or sqlStreamBackend
	defaultRegion string
}

func newDynamoStore(tables state.Store, items itemBackend, streams streamBackend, defaultRegion string) *dynamoStore {
	return &dynamoStore{tables: tables, items: items, streams: streams, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (s *dynamoStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

// ---- Table helpers ---------------------------------------------------------

func (s *dynamoStore) getTable(ctx context.Context, name string) (*Table, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), name)
	raw, found, err := s.tables.Get(ctx, nsTables, key)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errTableNotFound(name)
	}
	var t Table
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &t, nil
}

func (s *dynamoStore) putTable(ctx context.Context, t *Table) *protocol.AWSError {
	raw, err := json.Marshal(t)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), t.TableName)
	if err := s.tables.Set(ctx, nsTables, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *dynamoStore) tableExists(ctx context.Context, name string) (bool, *protocol.AWSError) {
	key := serviceutil.RegionKey(s.region(ctx), name)
	_, found, err := s.tables.Get(ctx, nsTables, key)
	if err != nil {
		return false, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return found, nil
}

func (s *dynamoStore) listTables(ctx context.Context, prefix string) ([]*Table, *protocol.AWSError) {
	keys, err := s.tables.List(ctx, nsTables, serviceutil.RegionKey(s.region(ctx), prefix))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	tables := make([]*Table, 0, len(keys))
	for _, k := range keys {
		// keys are region-prefixed; strip the prefix for getTable
		_, tableName := serviceutil.SplitRegionKey(k)
		t, aerr := s.getTable(ctx, tableName)
		if aerr != nil {
			// Table was deleted between List and getTable (TOCTOU race).
			if aerr.HTTPStatus == http.StatusBadRequest {
				continue
			}
			return nil, aerr
		}
		tables = append(tables, t)
	}
	return tables, nil
}

// ---- Item helpers ----------------------------------------------------------

// extractKeyValue extracts the scalar string value from a DynamoDB attribute node.
// e.g. {"S": "foo"} → "foo", {"N": "42"} → "42".
func extractKeyValue(attr attrValue) string {
	for _, v := range attr {
		switch s := v.(type) {
		case string:
			return s
		}
	}
	return ""
}

// resolveKeys extracts the (hashKey, sortKey) pair from a key or item map
// using the table's key schema.  sortKey is "" for hash-only tables.
func resolveKeys(table *Table, keyOrItem Item) (hashKey, sortKey string, aerr *protocol.AWSError) {
	hashName := table.hashKeyName()
	if hashName == "" {
		return "", "", &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "Table has no hash key defined.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	hashAttr, ok := keyOrItem[hashName]
	if !ok {
		return "", "", &protocol.AWSError{
			Code:       "ValidationException",
			Message:    fmt.Sprintf("The provided key element does not match the schema: missing hash key %q", hashName),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	hashKey = extractKeyValue(hashAttr)

	sortName := table.sortKeyName()
	if sortName != "" {
		if sortAttr, ok := keyOrItem[sortName]; ok {
			sortKey = extractKeyValue(sortAttr)
		}
	}
	return hashKey, sortKey, nil
}

// resolveStorageKeys extracts and encodes the (hashKey, sortKey) pair for
// storage/indexing purposes: Number-typed key components are transformed via
// encodeOrderableNumber so the backend's composite/ORDER BY key sorts
// numerically (docs/plans/dynamodb-gsi-design.md §2); String/Binary
// components pass through unchanged, since their raw form already sorts
// correctly (UTF-8 byte order). This is the single choke point every
// storage call site (put/get/delete/scanPage cursor resolution) goes
// through so memItemBackend/sqlItemBackend never need to know about table
// schema or key types — they just store/compare whatever strings they're
// given.
//
// resolveKeys itself is left returning raw (unencoded) values — callers that
// need the original decimal text (e.g. debug display) call it directly; only
// storage callers need the encoded form.
func resolveStorageKeys(table *Table, keyOrItem Item) (hashKey, sortKey string, aerr *protocol.AWSError) {
	hashKey, sortKey, aerr = resolveKeys(table, keyOrItem)
	if aerr != nil {
		return "", "", aerr
	}
	hashKey = encodeStorageKeyComponent(keyAttrType(table, table.hashKeyName()), hashKey)
	if sortName := table.sortKeyName(); sortName != "" {
		sortKey = encodeStorageKeyComponent(keyAttrType(table, sortName), sortKey)
	}
	return hashKey, sortKey, nil
}

func (s *dynamoStore) putItem(ctx context.Context, table *Table, item Item) *protocol.AWSError {
	hashKey, sortKey, aerr := resolveStorageKeys(table, item)
	if aerr != nil {
		return aerr
	}
	if err := s.items.put(ctx, table.TableName, hashKey, sortKey, item); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// putItemWithIndexMaintenance is putItem plus GSI index-row maintenance
// (dynamodb-gsi-design.md section 3): oldItem is the item's previous value
// (nil if it didn't previously exist), used to diff against item and decide
// which of table's GSI index rows need to change. The base-item write and
// every resulting index-row write happen atomically (one *sql.Tx on the SQL
// backend, one mutex critical section on the memory backend — see
// item_store.go's putWithIndexMutations).
//
// Callers (PutItem, UpdateItem, BatchWriteItem) are responsible for having
// already fetched oldItem when table has any GSIs — see each handler's own
// comment for why that read is free (already needed for streams/
// ReturnValues, or for UpdateItem's upsert semantics) rather than a new
// cost this adds.
func (s *dynamoStore) putItemWithIndexMaintenance(ctx context.Context, table *Table, item, oldItem Item) *protocol.AWSError {
	hashKey, sortKey, aerr := resolveStorageKeys(table, item)
	if aerr != nil {
		return aerr
	}
	mutations := diffIndexMutations(table, oldItem, item)
	if err := s.items.putWithIndexMutations(ctx, table.TableName, hashKey, sortKey, item, mutations); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *dynamoStore) getItem(ctx context.Context, table *Table, key Item) (Item, *protocol.AWSError) {
	hashKey, sortKey, aerr := resolveStorageKeys(table, key)
	if aerr != nil {
		return nil, aerr
	}
	item, _, err := s.items.get(ctx, table.TableName, hashKey, sortKey)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return item, nil // nil means not found — handler returns 200 with empty Item
}

func (s *dynamoStore) deleteItem(ctx context.Context, table *Table, key Item) *protocol.AWSError {
	hashKey, sortKey, aerr := resolveStorageKeys(table, key)
	if aerr != nil {
		return aerr
	}
	if err := s.items.remove(ctx, table.TableName, hashKey, sortKey); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// deleteItemWithIndexMaintenance is deleteItem plus GSI index-row cleanup:
// oldItem (the item's value before this delete, or nil if it never existed)
// is diffed against a nil "new" state, which — per diffIndexMutations —
// produces exactly one delete mutation per GSI whose key oldItem satisfied,
// and none for GSIs it didn't. Atomic with the base-item delete, same as
// putItemWithIndexMaintenance.
func (s *dynamoStore) deleteItemWithIndexMaintenance(ctx context.Context, table *Table, key, oldItem Item) *protocol.AWSError {
	hashKey, sortKey, aerr := resolveStorageKeys(table, key)
	if aerr != nil {
		return aerr
	}
	mutations := diffIndexMutations(table, oldItem, nil)
	if err := s.items.removeWithIndexMutations(ctx, table.TableName, hashKey, sortKey, mutations); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// scanItems returns all items in the table via a single backend call.
func (s *dynamoStore) scanItems(ctx context.Context, tableName string) ([]Item, *protocol.AWSError) {
	items, err := s.items.scanAll(ctx, tableName)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return items, nil
}

// scanItemsPage returns up to limit items from the table via a single
// keyset-paginated backend call, ordered by (hashKey, sortKey) — the plain
// Scan fast path for storage-access-plan.md A3. exclusiveStartKey is the raw
// DynamoDB ExclusiveStartKey item (nil means "start of table"); it is
// resolved to the backend's (hashKey, sortKey) cursor via the table's key
// schema, exactly like putItem/getItem/deleteItem already do.
//
// hasMore reports whether items beyond the returned page exist. The backend
// is asked for one extra item (limit+1) to answer this without a second
// round trip — the same "peek one ahead" trick state.MemoryStore/SQLiteStore
// ScanPage callers use elsewhere in this codebase.
func (s *dynamoStore) scanItemsPage(ctx context.Context, table *Table, exclusiveStartKey Item, limit int) (items []Item, hasMore bool, aerr *protocol.AWSError) {
	hasAfter := false
	var afterHash, afterSort string
	if exclusiveStartKey != nil {
		// Must encode identically to putItem/getItem/deleteItem
		// (resolveStorageKeys) — the cursor is compared against the same
		// encoded storage keys those calls wrote.
		h, sk, aerr := resolveStorageKeys(table, exclusiveStartKey)
		if aerr != nil {
			return nil, false, aerr
		}
		hasAfter, afterHash, afterSort = true, h, sk
	}

	fetched, err := s.items.scanPage(ctx, table.TableName, hasAfter, afterHash, afterSort, limit+1)
	if err != nil {
		return nil, false, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if len(fetched) > limit {
		return fetched[:limit], true, nil
	}
	return fetched, false, nil
}

// errIndexCursorMissingKeys is the rejection for an ExclusiveStartKey that
// does not carry the index's own key attributes. AWS requires an index
// query's cursor to name both the table's primary key and the index key,
// because the index key alone is not unique — without it there is no
// position in the index to resume from.
func errIndexCursorMissingKeys(idx *SecondaryIndex) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationException",
		Message:    fmt.Sprintf("The provided starting key is missing required keys for index %q", idx.IndexName),
		HTTPStatus: http.StatusBadRequest,
	}
}

// scanIndexPage returns up to limit entries from a GSI's ordered index
// structure via a single keyset-paginated backend call
// (dynamodb-gsi-design.md §4) — the GSI-Scan analogue of scanItemsPage.
// exclusiveStartKey is the raw LastEvaluatedKey-shaped item (table PK plus
// index key attributes, per extractItemKeysWithIndex); it is resolved to the
// backend's 4-tuple (indexHash, indexSort, baseHash, baseSort) cursor via
// idx's key schema (indexKeyComponents) and table's own key schema
// (resolveStorageKeys), the same encode-then-compare choke point every other
// storage call site goes through.
func (s *dynamoStore) scanIndexPage(ctx context.Context, table *Table, idx *SecondaryIndex, exclusiveStartKey Item, limit int) (items []Item, hasMore bool, aerr *protocol.AWSError) {
	hasAfter := false
	var afterIndexHash, afterIndexSort, afterBaseHash, afterBaseSort string
	if exclusiveStartKey != nil {
		ih, is, ok := indexKeyComponents(table, idx, exclusiveStartKey)
		if !ok {
			return nil, false, errIndexCursorMissingKeys(idx)
		}
		bh, bs, aerr := resolveStorageKeys(table, exclusiveStartKey)
		if aerr != nil {
			return nil, false, aerr
		}
		hasAfter, afterIndexHash, afterIndexSort, afterBaseHash, afterBaseSort = true, ih, is, bh, bs
	}

	fetched, err := s.items.scanIndexPage(ctx, table.TableName, idx.IndexName, hasAfter, afterIndexHash, afterIndexSort, afterBaseHash, afterBaseSort, limit+1)
	if err != nil {
		return nil, false, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if len(fetched) > limit {
		return fetched[:limit], true, nil
	}
	return fetched, false, nil
}

// scanIndexByHash returns every entry stored for (table, idx) sharing the
// index's hash-key value hashVal — an O(k) partition read into the GSI's
// ordered index structure (dynamodb-gsi-design.md §4), the GSI-Query
// analogue of scanItemsByHashKey. hashVal is the raw (unencoded) hash-key
// scalar value; it is encoded per idx's hash-key type before use as the
// storage lookup key, mirroring scanItemsByHashKey's own encoding of the
// table's hash key.
func (s *dynamoStore) scanIndexByHash(ctx context.Context, table *Table, idx *SecondaryIndex, hashVal string) ([]Item, *protocol.AWSError) {
	storageHashVal := encodeStorageKeyComponent(keyAttrType(table, indexHashKeyName(idx)), hashVal)
	items, err := s.items.queryIndexByHash(ctx, table.TableName, idx.IndexName, storageHashVal)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return items, nil
}

// scanExpiredTTL returns only items whose TTL attribute is expired (> 0 and <= cutoffUnix).
func (s *dynamoStore) scanExpiredTTL(ctx context.Context, tableName, ttlAttr string, cutoffUnix int64) ([]Item, *protocol.AWSError) {
	items, err := s.items.scanExpiredTTL(ctx, tableName, ttlAttr, cutoffUnix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return items, nil
}

// countItems returns the live item count for a table without loading item values.
func (s *dynamoStore) countItems(ctx context.Context, tableName string) (int64, *protocol.AWSError) {
	n, err := s.items.count(ctx, tableName)
	if err != nil {
		return 0, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return n, nil
}

// scanItemsByHashKey returns all items in a partition (hash key equality) via
// a single backend call — O(k) where k is the partition size. hashVal is the
// raw (unencoded) hash-key scalar value; it is encoded per the table's hash
// key type before use as the storage lookup key, mirroring
// resolveStorageKeys, so a Number-typed hash key looks up the same encoded
// rows putItem wrote (docs/plans/dynamodb-gsi-design.md §2).
func (s *dynamoStore) scanItemsByHashKey(ctx context.Context, table *Table, hashVal string) ([]Item, *protocol.AWSError) {
	storageHashVal := encodeStorageKeyComponent(keyAttrType(table, table.hashKeyName()), hashVal)
	items, err := s.items.queryByHash(ctx, table.TableName, storageHashVal)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return items, nil
}

// ---- Stream helpers -------------------------------------------------------

// appendStreamRecord adds a stream change record for a table.
func (s *dynamoStore) appendStreamRecord(ctx context.Context, tableName string, r *StreamRecord) *protocol.AWSError {
	if err := s.streams.append(ctx, tableName, r); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// getStreamRecordsSince returns stream records with SequenceNumber > afterSeq.
func (s *dynamoStore) getStreamRecordsSince(ctx context.Context, tableName string, afterSeq int64, limit int) ([]*StreamRecord, *protocol.AWSError) {
	recs, err := s.streams.since(ctx, tableName, afterSeq, limit)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return recs, nil
}

// latestStreamSeq returns the highest sequence number stored for the table.
func (s *dynamoStore) latestStreamSeq(ctx context.Context, tableName string) (int64, *protocol.AWSError) {
	seq, err := s.streams.latest(ctx, tableName)
	if err != nil {
		return 0, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return seq, nil
}

// deleteTable removes a table descriptor and all its items.
func (s *dynamoStore) deleteTable(ctx context.Context, name string) *protocol.AWSError {
	if err := s.items.deleteAll(ctx, name); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.items.deleteAllIndexEntriesForTable(ctx, name); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	key := serviceutil.RegionKey(s.region(ctx), name)
	if err := s.tables.Delete(ctx, nsTables, key); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ---- cross-region scan helpers (for background goroutines) -----------------

// scanAllTables returns all tables across all regions. Each returned KV has a
// region-prefixed key (e.g. "us-east-1/myTable").
func (s *dynamoStore) scanAllTables(ctx context.Context) ([]state.KV, error) {
	return s.tables.Scan(ctx, nsTables, "")
}

// ---- Error sentinels -------------------------------------------------------

func errTableNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    fmt.Sprintf("Requested resource not found: Table: %s not found", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errTableExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceInUseException",
		Message:    fmt.Sprintf("Table already exists: %s", name),
		HTTPStatus: http.StatusBadRequest,
	}
}
