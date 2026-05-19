package cognito

// handler_managed_login.go — OAuth2/OIDC endpoints and server-rendered managed login pages.
//
// Implemented:
//   - HandleAuthorize              GET  /_cognito/{poolId}/oauth2/authorize
//   - HandleToken                  POST /_cognito/{poolId}/oauth2/token
//   - HandleUserInfo               GET  /_cognito/{poolId}/oauth2/userInfo
//   - HandleRevoke                 POST /_cognito/{poolId}/oauth2/revoke
//   - HandleLoginPage              GET  /_cognito/{poolId}/login
//   - HandleLoginSubmit            POST /_cognito/{poolId}/login
//   - HandleLogout                 GET  /_cognito/{poolId}/logout
//   - HandleSignUpPage             GET  /_cognito/{poolId}/signup
//   - HandleSignUpSubmit           POST /_cognito/{poolId}/signup
//   - HandleConfirmPage            GET  /_cognito/{poolId}/confirm
//   - HandleConfirmSubmit          POST /_cognito/{poolId}/confirm
//   - HandleNewPasswordPage        GET  /_cognito/{poolId}/new-password
//   - HandleNewPasswordSubmit      POST /_cognito/{poolId}/new-password
//   - HandleMFAPage                GET  /_cognito/{poolId}/mfa
//   - HandleMFASubmit              POST /_cognito/{poolId}/mfa
//   - HandleForgotPasswordPage     GET  /_cognito/{poolId}/forgot-password
//   - HandleForgotPasswordSubmit   POST /_cognito/{poolId}/forgot-password
//   - HandleResetPasswordPage      GET  /_cognito/{poolId}/reset-password
//   - HandleResetPasswordSubmit    POST /_cognito/{poolId}/reset-password
//   - HandleOIDCDiscovery          GET  /{region}/{poolId}/.well-known/openid-configuration
//   - HandleDebugToken             GET  /_cognito/{poolId}/debug/token
//   - HandleGetPassword            GET  /_cognito/{poolId}/users/{username}/password

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ─── template data types ──────────────────────────────────────────────────────

type loginPageData struct {
	PoolName              string
	Branding              ManagedLoginBranding
	Error                 string
	FormAction            string
	ClientID              string
	RedirectURI           string
	ResponseType          string
	Scope                 string
	State                 string
	Nonce                 string
	CodeChallenge         string
	ChallengeMethod       string
	SignUpURL             string
	ForgotPasswordURL     string
	AllowSelfRegistration bool
	UsernameLabel         string
	UsernameInputType     string
	UsernameAutocomplete  string
}

type signUpPageData struct {
	PoolName             string
	Branding             ManagedLoginBranding
	Error                string
	FormAction           string
	ClientID             string
	RedirectURI          string
	ResponseType         string
	Scope                string
	State                string
	LoginURL             string
	UsernameLabel        string
	UsernameInputType    string
	UsernameAutocomplete string
	ShowEmailField       bool
}

type forgotPasswordPageData struct {
	PoolName             string
	Branding             ManagedLoginBranding
	Error                string
	FormAction           string
	ClientID             string
	RedirectURI          string
	ResponseType         string
	Scope                string
	State                string
	LoginURL             string
	UsernameLabel        string
	UsernameInputType    string
	UsernameAutocomplete string
}

type resetPasswordPageData struct {
	PoolName          string
	Branding          ManagedLoginBranding
	Error             string
	FormAction        string
	ClientID          string
	RedirectURI       string
	ResponseType      string
	Scope             string
	State             string
	Username          string
	LoginURL          string
	ForgotPasswordURL string
}

type confirmPageData struct {
	PoolName     string
	Branding     ManagedLoginBranding
	Error        string
	FormAction   string
	ClientID     string
	RedirectURI  string
	ResponseType string
	Scope        string
	State        string
	Username     string
}

type newPasswordPageData struct {
	PoolName     string
	Branding     ManagedLoginBranding
	Error        string
	FormAction   string
	Session      string
	ClientID     string
	RedirectURI  string
	ResponseType string
	Scope        string
	State        string
	Nonce        string
}

type mfaPageData struct {
	PoolName     string
	Branding     ManagedLoginBranding
	Error        string
	FormAction   string
	Session      string
	ClientID     string
	RedirectURI  string
	ResponseType string
	Scope        string
	State        string
	Nonce        string
}

// ─── template helpers ─────────────────────────────────────────────────────────

var (
	brandingPartial    = "templates/_branding_script.html"
	loginTmpl          = template.Must(template.ParseFS(templateFS, "templates/login.html", brandingPartial))
	signUpTmpl         = template.Must(template.ParseFS(templateFS, "templates/signup.html", brandingPartial))
	confirmTmpl        = template.Must(template.ParseFS(templateFS, "templates/confirm.html", brandingPartial))
	newPasswordTmpl    = template.Must(template.ParseFS(templateFS, "templates/new_password.html", brandingPartial))
	mfaTmpl            = template.Must(template.ParseFS(templateFS, "templates/mfa.html", brandingPartial))
	debugTokenTmpl     = template.Must(template.ParseFS(templateFS, "templates/debug_token.html"))
	forgotPasswordTmpl = template.Must(template.ParseFS(templateFS, "templates/forgot_password.html", brandingPartial))
	resetPasswordTmpl  = template.Must(template.ParseFS(templateFS, "templates/reset_password.html", brandingPartial))
)

func branding(pool *UserPool) ManagedLoginBranding {
	if pool.ManagedLoginBranding != nil {
		return *pool.ManagedLoginBranding
	}
	return ManagedLoginBranding{}
}

// allowSelfRegistration returns true when users are allowed to register themselves.
func allowSelfRegistration(pool *UserPool) bool {
	if pool.AdminCreateUserConfig == nil {
		return true
	}
	return !pool.AdminCreateUserConfig.AllowAdminCreateUserOnly
}

// usernameFieldConfig returns the label, HTML input type, and autocomplete
// token for the username field based on the pool's UsernameAttributes setting.
// The autocomplete token matches the input type so browsers fill the right
// credential (e.g. a saved email address when the pool uses email as username).
func usernameFieldConfig(pool *UserPool) (label, inputType, autocomplete string) {
	for _, attr := range pool.UsernameAttributes {
		switch attr {
		case "email":
			return "Email", "email", "email"
		case "phone_number":
			return "Phone number", "tel", "tel"
		}
	}
	return "Username", "text", "username"
}

// oauthParams bundles the OAuth query parameters threaded through the login flow.
type oauthParams struct {
	ClientID        string
	RedirectURI     string
	ResponseType    string
	Scope           string
	State           string
	Nonce           string
	CodeChallenge   string
	ChallengeMethod string
}

func parseOAuthParams(r *http.Request) oauthParams {
	return oauthParams{
		ClientID:        r.FormValue("client_id"),
		RedirectURI:     r.FormValue("redirect_uri"),
		ResponseType:    r.FormValue("response_type"),
		Scope:           r.FormValue("scope"),
		State:           r.FormValue("state"),
		Nonce:           r.FormValue("nonce"),
		CodeChallenge:   r.FormValue("code_challenge"),
		ChallengeMethod: r.FormValue("code_challenge_method"),
	}
}

func (p oauthParams) query() string {
	v := url.Values{}
	v.Set("client_id", p.ClientID)
	v.Set("redirect_uri", p.RedirectURI)
	v.Set("response_type", p.ResponseType)
	v.Set("scope", p.Scope)
	if p.State != "" {
		v.Set("state", p.State)
	}
	if p.Nonce != "" {
		v.Set("nonce", p.Nonce)
	}
	if p.CodeChallenge != "" {
		v.Set("code_challenge", p.CodeChallenge)
		v.Set("code_challenge_method", p.ChallengeMethod)
	}
	return v.Encode()
}

// basePath returns the URL prefix for the managed login routes for a pool.
func managedLoginBase(poolID string) string {
	return "/_cognito/" + poolID
}

// ─── OAuth2 authorize endpoint ────────────────────────────────────────────────

// HandleAuthorize handles GET /_cognito/{poolId}/oauth2/authorize.
// Validates the request and redirects to the login page.
func (s *Service) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	params := parseOAuthParams(r)

	// Validate client_id.
	client, err := s.loadClientByID(ctx, params.ClientID)
	if err != nil || client == nil || client.UserPoolID != pool.ID {
		http.Error(w, "invalid_client: Unknown client_id", http.StatusBadRequest)
		return
	}

	// Validate redirect_uri.
	if !isValidRedirectURI(client, params.RedirectURI) {
		http.Error(w, "invalid_request: redirect_uri mismatch", http.StatusBadRequest)
		return
	}

	// Validate response_type.
	if params.ResponseType != "code" && params.ResponseType != "token" {
		http.Error(w, "unsupported_response_type: response_type must be 'code' or 'token'", http.StatusBadRequest)
		return
	}

	// Check for existing login session cookie.
	if cookie, err := r.Cookie("cognito_session_" + pool.ID); err == nil {
		sess, loadErr := s.loadLoginSession(ctx, cookie.Value)
		if loadErr == nil && sess != nil && s.clk.Now().Before(sess.ExpiresAt) {
			// Already logged in — issue code/token directly.
			user, _ := s.loadUser(ctx, pool.ID, sess.Username)
			if user != nil {
				s.completeAuthorize(w, r, pool, client, user, params)
				return
			}
		}
	}

	// Redirect to login page with OAuth params.
	loginURL := managedLoginBase(poolID) + "/login?" + params.query()
	http.Redirect(w, r, loginURL, http.StatusFound)
}

// completeAuthorize finishes the authorization flow after successful authentication.
func (s *Service) completeAuthorize(w http.ResponseWriter, r *http.Request, pool *UserPool, client *UserPoolClient, user *User, params oauthParams) {
	ctx := r.Context()

	s.publish(r, events.CognitoSignIn, events.ResourcePayload{Name: user.Username})

	if params.ResponseType == "token" {
		// Implicit flow — return tokens in fragment.
		issuer := s.issuerURL(r, pool.ID)
		result, err := s.issueTokens(ctx, user, client, issuer, "", params.Nonce)
		if err != nil {
			http.Error(w, "server_error", http.StatusInternalServerError)
			return
		}
		fragment := url.Values{}
		fragment.Set("access_token", result.AccessToken)
		fragment.Set("id_token", result.IdToken)
		fragment.Set("token_type", "Bearer")
		fragment.Set("expires_in", fmt.Sprintf("%d", result.ExpiresIn))
		if params.State != "" {
			fragment.Set("state", params.State)
		}
		redir := params.RedirectURI + "#" + fragment.Encode()
		http.Redirect(w, r, redir, http.StatusFound)
		return
	}

	// Authorization code flow.
	code := generateToken()
	scopes := strings.Split(params.Scope, " ")
	ac := &AuthCode{
		Code:            code,
		ClientID:        client.ClientID,
		UserPoolID:      pool.ID,
		Username:        user.Username,
		RedirectURI:     params.RedirectURI,
		Scopes:          scopes,
		State:           params.State,
		Nonce:           params.Nonce,
		CodeChallenge:   params.CodeChallenge,
		ChallengeMethod: params.ChallengeMethod,
		CreatedAt:       s.clk.Now(),
		ExpiresAt:       s.clk.Now().Add(5 * time.Minute),
	}
	if err := s.saveAuthCode(ctx, ac); err != nil {
		http.Error(w, "server_error", http.StatusInternalServerError)
		return
	}

	redir := params.RedirectURI + "?code=" + url.QueryEscape(code)
	if params.State != "" {
		redir += "&state=" + url.QueryEscape(params.State)
	}
	http.Redirect(w, r, redir, http.StatusFound)
}

// ─── OAuth2 token endpoint ────────────────────────────────────────────────────

// HandleToken handles POST /_cognito/{poolId}/oauth2/token.
func (s *Service) HandleToken(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, "invalid_request", "malformed body", http.StatusBadRequest)
		return
	}

	grantType := r.FormValue("grant_type")
	clientID := r.FormValue("client_id")

	// Support client_id/client_secret in Authorization header (Basic auth).
	if clientID == "" {
		if user, pass, ok := r.BasicAuth(); ok {
			clientID = user
			_ = pass // client_secret validated below
		}
	}

	client, err := s.loadClientByID(ctx, clientID)
	if err != nil || client == nil || client.UserPoolID != pool.ID {
		writeOAuthError(w, "invalid_client", "Unknown client_id", http.StatusUnauthorized)
		return
	}

	// Validate client_secret if the client has one.
	if client.ClientSecret != "" {
		secret := r.FormValue("client_secret")
		if secret == "" {
			if _, pass, ok := r.BasicAuth(); ok {
				secret = pass
			}
		}
		if subtle.ConstantTimeCompare([]byte(secret), []byte(client.ClientSecret)) != 1 {
			writeOAuthError(w, "invalid_client", "Invalid client credentials", http.StatusUnauthorized)
			return
		}
	}

	switch grantType {
	case "authorization_code":
		s.handleTokenAuthCode(w, r, pool, client)
	case "refresh_token":
		s.handleTokenRefresh(w, r, pool, client)
	case "client_credentials":
		s.handleTokenClientCredentials(w, r, pool, client)
	default:
		writeOAuthError(w, "unsupported_grant_type", "grant_type must be authorization_code, refresh_token, or client_credentials", http.StatusBadRequest)
	}
}

func (s *Service) handleTokenAuthCode(w http.ResponseWriter, r *http.Request, pool *UserPool, client *UserPoolClient) {
	ctx := r.Context()
	code := r.FormValue("code")
	redirectURI := r.FormValue("redirect_uri")

	ac, err := s.loadAuthCode(ctx, code)
	if err != nil || ac == nil {
		writeOAuthError(w, "invalid_grant", "Invalid authorization code", http.StatusBadRequest)
		return
	}

	// Single-use: delete immediately.
	_ = s.removeAuthCode(ctx, code)

	// Validate.
	if s.clk.Now().After(ac.ExpiresAt) {
		writeOAuthError(w, "invalid_grant", "Authorization code expired", http.StatusBadRequest)
		return
	}
	if ac.ClientID != client.ClientID {
		writeOAuthError(w, "invalid_grant", "client_id mismatch", http.StatusBadRequest)
		return
	}
	if ac.RedirectURI != redirectURI {
		writeOAuthError(w, "invalid_grant", "redirect_uri mismatch", http.StatusBadRequest)
		return
	}

	// PKCE validation.
	if ac.CodeChallenge != "" {
		verifier := r.FormValue("code_verifier")
		if verifier == "" {
			writeOAuthError(w, "invalid_grant", "code_verifier required", http.StatusBadRequest)
			return
		}
		if !verifyPKCE(ac.CodeChallenge, ac.ChallengeMethod, verifier) {
			writeOAuthError(w, "invalid_grant", "PKCE verification failed", http.StatusBadRequest)
			return
		}
	}

	user, err := s.loadUser(ctx, pool.ID, ac.Username)
	if err != nil || user == nil {
		writeOAuthError(w, "invalid_grant", "User not found", http.StatusBadRequest)
		return
	}

	issuer := s.issuerURL(r, pool.ID)
	result, err := s.issueTokens(ctx, user, client, issuer, "", ac.Nonce)
	if err != nil {
		writeOAuthError(w, "server_error", "Failed to issue tokens", http.StatusInternalServerError)
		return
	}

	writeOAuthTokenResponse(w, result)
}

func (s *Service) handleTokenRefresh(w http.ResponseWriter, r *http.Request, pool *UserPool, client *UserPoolClient) {
	ctx := r.Context()
	refreshToken := r.FormValue("refresh_token")

	tok, err := s.loadToken(ctx, refreshToken)
	if err != nil || tok == nil || tok.Type != "refresh" {
		writeOAuthError(w, "invalid_grant", "Invalid refresh token", http.StatusBadRequest)
		return
	}
	if s.clk.Now().After(tok.ExpiresAt) {
		writeOAuthError(w, "invalid_grant", "Refresh token expired", http.StatusBadRequest)
		return
	}
	if tok.UserPoolID != pool.ID {
		writeOAuthError(w, "invalid_grant", "Token pool mismatch", http.StatusBadRequest)
		return
	}

	user, err := s.loadUser(ctx, pool.ID, tok.Username)
	if err != nil || user == nil {
		writeOAuthError(w, "invalid_grant", "User not found", http.StatusBadRequest)
		return
	}

	issuer := s.issuerURL(r, pool.ID)
	result, err := s.issueTokens(ctx, user, client, issuer, tok.OriginJTI, "")
	if err != nil {
		writeOAuthError(w, "server_error", "Failed to issue tokens", http.StatusInternalServerError)
		return
	}

	writeOAuthTokenResponse(w, result)
}

func (s *Service) handleTokenClientCredentials(w http.ResponseWriter, r *http.Request, pool *UserPool, client *UserPoolClient) {
	if client.ClientSecret == "" {
		writeOAuthError(w, "invalid_client", "client_credentials requires a client secret", http.StatusBadRequest)
		return
	}

	now := s.clk.Now()
	accessTTL := time.Hour
	if client.AccessTokenValidity > 0 {
		unit := "hours"
		if client.TokenValidityUnits != nil && client.TokenValidityUnits.AccessToken != "" {
			unit = client.TokenValidityUnits.AccessToken
		}
		accessTTL = tokenDuration(client.AccessTokenValidity, unit)
	}

	scope := r.FormValue("scope")
	if scope == "" {
		scope = strings.Join(client.AllowedOAuthScopes, " ")
	}

	// For client_credentials, generate a minimal access token (no user context).
	ctx := r.Context()
	priv, kid, err := s.getOrCreateSigningKey(ctx, pool.ID)
	if err != nil {
		writeOAuthError(w, "server_error", "Failed to generate token", http.StatusInternalServerError)
		return
	}

	claims := map[string]any{
		"sub":       client.ClientID,
		"iss":       s.issuerURL(r, pool.ID),
		"client_id": client.ClientID,
		"token_use": "access",
		"scope":     scope,
		"exp":       now.Add(accessTTL).Unix(),
		"iat":       now.Unix(),
		"jti":       uuid.NewString(),
		"version":   2,
	}

	accessJWT, err := signJWT(priv, kid, claims)
	if err != nil {
		writeOAuthError(w, "server_error", "Failed to sign token", http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"access_token": accessJWT,
		"token_type":   "Bearer",
		"expires_in":   int(accessTTL.Seconds()),
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// ─── UserInfo endpoint ────────────────────────────────────────────────────────

// HandleUserInfo handles GET/POST /_cognito/{poolId}/oauth2/userInfo.
func (s *Service) HandleUserInfo(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	// Extract bearer token.
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "invalid_token", http.StatusUnauthorized)
		return
	}
	tokenStr := strings.TrimPrefix(auth, "Bearer ")

	// Validate the access token.
	tok, ok := s.validateAccessToken(ctx, w, r, tokenStr)
	if !ok {
		return
	}
	if tok.UserPoolID != poolID {
		http.Error(w, "invalid_token: pool mismatch", http.StatusForbidden)
		return
	}

	user, err := s.loadUser(ctx, poolID, tok.Username)
	if err != nil || user == nil {
		http.Error(w, "invalid_token: user not found", http.StatusNotFound)
		return
	}

	claims := map[string]any{
		"sub":      user.Sub,
		"username": user.Username,
	}
	for _, attr := range user.Attributes {
		claims[attr.Name] = attr.Value
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(claims)
}

// ─── Revoke endpoint ──────────────────────────────────────────────────────────

// HandleRevoke handles POST /_cognito/{poolId}/oauth2/revoke.
func (s *Service) HandleRevoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, "invalid_request", "malformed body", http.StatusBadRequest)
		return
	}

	token := r.FormValue("token")
	if token == "" {
		writeOAuthError(w, "invalid_request", "token required", http.StatusBadRequest)
		return
	}

	// Remove the token (best-effort).
	_ = s.removeToken(ctx, token)
	w.WriteHeader(http.StatusOK)
}

// ─── Login pages ──────────────────────────────────────────────────────────────

// HandleLoginPage renders the managed login form.
func (s *Service) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	params := parseOAuthParams(r)
	base := managedLoginBase(poolID)
	uLabel, uType, uAuto := usernameFieldConfig(pool)

	data := loginPageData{
		PoolName:              pool.Name,
		Branding:              branding(pool),
		FormAction:            base + "/login",
		ClientID:              params.ClientID,
		RedirectURI:           params.RedirectURI,
		ResponseType:          params.ResponseType,
		Scope:                 params.Scope,
		State:                 params.State,
		Nonce:                 params.Nonce,
		CodeChallenge:         params.CodeChallenge,
		ChallengeMethod:       params.ChallengeMethod,
		SignUpURL:             base + "/signup?" + params.query(),
		ForgotPasswordURL:     base + "/forgot-password?" + params.query(),
		AllowSelfRegistration: allowSelfRegistration(pool),
		UsernameLabel:         uLabel,
		UsernameInputType:     uType,
		UsernameAutocomplete:  uAuto,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loginTmpl.Execute(w, data)
}

// HandleLoginSubmit processes the login form POST.
func (s *Service) HandleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	params := parseOAuthParams(r)
	username := r.FormValue("username")
	password := r.FormValue("password")

	// Load user — supports attribute-based lookup for pools using UsernameAttributes.
	user, err := s.resolveUser(ctx, pool, username)
	if err != nil || user == nil {
		s.publish(r, events.CognitoSignInFailed, events.ResourcePayload{Name: username})
		s.renderLoginError(w, pool, params, "Incorrect username or password.")
		return
	}

	if !user.Enabled {
		s.publish(r, events.CognitoSignInFailed, events.ResourcePayload{Name: user.Username})
		s.renderLoginError(w, pool, params, "User is disabled.")
		return
	}

	// Check FORCE_CHANGE_PASSWORD status.
	if user.Status == StatusForceChangePassword {
		if user.TempPassword == "" || password != user.TempPassword {
			s.renderLoginError(w, pool, params, "Incorrect username or password.")
			return
		}
		// Issue a session token and redirect to new-password page.
		session, sessErr := s.issueOpaqueToken(ctx, pool.ID, username, "session", 5*time.Minute)
		if sessErr != nil {
			s.renderLoginError(w, pool, params, "Internal error.")
			return
		}
		base := managedLoginBase(poolID)
		q := params.query() + "&session=" + url.QueryEscape(session)
		http.Redirect(w, r, base+"/new-password?"+q, http.StatusSeeOther)
		return
	}

	// Validate password.
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		s.publish(r, events.CognitoSignInFailed, events.ResourcePayload{Name: user.Username})
		s.renderLoginError(w, pool, params, "Incorrect username or password.")
		return
	}

	// Check MFA.
	if user.MFAEnabled && user.TOTPVerified {
		session, sessErr := s.issueOpaqueToken(ctx, pool.ID, username, "mfa", 5*time.Minute)
		if sessErr != nil {
			s.renderLoginError(w, pool, params, "Internal error.")
			return
		}
		base := managedLoginBase(poolID)
		q := params.query() + "&session=" + url.QueryEscape(session)
		http.Redirect(w, r, base+"/mfa?"+q, http.StatusSeeOther)
		return
	}

	// Successful authentication — complete the authorize flow.
	s.completeLoginFlow(w, r, pool, user, params)
}

func (s *Service) renderLoginError(w http.ResponseWriter, pool *UserPool, params oauthParams, errMsg string) {
	base := managedLoginBase(pool.ID)
	uLabel, uType, uAuto := usernameFieldConfig(pool)
	data := loginPageData{
		PoolName:              pool.Name,
		Branding:              branding(pool),
		Error:                 errMsg,
		FormAction:            base + "/login",
		ClientID:              params.ClientID,
		RedirectURI:           params.RedirectURI,
		ResponseType:          params.ResponseType,
		Scope:                 params.Scope,
		State:                 params.State,
		Nonce:                 params.Nonce,
		CodeChallenge:         params.CodeChallenge,
		ChallengeMethod:       params.ChallengeMethod,
		SignUpURL:             base + "/signup?" + params.query(),
		ForgotPasswordURL:     base + "/forgot-password?" + params.query(),
		AllowSelfRegistration: allowSelfRegistration(pool),
		UsernameLabel:         uLabel,
		UsernameInputType:     uType,
		UsernameAutocomplete:  uAuto,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK) // AWS returns 200 with error in page
	_ = loginTmpl.Execute(w, data)
}

// completeLoginFlow sets a session cookie and finishes the authorize redirect.
func (s *Service) completeLoginFlow(w http.ResponseWriter, r *http.Request, pool *UserPool, user *User, params oauthParams) {
	ctx := r.Context()

	// Create login session.
	sess := &LoginSession{
		SessionID:  uuid.NewString(),
		UserPoolID: pool.ID,
		Username:   user.Username,
		CreatedAt:  s.clk.Now(),
		ExpiresAt:  s.clk.Now().Add(1 * time.Hour),
	}
	if err := s.saveLoginSession(ctx, sess); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Set session cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     "cognito_session_" + pool.ID,
		Value:    sess.SessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   3600,
	})

	// Load client and complete authorization.
	client, err := s.loadClientByID(ctx, params.ClientID)
	if err != nil || client == nil {
		http.Error(w, "invalid_client", http.StatusBadRequest)
		return
	}

	s.completeAuthorize(w, r, pool, client, user, params)
}

// ─── Logout endpoint ──────────────────────────────────────────────────────────

// HandleLogout handles GET /_cognito/{poolId}/logout.
func (s *Service) HandleLogout(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	// Delete session cookie.
	if cookie, err := r.Cookie("cognito_session_" + poolID); err == nil {
		_ = s.removeLoginSession(ctx, cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "cognito_session_" + poolID,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	s.publish(r, events.CognitoSignOut, events.ResourcePayload{Name: poolID})

	logoutURI := r.URL.Query().Get("logout_uri")
	clientID := r.URL.Query().Get("client_id")

	// Validate logout_uri against client config if provided.
	if clientID != "" && logoutURI != "" {
		client, err := s.loadClientByID(ctx, clientID)
		if err == nil && client != nil {
			valid := false
			for _, u := range client.LogoutURLs {
				if u == logoutURI {
					valid = true
					break
				}
			}
			if !valid {
				http.Error(w, "invalid_request: logout_uri not registered", http.StatusBadRequest)
				return
			}
		}
	}

	if logoutURI != "" {
		http.Redirect(w, r, logoutURI, http.StatusFound)
		return
	}

	// No redirect — show a simple "logged out" page.
	loginURL := managedLoginBase(poolID) + "/login"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html><body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#f5f5f5;display:flex;align-items:center;justify-content:center;min-height:100vh"><div style="text-align:center;background:#fff;border-radius:8px;box-shadow:0 2px 12px rgba(0,0,0,0.1);padding:2rem 2.5rem;max-width:360px"><h2 style="font-size:1.25rem;font-weight:600;margin-bottom:0.5rem">Signed out</h2><p style="color:#718096;font-size:0.875rem;margin-bottom:1.5rem">You have been signed out successfully.</p><a href="%s" style="display:inline-block;padding:0.625rem 1.5rem;background:#0073bb;color:#fff;border-radius:4px;font-size:0.9375rem;font-weight:500;text-decoration:none">Back to sign in</a></div></body></html>`, loginURL)
}

// ─── Sign-up pages ────────────────────────────────────────────────────────────

// HandleSignUpPage renders the sign-up form.
func (s *Service) HandleSignUpPage(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	if !allowSelfRegistration(pool) {
		params := parseOAuthParams(r)
		base := managedLoginBase(poolID)
		http.Redirect(w, r, base+"/login?"+params.query(), http.StatusFound)
		return
	}

	params := parseOAuthParams(r)
	base := managedLoginBase(poolID)
	uLabel, uType, uAuto := usernameFieldConfig(pool)

	data := signUpPageData{
		PoolName:             pool.Name,
		Branding:             branding(pool),
		FormAction:           base + "/signup",
		ClientID:             params.ClientID,
		RedirectURI:          params.RedirectURI,
		ResponseType:         params.ResponseType,
		Scope:                params.Scope,
		State:                params.State,
		LoginURL:             base + "/login?" + params.query(),
		UsernameLabel:        uLabel,
		UsernameInputType:    uType,
		UsernameAutocomplete: uAuto,
		ShowEmailField:       uType != "email",
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = signUpTmpl.Execute(w, data)
}

// HandleSignUpSubmit processes the sign-up form POST.
func (s *Service) HandleSignUpSubmit(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	if !allowSelfRegistration(pool) {
		params := parseOAuthParams(r)
		s.renderSignUpError(w, pool, params, "Self-registration is disabled. Contact your administrator.")
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	params := parseOAuthParams(r)
	username := r.FormValue("username")
	email := r.FormValue("email")
	password := r.FormValue("password")
	base := managedLoginBase(poolID)

	// Check if user already exists (including attribute-based lookup).
	existing, _ := s.resolveUser(ctx, pool, username)
	if existing != nil {
		s.renderSignUpError(w, pool, params, "An account with the given username already exists.")
		return
	}

	// Validate password against pool policy.
	if aerr := validatePassword(pool, password); aerr != nil {
		s.renderSignUpError(w, pool, params, aerr.Message)
		return
	}

	// Hash password.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.renderSignUpError(w, pool, params, "Internal error.")
		return
	}

	// Create user.
	now := s.clk.Now()
	code := generateCode()
	user := &User{
		Username:          username,
		Sub:               uuid.NewString(),
		UserPoolID:        pool.ID,
		CreatedAt:         now,
		ModifiedAt:        now,
		Status:            StatusUnconfirmed,
		Enabled:           true,
		PasswordHash:      string(hash),
		PlaintextPassword: password,
		ConfirmationCode:  code,
		Attributes:        []UserAttribute{{Name: "sub", Value: uuid.NewString()}},
	}
	if email != "" {
		user.setAttr("email", email)
		user.setAttr("email_verified", "false")
	}

	if err := s.saveUser(ctx, user); err != nil {
		s.renderSignUpError(w, pool, params, "Internal error.")
		return
	}

	s.log.Info("user signed up via managed login", zap.String("pool", pool.ID), zap.String("user", username))
	s.publish(r, events.CognitoUserCreated, events.ResourcePayload{Name: username})

	// Redirect to confirm page.
	q := params.query() + "&username=" + url.QueryEscape(username)
	http.Redirect(w, r, base+"/confirm?"+q, http.StatusSeeOther)
}

func (s *Service) renderSignUpError(w http.ResponseWriter, pool *UserPool, params oauthParams, errMsg string) {
	base := managedLoginBase(pool.ID)
	uLabel, uType, uAuto := usernameFieldConfig(pool)
	data := signUpPageData{
		PoolName:             pool.Name,
		Branding:             branding(pool),
		Error:                errMsg,
		FormAction:           base + "/signup",
		ClientID:             params.ClientID,
		RedirectURI:          params.RedirectURI,
		ResponseType:         params.ResponseType,
		Scope:                params.Scope,
		State:                params.State,
		LoginURL:             base + "/login?" + params.query(),
		UsernameLabel:        uLabel,
		UsernameInputType:    uType,
		UsernameAutocomplete: uAuto,
		ShowEmailField:       uType != "email",
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = signUpTmpl.Execute(w, data)
}

// ─── Confirm page ─────────────────────────────────────────────────────────────

// HandleConfirmPage renders the confirmation code entry form.
func (s *Service) HandleConfirmPage(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	params := parseOAuthParams(r)
	username := r.URL.Query().Get("username")
	base := managedLoginBase(poolID)

	data := confirmPageData{
		PoolName:     pool.Name,
		Branding:     branding(pool),
		FormAction:   base + "/confirm",
		ClientID:     params.ClientID,
		RedirectURI:  params.RedirectURI,
		ResponseType: params.ResponseType,
		Scope:        params.Scope,
		State:        params.State,
		Username:     username,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = confirmTmpl.Execute(w, data)
}

// HandleConfirmSubmit processes the confirmation code POST.
func (s *Service) HandleConfirmSubmit(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	params := parseOAuthParams(r)
	username := r.FormValue("username")
	code := r.FormValue("code")
	base := managedLoginBase(poolID)

	user, err := s.loadUser(ctx, pool.ID, username)
	if err != nil || user == nil {
		s.renderConfirmError(w, pool, params, username, "User not found.")
		return
	}

	if user.ConfirmationCode == "" || user.ConfirmationCode != code {
		s.renderConfirmError(w, pool, params, username, "Invalid verification code.")
		return
	}

	// Confirm the user.
	user.Status = StatusConfirmed
	user.ConfirmationCode = ""
	if user.getAttr("email") != "" {
		user.setAttr("email_verified", "true")
	}
	if err := s.saveUser(ctx, user); err != nil {
		s.renderConfirmError(w, pool, params, username, "Internal error.")
		return
	}

	s.log.Info("user confirmed via managed login", zap.String("pool", pool.ID), zap.String("user", username))
	s.publish(r, events.CognitoUserConfirmed, events.ResourcePayload{Name: username})

	// Redirect to login page.
	http.Redirect(w, r, base+"/login?"+params.query(), http.StatusSeeOther)
}

func (s *Service) renderConfirmError(w http.ResponseWriter, pool *UserPool, params oauthParams, username, errMsg string) {
	base := managedLoginBase(pool.ID)
	data := confirmPageData{
		PoolName:     pool.Name,
		Branding:     branding(pool),
		Error:        errMsg,
		FormAction:   base + "/confirm",
		ClientID:     params.ClientID,
		RedirectURI:  params.RedirectURI,
		ResponseType: params.ResponseType,
		Scope:        params.Scope,
		State:        params.State,
		Username:     username,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = confirmTmpl.Execute(w, data)
}

// ─── New password page (FORCE_CHANGE_PASSWORD) ───────────────────────────────

// HandleNewPasswordPage renders the change-password form.
func (s *Service) HandleNewPasswordPage(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	params := parseOAuthParams(r)
	session := r.URL.Query().Get("session")
	base := managedLoginBase(poolID)

	data := newPasswordPageData{
		PoolName:     pool.Name,
		Branding:     branding(pool),
		FormAction:   base + "/new-password",
		Session:      session,
		ClientID:     params.ClientID,
		RedirectURI:  params.RedirectURI,
		ResponseType: params.ResponseType,
		Scope:        params.Scope,
		State:        params.State,
		Nonce:        params.Nonce,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = newPasswordTmpl.Execute(w, data)
}

// HandleNewPasswordSubmit processes the new password POST.
func (s *Service) HandleNewPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	params := parseOAuthParams(r)
	session := r.FormValue("session")
	newPassword := r.FormValue("new_password")

	// Validate session token.
	tok, err := s.loadToken(ctx, session)
	if err != nil || tok == nil || tok.Type != "session" || s.clk.Now().After(tok.ExpiresAt) {
		s.renderNewPasswordError(w, pool, params, session, "Session expired. Please sign in again.")
		return
	}
	_ = s.removeToken(ctx, session)

	user, err := s.loadUser(ctx, pool.ID, tok.Username)
	if err != nil || user == nil {
		s.renderNewPasswordError(w, pool, params, session, "User not found.")
		return
	}

	// Set new password.
	if aerr := validatePassword(pool, newPassword); aerr != nil {
		s.renderNewPasswordError(w, pool, params, session, aerr.Message)
		return
	}
	hash, hashErr := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if hashErr != nil {
		s.renderNewPasswordError(w, pool, params, session, "Internal error.")
		return
	}
	user.PasswordHash = string(hash)
	user.PlaintextPassword = newPassword
	user.TempPassword = ""
	user.Status = StatusConfirmed
	if err := s.saveUser(ctx, user); err != nil {
		s.renderNewPasswordError(w, pool, params, session, "Internal error.")
		return
	}
	s.publish(r, events.CognitoPasswordChanged, events.ResourcePayload{Name: user.Username})

	// Complete login.
	s.completeLoginFlow(w, r, pool, user, params)
}

func (s *Service) renderNewPasswordError(w http.ResponseWriter, pool *UserPool, params oauthParams, session, errMsg string) {
	base := managedLoginBase(pool.ID)
	data := newPasswordPageData{
		PoolName:     pool.Name,
		Branding:     branding(pool),
		Error:        errMsg,
		FormAction:   base + "/new-password",
		Session:      session,
		ClientID:     params.ClientID,
		RedirectURI:  params.RedirectURI,
		ResponseType: params.ResponseType,
		Scope:        params.Scope,
		State:        params.State,
		Nonce:        params.Nonce,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = newPasswordTmpl.Execute(w, data)
}

// ─── MFA page ─────────────────────────────────────────────────────────────────

// HandleMFAPage renders the MFA code entry form.
func (s *Service) HandleMFAPage(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	params := parseOAuthParams(r)
	session := r.URL.Query().Get("session")
	base := managedLoginBase(poolID)

	data := mfaPageData{
		PoolName:     pool.Name,
		Branding:     branding(pool),
		FormAction:   base + "/mfa",
		Session:      session,
		ClientID:     params.ClientID,
		RedirectURI:  params.RedirectURI,
		ResponseType: params.ResponseType,
		Scope:        params.Scope,
		State:        params.State,
		Nonce:        params.Nonce,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = mfaTmpl.Execute(w, data)
}

// HandleMFASubmit processes the MFA verification POST.
func (s *Service) HandleMFASubmit(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	params := parseOAuthParams(r)
	session := r.FormValue("session")
	code := r.FormValue("code")

	tok, err := s.loadToken(ctx, session)
	if err != nil || tok == nil || tok.Type != "mfa" || s.clk.Now().After(tok.ExpiresAt) {
		s.renderMFAError(w, pool, params, session, "Session expired. Please sign in again.")
		return
	}

	user, err := s.loadUser(ctx, pool.ID, tok.Username)
	if err != nil || user == nil {
		s.renderMFAError(w, pool, params, session, "User not found.")
		return
	}

	if !verifyTOTP(user.TOTPSecret, code, s.clk.Now()) {
		s.renderMFAError(w, pool, params, session, "Invalid MFA code. Please try again.")
		return
	}

	// MFA verified — consume session token.
	_ = s.removeToken(ctx, session)

	s.completeLoginFlow(w, r, pool, user, params)
}

func (s *Service) renderMFAError(w http.ResponseWriter, pool *UserPool, params oauthParams, session, errMsg string) {
	base := managedLoginBase(pool.ID)
	data := mfaPageData{
		PoolName:     pool.Name,
		Branding:     branding(pool),
		Error:        errMsg,
		FormAction:   base + "/mfa",
		Session:      session,
		ClientID:     params.ClientID,
		RedirectURI:  params.RedirectURI,
		ResponseType: params.ResponseType,
		Scope:        params.Scope,
		State:        params.State,
		Nonce:        params.Nonce,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = mfaTmpl.Execute(w, data)
}

// ─── OIDC discovery ───────────────────────────────────────────────────────────

// HandleOIDCDiscovery serves GET /{region}/{poolId}/.well-known/openid-configuration.
func (s *Service) HandleOIDCDiscovery(w http.ResponseWriter, r *http.Request) {
	region := chi.URLParam(r, "region")
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, err := s.loadPool(ctx, poolID)
	if err != nil || pool == nil {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	issuer := "http://" + r.Host + "/" + region + "/" + poolID
	base := "http://" + r.Host + "/_cognito/" + poolID

	doc := map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                base + "/oauth2/authorize",
		"token_endpoint":                        base + "/oauth2/token",
		"userinfo_endpoint":                     base + "/oauth2/userInfo",
		"revocation_endpoint":                   base + "/oauth2/revoke",
		"jwks_uri":                              issuer + "/.well-known/jwks.json",
		"response_types_supported":              []string{"code", "token"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "email", "phone", "profile", "aws.cognito.signin.user.admin"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"grant_types_supported":                 []string{"authorization_code", "implicit", "refresh_token", "client_credentials"},
		"claims_supported":                      []string{"sub", "iss", "aud", "exp", "iat", "token_use", "auth_time", "email", "email_verified", "phone_number", "phone_number_verified", "cognito:username", "cognito:groups", "nonce"},
		"code_challenge_methods_supported":      []string{"S256", "plain"},
		"end_session_endpoint":                  base + "/logout",
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(doc)
}

// ─── Emulator-only endpoints ──────────────────────────────────────────────────

// debugTokenPageData is the template data for the token inspector debug page.
type debugTokenPageData struct {
	PoolName     string
	AccessToken  string
	IDToken      string
	RefreshToken string
	LogoutURL    string
	Error        string
}

// HandleDebugToken serves GET /_cognito/{poolId}/debug/token.
// After the authorization code flow completes with the debug redirect URI, the
// browser lands here with ?code=.... This handler exchanges the code for tokens
// server-side and renders an interactive JWT inspector page.
func (s *Service) HandleDebugToken(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	logoutURL := managedLoginBase(poolID) + "/logout"

	renderErr := func(msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = debugTokenTmpl.Execute(w, debugTokenPageData{
			PoolName:  pool.Name,
			LogoutURL: logoutURL,
			Error:     msg,
		})
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		renderErr("No authorization code in URL. Complete a login flow first.")
		return
	}

	ac, err := s.loadAuthCode(ctx, code)
	if err != nil || ac == nil {
		renderErr("Invalid or expired authorization code.")
		return
	}
	_ = s.removeAuthCode(ctx, code) // single-use

	if s.clk.Now().After(ac.ExpiresAt) {
		renderErr("Authorization code has expired.")
		return
	}

	user, err := s.loadUser(ctx, pool.ID, ac.Username)
	if err != nil || user == nil {
		renderErr("User not found.")
		return
	}

	client, err := s.loadClientByID(ctx, ac.ClientID)
	if err != nil || client == nil {
		renderErr("Client not found.")
		return
	}

	issuer := s.issuerURL(r, pool.ID)
	result, err := s.issueTokens(ctx, user, client, issuer, "", ac.Nonce)
	if err != nil {
		renderErr("Failed to issue tokens: " + err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = debugTokenTmpl.Execute(w, debugTokenPageData{
		PoolName:     pool.Name,
		AccessToken:  result.AccessToken,
		IDToken:      result.IdToken,
		RefreshToken: result.RefreshToken,
		LogoutURL:    logoutURL,
	})
}

// ─── Branding endpoints ───────────────────────────────────────────────────────

// HandleGetBranding serves GET /_cognito/{poolId}/branding.
// Emulator-only: returns the managed login branding for the pool.
func (s *Service) HandleGetBranding(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, err := s.loadPool(ctx, poolID)
	if err != nil || pool == nil {
		http.Error(w, "Pool not found", http.StatusNotFound)
		return
	}

	branding := ManagedLoginBranding{}
	if pool.ManagedLoginBranding != nil {
		branding = *pool.ManagedLoginBranding
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(branding)
}

// HandleSetBranding serves PUT /_cognito/{poolId}/branding.
// Emulator-only: replaces the managed login branding for the pool.
func (s *Service) HandleSetBranding(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, err := s.loadPool(ctx, poolID)
	if err != nil || pool == nil {
		http.Error(w, "Pool not found", http.StatusNotFound)
		return
	}

	var branding ManagedLoginBranding
	if !serviceutil.DecodeJSON(w, r, &branding) {
		return
	}

	pool.ManagedLoginBranding = &branding
	if err := s.savePool(ctx, pool); err != nil {
		http.Error(w, "Failed to save branding", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(branding)
}

// HandleGetPassword serves GET /_cognito/{poolId}/users/{username}/password.
// Emulator-only endpoint for dev convenience — returns the user's plaintext password.
func (s *Service) HandleGetPassword(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	username := chi.URLParam(r, "username")
	ctx := r.Context()

	user, err := s.loadUser(ctx, poolID, username)
	if err != nil || user == nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	resp := map[string]string{
		"username": user.Username,
		"password": user.PlaintextPassword,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ─── Forgot / reset password pages ───────────────────────────────────────────

// HandleForgotPasswordPage renders the forgot-password form.
func (s *Service) HandleForgotPasswordPage(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	params := parseOAuthParams(r)
	base := managedLoginBase(poolID)
	uLabel, uType, uAuto := usernameFieldConfig(pool)

	data := forgotPasswordPageData{
		PoolName:             pool.Name,
		Branding:             branding(pool),
		FormAction:           base + "/forgot-password",
		ClientID:             params.ClientID,
		RedirectURI:          params.RedirectURI,
		ResponseType:         params.ResponseType,
		Scope:                params.Scope,
		State:                params.State,
		LoginURL:             base + "/login?" + params.query(),
		UsernameLabel:        uLabel,
		UsernameInputType:    uType,
		UsernameAutocomplete: uAuto,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = forgotPasswordTmpl.Execute(w, data)
}

// HandleForgotPasswordSubmit processes the forgot-password form POST.
func (s *Service) HandleForgotPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	params := parseOAuthParams(r)
	username := r.FormValue("username")
	base := managedLoginBase(poolID)
	uLabel, uType, uAuto := usernameFieldConfig(pool)

	renderFPErr := func(msg string) {
		data := forgotPasswordPageData{
			PoolName:             pool.Name,
			Branding:             branding(pool),
			Error:                msg,
			FormAction:           base + "/forgot-password",
			ClientID:             params.ClientID,
			RedirectURI:          params.RedirectURI,
			ResponseType:         params.ResponseType,
			Scope:                params.Scope,
			State:                params.State,
			LoginURL:             base + "/login?" + params.query(),
			UsernameLabel:        uLabel,
			UsernameInputType:    uType,
			UsernameAutocomplete: uAuto,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = forgotPasswordTmpl.Execute(w, data)
	}

	if username == "" {
		renderFPErr(uLabel + " is required.")
		return
	}

	user, err := s.loadUser(ctx, pool.ID, username)
	if err != nil || user == nil {
		// Don't reveal whether user exists — redirect to reset page anyway.
		// This matches AWS Cognito behaviour (no user-enumeration).
		q := params.query() + "&username=" + url.QueryEscape(username)
		http.Redirect(w, r, base+"/reset-password?"+q, http.StatusSeeOther)
		return
	}

	code := generateCode()
	user.PasswordResetCode = code
	if err := s.saveUser(ctx, user); err != nil {
		renderFPErr("Internal error. Please try again.")
		return
	}

	if emailAddr := user.email(); emailAddr != "" {
		s.sendPasswordResetEmail(pool, emailAddr, user.Username, code)
	}
	if phone := user.phoneNumber(); phone != "" {
		s.sendPasswordResetSMS(pool, phone, user.Username, code)
	}

	s.log.Info("password reset code issued via managed login",
		zap.String("pool", pool.ID), zap.String("user", username))

	q := params.query() + "&username=" + url.QueryEscape(username)
	http.Redirect(w, r, base+"/reset-password?"+q, http.StatusSeeOther)
}

// HandleResetPasswordPage renders the reset-password form (code + new password).
func (s *Service) HandleResetPasswordPage(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	params := parseOAuthParams(r)
	username := r.URL.Query().Get("username")
	base := managedLoginBase(poolID)

	data := resetPasswordPageData{
		PoolName:          pool.Name,
		Branding:          branding(pool),
		FormAction:        base + "/reset-password",
		ClientID:          params.ClientID,
		RedirectURI:       params.RedirectURI,
		ResponseType:      params.ResponseType,
		Scope:             params.Scope,
		State:             params.State,
		Username:          username,
		LoginURL:          base + "/login?" + params.query(),
		ForgotPasswordURL: base + "/forgot-password?" + params.query(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = resetPasswordTmpl.Execute(w, data)
}

// HandleResetPasswordSubmit processes the reset-password form POST.
func (s *Service) HandleResetPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "poolId")
	ctx := r.Context()

	pool, ok := s.requirePoolHTML(w, poolID, ctx)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	params := parseOAuthParams(r)
	username := r.FormValue("username")
	code := r.FormValue("code")
	newPassword := r.FormValue("password")
	base := managedLoginBase(poolID)

	renderRPErr := func(msg string) {
		data := resetPasswordPageData{
			PoolName:          pool.Name,
			Branding:          branding(pool),
			Error:             msg,
			FormAction:        base + "/reset-password",
			ClientID:          params.ClientID,
			RedirectURI:       params.RedirectURI,
			ResponseType:      params.ResponseType,
			Scope:             params.Scope,
			State:             params.State,
			Username:          username,
			LoginURL:          base + "/login?" + params.query(),
			ForgotPasswordURL: base + "/forgot-password?" + params.query(),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = resetPasswordTmpl.Execute(w, data)
	}

	if username == "" || code == "" || newPassword == "" {
		renderRPErr("All fields are required.")
		return
	}

	user, err := s.loadUser(ctx, pool.ID, username)
	if err != nil || user == nil {
		renderRPErr("Invalid or expired reset code.")
		return
	}

	if user.PasswordResetCode == "" || user.PasswordResetCode != code {
		renderRPErr("Invalid or expired reset code.")
		return
	}

	// Validate password against pool policy.
	if aerr := validatePassword(pool, newPassword); aerr != nil {
		renderRPErr(aerr.Message)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		renderRPErr("Internal error. Please try again.")
		return
	}

	user.PasswordHash = string(hash)
	user.PlaintextPassword = newPassword
	user.PasswordResetCode = ""
	user.TempPassword = ""
	if user.Status == StatusForceChangePassword {
		user.Status = StatusConfirmed
	}

	if err := s.saveUser(ctx, user); err != nil {
		renderRPErr("Internal error. Please try again.")
		return
	}

	s.log.Info("password reset via managed login",
		zap.String("pool", pool.ID), zap.String("user", username))
	s.publish(r, events.CognitoPasswordChanged, events.ResourcePayload{Name: username})

	http.Redirect(w, r, base+"/login?"+params.query(), http.StatusSeeOther)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// requirePoolHTML loads a pool and writes an HTML error if missing.
func (s *Service) requirePoolHTML(w http.ResponseWriter, poolID string, ctx context.Context) (*UserPool, bool) {
	pool, err := s.loadPool(ctx, poolID)
	if err != nil || pool == nil {
		http.Error(w, "User pool not found", http.StatusNotFound)
		return nil, false
	}
	return pool, true
}

// isValidRedirectURI checks whether the given URI is registered in the client's callback URLs.
func isValidRedirectURI(client *UserPoolClient, uri string) bool {
	if uri == "" {
		return false
	}
	// Always accept the emulator debug token page.
	if strings.HasSuffix(uri, "/debug/token") && strings.Contains(uri, "/_cognito/") {
		return true
	}
	for _, allowed := range client.CallbackURLs {
		if allowed == uri {
			return true
		}
	}
	// For localhost development, also accept if no callback URLs are configured.
	return len(client.CallbackURLs) == 0
}

// verifyPKCE verifies a PKCE code_challenge against a code_verifier.
func verifyPKCE(challenge, method, verifier string) bool {
	switch method {
	case "S256":
		h := sha256.Sum256([]byte(verifier))
		computed := base64.RawURLEncoding.EncodeToString(h[:])
		return computed == challenge
	case "plain", "":
		return challenge == verifier
	default:
		return false
	}
}

func writeOAuthError(w http.ResponseWriter, code, desc string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]string{"error": code, "error_description": desc}
	_ = json.NewEncoder(w).Encode(resp)
}

func writeOAuthTokenResponse(w http.ResponseWriter, result *authResultWire) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	resp := map[string]any{
		"access_token":  result.AccessToken,
		"id_token":      result.IdToken,
		"refresh_token": result.RefreshToken,
		"token_type":    result.TokenType,
		"expires_in":    result.ExpiresIn,
	}
	_ = json.NewEncoder(w).Encode(resp)
}
