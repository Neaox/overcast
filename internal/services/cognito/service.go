// Package cognito provides emulation of Amazon Cognito User Pools (IDP).
// See docs/services/cognito.md for the support matrix.
//
// Wire protocol: JSON (X-Amz-Target: AWSCognitoIdentityProviderService.*)
package cognito

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/smtp"
	"github.com/Neaox/overcast/internal/state"
)

const (
	serviceName     = "cognito"
	targetPrefix    = "AWSCognitoIdentityProviderService."
	nsPools         = "cognito:pools"
	nsUsersPrefix   = "cognito:users:"
	nsClientsPrefix = "cognito:clients:"
	nsClientLookup  = "cognito:clients:lookup"
	nsTokens        = "cognito:tokens"
)

// Service implements router.Service and router.TargetDispatcher for Cognito User Pools.
type Service struct {
	log       *serviceutil.ServiceLogger
	store     state.Store
	cfg       *config.Config
	clk       clock.Clock
	bus       *events.Bus
	mailer    smtp.Mailer    // nil when email delivery is not configured
	smsSender smtp.SMSSender // nil when SMS capture is not configured
	emailWg   sync.WaitGroup
	typedOp   map[string]op.Operation
}

// New returns a configured Cognito Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		log:   serviceutil.NewServiceLogger(logger, serviceName),
		store: store,
		cfg:   cfg,
		clk:   clk,
	}
	s.typedOp = s.typedOps()
	return s
}

// InitEmailDelivery wires the SMTP mailer for verification and temp-password emails.
// Call this after the router has constructed the mailer.
func (s *Service) InitEmailDelivery(m smtp.Mailer) { s.mailer = m }

// InitSMSDelivery wires the SMS sender so verification and MFA codes sent via
// SMS are captured in the inbox. Call this after the router builds the SMS sender.
func (s *Service) InitSMSDelivery(ss smtp.SMSSender) { s.smsSender = ss }

// InitBus wires the event bus for resource lifecycle events.
func (s *Service) InitBus(bus *events.Bus) { s.bus = bus }

// publish emits a lifecycle event to the event bus (if wired).
func (s *Service) publish(r *http.Request, t events.Type, payload any) {
	if s.bus != nil {
		s.bus.Publish(r.Context(), events.Event{
			Type:    t,
			Source:  serviceName,
			Payload: payload,
		})
	}
}

// Shutdown waits for any in-flight async email goroutines to finish.
func (s *Service) Shutdown() { s.emailWg.Wait() }

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. Registers the JWKS discovery endpoint,
// OIDC discovery, and managed login (OAuth2) routes.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Post("/_overcast/cognito/user-pools/{poolId}/import-users", s.handleImportUsers)

	r.Get("/{region}/{poolId}/.well-known/jwks.json", s.serveJWKS)
	r.Get("/{region}/{poolId}/.well-known/openid-configuration", s.HandleOIDCDiscovery)

	// Managed login OAuth2/OIDC endpoints (emulator convenience routes).
	// These routes are hit by plain browser requests with no SigV4 headers,
	// so the Region middleware cannot extract a region. The poolRegion
	// middleware parses the region from the Cognito pool ID ("{region}_{id}")
	// and injects it into the request context so store lookups use the
	// correct region-scoped key.
	r.Route("/_cognito/{poolId}", func(sub chi.Router) {
		sub.Use(s.poolRegionMiddleware)
		sub.Get("/oauth2/authorize", s.HandleAuthorize)
		sub.Post("/oauth2/token", s.HandleToken)
		sub.Get("/oauth2/userInfo", s.HandleUserInfo)
		sub.Post("/oauth2/userInfo", s.HandleUserInfo)
		sub.Post("/oauth2/revoke", s.HandleRevoke)

		sub.Get("/login", s.HandleLoginPage)
		sub.Post("/login", s.HandleLoginSubmit)
		sub.Get("/logout", s.HandleLogout)
		sub.Get("/signup", s.HandleSignUpPage)
		sub.Post("/signup", s.HandleSignUpSubmit)
		sub.Get("/confirm", s.HandleConfirmPage)
		sub.Post("/confirm", s.HandleConfirmSubmit)
		sub.Get("/new-password", s.HandleNewPasswordPage)
		sub.Post("/new-password", s.HandleNewPasswordSubmit)
		sub.Get("/mfa", s.HandleMFAPage)
		sub.Post("/mfa", s.HandleMFASubmit)
		sub.Get("/forgot-password", s.HandleForgotPasswordPage)
		sub.Post("/forgot-password", s.HandleForgotPasswordSubmit)
		sub.Get("/reset-password", s.HandleResetPasswordPage)
		sub.Post("/reset-password", s.HandleResetPasswordSubmit)

		// Emulator-only: debug token inspector and plaintext password retrieval.
		sub.Get("/debug/token", s.HandleDebugToken)
		sub.Get("/users/{username}/password", s.HandleGetPassword)

		// Emulator-only: managed login branding.
		sub.Get("/branding", s.HandleGetBranding)
		sub.Put("/branding", s.HandleSetBranding)
	})
}

// TargetPrefix satisfies router.TargetDispatcher.
func (s *Service) TargetPrefix() string { return targetPrefix }

// Dispatch satisfies router.TargetDispatcher.
func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "Cognito does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if c.Name() != codec.NameRPCv2CBOR {
			s.dispatchLegacy(w, r, opName)
			return
		}
		if typed, ok := s.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}

	suffix := r.Header.Get("X-Amz-Target")[len(targetPrefix):]
	s.dispatchLegacy(w, r, suffix)
}

func (s *Service) dispatchLegacy(w http.ResponseWriter, r *http.Request, suffix string) {
	switch suffix {
	// ── Pool management ───────────────────────────────────────────────────────
	case "CreateUserPool":
		s.createUserPool(w, r)
	case "DescribeUserPool":
		s.describeUserPool(w, r)
	case "DeleteUserPool":
		s.deleteUserPool(w, r)
	case "ListUserPools":
		s.listUserPools(w, r)
	case "UpdateUserPool":
		s.updateUserPool(w, r)
	case "SetUserPoolMfaConfig":
		s.setUserPoolMfaConfig(w, r)
	case "GetUserPoolMfaConfig":
		s.getUserPoolMfaConfig(w, r)
	// ── Domain management ─────────────────────────────────────────────────────
	case "CreateUserPoolDomain":
		s.createUserPoolDomain(w, r)
	case "DescribeUserPoolDomain":
		s.describeUserPoolDomain(w, r)
	case "DeleteUserPoolDomain":
		s.deleteUserPoolDomain(w, r)
	case "UpdateUserPoolDomain":
		s.updateUserPoolDomain(w, r)
	// ── Pool client management ────────────────────────────────────────────────
	case "CreateUserPoolClient":
		s.createUserPoolClient(w, r)
	case "DescribeUserPoolClient":
		s.describeUserPoolClient(w, r)
	case "DeleteUserPoolClient":
		s.deleteUserPoolClient(w, r)
	case "ListUserPoolClients":
		s.listUserPoolClients(w, r)
	case "UpdateUserPoolClient":
		s.updateUserPoolClient(w, r)
	// ── Admin user management ─────────────────────────────────────────────────
	case "AdminCreateUser":
		s.adminCreateUser(w, r)
	case "AdminDeleteUser":
		s.adminDeleteUser(w, r)
	case "AdminGetUser":
		s.adminGetUser(w, r)
	case "AdminSetUserPassword":
		s.adminSetUserPassword(w, r)
	case "AdminConfirmSignUp":
		s.adminConfirmSignUp(w, r)
	case "AdminUpdateUserAttributes":
		s.adminUpdateUserAttributes(w, r)
	case "AdminDeleteUserAttributes":
		s.adminDeleteUserAttributes(w, r)
	case "AdminDisableUser":
		s.adminDisableUser(w, r)
	case "AdminEnableUser":
		s.adminEnableUser(w, r)
	case "AdminInitiateAuth":
		s.adminInitiateAuth(w, r)
	case "AdminRespondToAuthChallenge":
		s.adminRespondToAuthChallenge(w, r)
	case "ListUsers":
		s.listUsers(w, r)
	// ── Self-service sign-up ──────────────────────────────────────────────────
	case "SignUp":
		s.signUp(w, r)
	case "ConfirmSignUp":
		s.confirmSignUp(w, r)
	case "ResendConfirmationCode":
		s.resendConfirmationCode(w, r)
	// ── Auth flows ────────────────────────────────────────────────────────────
	case "InitiateAuth":
		s.initiateAuth(w, r)
	case "RespondToAuthChallenge":
		s.respondToAuthChallenge(w, r)
	case "ConfirmDevice":
		s.confirmDevice(w, r)
	case "GetDevice":
		s.getDevice(w, r)
	case "ListDevices":
		s.listDevices(w, r)
	case "UpdateDeviceStatus":
		s.updateDeviceStatus(w, r)
	case "ForgetDevice":
		s.forgetDevice(w, r)
	case "AdminGetDevice":
		s.adminGetDevice(w, r)
	case "AdminListDevices":
		s.adminListDevices(w, r)
	case "AdminUpdateDeviceStatus":
		s.adminUpdateDeviceStatus(w, r)
	case "AdminForgetDevice":
		s.adminForgetDevice(w, r)
	// ── Password management ───────────────────────────────────────────────────
	case "ForgotPassword":
		s.forgotPassword(w, r)
	case "ConfirmForgotPassword":
		s.confirmForgotPassword(w, r)
	case "ChangePassword":
		s.changePassword(w, r)
	// ── MFA ─────────────────────────────────────────────────────────────────
	case "AssociateSoftwareToken":
		s.associateSoftwareToken(w, r)
	case "VerifySoftwareToken":
		s.verifySoftwareToken(w, r)
	case "StartWebAuthnRegistration":
		s.startWebAuthnRegistration(w, r)
	case "CompleteWebAuthnRegistration":
		s.completeWebAuthnRegistration(w, r)
	case "SetUserMFAPreference":
		s.setUserMFAPreference(w, r)
	case "AdminSetUserMFAPreference":
		s.adminSetUserMFAPreference(w, r)
	// ── Group management ─────────────────────────────────────────────────────
	case "CreateGroup":
		s.createGroup(w, r)
	case "GetGroup":
		s.getGroup(w, r)
	case "DeleteGroup":
		s.deleteGroup(w, r)
	case "UpdateGroup":
		s.updateGroup(w, r)
	case "ListGroups":
		s.listGroups(w, r)
	case "AdminAddUserToGroup":
		s.adminAddUserToGroup(w, r)
	case "AdminRemoveUserFromGroup":
		s.adminRemoveUserFromGroup(w, r)
	case "AdminListGroupsForUser":
		s.adminListGroupsForUser(w, r)
	case "ListUsersInGroup":
		s.listUsersInGroup(w, r)
	// ── Token / user info ─────────────────────────────────────────────────────
	case "GetUser":
		s.getUser(w, r)
	case "UpdateUserAttributes":
		s.updateUserAttributes(w, r)
	case "VerifyUserAttribute":
		s.verifyUserAttribute(w, r)
	case "GetUserAttributeVerificationCode":
		s.getUserAttributeVerificationCode(w, r)
	case "DeleteUserAttributes":
		s.deleteUserAttributes(w, r)
	case "GlobalSignOut":
		s.globalSignOut(w, r)
	case "RevokeToken":
		s.revokeToken(w, r)
	default:
		protocol.NotImplementedJSON(w, r)
	}
}

// writeJSON serialises v to JSON and writes it with the Cognito content-type and
// the given HTTP status code.
func (s *Service) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
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
