package acm

import (
	"context"
	"net/http"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/op"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// Handler holds ACM handler dependencies.
type Handler struct {
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
	store   *acmStore
	cfg     *config.Config
	clk     clock.Clock
}

func newHandler(cfg *config.Config, store *acmStore, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: store, clk: clk}
	h.initOps()
	return h
}

func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"DescribeCertificate": h.describeCertificate,
		"ListCertificates":    h.listCertificates,
		"DeleteCertificate":   h.deleteCertificate,
		// RequestCertificate and every tag operation are implemented once, as
		// typed functions, and reached from the JSON path through an adapter.
		// Keeping two copies is how the tag handlers drifted into not checking
		// that the certificate exists.
		"RequestCertificate":        serveTyped(h.requestCertificateTyped),
		"ListTagsForCertificate":    serveTyped(h.listTagsForCertificateTyped),
		"AddTagsToCertificate":      serveTyped(h.addTagsToCertificateTyped),
		"RemoveTagsFromCertificate": serveTyped(h.removeTagsFromCertificateTyped),
		"TagResource":               serveTyped(h.tagResourceTyped),
		"UntagResource":             serveTyped(h.untagResourceTyped),
		"ListTagsForResource":       serveTyped(h.listTagsForResourceTyped),
	}
	h.typedOp = h.typedOps()
}

// serveTyped adapts a typed operation to the JSON 1.0/1.1 dispatch table.
func serveTyped[In any, Out any](fn func(context.Context, *In) (*Out, *protocol.AWSError)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in In
		if !serviceutil.DecodeJSON(w, r, &in) {
			return
		}
		out, aerr := fn(r.Context(), &in)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		if out == nil {
			protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
			return
		}
		protocol.WriteJSON(w, r, http.StatusOK, out)
	}
}

func (h *Handler) describeCertificate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CertificateArn string `json:"CertificateArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	cert, found := h.store.getCert(r.Context(), req.CertificateArn)
	if !found {
		protocol.WriteJSONError(w, r, errCertificateNotFound(req.CertificateArn))
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Certificate": cert})
}

func (h *Handler) listCertificates(w http.ResponseWriter, r *http.Request) {
	certs, err := h.store.listCerts(r.Context())
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	summaries := make([]map[string]any, 0, len(certs))
	for _, c := range certs {
		summaries = append(summaries, map[string]any{
			"CertificateArn": c.CertificateArn,
			"DomainName":     c.DomainName,
			"Status":         c.Status,
			"Type":           c.Type,
		})
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"CertificateSummaryList": summaries})
}

func (h *Handler) deleteCertificate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CertificateArn string `json:"CertificateArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, found := h.store.getCert(r.Context(), req.CertificateArn); !found {
		protocol.WriteJSONError(w, r, errCertificateNotFound(req.CertificateArn))
		return
	}
	if err := h.store.deleteCert(r.Context(), req.CertificateArn); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}
