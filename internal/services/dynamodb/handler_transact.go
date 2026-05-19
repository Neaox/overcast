package dynamodb

// handler_transact.go implements DynamoDB TransactWriteItems and TransactGetItems.
// Both operations are all-or-nothing: if any condition check fails, the entire
// transaction is rolled back (TransactWriteItems) or returns an error (TransactGetItems).

import (
	"context"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ---- TransactWriteItems ----------------------------------------------------

type transactWriteItemsRequest struct {
	TransactItems []transactWriteItem `json:"TransactItems"`
}

type transactWriteItem struct {
	ConditionCheck *transactConditionCheck `json:"ConditionCheck,omitempty"`
	Put            *transactPut            `json:"Put,omitempty"`
	Delete         *transactDelete         `json:"Delete,omitempty"`
	Update         *transactUpdate         `json:"Update,omitempty"`
}

type transactConditionCheck struct {
	TableName                 string            `json:"TableName"`
	Key                       Item              `json:"Key"`
	ConditionExpression       string            `json:"ConditionExpression"`
	ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames,omitempty"`
	ExpressionAttributeValues Item              `json:"ExpressionAttributeValues,omitempty"`
}

type transactPut struct {
	TableName                 string            `json:"TableName"`
	Item                      Item              `json:"Item"`
	ConditionExpression       string            `json:"ConditionExpression,omitempty"`
	ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames,omitempty"`
	ExpressionAttributeValues Item              `json:"ExpressionAttributeValues,omitempty"`
}

type transactDelete struct {
	TableName                 string            `json:"TableName"`
	Key                       Item              `json:"Key"`
	ConditionExpression       string            `json:"ConditionExpression,omitempty"`
	ExpressionAttributeNames  map[string]string `json:"ExpressionAttributeNames,omitempty"`
	ExpressionAttributeValues Item              `json:"ExpressionAttributeValues,omitempty"`
}

type transactUpdate struct {
	TableName                 string               `json:"TableName"`
	Key                       Item                 `json:"Key"`
	UpdateExpression          string               `json:"UpdateExpression"`
	ConditionExpression       string               `json:"ConditionExpression,omitempty"`
	ExpressionAttributeNames  map[string]string    `json:"ExpressionAttributeNames,omitempty"`
	ExpressionAttributeValues map[string]attrValue `json:"ExpressionAttributeValues,omitempty"`
}

// TransactWriteItems handles the DynamoDB TransactWriteItems operation.
// All-or-nothing: validates all conditions first, then applies all mutations.
// AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactWriteItems.html
func (h *Handler) TransactWriteItems(w http.ResponseWriter, r *http.Request) {
	var req transactWriteItemsRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.transactWriteItemsTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) transactWriteItemsTyped(ctx context.Context, req *transactWriteItemsRequest) (*struct{}, *protocol.AWSError) {
	if len(req.TransactItems) > 100 {
		return nil, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "Member must have length less than or equal to 100",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	// Phase 1: Validate all tables exist, load existing items, evaluate conditions.
	type resolvedAction struct {
		table   *Table
		oldItem Item // for stream records (Put/Delete/Update)
	}
	resolved := make([]resolvedAction, len(req.TransactItems))
	cancellationReasons := make([]string, len(req.TransactItems))
	cancelled := false

	for i, txItem := range req.TransactItems {
		var tableName string
		var key Item
		var condExpr string
		var condNames map[string]string
		var condValues Item

		switch {
		case txItem.ConditionCheck != nil:
			cc := txItem.ConditionCheck
			tableName = cc.TableName
			key = cc.Key
			condExpr = cc.ConditionExpression
			condNames = cc.ExpressionAttributeNames
			condValues = cc.ExpressionAttributeValues
		case txItem.Put != nil:
			p := txItem.Put
			tableName = p.TableName
			key = p.Item // extractKeys will pull out proper keys
			condExpr = p.ConditionExpression
			condNames = p.ExpressionAttributeNames
			condValues = p.ExpressionAttributeValues
		case txItem.Delete != nil:
			d := txItem.Delete
			tableName = d.TableName
			key = d.Key
			condExpr = d.ConditionExpression
			condNames = d.ExpressionAttributeNames
			condValues = d.ExpressionAttributeValues
		case txItem.Update != nil:
			u := txItem.Update
			tableName = u.TableName
			key = u.Key
			condExpr = u.ConditionExpression
			condNames = u.ExpressionAttributeNames
			// Convert ExpressionAttributeValues from map[string]attrValue to Item
			if u.ExpressionAttributeValues != nil {
				condValues = make(Item, len(u.ExpressionAttributeValues))
				for k, v := range u.ExpressionAttributeValues {
					condValues[k] = v
				}
			}
		default:
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    "TransactItems member must contain exactly one of Put, Delete, Update, or ConditionCheck",
				HTTPStatus: http.StatusBadRequest,
			}
		}

		table, aerr := h.store.getTable(ctx, tableName)
		if aerr != nil {
			return nil, aerr
		}
		resolved[i].table = table

		// Load existing item for condition checks and stream records.
		existing, aerr := h.store.getItem(ctx, table, key)
		if aerr != nil {
			return nil, aerr
		}
		resolved[i].oldItem = existing

		// Evaluate condition expression if present.
		if condExpr != "" {
			filter, err := compileFilter(condExpr, condNames, condValues)
			if err != nil {
				return nil, &protocol.AWSError{
					Code:       "ValidationException",
					Message:    err.Error(),
					HTTPStatus: http.StatusBadRequest,
				}
			}
			checkItem := existing
			if checkItem == nil {
				checkItem = Item{} // empty item for attribute_not_exists etc.
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
				cancellationReasons[i] = "ConditionalCheckFailed"
				cancelled = true
			}
		}
	}

	if cancelled {
		return nil, &protocol.AWSError{
			Code:       "TransactionCanceledException",
			Message:    "Transaction cancelled, please refer cancellation reasons for specific reasons [" + joinReasons(cancellationReasons) + "]",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	// Phase 2: Apply all mutations (conditions already validated).
	mutatedTables := make(map[string]bool)

	for i, txItem := range req.TransactItems {
		table := resolved[i].table
		oldItem := resolved[i].oldItem

		switch {
		case txItem.ConditionCheck != nil:
			// No mutation needed — condition already validated.

		case txItem.Put != nil:
			if aerr := h.store.putItem(ctx, table, txItem.Put.Item); aerr != nil {
				return nil, aerr
			}
			if table.streamEnabled() {
				h.publishPutStreamRecord(ctx, table, txItem.Put.Item, oldItem)
			}
			mutatedTables[table.TableName] = true

		case txItem.Delete != nil:
			if aerr := h.store.deleteItem(ctx, table, txItem.Delete.Key); aerr != nil {
				return nil, aerr
			}
			if table.streamEnabled() && oldItem != nil {
				h.publishDeleteStreamRecord(ctx, table, txItem.Delete.Key, oldItem)
			}
			mutatedTables[table.TableName] = true

		case txItem.Update != nil:
			u := txItem.Update
			item := cloneItem(u.Key)
			if oldItem != nil {
				item = cloneItem(oldItem)
			}
			if u.UpdateExpression != "" {
				if err := applyUpdateExpression(item, u.UpdateExpression,
					u.ExpressionAttributeNames, u.ExpressionAttributeValues); err != nil {
					return nil, &protocol.AWSError{
						Code:       "ValidationException",
						Message:    err.Error(),
						HTTPStatus: http.StatusBadRequest,
					}
				}
			}
			if aerr := h.store.putItem(ctx, table, item); aerr != nil {
				return nil, aerr
			}
			if table.streamEnabled() {
				h.publishPutStreamRecord(ctx, table, item, oldItem)
			}
			mutatedTables[table.TableName] = true
		}
	}

	// Publish mutation events for all affected tables.
	for tableName := range mutatedTables {
		h.bus.Publish(ctx, events.Event{
			Type:    events.DynamoDBItemMutated,
			Source:  "dynamodb",
			Payload: events.ResourcePayload{Name: tableName},
		})
	}

	return &struct{}{}, nil
}

// joinReasons formats cancellation reasons for the error message.
func joinReasons(reasons []string) string {
	var b strings.Builder
	for i, r := range reasons {
		if i > 0 {
			b.WriteString(", ")
		}
		if r == "" {
			b.WriteString("None")
		} else {
			b.WriteString(r)
		}
	}
	return b.String()
}

// ---- TransactGetItems ------------------------------------------------------

type transactGetItemsRequest struct {
	TransactItems []transactGetItem `json:"TransactItems"`
}

type transactGetItem struct {
	Get *transactGet `json:"Get"`
}

type transactGet struct {
	TableName string `json:"TableName"`
	Key       Item   `json:"Key"`
}

type transactGetItemsResponse struct {
	Responses []transactGetResponse `json:"Responses"`
}

type transactGetResponse struct {
	Item Item `json:"Item"`
}

// TransactGetItems handles the DynamoDB TransactGetItems operation.
// AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactGetItems.html
func (h *Handler) TransactGetItems(w http.ResponseWriter, r *http.Request) {
	var req transactGetItemsRequest
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	resp, aerr := h.transactGetItemsTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) transactGetItemsTyped(ctx context.Context, req *transactGetItemsRequest) (*transactGetItemsResponse, *protocol.AWSError) {
	if len(req.TransactItems) > 100 {
		return nil, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "Member must have length less than or equal to 100",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	responses := make([]transactGetResponse, len(req.TransactItems))

	for i, txItem := range req.TransactItems {
		if txItem.Get == nil {
			return nil, &protocol.AWSError{
				Code:       "ValidationException",
				Message:    "TransactItems member must contain Get",
				HTTPStatus: http.StatusBadRequest,
			}
		}

		table, aerr := h.store.getTable(ctx, txItem.Get.TableName)
		if aerr != nil {
			return nil, aerr
		}

		item, aerr := h.store.getItem(ctx, table, txItem.Get.Key)
		if aerr != nil {
			return nil, aerr
		}

		responses[i] = transactGetResponse{Item: item}
	}

	return &transactGetItemsResponse{
		Responses: responses,
	}, nil
}
