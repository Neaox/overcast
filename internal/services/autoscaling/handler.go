package autoscaling

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

// Handler holds the Auto Scaling operation registry. Every operation has
// exactly one implementation — the typed one — so there is no second copy of
// the business logic to drift.
type Handler struct {
	svc     *Service
	typedOp map[string]op.Operation
}

func newHandler(s *Service) *Handler {
	h := &Handler{svc: s}
	h.typedOp = h.typedOps()
	return h
}

// dispatch resolves the operation from the request form and runs it through
// the Query codec. This is the path for callers that reached the service
// without the protocol middleware having identified the operation.
func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		protocol.NotImplementedQueryXML(w, r)
		return
	}
	action := r.FormValue("Action")
	if operation, ok := h.typedOp[action]; ok {
		operation.Invoke(w, r, codec.QueryXML)
		return
	}
	protocol.NotImplementedQueryXML(w, r)
}
