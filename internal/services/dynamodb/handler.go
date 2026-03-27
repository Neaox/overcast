package dynamodb

import (
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/serviceutil"
	"github.com/your-org/overcast/internal/state"
)

// Handler holds DynamoDB handler dependencies.
type Handler struct {
	cfg   *config.Config
	store *dynamoStore
	log   *serviceutil.ServiceLogger
	clk   clock.Clock
	ops   map[string]http.HandlerFunc
}

// newHandler constructs a Handler from the raw dependencies.
func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: newDynamoStore(store), log: log, clk: clk}
	h.initOps()
	return h
}

// initOps registers every known DynamoDB operation to its handler.
// Implemented operations point to their handler method; stubs live in handler_stubs.go.
// Adding a new operation: add an entry here, implement in handler.go, delete from handler_stubs.go.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		// Table management
		"CreateTable":   h.CreateTable,
		"DescribeTable": h.DescribeTable,
		"ListTables":    h.ListTables,
		// TODO(priority:P2): implement DeleteTable
		"DeleteTable": h.DeleteTable,
		// Item operations
		"PutItem":    h.PutItem,
		"GetItem":    h.GetItem,
		"DeleteItem": h.DeleteItem,
		// TODO(priority:P2): implement UpdateItem
		"UpdateItem": h.UpdateItem,
		// TODO(priority:P2): implement batch operations
		"BatchGetItem":   h.BatchGetItem,
		"BatchWriteItem": h.BatchWriteItem,
		// Query & scan
		"Scan":  h.Scan,
		"Query": h.Query,
		// TODO(priority:P3): implement transactional operations
		"TransactWriteItems": h.TransactWriteItems,
		"TransactGetItems":   h.TransactGetItems,
	}
}

// ---- Request / response types ----------------------------------------------

type createTableRequest struct {
	TableName            string             `json:"TableName"`
	KeySchema            []KeySchemaElement `json:"KeySchema"`
	AttributeDefinitions []AttributeDef     `json:"AttributeDefinitions"`
	BillingMode          string             `json:"BillingMode,omitempty"`
}

type createTableResponse struct {
	TableDescription *Table `json:"TableDescription"`
}

type describeTableRequest struct {
	TableName string `json:"TableName"`
}

type describeTableResponse struct {
	Table *Table `json:"Table"`
}

type putItemRequest struct {
	TableName string `json:"TableName"`
	Item      Item   `json:"Item"`
}

type getItemRequest struct {
	TableName string `json:"TableName"`
	Key       Item   `json:"Key"`
}

type getItemResponse struct {
	Item Item `json:"Item,omitempty"`
}

type deleteItemRequest struct {
	TableName string `json:"TableName"`
	Key       Item   `json:"Key"`
}

type scanRequest struct {
	TableName string `json:"TableName"`
}

type scanResponse struct {
	Items []Item `json:"Items"`
	Count int    `json:"Count"`
}

type queryRequest struct {
	TableName                 string               `json:"TableName"`
	KeyConditionExpression    string               `json:"KeyConditionExpression"`
	ExpressionAttributeValues map[string]attrValue `json:"ExpressionAttributeValues"`
	ExpressionAttributeNames  map[string]string    `json:"ExpressionAttributeNames,omitempty"`
}

type queryResponse struct {
	Items []Item `json:"Items"`
	Count int    `json:"Count"`
}

type listTablesRequest struct {
	ExclusiveStartTableName string `json:"ExclusiveStartTableName,omitempty"`
	Limit                   int    `json:"Limit,omitempty"`
}

type listTablesResponse struct {
	TableNames []string `json:"TableNames"`
}

// ---- Handlers --------------------------------------------------------------

// CreateTable handles the DynamoDB CreateTable operation.
func (h *Handler) CreateTable(w http.ResponseWriter, r *http.Request) {
	var req createTableRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.TableName, "TableName") {
		return
	}

	ctx := r.Context()

	exists, aerr := h.store.tableExists(ctx, req.TableName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if exists {
		protocol.WriteJSONError(w, r, errTableExists(req.TableName))
		return
	}

	table := &Table{
		TableName:            req.TableName,
		KeySchema:            req.KeySchema,
		AttributeDefinitions: req.AttributeDefinitions,
		TableStatus:          "ACTIVE",
		BillingMode:          req.BillingMode,
		TableARN:             "arn:aws:dynamodb:" + h.cfg.Region + ":" + h.cfg.AccountID + ":table/" + req.TableName,
		CreationDateTime:     float64(time.Now().UnixMilli()) / 1000.0,
		ItemCount:            0,
	}

	if aerr := h.store.putTable(ctx, table); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.log.Info("table created", zap.String("table", req.TableName))
	protocol.WriteJSON(w, r, http.StatusOK, &createTableResponse{TableDescription: table})
}

// ListTables handles the DynamoDB ListTables operation.
func (h *Handler) ListTables(w http.ResponseWriter, r *http.Request) {
	var req listTablesRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	tables, aerr := h.store.listTables(r.Context(), "")
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = t.TableName
	}

	protocol.WriteJSON(w, r, http.StatusOK, &listTablesResponse{TableNames: names})
}

// DescribeTable handles the DynamoDB DescribeTable operation.
func (h *Handler) DescribeTable(w http.ResponseWriter, r *http.Request) {
	var req describeTableRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.TableName, "TableName") {
		return
	}

	table, aerr := h.store.getTable(r.Context(), req.TableName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, &describeTableResponse{Table: table})
}

// PutItem handles the DynamoDB PutItem operation.
func (h *Handler) PutItem(w http.ResponseWriter, r *http.Request) {
	var req putItemRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.TableName, "TableName") {
		return
	}

	ctx := r.Context()

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.putItem(ctx, table, req.Item); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// GetItem handles the DynamoDB GetItem operation.
func (h *Handler) GetItem(w http.ResponseWriter, r *http.Request) {
	var req getItemRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.TableName, "TableName") {
		return
	}

	ctx := r.Context()

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	item, aerr := h.store.getItem(ctx, table, req.Key)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// AWS returns 200 with empty Item when not found.
	resp := getItemResponse{}
	if item != nil {
		resp.Item = item
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

// DeleteItem handles the DynamoDB DeleteItem operation.
func (h *Handler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	var req deleteItemRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.TableName, "TableName") {
		return
	}

	ctx := r.Context()

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteItem(ctx, table, req.Key); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// Scan handles the DynamoDB Scan operation.
func (h *Handler) Scan(w http.ResponseWriter, r *http.Request) {
	var req scanRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.TableName, "TableName") {
		return
	}

	ctx := r.Context()

	_, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	items, aerr := h.store.scanItems(ctx, req.TableName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if items == nil {
		items = []Item{}
	}

	protocol.WriteJSON(w, r, http.StatusOK, &scanResponse{Items: items, Count: len(items)})
}

// Query handles the DynamoDB Query operation.
// P1 supports simple hash-key equality: "attrName = :placeholder".
func (h *Handler) Query(w http.ResponseWriter, r *http.Request) {
	var req queryRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.TableName, "TableName") {
		return
	}
	if !serviceutil.RequireString(w, r, req.KeyConditionExpression, "KeyConditionExpression") {
		return
	}

	ctx := r.Context()

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	attrName, placeholder, ok := parseSimpleEquality(req.KeyConditionExpression)
	if !ok {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "KeyConditionExpression must be in the form: attrName = :placeholder",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Resolve attribute name alias (#name → actual name).
	if alias, hasAlias := req.ExpressionAttributeNames[attrName]; hasAlias {
		attrName = alias
	}

	// Look up the placeholder value.
	expectedAttr, found := req.ExpressionAttributeValues[placeholder]
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "ExpressionAttributeValues does not contain key: " + placeholder,
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	expectedVal := extractKeyValue(expectedAttr)

	// Scan all items and filter by the hash key condition.
	allItems, aerr := h.store.scanItems(ctx, table.TableName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	matched := make([]Item, 0)
	for _, item := range allItems {
		attr, ok := item[attrName]
		if !ok {
			continue
		}
		if extractKeyValue(attr) == expectedVal {
			matched = append(matched, item)
		}
	}

	protocol.WriteJSON(w, r, http.StatusOK, &queryResponse{Items: matched, Count: len(matched)})
}
