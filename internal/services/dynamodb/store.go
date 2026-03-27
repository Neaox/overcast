package dynamodb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/state"
)

const (
	nsTables = "dynamodb:tables"
	nsItems  = "dynamodb:items"
)

// Table represents a DynamoDB table definition.
type Table struct {
	TableName            string             `json:"TableName"`
	KeySchema            []KeySchemaElement `json:"KeySchema"`
	AttributeDefinitions []AttributeDef     `json:"AttributeDefinitions"`
	TableStatus          string             `json:"TableStatus"`
	BillingMode          string             `json:"BillingMode,omitempty"`
	TableARN             string             `json:"TableArn"`
	CreationDateTime     float64            `json:"CreationDateTime"`
	ItemCount            int64              `json:"ItemCount"`
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

// DynamoDB attribute value: {"S": "foo"} or {"N": "123"} etc.
type attrValue = map[string]any

// Item is a DynamoDB item represented in DynamoDB JSON format.
type Item = map[string]attrValue

// dynamoStore wraps state.Store with DynamoDB-specific helpers.
type dynamoStore struct {
	store state.Store
}

func newDynamoStore(store state.Store) *dynamoStore {
	return &dynamoStore{store: store}
}

// ---- Table helpers ---------------------------------------------------------

func (s *dynamoStore) getTable(ctx context.Context, name string) (*Table, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsTables, name)
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
	if err := s.store.Set(ctx, nsTables, t.TableName, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *dynamoStore) tableExists(ctx context.Context, name string) (bool, *protocol.AWSError) {
	_, found, err := s.store.Get(ctx, nsTables, name)
	if err != nil {
		return false, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return found, nil
}

func (s *dynamoStore) listTables(ctx context.Context, prefix string) ([]*Table, *protocol.AWSError) {
	keys, err := s.store.List(ctx, nsTables, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	tables := make([]*Table, 0, len(keys))
	for _, k := range keys {
		t, aerr := s.getTable(ctx, k)
		if aerr != nil {
			return nil, aerr
		}
		tables = append(tables, t)
	}
	return tables, nil
}

// ---- Item helpers ----------------------------------------------------------

// itemKey builds a store key for an item: "<table>/<hashVal>" or "<table>/<hashVal>/<sortVal>".
func itemKey(tableName, hashVal, sortVal string) string {
	if sortVal != "" {
		return fmt.Sprintf("%s/%s/%s", tableName, hashVal, sortVal)
	}
	return fmt.Sprintf("%s/%s", tableName, hashVal)
}

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

// resolveItemKey resolves the store key for an item given the table definition and the item/key map.
func resolveItemKey(table *Table, keyOrItem Item) (string, *protocol.AWSError) {
	hashName := table.hashKeyName()
	if hashName == "" {
		return "", &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "Table has no hash key defined.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	hashAttr, ok := keyOrItem[hashName]
	if !ok {
		return "", &protocol.AWSError{
			Code:       "ValidationException",
			Message:    fmt.Sprintf("The provided key element does not match the schema: missing hash key %q", hashName),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	hashVal := extractKeyValue(hashAttr)

	sortName := table.sortKeyName()
	sortVal := ""
	if sortName != "" {
		if sortAttr, ok := keyOrItem[sortName]; ok {
			sortVal = extractKeyValue(sortAttr)
		}
	}

	return itemKey(table.TableName, hashVal, sortVal), nil
}

func (s *dynamoStore) putItem(ctx context.Context, table *Table, item Item) *protocol.AWSError {
	key, aerr := resolveItemKey(table, item)
	if aerr != nil {
		return aerr
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsItems, key, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *dynamoStore) getItem(ctx context.Context, table *Table, key Item) (Item, *protocol.AWSError) {
	storeKey, aerr := resolveItemKey(table, key)
	if aerr != nil {
		return nil, aerr
	}
	raw, found, err := s.store.Get(ctx, nsItems, storeKey)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, nil // 200 with empty Item
	}
	var item Item
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return item, nil
}

func (s *dynamoStore) deleteItem(ctx context.Context, table *Table, key Item) *protocol.AWSError {
	storeKey, aerr := resolveItemKey(table, key)
	if aerr != nil {
		return aerr
	}
	if err := s.store.Delete(ctx, nsItems, storeKey); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// scanItems returns all items stored under the given table name.
func (s *dynamoStore) scanItems(ctx context.Context, tableName string) ([]Item, *protocol.AWSError) {
	prefix := tableName + "/"
	keys, err := s.store.List(ctx, nsItems, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	items := make([]Item, 0, len(keys))
	for _, k := range keys {
		raw, found, err := s.store.Get(ctx, nsItems, k)
		if err != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, err)
		}
		if !found {
			continue
		}
		var item Item
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, err)
		}
		items = append(items, item)
	}
	return items, nil
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

// ---- KeyConditionExpression parser -----------------------------------------

// parseSimpleEquality parses a KeyConditionExpression of the form:
//
//	<attrName> = <:placeholder>
//
// Returns (attrName, placeholder, true) on success, ("", "", false) if the
// expression doesn't match this simple form.
func parseSimpleEquality(expr string) (attrName, placeholder string, ok bool) {
	expr = strings.TrimSpace(expr)
	// Expect exactly one "=" that is not part of "<=" or ">=" or "<>"
	parts := strings.SplitN(expr, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	lhs := strings.TrimSpace(parts[0])
	rhs := strings.TrimSpace(parts[1])
	if lhs == "" || rhs == "" {
		return "", "", false
	}
	return lhs, rhs, true
}
