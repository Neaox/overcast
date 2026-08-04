package rds

// handler_instances.go — the raw Query entry points for the instance
// stop/start/modify operations.
//
// These used to be full second implementations, reachable whenever
// Service.DispatchQuery found no codec in context and fell back to
// h.dispatch. Two implementations of the same operation is two chances to be
// wrong, and these had already diverged: the raw ModifyDBInstance honoured
// `MultiAZ=false` and the typed one silently ignored it, so the answer to
// "can I turn Multi-AZ off" depended on which path the request took.
//
// They are adapters now. The behaviour lives once, in typed_logic.go; see
// invokeTypedAsQuery for why the raw registry entry still exists.

import "net/http"

// CreateDBInstance creates a DB instance.
func (h *Handler) CreateDBInstance(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("CreateDBInstance", w, r)
}

// DeleteDBInstance deletes a DB instance.
func (h *Handler) DeleteDBInstance(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DeleteDBInstance", w, r)
}

// StopDBInstance stops a running DB instance.
func (h *Handler) StopDBInstance(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("StopDBInstance", w, r)
}

// StartDBInstance starts a previously stopped DB instance.
func (h *Handler) StartDBInstance(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("StartDBInstance", w, r)
}

// ModifyDBInstance modifies metadata properties of an existing DB instance.
func (h *Handler) ModifyDBInstance(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("ModifyDBInstance", w, r)
}
