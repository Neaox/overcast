package secretsmanager

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ─── Stubs (not implemented) ───────────────────────────────────────────────

func (h *Handler) RestoreSecret(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

func (h *Handler) GetResourcePolicy(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

func (h *Handler) PutResourcePolicy(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

func (h *Handler) DeleteResourcePolicy(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

func (h *Handler) ReplicateSecretToRegions(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

func (h *Handler) RemoveRegionsFromReplication(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

func (h *Handler) ValidateResourcePolicy(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}
