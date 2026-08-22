package dynamodb

// metrics_dynamodb.go is DynamoDB's half of the service-metrics substrate
// (docs/plans/service-metrics-platform.md, phase 2). Unlike Lambda/SQS/SNS,
// DynamoDB has no single dispatch chokepoint that already knows every
// operation's table name, success/failure, and timing (both the legacy
// http.HandlerFunc registry and the codec-generic typed-op registry funnel
// into the same "xxxTyped" business-logic function per operation — see
// newHandler's `h.rawOp = h.typedOps()` — but op.Typed/TypedAny's generic
// Invoke never sees TableName). So the outcome helper pattern here is: each
// data-plane operation's original "xxxTyped" function is renamed
// "xxxTypedCore", and a thin "xxxTyped" wrapper of the same name/signature
// (the one both dispatch paths already call — nothing else changes) records
// the outcome around it. This keeps exactly one measurement per operation,
// at its one authoritative call site, matching the plan's "one per-service
// outcome helper" rule even though DynamoDB needs one wrapper per operation
// rather than Lambda's one function shared by several call sites.
//
// AWS reference: https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/metrics-dimensions.html
//
// Disposition per metric:
//
//   - SuccessfulRequestLatency (TableName, Operation; Milliseconds): recorded
//     for every operation below, only on success — matches AWS's own name.
//   - ConsumedReadCapacityUnits / ConsumedWriteCapacityUnits (TableName
//     [+ GlobalSecondaryIndexName [+ Source for writes]]; Count): recorded
//     only on success, approximated from json.Marshal(item)-byte-length —
//     NOT AWS's precise per-attribute size algorithm — divided by the 4 KB
//     (read) / 1 KB (write) accounting unit AWS documents, doubled for
//     transactional operations, halved for a non-consistent read. This is a
//     disclosed approximation, not a wire-exact replica of AWS's billing
//     math; Query/Scan additionally scale the estimate by
//     scannedCount/returnedCount when a FilterExpression discarded scanned
//     items, since AWS bills on items examined, not items returned.
//   - UserErrors (no dimensions — AWS publishes this one account/region-wide,
//     never per-table; Count): recorded for any HTTP 4xx response, EXCLUDING
//     ConditionalCheckFailedException (AWS's own ConditionalCheckFailedRequests
//     metric instead) and ProvisionedThroughputExceededException (AWS's own
//     ThrottledRequests metric instead — not modeled here, see below).
//   - SystemErrors (TableName, Operation; Count): recorded for any HTTP 5xx
//     response.
//   - ThrottledRequests / ReadThrottleEvents / WriteThrottleEvents: NOT
//     recorded. This emulator does not model DynamoDB throttling at all (no
//     ProvisionedThroughputExceededException is ever returned) — there is no
//     underlying fact to observe, per the plan's "only where the emulator
//     can observe the underlying fact" rule. Revisit if/when throughput
//     limiting is modeled.
//
// Operation dimension values match AWS's documented list exactly:
// PutItem, DeleteItem, UpdateItem, GetItem, BatchGetItem, Scan, Query,
// BatchWriteItem, TransactWriteItems, TransactGetItems. (AWS's list also
// includes ExecuteTransaction/BatchExecuteStatement/ExecuteStatement —
// PartiQL is not implemented by this emulator, so those are absent.)
import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/metrics"
	"github.com/Neaox/overcast/internal/protocol"
)

// metricsRecorder is the narrow interface DynamoDB depends on to record
// outcome facts — never internal/services/cloudwatch (plan acceptance
// criteria: "no service imports internal/services/cloudwatch"). Satisfied by
// *metrics.Service.
type metricsRecorder interface {
	Observe(ctx context.Context, o metrics.Observation) error
}

const dynamoDBMetricsNamespace = "AWS/DynamoDB"

// assumedItemBytesForCapacityEstimate stands in for a scanned item's size
// when no returned item is available to measure (Select=COUNT, or every
// scanned item was discarded by a FilterExpression) — the same "commonly
// well under 1 KB" guidance dynamoDefaultPageLimit's doc comment already
// leans on elsewhere in this package.
const assumedItemBytesForCapacityEstimate = 1024

// observeDynamoDBMetric records one AWS/DynamoDB observation, logging (never
// failing the caller's request) on error. A nil h.metrics (collection
// disabled, or a unit test that never called Service.InitMetrics) makes this
// a no-op.
func (h *Handler) observeDynamoDBMetric(ctx context.Context, name string, dims []metrics.Dimension, unit string, value float64) {
	if h.metrics == nil {
		return
	}
	if err := h.metrics.Observe(ctx, metrics.Observation{
		Namespace:  dynamoDBMetricsNamespace,
		Name:       name,
		Dimensions: dims,
		Timestamp:  h.clk.Now(),
		Unit:       unit,
		Value:      value,
	}); err != nil {
		h.log.Debug("dynamodb: metrics observe failed", zap.String("metric", name), zap.Error(err))
	}
}

// recordDynamoDBOutcome is the shared outcome tail every "xxxTyped" wrapper
// below calls exactly once: SuccessfulRequestLatency on success,
// UserErrors/SystemErrors classified from aerr.HTTPStatus on failure.
// Capacity-unit recording is operation-specific (each wrapper calls
// recordConsumedReadCapacity/recordConsumedWriteCapacity itself, since only
// the caller knows whether the operation reads or writes and how to size it).
func (h *Handler) recordDynamoDBOutcome(ctx context.Context, operation, tableName string, start time.Time, aerr *protocol.AWSError) {
	if h.metrics == nil {
		return
	}
	if aerr == nil {
		h.observeDynamoDBMetric(ctx,
			"SuccessfulRequestLatency",
			[]metrics.Dimension{{Name: "TableName", Value: tableName}, {Name: "Operation", Value: operation}},
			"Milliseconds", float64(h.clk.Now().Sub(start).Milliseconds()))
		return
	}
	switch {
	case aerr.HTTPStatus >= 500:
		h.observeDynamoDBMetric(ctx,
			"SystemErrors",
			[]metrics.Dimension{{Name: "TableName", Value: tableName}, {Name: "Operation", Value: operation}},
			"Count", 1)
	case aerr.HTTPStatus >= 400:
		if aerr.Code != "ConditionalCheckFailedException" && aerr.Code != "ProvisionedThroughputExceededException" {
			// Account/region-level: no TableName/Operation dimensions — see
			// this file's doc comment.
			h.observeDynamoDBMetric(ctx, "UserErrors", nil, "Count", 1)
		}
	}
}

func (h *Handler) recordConsumedReadCapacity(ctx context.Context, tableName, gsiName string, units float64) {
	dims := []metrics.Dimension{{Name: "TableName", Value: tableName}}
	if gsiName != "" {
		dims = append(dims, metrics.Dimension{Name: "GlobalSecondaryIndexName", Value: gsiName})
	}
	h.observeDynamoDBMetric(ctx, "ConsumedReadCapacityUnits", dims, "Count", units)
}

func (h *Handler) recordConsumedWriteCapacity(ctx context.Context, tableName, gsiName string, units float64) {
	dims := []metrics.Dimension{{Name: "TableName", Value: tableName}}
	if gsiName != "" {
		dims = append(dims, metrics.Dimension{Name: "GlobalSecondaryIndexName", Value: gsiName})
	}
	// Source distinguishes Customer vs GlobalTable writes; global tables are
	// not modeled here, so every recorded write is a direct customer write.
	dims = append(dims, metrics.Dimension{Name: "Source", Value: "Customer"})
	h.observeDynamoDBMetric(ctx, "ConsumedWriteCapacityUnits", dims, "Count", units)
}

// estimateItemSize approximates an item's AWS-billed size in bytes via its
// DynamoDB-JSON encoding length — not AWS's precise per-attribute-type size
// algorithm (see file doc comment), but proportionate to it for the common
// small-item case this emulator targets.
func estimateItemSize(item Item) int {
	if len(item) == 0 {
		return 0
	}
	b, err := json.Marshal(item)
	if err != nil {
		return 0
	}
	return len(b)
}

// rcuForRead converts an estimated byte size into AWS's read-capacity-unit
// accounting: one RCU per 4 KB (rounded up), halved for an eventually
// consistent read, doubled for a transactional read (AWS: TransactGetItems
// consumes 2x the read capacity of an equivalent GetItem).
func rcuForRead(bytes int, consistent, transactional bool) float64 {
	units := ceilDiv(bytes, 4096)
	if units < 1 {
		units = 1
	}
	result := float64(units)
	switch {
	case transactional:
		result *= 2
	case !consistent:
		result /= 2
	}
	return result
}

// wcuForWrite converts an estimated byte size into AWS's write-capacity-unit
// accounting: one WCU per 1 KB (rounded up), doubled for a transactional
// write (AWS: TransactWriteItems consumes 2x the write capacity of an
// equivalent PutItem/UpdateItem/DeleteItem).
func wcuForWrite(bytes int, transactional bool) float64 {
	units := ceilDiv(bytes, 1024)
	if units < 1 {
		units = 1
	}
	result := float64(units)
	if transactional {
		result *= 2
	}
	return result
}

func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// estimateReadBytes approximates the bytes DynamoDB examined for a Query/Scan
// call from its returned items, scaling up to scannedCount when a
// FilterExpression discarded some of what was examined (AWS bills on
// examined, not returned, items) — or, when no item survived to measure
// (Select=COUNT, or every scanned item was filtered out), falling back to
// assumedItemBytesForCapacityEstimate per scanned item.
func estimateReadBytes(items []Item, scannedCount int) int {
	if len(items) > 0 {
		total := 0
		for _, it := range items {
			total += estimateItemSize(it)
		}
		if len(items) < scannedCount {
			total = total * scannedCount / len(items)
		}
		return total
	}
	if scannedCount > 0 {
		return scannedCount * assumedItemBytesForCapacityEstimate
	}
	return 0
}

// ---- per-operation outcome wrappers ----------------------------------------
//
// Each wrapper below has the exact name/signature the original "xxxTyped"
// function had (see the "Core" rename in handler.go/handler_update.go/
// handler_transact.go's doc comments) — typed_ops.go's op.NewTyped/NewTypedAny
// registrations and each legacy http.HandlerFunc still call this name
// unchanged, so both dispatch paths get metrics for free.

func (h *Handler) putItemTyped(ctx context.Context, req *putItemRequest) (*putItemResponse, *protocol.AWSError) {
	if h.metrics == nil {
		return h.putItemTypedCore(ctx, req)
	}
	start := h.clk.Now()
	resp, aerr := h.putItemTypedCore(ctx, req)
	h.recordDynamoDBOutcome(ctx, "PutItem", req.TableName, start, aerr)
	if aerr == nil {
		h.recordConsumedWriteCapacity(ctx, req.TableName, "", wcuForWrite(estimateItemSize(req.Item), false))
	}
	return resp, aerr
}

func (h *Handler) getItemTyped(ctx context.Context, req *getItemRequest) (*getItemResponse, *protocol.AWSError) {
	if h.metrics == nil {
		return h.getItemTypedCore(ctx, req)
	}
	start := h.clk.Now()
	resp, aerr := h.getItemTypedCore(ctx, req)
	h.recordDynamoDBOutcome(ctx, "GetItem", req.TableName, start, aerr)
	if aerr == nil {
		var bytes int
		if resp != nil {
			bytes = estimateItemSize(resp.Item)
		}
		// GetItem's request shape here has no ConsistentRead field (not
		// modeled — see this file's doc comment), so every read is treated as
		// eventually consistent, matching AWS's own default when the
		// parameter is omitted.
		h.recordConsumedReadCapacity(ctx, req.TableName, "", rcuForRead(bytes, false, false))
	}
	return resp, aerr
}

func (h *Handler) deleteItemTyped(ctx context.Context, req *deleteItemRequest) (*deleteItemResponse, *protocol.AWSError) {
	if h.metrics == nil {
		return h.deleteItemTypedCore(ctx, req)
	}
	start := h.clk.Now()
	resp, aerr := h.deleteItemTypedCore(ctx, req)
	h.recordDynamoDBOutcome(ctx, "DeleteItem", req.TableName, start, aerr)
	if aerr == nil {
		// The deleted item's full size isn't always fetched by the core path
		// (only when streams/GSIs/ReturnValues need it) — req.Key is always
		// available and is used as the size floor, an accepted undercount
		// for a delete of a large item with no other reason to have read it.
		bytes := estimateItemSize(req.Key)
		if resp != nil && len(resp.Attributes) > 0 {
			bytes = estimateItemSize(resp.Attributes)
		}
		h.recordConsumedWriteCapacity(ctx, req.TableName, "", wcuForWrite(bytes, false))
	}
	return resp, aerr
}

func (h *Handler) updateItemTyped(ctx context.Context, req *updateItemRequest) (*updateItemResponse, *protocol.AWSError) {
	if h.metrics == nil {
		return h.updateItemTypedCore(ctx, req)
	}
	start := h.clk.Now()
	resp, aerr := h.updateItemTypedCore(ctx, req)
	h.recordDynamoDBOutcome(ctx, "UpdateItem", req.TableName, start, aerr)
	if aerr == nil {
		// The post-update item is not always returned (ReturnValues=NONE is
		// the default) — approximate from what the request itself carries:
		// the key plus the values the UpdateExpression referenced. Not exact,
		// but proportionate for the common small-item case (see file doc).
		bytes := estimateItemSize(req.Key)
		if resp != nil && len(resp.Attributes) > 0 {
			bytes += estimateItemSize(resp.Attributes)
		} else if len(req.ExpressionAttributeValues) > 0 {
			bytes += estimateItemSize(Item(req.ExpressionAttributeValues))
		}
		h.recordConsumedWriteCapacity(ctx, req.TableName, "", wcuForWrite(bytes, false))
	}
	return resp, aerr
}

func (h *Handler) batchGetItemTyped(ctx context.Context, req *batchGetItemRequest) (*batchGetItemResponse, *protocol.AWSError) {
	if h.metrics == nil {
		return h.batchGetItemTypedCore(ctx, req)
	}
	start := h.clk.Now()
	resp, aerr := h.batchGetItemTypedCore(ctx, req)
	for tableName := range req.RequestItems {
		h.recordDynamoDBOutcome(ctx, "BatchGetItem", tableName, start, aerr)
	}
	if aerr == nil && resp != nil {
		for tableName, items := range resp.Responses {
			bytes := 0
			for _, it := range items {
				bytes += estimateItemSize(it)
			}
			// BatchGetItem has no top-level ConsistentRead — AWS scopes it
			// per-table request; not modeled by this emulator's request
			// shape, so every read is treated as eventually consistent (see
			// getItemTyped's identical note).
			h.recordConsumedReadCapacity(ctx, tableName, "", rcuForRead(bytes, false, false))
		}
	}
	return resp, aerr
}

func (h *Handler) batchWriteItemTyped(ctx context.Context, req *batchWriteItemRequest) (*batchWriteItemResponse, *protocol.AWSError) {
	if h.metrics == nil {
		return h.batchWriteItemTypedCore(ctx, req)
	}
	start := h.clk.Now()
	resp, aerr := h.batchWriteItemTypedCore(ctx, req)
	for tableName, ops := range req.RequestItems {
		h.recordDynamoDBOutcome(ctx, "BatchWriteItem", tableName, start, aerr)
		if aerr != nil {
			continue
		}
		bytes := 0
		for _, op := range ops {
			switch {
			case op.PutRequest != nil:
				bytes += estimateItemSize(op.PutRequest.Item)
			case op.DeleteRequest != nil:
				bytes += estimateItemSize(op.DeleteRequest.Key)
			}
		}
		h.recordConsumedWriteCapacity(ctx, tableName, "", wcuForWrite(bytes, false))
	}
	return resp, aerr
}

func (h *Handler) scanTyped(ctx context.Context, req *scanRequest) (any, *protocol.AWSError) {
	if h.metrics == nil {
		return h.scanTypedCore(ctx, req)
	}
	start := h.clk.Now()
	resp, aerr := h.scanTypedCore(ctx, req)
	h.recordDynamoDBOutcome(ctx, "Scan", req.TableName, start, aerr)
	if aerr == nil {
		items, scannedCount := scanLikeResponseShape(resp)
		bytes := estimateReadBytes(items, scannedCount)
		h.recordConsumedReadCapacity(ctx, req.TableName, h.gsiNameFor(ctx, req.TableName, req.IndexName), rcuForRead(bytes, req.ConsistentRead, false))
	}
	return resp, aerr
}

func (h *Handler) queryTyped(ctx context.Context, req *queryRequest) (any, *protocol.AWSError) {
	if h.metrics == nil {
		return h.queryTypedCore(ctx, req)
	}
	start := h.clk.Now()
	resp, aerr := h.queryTypedCore(ctx, req)
	h.recordDynamoDBOutcome(ctx, "Query", req.TableName, start, aerr)
	if aerr == nil {
		items, scannedCount := scanLikeResponseShape(resp)
		bytes := estimateReadBytes(items, scannedCount)
		h.recordConsumedReadCapacity(ctx, req.TableName, h.gsiNameFor(ctx, req.TableName, req.IndexName), rcuForRead(bytes, req.ConsistentRead, false))
	}
	return resp, aerr
}

// scanLikeResponseShape extracts the (possibly absent) Items and the
// ScannedCount common to scanResponse, queryResponse, and countOnlyResponse —
// Scan/Query return `any` because Select=COUNT swaps in the Items-less
// variant (see scanTypedCore/queryTypedCore).
func scanLikeResponseShape(resp any) (items []Item, scannedCount int) {
	switch v := resp.(type) {
	case *scanResponse:
		return v.Items, v.ScannedCount
	case *queryResponse:
		return v.Items, v.ScannedCount
	case *countOnlyResponse:
		return nil, v.ScannedCount
	default:
		return nil, 0
	}
}

// gsiNameFor returns indexName when it names an actual GSI on tableName
// (never an LSI, which shares the base table's capacity and dimension, not
// a GlobalSecondaryIndexName-dimensioned series of its own). A lookup
// failure (table since deleted, metrics-only best-effort path) degrades to
// "no GSI dimension" rather than failing the caller's already-succeeded
// request.
func (h *Handler) gsiNameFor(ctx context.Context, tableName, indexName string) string {
	if indexName == "" {
		return ""
	}
	table, aerr := h.store.getTable(ctx, tableName)
	if aerr != nil || !table.isGSI(indexName) {
		return ""
	}
	return indexName
}

func (h *Handler) transactWriteItemsTyped(ctx context.Context, req *transactWriteItemsRequest) (*struct{}, *protocol.AWSError) {
	if h.metrics == nil {
		return h.transactWriteItemsTypedCore(ctx, req)
	}
	start := h.clk.Now()
	resp, aerr := h.transactWriteItemsTypedCore(ctx, req)
	bytesByTable := make(map[string]int)
	for _, txItem := range req.TransactItems {
		var tableName string
		var bytes int
		switch {
		case txItem.Put != nil:
			tableName, bytes = txItem.Put.TableName, estimateItemSize(txItem.Put.Item)
		case txItem.Delete != nil:
			tableName, bytes = txItem.Delete.TableName, estimateItemSize(txItem.Delete.Key)
		case txItem.Update != nil:
			tableName, bytes = txItem.Update.TableName, estimateItemSize(txItem.Update.Key)+estimateItemSize(Item(txItem.Update.ExpressionAttributeValues))
		case txItem.ConditionCheck != nil:
			tableName, bytes = txItem.ConditionCheck.TableName, 0
		default:
			continue
		}
		if tableName == "" {
			continue
		}
		bytesByTable[tableName] += bytes
	}
	for tableName, bytes := range bytesByTable {
		h.recordDynamoDBOutcome(ctx, "TransactWriteItems", tableName, start, aerr)
		if aerr == nil {
			h.recordConsumedWriteCapacity(ctx, tableName, "", wcuForWrite(bytes, true))
		}
	}
	return resp, aerr
}

func (h *Handler) transactGetItemsTyped(ctx context.Context, req *transactGetItemsRequest) (*transactGetItemsResponse, *protocol.AWSError) {
	if h.metrics == nil {
		return h.transactGetItemsTypedCore(ctx, req)
	}
	start := h.clk.Now()
	resp, aerr := h.transactGetItemsTypedCore(ctx, req)
	bytesByTable := make(map[string]int)
	for i, txItem := range req.TransactItems {
		if txItem.Get == nil || txItem.Get.TableName == "" {
			continue
		}
		bytes := 0
		if resp != nil && i < len(resp.Responses) {
			bytes = estimateItemSize(resp.Responses[i].Item)
		}
		bytesByTable[txItem.Get.TableName] += bytes
	}
	for tableName, bytes := range bytesByTable {
		h.recordDynamoDBOutcome(ctx, "TransactGetItems", tableName, start, aerr)
		if aerr == nil {
			// TransactGetItems has no ConsistentRead concept (transactional
			// reads are always strongly consistent) — pass consistent=true so
			// the transactional 2x multiplier is the only adjustment applied.
			h.recordConsumedReadCapacity(ctx, tableName, "", rcuForRead(bytes, true, true))
		}
	}
	return resp, aerr
}
