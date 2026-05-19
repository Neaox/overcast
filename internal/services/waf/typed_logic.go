package waf

import (
	"context"
	"encoding/json"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

type createWebACLRequest struct {
	Name             string            `json:"Name"`
	Scope            string            `json:"Scope"`
	Description      string            `json:"Description"`
	DefaultAction    map[string]any    `json:"DefaultAction"`
	VisibilityConfig map[string]any    `json:"VisibilityConfig"`
	Rules            []any             `json:"Rules"`
	Tags             map[string]string `json:"Tags"`
}

type createWebACLResponse struct {
	Summary webACLSummaryWire `json:"Summary"`
}

type webACLSummaryWire struct {
	Id          string            `json:"Id"`
	Name        string            `json:"Name"`
	Description string            `json:"Description"`
	LockToken   string            `json:"LockToken"`
	ARN         string            `json:"ARN"`
	Tags        map[string]string `json:"Tags,omitempty"`
}

type getWebACLRequest struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	Scope string `json:"Scope"`
}

type getWebACLResponse struct {
	WebACL    webACLWire `json:"WebACL"`
	LockToken string     `json:"LockToken"`
}

type webACLWire struct {
	Id               string         `json:"Id"`
	Name             string         `json:"Name"`
	ARN              string         `json:"ARN"`
	Description      string         `json:"Description"`
	DefaultAction    map[string]any `json:"DefaultAction"`
	VisibilityConfig map[string]any `json:"VisibilityConfig"`
	Rules            []any          `json:"Rules"`
}

type listWebACLsRequest struct {
	Scope string `json:"Scope"`
}

type listWebACLsResponse struct {
	WebACLs []webACLSummaryWire `json:"WebACLs"`
}

type deleteWebACLRequest struct {
	ID        string `json:"Id"`
	Name      string `json:"Name"`
	Scope     string `json:"Scope"`
	LockToken string `json:"LockToken"`
}

func (h *Handler) createWebACLTyped(ctx context.Context, req *createWebACLRequest) (*createWebACLResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, protocol.ErrMissingParameter("Name")
	}
	if req.Scope == "" {
		return nil, protocol.ErrMissingParameter("Scope")
	}

	id := generateID()
	token := generateID()
	acl := &WebACL{
		ID:               id,
		Name:             req.Name,
		Scope:            req.Scope,
		ARN:              h.wafARN(ctx, req.Scope, id),
		LockToken:        token,
		Description:      req.Description,
		DefaultAction:    req.DefaultAction,
		VisibilityConfig: req.VisibilityConfig,
		Rules:            req.Rules,
		Tags:             req.Tags,
		CreatedAt:        h.clk.Now(),
	}

	raw, _ := json.Marshal(acl)
	if err := h.store.Set(ctx, nsWebACLs, h.storeKey(req.Scope, id), string(raw)); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}

	return &createWebACLResponse{
		Summary: webACLSummaryWire{
			Id:          acl.ID,
			Name:        acl.Name,
			Description: acl.Description,
			LockToken:   acl.LockToken,
			ARN:         acl.ARN,
		},
	}, nil
}

func (h *Handler) getWebACLTyped(ctx context.Context, req *getWebACLRequest) (*getWebACLResponse, *protocol.AWSError) {
	acl, aerr := h.getACL(ctx, req.Scope, req.ID)
	if aerr != nil {
		return nil, aerr
	}

	return &getWebACLResponse{
		WebACL: webACLWire{
			Id:               acl.ID,
			Name:             acl.Name,
			ARN:              acl.ARN,
			Description:      acl.Description,
			DefaultAction:    acl.DefaultAction,
			VisibilityConfig: acl.VisibilityConfig,
			Rules:            acl.Rules,
		},
		LockToken: acl.LockToken,
	}, nil
}

func (h *Handler) listWebACLsTyped(ctx context.Context, req *listWebACLsRequest) (*listWebACLsResponse, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(h.cfg.Region, req.Scope+"/")
	pairs, err := h.store.Scan(ctx, nsWebACLs, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}

	summaries := make([]webACLSummaryWire, 0, len(pairs))
	for _, kv := range pairs {
		var acl WebACL
		if json.Unmarshal([]byte(kv.Value), &acl) != nil {
			continue
		}
		summaries = append(summaries, webACLSummaryWire{
			Id:          acl.ID,
			Name:        acl.Name,
			Description: acl.Description,
			LockToken:   acl.LockToken,
			ARN:         acl.ARN,
		})
	}

	return &listWebACLsResponse{WebACLs: summaries}, nil
}

func (h *Handler) deleteWebACLTyped(ctx context.Context, req *deleteWebACLRequest) (*struct{}, *protocol.AWSError) {
	if _, aerr := h.getACL(ctx, req.Scope, req.ID); aerr != nil {
		return nil, aerr
	}

	if err := h.store.Delete(ctx, nsWebACLs, h.storeKey(req.Scope, req.ID)); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}

	return &struct{}{}, nil
}
