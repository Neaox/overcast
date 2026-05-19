package ses

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsIdentities = "ses:identities"
	nsTemplates  = "ses:templates"
)

// VerifiedIdentity represents a stored SES verified identity (email or domain).
type VerifiedIdentity struct {
	Identity  string    `json:"identity"`
	Type      string    `json:"type"` // "email" or "domain"
	Token     string    `json:"token,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Template represents an SES email template.
type Template struct {
	TemplateName string    `json:"template_name"`
	SubjectPart  string    `json:"subject_part,omitempty"`
	TextPart     string    `json:"text_part,omitempty"`
	HtmlPart     string    `json:"html_part,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// sesStore wraps state.Store with SES-specific access helpers.
type sesStore struct {
	store         state.Store
	clk           clock.Clock
	defaultRegion string
}

func newSESStore(store state.Store, clk clock.Clock, defaultRegion string) *sesStore {
	return &sesStore{store: store, clk: clk, defaultRegion: defaultRegion}
}

// region extracts the per-request region from context, falling back to the default.
func (s *sesStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

// ─── Identities ──────────────────────────────────────────────────────────────

func (s *sesStore) getIdentity(ctx context.Context, identity string) (*VerifiedIdentity, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsIdentities, serviceutil.RegionKey(s.region(ctx), strings.ToLower(identity)))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errIdentityNotFound(identity)
	}
	var v VerifiedIdentity
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &v, nil
}

func (s *sesStore) putIdentity(ctx context.Context, v *VerifiedIdentity) *protocol.AWSError {
	raw, err := json.Marshal(v)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsIdentities, serviceutil.RegionKey(s.region(ctx), strings.ToLower(v.Identity)), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *sesStore) listIdentities(ctx context.Context) ([]VerifiedIdentity, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsIdentities, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]VerifiedIdentity, 0, len(pairs))
	for _, p := range pairs {
		var v VerifiedIdentity
		if err := json.Unmarshal([]byte(p.Value), &v); err != nil {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *sesStore) deleteIdentity(ctx context.Context, identity string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsIdentities, serviceutil.RegionKey(s.region(ctx), strings.ToLower(identity))); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ─── Templates ───────────────────────────────────────────────────────────────

func (s *sesStore) getTemplate(ctx context.Context, name string) (*Template, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsTemplates, serviceutil.RegionKey(s.region(ctx), name))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errTemplateNotFound(name)
	}
	var t Template
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &t, nil
}

func (s *sesStore) putTemplate(ctx context.Context, t *Template) *protocol.AWSError {
	raw, err := json.Marshal(t)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsTemplates, serviceutil.RegionKey(s.region(ctx), t.TemplateName), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *sesStore) listTemplates(ctx context.Context) ([]Template, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsTemplates, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]Template, 0, len(pairs))
	for _, p := range pairs {
		var t Template
		if err := json.Unmarshal([]byte(p.Value), &t); err != nil {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *sesStore) deleteTemplate(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsTemplates, serviceutil.RegionKey(s.region(ctx), name)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ─── Errors ──────────────────────────────────────────────────────────────────

func errIdentityNotFound(identity string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NotFoundException",
		Message:    fmt.Sprintf("Identity not found: %s", identity),
		HTTPStatus: 404,
	}
}

func errTemplateNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "TemplateDoesNotExist",
		Message:    fmt.Sprintf("Template %s does not exist.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errTemplateAlreadyExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "AlreadyExists",
		Message:    fmt.Sprintf("Template %s already exists.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

var (
	errInvalidParam = &protocol.AWSError{
		Code:       "InvalidParameterValue",
		Message:    "One or more parameters are invalid.",
		HTTPStatus: 400,
	}
)
