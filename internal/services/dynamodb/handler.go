package dynamodb

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Handler holds DynamoDB handler dependencies.
type Handler struct {
	cfg   *config.Config
	store *dynamoStore
	bus   *events.Bus
	log   *serviceutil.ServiceLogger
	clk   clock.Clock
	ops   map[string]http.HandlerFunc
	rawOp map[string]op.Operation
}

// newHandler constructs a Handler from the raw dependencies.
func newHandler(cfg *config.Config, tables state.Store, items itemBackend, streams streamBackend, bus *events.Bus, log *serviceutil.ServiceLogger, clk clock.Clock, defaultRegion string) *Handler {
	h := &Handler{cfg: cfg, store: newDynamoStore(tables, items, streams, defaultRegion), bus: bus, log: log, clk: clk}
	h.initOps()
	h.rawOp = h.typedOps()
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
		"DeleteTable":   h.DeleteTable,
		// TODO(priority:P2): implement full UpdateTable (GSI/LSI, provisioned throughput)
		"UpdateTable": h.UpdateTable,
		// Item operations
		"PutItem":    h.PutItem,
		"GetItem":    h.GetItem,
		"DeleteItem": h.DeleteItem,
		// UpdateItem — handler_update.go
		"UpdateItem":     h.UpdateItem,
		"BatchGetItem":   h.BatchGetItem,
		"BatchWriteItem": h.BatchWriteItem,
		// Query & scan
		"Scan":  h.Scan,
		"Query": h.Query,
		// TTL
		"UpdateTimeToLive":   h.UpdateTimeToLive,
		"DescribeTimeToLive": h.DescribeTimeToLive,
		// Transactions
		"TransactWriteItems": h.TransactWriteItems,
		"TransactGetItems":   h.TransactGetItems,
	}
}

// ---- Request / response types ----------------------------------------------

type createTableRequest struct {
	TableName              string                 `json:"TableName"`
	KeySchema              []KeySchemaElement     `json:"KeySchema"`
	AttributeDefinitions   []AttributeDef         `json:"AttributeDefinitions"`
	BillingMode            string                 `json:"BillingMode,omitempty"`
	ProvisionedThroughput  *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
	StreamSpecification    *StreamSpecification   `json:"StreamSpecification,omitempty"`
	GlobalSecondaryIndexes []SecondaryIndex       `json:"GlobalSecondaryIndexes,omitempty"`
	LocalSecondaryIndexes  []SecondaryIndex       `json:"LocalSecondaryIndexes,omitempty"`
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

type deleteTableRequest struct {
	TableName string `json:"TableName"`
}

type putItemRequest struct {
	TableName                 string               `json:"TableName"`
	Item                      Item                 `json:"Item"`
	ConditionExpression       string               `json:"ConditionExpression,omitempty"`
	ExpressionAttributeNames  map[string]string    `json:"ExpressionAttributeNames,omitempty"`
	ExpressionAttributeValues map[string]attrValue `json:"ExpressionAttributeValues,omitempty"`
	ReturnValues              string               `json:"ReturnValues,omitempty"`
}

type putItemResponse struct {
	Attributes Item `json:"Attributes,omitempty"`
}

type getItemRequest struct {
	TableName                string            `json:"TableName"`
	Key                      Item              `json:"Key"`
	ProjectionExpression     string            `json:"ProjectionExpression,omitempty"`
	ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames,omitempty"`
}

type getItemResponse struct {
	Item Item `json:"Item,omitempty"`
}

type deleteItemRequest struct {
	TableName                 string               `json:"TableName"`
	Key                       Item                 `json:"Key"`
	ReturnValues              string               `json:"ReturnValues,omitempty"`
	ConditionExpression       string               `json:"ConditionExpression,omitempty"`
	ExpressionAttributeNames  map[string]string    `json:"ExpressionAttributeNames,omitempty"`
	ExpressionAttributeValues map[string]attrValue `json:"ExpressionAttributeValues,omitempty"`
}

type deleteItemResponse struct {
	Attributes Item `json:"Attributes,omitempty"`
}

type scanRequest struct {
	TableName                 string               `json:"TableName"`
	IndexName                 string               `json:"IndexName,omitempty"`
	FilterExpression          string               `json:"FilterExpression,omitempty"`
	ProjectionExpression      string               `json:"ProjectionExpression,omitempty"`
	ExpressionAttributeValues map[string]attrValue `json:"ExpressionAttributeValues,omitempty"`
	ExpressionAttributeNames  map[string]string    `json:"ExpressionAttributeNames,omitempty"`
	Limit                     int                  `json:"Limit,omitempty"`
	ExclusiveStartKey         Item                 `json:"ExclusiveStartKey,omitempty"`
	Segment                   int                  `json:"Segment,omitempty"`
	TotalSegments             int                  `json:"TotalSegments,omitempty"`
	Select                    string               `json:"Select,omitempty"`
}

type scanResponse struct {
	Items            []Item `json:"Items"`
	Count            int    `json:"Count"`
	ScannedCount     int    `json:"ScannedCount"`
	LastEvaluatedKey Item   `json:"LastEvaluatedKey,omitempty"`
}

// countOnlyResponse is used when Select="COUNT": Items must be absent from the response.
type countOnlyResponse struct {
	Count            int  `json:"Count"`
	ScannedCount     int  `json:"ScannedCount"`
	LastEvaluatedKey Item `json:"LastEvaluatedKey,omitempty"`
}

type queryRequest struct {
	TableName                 string               `json:"TableName"`
	IndexName                 string               `json:"IndexName,omitempty"`
	KeyConditionExpression    string               `json:"KeyConditionExpression"`
	FilterExpression          string               `json:"FilterExpression,omitempty"`
	ProjectionExpression      string               `json:"ProjectionExpression,omitempty"`
	ExpressionAttributeValues map[string]attrValue `json:"ExpressionAttributeValues"`
	ExpressionAttributeNames  map[string]string    `json:"ExpressionAttributeNames,omitempty"`
	Limit                     int                  `json:"Limit,omitempty"`
	ExclusiveStartKey         Item                 `json:"ExclusiveStartKey,omitempty"`
	ScanIndexForward          *bool                `json:"ScanIndexForward,omitempty"`
	Select                    string               `json:"Select,omitempty"`
}

type queryResponse struct {
	Items            []Item `json:"Items"`
	Count            int    `json:"Count"`
	ScannedCount     int    `json:"ScannedCount"`
	LastEvaluatedKey Item   `json:"LastEvaluatedKey,omitempty"`
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
	resp, aerr := h.createTableTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) createTableTyped(ctx context.Context, req *createTableRequest) (*createTableResponse, *protocol.AWSError) {
	if req.TableName == "" {
		return nil, protocol.ErrMissingParameter("TableName")
	}

	exists, aerr := h.store.tableExists(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}
	if exists {
		return nil, errTableExists(req.TableName)
	}

	region := middleware.RegionFromContext(ctx, h.cfg.Region)
	table := &Table{
		TableName:            req.TableName,
		KeySchema:            req.KeySchema,
		AttributeDefinitions: req.AttributeDefinitions,
		TableStatus:          "ACTIVE",
		BillingMode:          req.BillingMode,
		TableARN:             "arn:aws:dynamodb:" + region + ":" + h.cfg.AccountID + ":table/" + req.TableName,
		CreationDateTime:     float64(h.clk.Now().UnixMilli()) / 1000.0,
		ItemCount:            0,
	}
	if req.BillingMode != "" {
		table.BillingModeSummary = &BillingModeSummary{BillingMode: req.BillingMode}
	}
	if req.ProvisionedThroughput != nil {
		table.ProvisionedThroughput = req.ProvisionedThroughput
	}

	// Populate GSI definitions with ARN and status.
	for i := range req.GlobalSecondaryIndexes {
		gsi := &req.GlobalSecondaryIndexes[i]
		gsi.IndexArn = table.TableARN + "/index/" + gsi.IndexName
		gsi.IndexStatus = "ACTIVE"
	}
	table.GlobalSecondaryIndexes = req.GlobalSecondaryIndexes

	// Populate LSI definitions with ARN.
	for i := range req.LocalSecondaryIndexes {
		lsi := &req.LocalSecondaryIndexes[i]
		lsi.IndexArn = table.TableARN + "/index/" + lsi.IndexName
	}
	table.LocalSecondaryIndexes = req.LocalSecondaryIndexes

	if req.StreamSpecification != nil && (req.StreamSpecification.StreamEnabled || req.StreamSpecification.StreamViewType != "") {
		req.StreamSpecification.StreamEnabled = true
		h.applyStreamSpec(table, req.StreamSpecification, region)
	}

	if aerr := h.store.putTable(ctx, table); aerr != nil {
		return nil, aerr
	}

	h.log.Info("table created", zap.String("table", req.TableName))
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.DynamoDBTableCreated,
			Time:    h.clk.Now(),
			Source:  "dynamodb",
			Payload: events.ResourcePayload{Name: req.TableName},
		})
	}
	return &createTableResponse{TableDescription: table}, nil
}

// ListTables handles the DynamoDB ListTables operation.
func (h *Handler) ListTables(w http.ResponseWriter, r *http.Request) {
	var req listTablesRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.listTablesTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) listTablesTyped(ctx context.Context, _ *listTablesRequest) (*listTablesResponse, *protocol.AWSError) {
	tables, aerr := h.store.listTables(ctx, "")
	if aerr != nil {
		return nil, aerr
	}

	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = t.TableName
	}

	return &listTablesResponse{TableNames: names}, nil
}

// DescribeTable handles the DynamoDB DescribeTable operation.
func (h *Handler) DescribeTable(w http.ResponseWriter, r *http.Request) {
	var req describeTableRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.describeTableTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) describeTableTyped(ctx context.Context, req *describeTableRequest) (*describeTableResponse, *protocol.AWSError) {
	if req.TableName == "" {
		return nil, protocol.ErrMissingParameter("TableName")
	}

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}
	// Populate live item count — the stored descriptor always has 0.
	if n, aerr := h.store.countItems(ctx, req.TableName); aerr == nil {
		table.ItemCount = n
	}

	return &describeTableResponse{Table: table}, nil
}

// PutItem handles the DynamoDB PutItem operation.
func (h *Handler) PutItem(w http.ResponseWriter, r *http.Request) {
	var req putItemRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.putItemTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) putItemTyped(ctx context.Context, req *putItemRequest) (*putItemResponse, *protocol.AWSError) {
	if req.TableName == "" {
		return nil, protocol.ErrMissingParameter("TableName")
	}

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}

	// Evaluate ConditionExpression against the existing item, if any.
	if req.ConditionExpression != "" {
		existing, aerr := h.store.getItem(ctx, table, req.Item)
		if aerr != nil {
			return nil, aerr
		}
		filter, err := compileFilter(req.ConditionExpression, req.ExpressionAttributeNames, req.ExpressionAttributeValues)
		if err != nil {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    err.Error(),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		checkItem := existing
		if checkItem == nil {
			checkItem = Item{}
		}
		ok, err := evalFilter(filter, checkItem)
		if err != nil {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    err.Error(),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if !ok {
			return nil, &protocol.AWSError{
				Code:       "ConditionalCheckFailedException",
				Message:    "The conditional request failed",
				HTTPStatus: http.StatusBadRequest,
			}
		}
	}

	// For stream OLD_IMAGE capture or ReturnValues=ALL_OLD, read the existing item.
	var oldItem Item
	if table.streamEnabled() || req.ReturnValues == "ALL_OLD" {
		oldItem, _ = h.store.getItem(ctx, table, req.Item)
	}

	if aerr := h.store.putItem(ctx, table, req.Item); aerr != nil {
		return nil, aerr
	}

	if table.streamEnabled() {
		h.publishPutStreamRecord(ctx, table, req.Item, oldItem)
	}

	h.bus.Publish(ctx, events.Event{
		Type:    events.DynamoDBItemMutated,
		Source:  "dynamodb",
		Payload: events.ResourcePayload{Name: req.TableName},
	})

	if req.ReturnValues == "ALL_OLD" && oldItem != nil {
		return &putItemResponse{Attributes: oldItem}, nil
	}
	return &putItemResponse{}, nil
}

// GetItem handles the DynamoDB GetItem operation.
func (h *Handler) GetItem(w http.ResponseWriter, r *http.Request) {
	var req getItemRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.getItemTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) getItemTyped(ctx context.Context, req *getItemRequest) (*getItemResponse, *protocol.AWSError) {
	if req.TableName == "" {
		return nil, protocol.ErrMissingParameter("TableName")
	}

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}
	item, aerr := h.store.getItem(ctx, table, req.Key)
	if aerr != nil {
		return nil, aerr
	}

	// AWS returns 200 with empty Item when not found.
	resp := getItemResponse{}
	if item != nil {
		// Apply ProjectionExpression if provided.
		if req.ProjectionExpression != "" {
			proj, err := compileProjection(req.ProjectionExpression, req.ExpressionAttributeNames)
			if err != nil {
				return nil, &protocol.AWSError{
					Code:       "ValidationException",
					Message:    err.Error(),
					HTTPStatus: http.StatusBadRequest,
				}
			}
			item = applyProjection(item, proj, table)
		}
		resp.Item = item
	}
	return &resp, nil
}

// DeleteItem handles the DynamoDB DeleteItem operation.
func (h *Handler) DeleteItem(w http.ResponseWriter, r *http.Request) {
	var req deleteItemRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.deleteItemTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) deleteItemTyped(ctx context.Context, req *deleteItemRequest) (*deleteItemResponse, *protocol.AWSError) {
	if req.TableName == "" {
		return nil, protocol.ErrMissingParameter("TableName")
	}

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}

	// Capture old item (needed for ConditionExpression, ReturnValues, and streams).
	var oldItem Item
	if table.streamEnabled() || req.ConditionExpression != "" || req.ReturnValues == "ALL_OLD" {
		oldItem, _ = h.store.getItem(ctx, table, req.Key)
	}

	// Evaluate ConditionExpression if provided.
	if req.ConditionExpression != "" {
		filter, err := compileFilter(req.ConditionExpression, req.ExpressionAttributeNames, req.ExpressionAttributeValues)
		if err != nil {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    err.Error(),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		checkItem := oldItem
		if checkItem == nil {
			checkItem = Item{}
		}
		ok, err := evalFilter(filter, checkItem)
		if err != nil {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    err.Error(),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		if !ok {
			return nil, &protocol.AWSError{
				Code:       "ConditionalCheckFailedException",
				Message:    "The conditional request failed",
				HTTPStatus: http.StatusBadRequest,
			}
		}
	}

	if aerr := h.store.deleteItem(ctx, table, req.Key); aerr != nil {
		return nil, aerr
	}

	if table.streamEnabled() && oldItem != nil {
		h.publishDeleteStreamRecord(ctx, table, req.Key, oldItem)
	}

	h.bus.Publish(ctx, events.Event{
		Type:    events.DynamoDBItemMutated,
		Source:  "dynamodb",
		Payload: events.ResourcePayload{Name: req.TableName},
	})

	if req.ReturnValues == "ALL_OLD" && oldItem != nil {
		return &deleteItemResponse{Attributes: oldItem}, nil
	}
	return &deleteItemResponse{}, nil
}

// Scan handles the DynamoDB Scan operation.
func (h *Handler) Scan(w http.ResponseWriter, r *http.Request) {
	var req scanRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.scanTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) scanTyped(ctx context.Context, req *scanRequest) (any, *protocol.AWSError) {
	if req.TableName == "" {
		return nil, protocol.ErrMissingParameter("TableName")
	}

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}
	items, aerr := h.store.scanItems(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}

	// When scanning a GSI, exclude items that lack the index's hash key attribute.
	var scanIdx *SecondaryIndex
	if req.IndexName != "" {
		scanIdx = table.findIndex(req.IndexName)
		if scanIdx == nil {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    "The table does not have the specified index: " + req.IndexName,
				HTTPStatus: http.StatusBadRequest,
			}
		}
		hashKey := indexHashKeyName(scanIdx)
		filtered := make([]Item, 0, len(items))
		for _, item := range items {
			if _, ok := item[hashKey]; ok {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	if items == nil {
		items = []Item{}
	}

	// Sort items by primary hash key for deterministic ordering (pagination + parallel scan).
	hashKeyName := table.hashKeyName()
	sort.Slice(items, func(i, j int) bool {
		return extractKeyValue(items[i][hashKeyName]) < extractKeyValue(items[j][hashKeyName])
	})

	// Parallel scan: slice items by segment.
	if req.TotalSegments > 1 {
		seg := req.Segment
		if seg < 0 {
			seg = 0
		}
		n := len(items)
		segSize := (n + req.TotalSegments - 1) / req.TotalSegments
		start := seg * segSize
		if start >= n {
			items = []Item{}
		} else {
			end := start + segSize
			if end > n {
				end = n
			}
			items = items[start:end]
		}
	}

	// Apply ExclusiveStartKey: skip items up to and including the start key.
	if req.ExclusiveStartKey != nil {
		startIdx := -1
		for i, item := range items {
			if itemKeysEqual(item, req.ExclusiveStartKey, table) {
				startIdx = i
				break
			}
		}
		if startIdx >= 0 {
			items = items[startIdx+1:]
		}
	}

	// Apply Limit (must be before FilterExpression per DynamoDB semantics: Limit caps the
	// number of items READ, not the number returned after filtering).
	var lastKey Item
	if req.Limit > 0 && len(items) > req.Limit {
		items = items[:req.Limit]
		lastKey = extractItemKeysWithIndex(items[len(items)-1], table, scanIdx)
	}

	scannedCount := len(items)

	// Apply FilterExpression if provided.
	if req.FilterExpression != "" {
		filter, err := compileFilter(req.FilterExpression, req.ExpressionAttributeNames, req.ExpressionAttributeValues)
		if err != nil {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    err.Error(),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		filtered := items[:0]
		for _, item := range items {
			pass, err := evalFilter(filter, item)
			if err != nil {
				return nil, &protocol.AWSError{
					Code:       "ValidationException",
					Message:    err.Error(),
					HTTPStatus: http.StatusBadRequest,
				}
			}
			if pass {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	// Select=COUNT: return only counts, no items.
	if req.Select == "COUNT" {
		return &countOnlyResponse{Count: len(items), ScannedCount: scannedCount, LastEvaluatedKey: lastKey}, nil
	}

	// Apply ProjectionExpression if provided.
	if req.ProjectionExpression != "" {
		proj, err := compileProjection(req.ProjectionExpression, req.ExpressionAttributeNames)
		if err != nil {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    err.Error(),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		for i, item := range items {
			items[i] = applyProjection(item, proj, table)
		}
	}

	return &scanResponse{Items: items, Count: len(items), ScannedCount: scannedCount, LastEvaluatedKey: lastKey}, nil
}

// DeleteTable handles the DynamoDB DeleteTable operation.
func (h *Handler) DeleteTable(w http.ResponseWriter, r *http.Request) {
	var req deleteTableRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.deleteTableTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) deleteTableTyped(ctx context.Context, req *deleteTableRequest) (*describeTableResponse, *protocol.AWSError) {
	if req.TableName == "" {
		return nil, protocol.ErrMissingParameter("TableName")
	}

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}

	if aerr := h.store.deleteTable(ctx, req.TableName); aerr != nil {
		return nil, aerr
	}

	h.log.Info("table deleted", zap.String("table", req.TableName))
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.DynamoDBTableDeleted,
			Time:    h.clk.Now(),
			Source:  "dynamodb",
			Payload: events.ResourcePayload{Name: req.TableName},
		})
	}
	return &describeTableResponse{Table: table}, nil
}

// Query handles the DynamoDB Query operation.
// Supports hash-key equality, sort-key conditions (=, <, <=, >, >=, BETWEEN,
// begins_with), FilterExpression, ProjectionExpression, and GSI/LSI via IndexName.
func (h *Handler) Query(w http.ResponseWriter, r *http.Request) {
	var req queryRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.queryTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) queryTyped(ctx context.Context, req *queryRequest) (any, *protocol.AWSError) {
	if req.TableName == "" {
		return nil, protocol.ErrMissingParameter("TableName")
	}
	if req.KeyConditionExpression == "" {
		return nil, protocol.ErrMissingParameter("KeyConditionExpression")
	}

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}

	// Resolve key schema: either from the index or the table.
	var idxHashKeyName, idxSortKeyName string
	var activeIdx *SecondaryIndex
	if req.IndexName != "" {
		activeIdx = table.findIndex(req.IndexName)
		if activeIdx == nil {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    "The table does not have the specified index: " + req.IndexName,
				HTTPStatus: http.StatusBadRequest,
			}
		}
		idxHashKeyName = indexHashKeyName(activeIdx)
		idxSortKeyName = indexSortKeyName(activeIdx)
	}

	// Parse the KeyConditionExpression using the full expression parser.
	kc, err := compileKeyCondition(req.KeyConditionExpression, req.ExpressionAttributeNames, req.ExpressionAttributeValues)
	if err != nil {
		return nil, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    err.Error(),
			HTTPStatus: http.StatusBadRequest,
		}
	}

	hashVal := extractKeyValue(kc.hashVal)

	// Determine which attribute names to use for matching.
	hashAttrName := table.hashKeyName()
	sortAttrName := table.sortKeyName()
	if req.IndexName != "" {
		hashAttrName = idxHashKeyName
		sortAttrName = idxSortKeyName
	}

	// Collect matching items.
	var matched []Item
	if req.IndexName != "" {
		// Index query: scan all items and filter by index hash key.
		allItems, aerr := h.store.scanItems(ctx, req.TableName)
		if aerr != nil {
			return nil, aerr
		}
		for _, item := range allItems {
			av, ok := item[hashAttrName]
			if !ok || extractKeyValue(av) != hashVal {
				continue
			}
			// Apply sort key condition if present.
			if kc.sortCond != nil {
				sc := *kc.sortCond
				sc.attr = sortAttrName
				if !sc.matchItem(item) {
					continue
				}
			}
			matched = append(matched, item)
		}
	} else if sortAttrName == "" {
		// Hash-only table: point lookup.
		keyMap := Item{hashAttrName: kc.hashVal}
		item, aerr := h.store.getItem(ctx, table, keyMap)
		if aerr != nil {
			return nil, aerr
		}
		if item != nil {
			matched = []Item{item}
		} else {
			matched = []Item{}
		}
	} else {
		// Hash+sort table: load all items for the hash key, then filter by sort condition.
		candidates, aerr := h.store.scanItemsByHashKey(ctx, table.TableName, hashVal)
		if aerr != nil {
			return nil, aerr
		}
		if kc.sortCond != nil {
			sc := *kc.sortCond
			sc.attr = sortAttrName
			for _, item := range candidates {
				if sc.matchItem(item) {
					matched = append(matched, item)
				}
			}
		} else {
			matched = candidates
		}
	}

	if matched == nil {
		matched = []Item{}
	}

	// Sort matched items by sort key for stable pagination order.
	effectiveSortKey := sortAttrName
	if effectiveSortKey != "" {
		sort.Slice(matched, func(i, j int) bool {
			iv := extractKeyValue(matched[i][effectiveSortKey])
			jv := extractKeyValue(matched[j][effectiveSortKey])
			return iv < jv
		})
	}

	// Apply ScanIndexForward (default true, false = reverse order).
	if req.ScanIndexForward != nil && !*req.ScanIndexForward && effectiveSortKey != "" {
		for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
			matched[i], matched[j] = matched[j], matched[i]
		}
	}

	// Apply ExclusiveStartKey: skip items up to and including the start key.
	if req.ExclusiveStartKey != nil {
		startIdx := -1
		for i, item := range matched {
			if itemKeysEqual(item, req.ExclusiveStartKey, table) {
				startIdx = i
				break
			}
		}
		if startIdx >= 0 {
			matched = matched[startIdx+1:]
		}
	}

	// Apply Limit (must be before FilterExpression per DynamoDB semantics: Limit caps the
	// number of items READ, not the number returned after filtering).
	var lastKey Item
	if req.Limit > 0 && len(matched) > req.Limit {
		matched = matched[:req.Limit]
		lastKey = extractItemKeysWithIndex(matched[len(matched)-1], table, activeIdx)
	}

	scannedCount := len(matched)

	// Apply FilterExpression (post-key-condition, per DynamoDB semantics).
	if req.FilterExpression != "" {
		filter, err := compileFilter(req.FilterExpression, req.ExpressionAttributeNames, req.ExpressionAttributeValues)
		if err != nil {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    err.Error(),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		out := matched[:0]
		for _, item := range matched {
			pass, err := evalFilter(filter, item)
			if err != nil {
				return nil, &protocol.AWSError{
					Code:       "ValidationException",
					Message:    err.Error(),
					HTTPStatus: http.StatusBadRequest,
				}
			}
			if pass {
				out = append(out, item)
			}
		}
		matched = out
	}

	// Select=COUNT: return only counts, no items.
	if req.Select == "COUNT" {
		return &countOnlyResponse{Count: len(matched), ScannedCount: scannedCount, LastEvaluatedKey: lastKey}, nil
	}

	// Apply ProjectionExpression if provided.
	if req.ProjectionExpression != "" {
		proj, err := compileProjection(req.ProjectionExpression, req.ExpressionAttributeNames)
		if err != nil {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    err.Error(),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		for i, item := range matched {
			matched[i] = applyProjection(item, proj, table)
		}
	}

	return &queryResponse{Items: matched, Count: len(matched), ScannedCount: scannedCount, LastEvaluatedKey: lastKey}, nil
}

// GSIUpdate describes a single GlobalSecondaryIndex update operation.
type GSIUpdate struct {
	Create *SecondaryIndex `json:"Create,omitempty"`
	Delete *struct {
		IndexName string `json:"IndexName"`
	} `json:"Delete,omitempty"`
	Update *struct {
		IndexName             string                 `json:"IndexName"`
		ProvisionedThroughput *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
	} `json:"Update,omitempty"`
}

type updateTableRequest struct {
	TableName                   string                 `json:"TableName"`
	BillingMode                 string                 `json:"BillingMode,omitempty"`
	AttributeDefinitions        []AttributeDef         `json:"AttributeDefinitions,omitempty"`
	StreamSpecification         *StreamSpecification   `json:"StreamSpecification,omitempty"`
	ProvisionedThroughput       *ProvisionedThroughput `json:"ProvisionedThroughput,omitempty"`
	GlobalSecondaryIndexUpdates []GSIUpdate            `json:"GlobalSecondaryIndexUpdates,omitempty"`
}

// UpdateTable handles the DynamoDB UpdateTable operation.
func (h *Handler) UpdateTable(w http.ResponseWriter, r *http.Request) {
	var req updateTableRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.updateTableTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) updateTableTyped(ctx context.Context, req *updateTableRequest) (*createTableResponse, *protocol.AWSError) {
	if req.TableName == "" {
		return nil, protocol.ErrMissingParameter("TableName")
	}

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}

	changed := false

	// ── BillingMode ─────────────────────────────────────────────────────
	if req.BillingMode != "" {
		table.BillingMode = req.BillingMode
		summary := &BillingModeSummary{BillingMode: req.BillingMode}
		if req.BillingMode == "PAY_PER_REQUEST" {
			summary.LastUpdateToPayPerRequest = float64(h.clk.Now().UnixMilli()) / 1000.0
		}
		table.BillingModeSummary = summary
		changed = true
	}

	// ── AttributeDefinitions ────────────────────────────────────────────
	if len(req.AttributeDefinitions) > 0 {
		table.AttributeDefinitions = req.AttributeDefinitions
		changed = true
	}

	// ── ProvisionedThroughput ────────────────────────────────────────────
	if req.ProvisionedThroughput != nil {
		table.ProvisionedThroughput = req.ProvisionedThroughput
		changed = true
	}

	// ── GlobalSecondaryIndexUpdates ─────────────────────────────────────
	for _, update := range req.GlobalSecondaryIndexUpdates {
		if update.Create != nil {
			gsi := *update.Create
			gsi.IndexArn = table.TableARN + "/index/" + gsi.IndexName
			gsi.IndexStatus = "ACTIVE"
			table.GlobalSecondaryIndexes = append(table.GlobalSecondaryIndexes, gsi)
			changed = true
		}
		if update.Delete != nil {
			filtered := table.GlobalSecondaryIndexes[:0]
			for _, g := range table.GlobalSecondaryIndexes {
				if g.IndexName != update.Delete.IndexName {
					filtered = append(filtered, g)
				}
			}
			table.GlobalSecondaryIndexes = filtered
			changed = true
		}
		if update.Update != nil {
			for i := range table.GlobalSecondaryIndexes {
				if table.GlobalSecondaryIndexes[i].IndexName == update.Update.IndexName {
					if update.Update.ProvisionedThroughput != nil {
						table.GlobalSecondaryIndexes[i].ProvisionedThroughput = update.Update.ProvisionedThroughput
					}
					changed = true
					break
				}
			}
		}
	}

	// ── StreamSpecification ─────────────────────────────────────────────
	if req.StreamSpecification != nil {
		if req.StreamSpecification.StreamEnabled || req.StreamSpecification.StreamViewType != "" {
			req.StreamSpecification.StreamEnabled = true
			h.applyStreamSpec(table, req.StreamSpecification, middleware.RegionFromContext(ctx, h.cfg.Region))
		} else {
			table.StreamSpecification = &StreamSpecification{StreamEnabled: false}
		}
		changed = true
	}

	if changed {
		if aerr := h.store.putTable(ctx, table); aerr != nil {
			return nil, aerr
		}
		h.log.Info("table updated", zap.String("table", req.TableName))
		if h.bus != nil {
			h.bus.Publish(ctx, events.Event{
				Type:    events.DynamoDBStreamUpdated,
				Time:    h.clk.Now(),
				Source:  "dynamodb",
				Payload: events.ResourcePayload{Name: req.TableName},
			})
		}
	}

	return &createTableResponse{TableDescription: table}, nil
}

// ---- TTL -------------------------------------------------------------------

type updateTimeToLiveRequest struct {
	TableName               string                   `json:"TableName"`
	TimeToLiveSpecification *TimeToLiveSpecification `json:"TimeToLiveSpecification"`
}

type updateTimeToLiveResponse struct {
	TimeToLiveSpecification *TimeToLiveSpecification `json:"TimeToLiveSpecification"`
}

type describeTimeToLiveRequest struct {
	TableName string `json:"TableName"`
}

type describeTimeToLiveResponse struct {
	TimeToLiveDescription *TimeToLiveDescription `json:"TimeToLiveDescription"`
}

// UpdateTimeToLive handles the DynamoDB UpdateTimeToLive operation.
func (h *Handler) UpdateTimeToLive(w http.ResponseWriter, r *http.Request) {
	var req updateTimeToLiveRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.updateTimeToLiveTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) updateTimeToLiveTyped(ctx context.Context, req *updateTimeToLiveRequest) (*updateTimeToLiveResponse, *protocol.AWSError) {
	if req.TableName == "" {
		return nil, protocol.ErrMissingParameter("TableName")
	}
	if req.TimeToLiveSpecification == nil {
		return nil, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "TimeToLiveSpecification is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if req.TimeToLiveSpecification.Enabled && req.TimeToLiveSpecification.AttributeName == "" {
		return nil, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "TimeToLiveSpecification.AttributeName must be specified when enabling TTL",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}

	// AWS rejects enabling TTL if it's already enabled with a different attribute.
	if req.TimeToLiveSpecification.Enabled && table.ttlEnabled() {
		if table.TTL.AttributeName != req.TimeToLiveSpecification.AttributeName {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    "TimeToLive is already enabled with AttributeName " + table.TTL.AttributeName,
				HTTPStatus: http.StatusBadRequest,
			}
		}
		// Already enabled with the same attribute — idempotent.
	}

	table.TTL = req.TimeToLiveSpecification

	if aerr := h.store.putTable(ctx, table); aerr != nil {
		return nil, aerr
	}

	h.log.Info("table TTL updated",
		zap.String("table", req.TableName),
		zap.Bool("enabled", req.TimeToLiveSpecification.Enabled),
		zap.String("attribute", req.TimeToLiveSpecification.AttributeName),
	)

	return &updateTimeToLiveResponse{
		TimeToLiveSpecification: req.TimeToLiveSpecification,
	}, nil
}

// DescribeTimeToLive handles the DynamoDB DescribeTimeToLive operation.
func (h *Handler) DescribeTimeToLive(w http.ResponseWriter, r *http.Request) {
	var req describeTimeToLiveRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.describeTimeToLiveTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) describeTimeToLiveTyped(ctx context.Context, req *describeTimeToLiveRequest) (*describeTimeToLiveResponse, *protocol.AWSError) {
	if req.TableName == "" {
		return nil, protocol.ErrMissingParameter("TableName")
	}

	table, aerr := h.store.getTable(ctx, req.TableName)
	if aerr != nil {
		return nil, aerr
	}

	return &describeTimeToLiveResponse{
		TimeToLiveDescription: table.ttlDescription(),
	}, nil
}

// ---- Stream helpers --------------------------------------------------------

// applyStreamSpec sets the stream fields on a table, generating a new stream ARN/label.
func (h *Handler) applyStreamSpec(table *Table, spec *StreamSpecification, region string) {
	now := h.clk.Now()
	label := now.UTC().Format("2006-01-02T15:04:05.000")
	table.StreamSpecification = spec
	table.LatestStreamLabel = label
	table.LatestStreamArn = fmt.Sprintf(
		"arn:aws:dynamodb:%s:%s:table/%s/stream/%s",
		region, h.cfg.AccountID, table.TableName, label,
	)
}

// extractKeys builds a key-only Item from a full item using the table's key schema.
func extractKeys(table *Table, item Item) Item {
	keys := make(Item, 2)
	for _, k := range table.KeySchema {
		if v, ok := item[k.AttributeName]; ok {
			keys[k.AttributeName] = v
		}
	}
	return keys
}

// buildStreamImages returns newImage and oldImage based on the table's StreamViewType.
func buildStreamImages(viewType string, newItem, oldItem Item) (newImage, oldImage Item) {
	switch viewType {
	case "NEW_IMAGE":
		newImage = newItem
	case "OLD_IMAGE":
		oldImage = oldItem
	case "NEW_AND_OLD_IMAGES":
		newImage = newItem
		oldImage = oldItem
		// KEYS_ONLY: neither image is included
	}
	return
}

// publishPutStreamRecord publishes an INSERT or MODIFY stream record and events bus event.
func (h *Handler) publishPutStreamRecord(ctx context.Context, table *Table, newItem, oldItem Item) {
	eventName := "INSERT"
	if oldItem != nil {
		eventName = "MODIFY"
	}

	keys := extractKeys(table, newItem)
	newImage, oldImage := buildStreamImages(table.streamViewType(), newItem, oldItem)

	rec := &StreamRecord{
		EventName: eventName,
		Keys:      keys,
		NewImage:  newImage,
		OldImage:  oldImage,
		CreatedAt: h.clk.Now().UnixMilli(),
	}
	if aerr := h.store.appendStreamRecord(ctx, table.TableName, rec); aerr != nil {
		h.log.Error("stream: append record", zap.String("table", table.TableName), zap.String("event", eventName))
		return
	}

	if h.bus != nil {
		evtType := events.DynamoDBStreamInsert
		if eventName == "MODIFY" {
			evtType = events.DynamoDBStreamModify
		}
		seqStr := fmt.Sprintf("%021d", rec.SequenceNumber)
		ddbRecord := map[string]any{
			"ApproximateCreationDateTime": float64(rec.CreatedAt) / 1000.0,
			"Keys":                        keys,
			"NewImage":                    newImage,
			"OldImage":                    oldImage,
			"SequenceNumber":              seqStr,
			"StreamViewType":              table.streamViewType(),
		}
		h.bus.Publish(ctx, events.Event{
			Type:   evtType,
			Time:   h.clk.Now(),
			Source: "dynamodb",
			Payload: events.DynamoDBStreamPayload{
				Table:          table.TableName,
				EventName:      eventName,
				SequenceNumber: rec.SequenceNumber,
				Keys:           keys,
				NewImage:       newImage,
				OldImage:       oldImage,
				CreatedAt:      rec.CreatedAt,
			},
		})
		// Companion observability event: AWS StreamRecord shape so the event console
		// shows exactly what ESM filter patterns are evaluated against.
		h.bus.Publish(ctx, events.Event{
			Type:   events.DynamoDBStreamRecord,
			Time:   h.clk.Now(),
			Source: "dynamodb",
			Payload: events.DynamoDBStreamRecordPayload{
				Table:     table.TableName,
				EventName: eventName,
				Dynamodb:  ddbRecord,
			},
		})
	}
}

// publishDeleteStreamRecord publishes a REMOVE stream record and events bus event.
func (h *Handler) publishDeleteStreamRecord(ctx context.Context, table *Table, _, oldItem Item) {
	keys := extractKeys(table, oldItem)
	_, oldImage := buildStreamImages(table.streamViewType(), nil, oldItem)

	rec := &StreamRecord{
		EventName: "REMOVE",
		Keys:      keys,
		OldImage:  oldImage,
		CreatedAt: h.clk.Now().UnixMilli(),
	}
	if aerr := h.store.appendStreamRecord(ctx, table.TableName, rec); aerr != nil {
		h.log.Error("stream: append remove record", zap.String("table", table.TableName))
		return
	}

	if h.bus != nil {
		seqStr := fmt.Sprintf("%021d", rec.SequenceNumber)
		ddbRecord := map[string]any{
			"ApproximateCreationDateTime": float64(rec.CreatedAt) / 1000.0,
			"Keys":                        keys,
			"OldImage":                    oldImage,
			"SequenceNumber":              seqStr,
			"StreamViewType":              table.streamViewType(),
		}
		h.bus.Publish(ctx, events.Event{
			Type:   events.DynamoDBStreamRemove,
			Time:   h.clk.Now(),
			Source: "dynamodb",
			Payload: events.DynamoDBStreamPayload{
				Table:          table.TableName,
				EventName:      "REMOVE",
				SequenceNumber: rec.SequenceNumber,
				Keys:           keys,
				OldImage:       oldImage,
				CreatedAt:      rec.CreatedAt,
			},
		})
		// Companion observability event: AWS StreamRecord shape.
		h.bus.Publish(ctx, events.Event{
			Type:   events.DynamoDBStreamRecord,
			Time:   h.clk.Now(),
			Source: "dynamodb",
			Payload: events.DynamoDBStreamRecordPayload{
				Table:     table.TableName,
				EventName: "REMOVE",
				Dynamodb:  ddbRecord,
			},
		})
	}
}

// ---- BatchGetItem ----------------------------------------------------------

type batchGetItemRequest struct {
	RequestItems map[string]batchGetTableRequest `json:"RequestItems"`
}

type batchGetTableRequest struct {
	Keys []Item `json:"Keys"`
}

type batchGetItemResponse struct {
	Responses       map[string][]Item               `json:"Responses"`
	UnprocessedKeys map[string]batchGetTableRequest `json:"UnprocessedKeys"`
}

// BatchGetItem handles the DynamoDB BatchGetItem operation.
func (h *Handler) BatchGetItem(w http.ResponseWriter, r *http.Request) {
	var req batchGetItemRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.batchGetItemTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) batchGetItemTyped(ctx context.Context, req *batchGetItemRequest) (*batchGetItemResponse, *protocol.AWSError) {
	responses := make(map[string][]Item, len(req.RequestItems))

	for tableName, tableReq := range req.RequestItems {
		table, aerr := h.store.getTable(ctx, tableName)
		if aerr != nil {
			return nil, aerr
		}

		items := make([]Item, 0, len(tableReq.Keys))
		for _, key := range tableReq.Keys {
			item, aerr := h.store.getItem(ctx, table, key)
			if aerr != nil {
				return nil, aerr
			}
			if item != nil {
				items = append(items, item)
			}
		}
		responses[tableName] = items
	}

	return &batchGetItemResponse{
		Responses:       responses,
		UnprocessedKeys: map[string]batchGetTableRequest{},
	}, nil
}

// ---- BatchWriteItem --------------------------------------------------------

type batchWriteItemRequest struct {
	RequestItems map[string][]writeRequest `json:"RequestItems"`
}

type writeRequest struct {
	PutRequest    *putRequest    `json:"PutRequest,omitempty"`
	DeleteRequest *deleteRequest `json:"DeleteRequest,omitempty"`
}

type putRequest struct {
	Item Item `json:"Item"`
}

type deleteRequest struct {
	Key Item `json:"Key"`
}

type batchWriteItemResponse struct {
	UnprocessedItems map[string][]writeRequest `json:"UnprocessedItems"`
}

// BatchWriteItem handles the DynamoDB BatchWriteItem operation.
func (h *Handler) BatchWriteItem(w http.ResponseWriter, r *http.Request) {
	var req batchWriteItemRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.batchWriteItemTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) batchWriteItemTyped(ctx context.Context, req *batchWriteItemRequest) (*batchWriteItemResponse, *protocol.AWSError) {
	// Count total operations — AWS limit is 25.
	var totalOps int
	for _, ops := range req.RequestItems {
		totalOps += len(ops)
	}
	if totalOps > 25 {
		return nil, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "Too many items requested for the BatchWriteItem call",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	for tableName, ops := range req.RequestItems {
		table, aerr := h.store.getTable(ctx, tableName)
		if aerr != nil {
			return nil, aerr
		}

		for _, op := range ops {
			switch {
			case op.PutRequest != nil:
				var oldItem Item
				if table.streamEnabled() {
					oldItem, _ = h.store.getItem(ctx, table, op.PutRequest.Item)
				}
				if aerr := h.store.putItem(ctx, table, op.PutRequest.Item); aerr != nil {
					return nil, aerr
				}
				if table.streamEnabled() {
					h.publishPutStreamRecord(ctx, table, op.PutRequest.Item, oldItem)
				}

			case op.DeleteRequest != nil:
				var oldItem Item
				if table.streamEnabled() {
					oldItem, _ = h.store.getItem(ctx, table, op.DeleteRequest.Key)
				}
				if aerr := h.store.deleteItem(ctx, table, op.DeleteRequest.Key); aerr != nil {
					return nil, aerr
				}
				if table.streamEnabled() && oldItem != nil {
					h.publishDeleteStreamRecord(ctx, table, op.DeleteRequest.Key, oldItem)
				}
			}
		}

		h.bus.Publish(ctx, events.Event{
			Type:    events.DynamoDBItemMutated,
			Source:  "dynamodb",
			Payload: events.ResourcePayload{Name: tableName},
		})
	}

	return &batchWriteItemResponse{
		UnprocessedItems: map[string][]writeRequest{},
	}, nil
}

// itemKeysEqual checks whether item's key attributes match the given key map.
func itemKeysEqual(item, key Item, table *Table) bool {
	hk := table.hashKeyName()
	if extractKeyValue(item[hk]) != extractKeyValue(key[hk]) {
		return false
	}
	sk := table.sortKeyName()
	if sk != "" {
		if extractKeyValue(item[sk]) != extractKeyValue(key[sk]) {
			return false
		}
	}
	return true
}

// extractItemKeys returns only the table primary-key attributes from the given item.
func extractItemKeys(item Item, table *Table) Item {
	keys := Item{}
	hk := table.hashKeyName()
	if v, ok := item[hk]; ok {
		keys[hk] = v
	}
	sk := table.sortKeyName()
	if sk != "" {
		if v, ok := item[sk]; ok {
			keys[sk] = v
		}
	}
	return keys
}

// extractItemKeysWithIndex returns the primary-key attributes PLUS the index key
// attributes for the given item. AWS requires LastEvaluatedKey for index operations
// to include both the table's primary key and the index key attributes.
func extractItemKeysWithIndex(item Item, table *Table, idx *SecondaryIndex) Item {
	keys := extractItemKeys(item, table)
	if idx == nil {
		return keys
	}
	if hk := indexHashKeyName(idx); hk != "" {
		if v, ok := item[hk]; ok {
			keys[hk] = v
		}
	}
	if sk := indexSortKeyName(idx); sk != "" {
		if v, ok := item[sk]; ok {
			keys[sk] = v
		}
	}
	return keys
}
