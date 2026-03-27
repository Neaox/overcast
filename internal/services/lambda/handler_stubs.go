package lambda

// handler_stubs.go contains stub handlers that return 501 Not Implemented.
// Each stub must be replaced by a real implementation (in handler.go or a
// handler_<group>.go file) before the corresponding test is written.

import (
	"net/http"

	"github.com/your-org/overcast/internal/protocol"
)

// DispatchFunctionOp handles all function management operations
// (CreateFunction, GetFunction, ListFunctions, DeleteFunction, UpdateFunctionCode).
// TODO(priority:P1): implement function CRUD — see docs/services/lambda.md.
func (h *Handler) DispatchFunctionOp(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// InvokeFunction handles synchronous Lambda invocations.
// TODO(priority:P1): implement invocation via Runtime strategy — see docs/services/lambda.md.
func (h *Handler) InvokeFunction(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}
