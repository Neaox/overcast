package waf

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Handler holds WAF handler dependencies.
type Handler struct {
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
	store   state.Store
	cfg     *config.Config
	clk     clock.Clock
	bus     *events.Bus
}

func (h *Handler) publish(ctx context.Context, eventType events.Type, acl *WebACL) {
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    eventType,
			Payload: events.ResourcePayload{Name: acl.Name, ARN: acl.ARN},
		})
	}
}

func newHandler(cfg *config.Config, store state.Store, clk clock.Clock) *Handler {
	h := &Handler{cfg: cfg, store: store, clk: clk}
	h.initOps()
	return h
}

func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"CreateWebACL":        h.createWebACL,
		"GetWebACL":           h.getWebACL,
		"ListWebACLs":         h.listWebACLs,
		"DeleteWebACL":        h.deleteWebACL,
		"TagResource":         h.tagResource,
		"UntagResource":       h.untagResource,
		"ListTagsForResource": h.listTagsForResource,
	}
	h.typedOp = h.typedOps()
}

func (h *Handler) storeKey(scope, id string) string {
	return serviceutil.RegionKey(h.cfg.Region, scope+"/"+id)
}

func (h *Handler) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("x-amzn-requestid", protocol.RequestIDFromContext(r.Context()))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Handler) wafARN(ctx context.Context, scope, id string) string {
	rtype := "regional/webacl"
	if scope == "CLOUDFRONT" {
		rtype = "global/webacl"
	}
	return fmt.Sprintf("arn:aws:wafv2:%s:%s:%s/%s", middleware.RegionFromContext(ctx, h.cfg.Region), h.cfg.AccountID, rtype, id)
}

func (h *Handler) getACL(ctx context.Context, scope, id string) (*WebACL, *protocol.AWSError) {
	raw, found, err := h.store.Get(ctx, nsWebACLs, h.storeKey(scope, id))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, &protocol.AWSError{
			Code:       "WAFNonexistentItemException",
			Message:    fmt.Sprintf("WebACL %s not found", id),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	var acl WebACL
	if err := json.Unmarshal([]byte(raw), &acl); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &acl, nil
}

func (h *Handler) createWebACL(w http.ResponseWriter, r *http.Request) {
	// Real WAFv2 sends Tags as a LIST of {Key,Value} structs, never a map.
	var req struct {
		Name             string                `json:"Name"`
		Scope            string                `json:"Scope"`
		Description      string                `json:"Description"`
		DefaultAction    map[string]any        `json:"DefaultAction"`
		VisibilityConfig map[string]any        `json:"VisibilityConfig"`
		Rules            []any                 `json:"Rules"`
		Tags             []serviceutil.TagPair `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.Name, "Name") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Scope, "Scope") {
		return
	}
	tags := serviceutil.TagsFromList(req.Tags)
	if aerr := serviceutil.ValidateTags(wafTagCfg, tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	id := generateID()
	token := generateID()
	acl := &WebACL{
		ID:               id,
		Name:             req.Name,
		Scope:            req.Scope,
		ARN:              h.wafARN(r.Context(), req.Scope, id),
		LockToken:        token,
		Description:      req.Description,
		DefaultAction:    req.DefaultAction,
		VisibilityConfig: req.VisibilityConfig,
		Rules:            req.Rules,
		Tags:             tags,
		CreatedAt:        h.clk.Now(),
	}

	raw, _ := json.Marshal(acl)
	if err := h.store.Set(r.Context(), nsWebACLs, h.storeKey(req.Scope, id), string(raw)); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	h.publish(r.Context(), events.WAFWebACLCreated, acl)

	h.writeJSON(w, r, http.StatusOK, map[string]any{
		"Summary": map[string]any{
			"Id":          acl.ID,
			"Name":        acl.Name,
			"Description": acl.Description,
			"LockToken":   acl.LockToken,
			"ARN":         acl.ARN,
		},
	})
}

func (h *Handler) getWebACL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		Scope string `json:"Scope"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	acl, aerr := h.getACL(r.Context(), req.Scope, req.ID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.writeJSON(w, r, http.StatusOK, map[string]any{
		"WebACL": map[string]any{
			"Id":               acl.ID,
			"Name":             acl.Name,
			"ARN":              acl.ARN,
			"Description":      acl.Description,
			"DefaultAction":    acl.DefaultAction,
			"VisibilityConfig": acl.VisibilityConfig,
			"Rules":            acl.Rules,
		},
		"LockToken": acl.LockToken,
	})
}

func (h *Handler) listWebACLs(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope string `json:"Scope"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	prefix := serviceutil.RegionKey(h.cfg.Region, req.Scope+"/")
	pairs, err := h.store.Scan(r.Context(), nsWebACLs, prefix)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	summaries := make([]map[string]any, 0, len(pairs))
	for _, kv := range pairs {
		var acl WebACL
		if json.Unmarshal([]byte(kv.Value), &acl) != nil {
			continue
		}
		summaries = append(summaries, map[string]any{
			"Id":          acl.ID,
			"Name":        acl.Name,
			"Description": acl.Description,
			"LockToken":   acl.LockToken,
			"ARN":         acl.ARN,
		})
	}

	h.writeJSON(w, r, http.StatusOK, map[string]any{"WebACLs": summaries})
}

func (h *Handler) deleteWebACL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        string `json:"Id"`
		Name      string `json:"Name"`
		Scope     string `json:"Scope"`
		LockToken string `json:"LockToken"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	acl, aerr := h.getACL(r.Context(), req.Scope, req.ID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if err := h.store.Delete(r.Context(), nsWebACLs, h.storeKey(req.Scope, req.ID)); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	h.publish(r.Context(), events.WAFWebACLDeleted, acl)

	h.writeJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) parseWAFARN(arn string) (scope, id string, aerr *protocol.AWSError) {
	if !strings.HasPrefix(arn, "arn:aws:wafv2:") {
		return "", "", &protocol.AWSError{
			Code: "WAFInvalidParameterException", Message: "Invalid ARN", HTTPStatus: http.StatusBadRequest,
		}
	}
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return "", "", &protocol.AWSError{
			Code: "WAFInvalidParameterException", Message: "Invalid ARN", HTTPStatus: http.StatusBadRequest,
		}
	}
	resource := parts[5]
	idx := strings.LastIndex(resource, "/")
	if idx < 0 {
		return "", "", &protocol.AWSError{
			Code: "WAFInvalidParameterException", Message: "Invalid ARN", HTTPStatus: http.StatusBadRequest,
		}
	}
	resourceType := resource[:idx]
	id = resource[idx+1:]
	scope = "REGIONAL"
	if resourceType == "global/webacl" {
		scope = "CLOUDFRONT"
	}
	if resourceType != "regional/webacl" && resourceType != "global/webacl" {
		return "", "", &protocol.AWSError{
			Code: "WAFInvalidParameterException", Message: "Unsupported resource type in ARN", HTTPStatus: http.StatusBadRequest,
		}
	}
	return scope, id, nil
}

var wafTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "WAFLimitsExceededException",
	InvalidCode:     "WAFInvalidParameterException",
	ExceededMessage: "Tag key list exceeds maximum tag limit",
}

func (h *Handler) putACL(ctx context.Context, acl *WebACL) *protocol.AWSError {
	raw, _ := json.Marshal(acl)
	if err := h.store.Set(ctx, nsWebACLs, h.storeKey(acl.Scope, acl.ID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	// Real WAFv2 sends Tags as a LIST of {Key,Value} structs, never a map.
	var req struct {
		ResourceARN string                `json:"ResourceARN"`
		Tags        []serviceutil.TagPair `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	scope, id, aerr := h.parseWAFARN(req.ResourceARN)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	ctx := r.Context()
	getter := func(ctx context.Context, _ string) (*WebACL, *protocol.AWSError) {
		return h.getACL(ctx, scope, id)
	}
	if aerr := serviceutil.ApplyTags(ctx, wafTagCfg, scope+"/"+id, req.Tags, getter, h.putACL); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	h.writeJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string   `json:"ResourceARN"`
		TagKeys     []string `json:"TagKeys"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	scope, id, aerr := h.parseWAFARN(req.ResourceARN)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	ctx := r.Context()
	getter := func(ctx context.Context, _ string) (*WebACL, *protocol.AWSError) {
		return h.getACL(ctx, scope, id)
	}
	if aerr := serviceutil.RemoveTags(ctx, scope+"/"+id, req.TagKeys, getter, h.putACL); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	h.writeJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string `json:"ResourceARN"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	scope, id, aerr := h.parseWAFARN(req.ResourceARN)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	ctx := r.Context()
	acl, aerr := h.getACL(ctx, scope, id)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	// Real WAFv2 returns TagList as a LIST of Tag structs, never a map.
	h.writeJSON(w, r, http.StatusOK, map[string]any{"TagInfoForResource": map[string]any{
		"ResourceARN": req.ResourceARN,
		"TagList":     serviceutil.TagsToList(acl.Tags),
	}})
}
