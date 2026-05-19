package kms

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// CreateGrant creates a grant for a KMS key.
func (h *Handler) CreateGrant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyId             string           `json:"KeyId"`
		GranteePrincipal  string           `json:"GranteePrincipal"`
		RetiringPrincipal string           `json:"RetiringPrincipal"`
		Operations        []string         `json:"Operations"`
		Constraints       *GrantConstraint `json:"Constraints"`
		GrantTokens       []string         `json:"GrantTokens"`
		Name              string           `json:"Name"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	k, err := h.resolveKey(ctx, req.KeyId)
	if err != nil || k == nil {
		if k == nil {
			protocol.WriteJSONError(w, r, errNotFound(req.KeyId))
			return
		}
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	grantID := uuid.NewString()
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	grantToken := hex.EncodeToString(tokenBytes)

	g := &Grant{
		GrantID:           grantID,
		GrantToken:        grantToken,
		KeyID:             k.KeyID,
		GranteePrincipal:  req.GranteePrincipal,
		RetiringPrincipal: req.RetiringPrincipal,
		Operations:        req.Operations,
		Constraints:       req.Constraints,
		Name:              req.Name,
		CreationDate:      h.clk.Now(),
	}
	if err := h.store.PutGrant(ctx, g); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"GrantId":    grantID,
		"GrantToken": grantToken,
	})
}

// ListGrants lists grants for a KMS key.
func (h *Handler) ListGrants(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyId            string `json:"KeyId"`
		GrantId          string `json:"GrantId"`
		GranteePrincipal string `json:"GranteePrincipal"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	k, err := h.resolveKey(ctx, req.KeyId)
	if err != nil || k == nil {
		if k == nil {
			protocol.WriteJSONError(w, r, errNotFound(req.KeyId))
			return
		}
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	grants, err := h.store.ScanGrantsByKey(ctx, k.KeyID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if req.GrantId != "" {
		filtered := grants[:0]
		for _, g := range grants {
			if g.GrantID == req.GrantId {
				filtered = append(filtered, g)
			}
		}
		grants = filtered
	}
	if req.GranteePrincipal != "" {
		filtered := grants[:0]
		for _, g := range grants {
			if g.GranteePrincipal == req.GranteePrincipal {
				filtered = append(filtered, g)
			}
		}
		grants = filtered
	}

	type grantEntry struct {
		GrantID           string           `json:"GrantId"`
		GranteePrincipal  string           `json:"GranteePrincipal"`
		RetiringPrincipal string           `json:"RetiringPrincipal,omitempty"`
		Operations        []string         `json:"Operations"`
		Constraints       *GrantConstraint `json:"Constraints,omitempty"`
		Name              string           `json:"Name,omitempty"`
		CreationDate      float64          `json:"CreationDate"`
		KeyId             string           `json:"KeyId"`
	}
	entries := make([]grantEntry, 0, len(grants))
	for _, g := range grants {
		entries = append(entries, grantEntry{
			GrantID:           g.GrantID,
			GranteePrincipal:  g.GranteePrincipal,
			RetiringPrincipal: g.RetiringPrincipal,
			Operations:        g.Operations,
			Constraints:       g.Constraints,
			Name:              g.Name,
			CreationDate:      float64(g.CreationDate.UnixMilli()) / 1000.0,
			KeyId:             k.ARN,
		})
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"Grants":    entries,
		"Truncated": false,
	})
}

// RevokeGrant revokes a grant from a KMS key.
func (h *Handler) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyId   string `json:"KeyId"`
		GrantId string `json:"GrantId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	k, err := h.resolveKey(ctx, req.KeyId)
	if err != nil || k == nil {
		if k == nil {
			protocol.WriteJSONError(w, r, errNotFound(req.KeyId))
			return
		}
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	g, err := h.store.GetGrant(ctx, req.GrantId)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if g == nil || g.KeyID != k.KeyID {
		protocol.WriteJSONError(w, r, errNotFound(req.GrantId))
		return
	}
	if err := h.store.DeleteGrant(ctx, req.GrantId); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// RetireGrant retires a grant. The grantId must match the retiring principal.
func (h *Handler) RetireGrant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyId      string `json:"KeyId"`
		GrantId    string `json:"GrantId"`
		GrantToken string `json:"GrantToken"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	if req.GrantToken != "" {
		grants, err := h.store.ScanAllGrants(ctx)
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.ErrInternalError)
			return
		}
		found := false
		for _, g := range grants {
			if g.GrantToken == req.GrantToken {
				req.GrantId = g.GrantID
				found = true
				break
			}
		}
		if !found {
			protocol.WriteJSONError(w, r, errNotFound(req.GrantToken))
			return
		}
	}
	g, err := h.store.GetGrant(ctx, req.GrantId)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if g == nil {
		protocol.WriteJSONError(w, r, errNotFound(req.GrantId))
		return
	}
	if err := h.store.DeleteGrant(ctx, req.GrantId); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// ListRetirableGrants lists grants that the specified principal can retire.
func (h *Handler) ListRetirableGrants(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RetiringPrincipal string `json:"RetiringPrincipal"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	grants, err := h.store.ScanGrantsByPrincipal(ctx, req.RetiringPrincipal)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	type retirableEntry struct {
		GrantID          string   `json:"GrantId"`
		GranteePrincipal string   `json:"GranteePrincipal"`
		Operations       []string `json:"Operations"`
		CreationDate     float64  `json:"CreationDate"`
		Name             string   `json:"Name,omitempty"`
	}
	entries := make([]retirableEntry, 0, len(grants))
	for _, g := range grants {
		entries = append(entries, retirableEntry{
			GrantID:          g.GrantID,
			GranteePrincipal: g.GranteePrincipal,
			Operations:       g.Operations,
			CreationDate:     float64(g.CreationDate.UnixMilli()) / 1000.0,
			Name:             g.Name,
		})
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"Grants":    entries,
		"Truncated": false,
	})
}
