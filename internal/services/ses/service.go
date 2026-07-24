// Package ses provides emulation of Amazon Simple Email Service (SES v1).
//
// Both AWS SDK v1 Query protocol clients (aws-sdk-go-v2/service/ses,
// boto3 ses, @aws-sdk/client-ses) and SES v2 REST-JSON clients
// (aws-sdk-go-v2/service/sesv2, boto3 sesv2, @aws-sdk/client-sesv2)
// are supported.
//
// SES v1 (Query protocol) implemented operations:
//   - SendEmail, SendRawEmail
//   - VerifyEmailIdentity, VerifyDomainIdentity
//   - ListIdentities, ListVerifiedEmailAddresses
//   - GetIdentityVerificationAttributes
//   - DeleteIdentity
//   - GetSendQuota
//
// SES v2 (REST-JSON) implemented operations:
//   - POST /v2/email/outbound-emails (SendEmail)
//   - PUT  /v2/email/identities
//   - GET  /v2/email/identities
//   - GET  /v2/email/identities/{EmailIdentity}
//   - DELETE /v2/email/identities/{EmailIdentity}
//
// Admin endpoints (for web console):
//
//	GET  /_overcast/ses/identities
//	DELETE /_overcast/ses/identities/{identity}
package ses

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/smtp"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "ses"

// Service implements router.Service for SES.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured SES Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:     cfg,
		store:   store,
		log:     log,
		handler: newHandler(cfg, store, log, clk),
	}
}

// InitBus wires the event bus for SES lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// InitEmailDelivery wires the SMTP mailer so that SendEmail and SendRawEmail
// deliver messages. Call this after the router builds the mailer.
func (s *Service) InitEmailDelivery(m smtp.Mailer) {
	s.handler.setMailer(m)
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes mounts the SES v2 REST-JSON endpoints and admin routes.
func (s *Service) RegisterRoutes(r chi.Router) {
	// SES v2 (REST-JSON) — path-based routing.
	r.Post("/v2/email/outbound-emails", s.handler.V2SendEmail)
	r.Put("/v2/email/identities", s.handler.V2CreateEmailIdentity)
	r.Get("/v2/email/identities", s.handler.V2ListEmailIdentities)
	r.Get("/v2/email/identities/{EmailIdentity}", s.handler.V2GetEmailIdentity)
	r.Delete("/v2/email/identities/{EmailIdentity}", s.handler.V2DeleteEmailIdentity)

	// Admin endpoints for the web console.
	r.Get("/_overcast/ses/identities", s.adminListIdentities)
	r.Post("/_overcast/ses/identities", s.adminCreateIdentity)
	r.Delete("/_overcast/ses/identities/{identity}", s.adminDeleteIdentity)
}

// DispatchQuery satisfies router.QueryDispatcher.
// SES v1 uses the AWS Query protocol: form-encoded POST body with Action field,
// XML responses. The router calls r.ParseForm() before invoking this method.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !serviceutil.AllowProtocolDrift(s.cfg, s.log, opName, c, s.SupportedProtocols()) {
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "SES does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		// No typed impl for this op — fall through to legacy dispatch below.
	}
	s.handler.dispatch(w, r)
}

// OwnsAction satisfies router.QueryActionOwner.
// Returns true for every v1 Action this service handles so the router does not
// try this dispatcher for SNS actions.
func (s *Service) OwnsAction(action string) bool {
	return s.handler.ownsAction(action)
}

// ─── Admin handlers ─────────────────────────────────────────────────────────

// adminListIdentities returns all verified identities for the web console.
func (s *Service) adminListIdentities(w http.ResponseWriter, r *http.Request) {
	identities, aerr := s.handler.sesStore.listIdentities(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	type identityOut struct {
		Identity string `json:"identity"`
		Type     string `json:"type"`
	}
	out := make([]identityOut, 0, len(identities))
	for _, id := range identities {
		out = append(out, identityOut{Identity: id.Identity, Type: id.Type})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"identities": out})
}

// adminCreateIdentity adds a verified identity via the web console.
func (s *Service) adminCreateIdentity(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Identity string `json:"identity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Identity == "" {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("identity is required"))
		return
	}
	idType := "domain"
	if strings.Contains(req.Identity, "@") {
		idType = "email"
	}
	v := &VerifiedIdentity{
		Identity:  req.Identity,
		Type:      idType,
		CreatedAt: s.handler.clk.Now(),
	}
	if aerr := s.handler.sesStore.putIdentity(r.Context(), v); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"identity": v.Identity, "type": v.Type})
}

// adminDeleteIdentity removes a verified identity via the web console.
func (s *Service) adminDeleteIdentity(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "identity")
	identity, err := url.PathUnescape(raw)
	if err != nil {
		identity = raw
	}
	if aerr := s.handler.sesStore.deleteIdentity(r.Context(), identity); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
