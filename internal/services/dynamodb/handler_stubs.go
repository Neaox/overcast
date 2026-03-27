package dynamodb

// handler_stubs.go contains every DynamoDB handler that is not yet implemented.
// Each method returns HTTP 501 Not Implemented with x-emulator-unsupported: true.
//
// Convention: when an operation is implemented, move its method body out of this
// file and into handler.go (or handler_<group>.go for large feature groups).
// handler.go is the authoritative inventory of what actually works.

import (
	"net/http"

	"github.com/your-org/overcast/internal/protocol"
)

// DeleteTable handles the DynamoDB DeleteTable operation.
// AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DeleteTable.html
func (h *Handler) DeleteTable(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// UpdateItem handles the DynamoDB UpdateItem operation.
// AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateItem.html
func (h *Handler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// BatchGetItem handles the DynamoDB BatchGetItem operation.
// AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchGetItem.html
func (h *Handler) BatchGetItem(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// BatchWriteItem handles the DynamoDB BatchWriteItem operation.
// AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchWriteItem.html
func (h *Handler) BatchWriteItem(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// TransactWriteItems handles the DynamoDB TransactWriteItems operation.
// AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactWriteItems.html
func (h *Handler) TransactWriteItems(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// TransactGetItems handles the DynamoDB TransactGetItems operation.
// AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactGetItems.html
func (h *Handler) TransactGetItems(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}
