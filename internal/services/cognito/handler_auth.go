package cognito

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// checkSecretHash validates the SECRET_HASH parameter when a client has a client
// secret configured.  The AWS formula is:
//
//	Base64( HMAC-SHA256( username + clientId , clientSecret ) )
//
// If the client has no secret, the check is skipped (SECRET_HASH must be absent).
// Returns false and writes the appropriate JSON error when validation fails.
func (s *Service) checkSecretHash(w http.ResponseWriter, r *http.Request, c *UserPoolClient, username, secretHash string) bool {
	if c.ClientSecret == "" {
		// No secret configured — SECRET_HASH must not be present.
		if secretHash != "" {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "InvalidParameterException",
				Message:    "Client " + c.ClientID + " does not have a client secret.",
				HTTPStatus: 400,
			})
			return false
		}
		return true
	}
	// Secret configured — SECRET_HASH must be present and correct.
	if secretHash == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "SECRET_HASH is required for client " + c.ClientID + ".",
			HTTPStatus: 400,
		})
		return false
	}
	mac := hmac.New(sha256.New, []byte(c.ClientSecret))
	mac.Write([]byte(username + c.ClientID))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(secretHash), []byte(expected)) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Unable to verify secret hash for client "+c.ClientID+"."))
		return false
	}
	return true
}

// signUp — SignUp
// Registers a new user with UNCONFIRMED status. If the user provides an email
// attribute, a verification code is sent via the configured SMTP mailer.
func (s *Service) signUp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID       string          `json:"ClientId"`
		Username       string          `json:"Username"`
		Password       string          `json:"Password"`
		SecretHash     string          `json:"SecretHash"`
		UserAttributes []UserAttribute `json:"UserAttributes"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Username, "Username") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Password, "Password") {
		return
	}

	c, ok := s.requireClientByID(r.Context(), w, r, req.ClientID)
	if !ok {
		return
	}
	if !s.checkSecretHash(w, r, c, req.Username, req.SecretHash) {
		return
	}

	pool, err := s.loadPool(r.Context(), c.UserPoolID)
	if err != nil || pool == nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if !allowSelfRegistration(pool) {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "NotAuthorizedException",
			Message:    "User pool is configured to only allow admin-created users.",
			HTTPStatus: 400,
		})
		return
	}
	if err := validatePassword(pool, req.Password); err != nil {
		protocol.WriteJSONError(w, r, err)
		return
	}

	existing, err := s.resolveUser(r.Context(), pool, req.Username)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing != nil {
		protocol.WriteJSONError(w, r, errUsernameExists(req.Username))
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	code := generateCode()
	attrs := req.UserAttributes
	if attrs == nil {
		attrs = []UserAttribute{}
	}
	internalUsername := req.Username
	sub := uuid.NewString()
	if len(pool.UsernameAttributes) > 0 {
		internalUsername = sub
		autoSetUsernameAttribute(pool.UsernameAttributes, &attrs, req.Username)
	}

	u := &User{
		Username:          internalUsername,
		Sub:               sub,
		UserPoolID:        c.UserPoolID,
		CreatedAt:         s.clk.Now(),
		Status:            StatusUnconfirmed,
		Enabled:           true,
		PasswordHash:      string(hash),
		PlaintextPassword: req.Password,
		Attributes:        attrs,
		ConfirmationCode:  code,
	}
	u.setAttr("sub", u.Sub)
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	if emailAddr := u.email(); emailAddr != "" {
		s.sendVerificationEmail(pool, emailAddr, u.Username, code)
	}
	if phone := u.phoneNumber(); phone != "" {
		s.sendVerificationSMS(pool, phone, u.Username, code)
	}

	s.log.Info("user signed up",
		zap.String("poolId", c.UserPoolID), zap.String("username", req.Username))
	s.publish(r, events.CognitoUserCreated, events.ResourcePayload{Name: req.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"UserConfirmed": false,
		"UserSub":       u.Sub,
	})
}

// confirmSignUp — ConfirmSignUp.
func (s *Service) confirmSignUp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID           string `json:"ClientId"`
		Username           string `json:"Username"`
		ConfirmationCode   string `json:"ConfirmationCode"`
		SecretHash         string `json:"SecretHash"`
		ForceAliasCreation bool   `json:"ForceAliasCreation"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Username, "Username") {
		return
	}
	if !serviceutil.RequireString(w, r, req.ConfirmationCode, "ConfirmationCode") {
		return
	}

	c, ok := s.requireClientByID(r.Context(), w, r, req.ClientID)
	if !ok {
		return
	}
	if !s.checkSecretHash(w, r, c, req.Username, req.SecretHash) {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, c.UserPoolID, req.Username)
	if !ok {
		return
	}
	if u.ConfirmationCode == "" {
		protocol.WriteJSONError(w, r, errExpiredCode())
		return
	}
	if u.ConfirmationCode != req.ConfirmationCode {
		protocol.WriteJSONError(w, r, errCodeMismatch())
		return
	}
	pool, err := s.loadPool(r.Context(), c.UserPoolID)
	if err != nil || pool == nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	aliasOwner, aliasAttr, err := s.findVerifiedAliasOwner(r.Context(), pool, attributesAfterConfirmation(u))
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if aliasOwner != nil && aliasOwner.Username != u.Username {
		if !req.ForceAliasCreation {
			protocol.WriteJSONError(w, r, errAliasExists())
			return
		}
		if err := s.migrateVerifiedAlias(r.Context(), aliasOwner, aliasAttr); err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
	}

	u.Status = StatusConfirmed
	u.ConfirmationCode = ""
	if u.email() != "" {
		u.setAttr("email_verified", "true")
	}
	if u.phoneNumber() != "" {
		u.setAttr("phone_number_verified", "true")
	}
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	session, err := s.issueOpaqueToken(r.Context(), c.UserPoolID, u.Username, "confirm", 5*time.Minute)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.log.Info("user confirmed signup",
		zap.String("poolId", c.UserPoolID), zap.String("username", req.Username))
	s.publish(r, events.CognitoUserConfirmed, events.ResourcePayload{Name: req.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"Session": session})
}

// resendConfirmationCode — ResendConfirmationCode.
func (s *Service) resendConfirmationCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID   string `json:"ClientId"`
		Username   string `json:"Username"`
		SecretHash string `json:"SecretHash"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Username, "Username") {
		return
	}

	c, ok := s.requireClientByID(r.Context(), w, r, req.ClientID)
	if !ok {
		return
	}
	if !s.checkSecretHash(w, r, c, req.Username, req.SecretHash) {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, c.UserPoolID, req.Username)
	if !ok {
		return
	}

	code := generateCode()
	u.ConfirmationCode = code
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if emailAddr := u.email(); emailAddr != "" {
		pool, _ := s.loadPool(r.Context(), c.UserPoolID)
		if pool != nil {
			s.sendVerificationEmail(pool, emailAddr, u.Username, code)
		}
	}
	if phone := u.phoneNumber(); phone != "" {
		pool, _ := s.loadPool(r.Context(), c.UserPoolID)
		if pool != nil {
			s.sendVerificationSMS(pool, phone, u.Username, code)
		}
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// issuerURL constructs the Cognito issuer URL embedded in JWTs.
// Clients that validate tokens must configure their JWT library to use this URL.
func (s *Service) issuerURL(r *http.Request, poolID string) string {
	return "http://" + r.Host + "/" + s.region(r.Context()) + "/" + poolID
}

// initiateAuth — InitiateAuth (USER_PASSWORD_AUTH + REFRESH_TOKEN_AUTH).
func (s *Service) initiateAuth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID       string            `json:"ClientId"`
		AuthFlow       string            `json:"AuthFlow"`
		AuthParameters map[string]string `json:"AuthParameters"`
		Session        string            `json:"Session"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}

	c, ok := s.requireClientByID(r.Context(), w, r, req.ClientID)
	if !ok {
		return
	}

	// For USER_PASSWORD_AUTH, validate SECRET_HASH now (username is in AuthParameters).
	// For REFRESH_TOKEN flows the username is resolved from the stored token;
	// handleRefreshTokenAuth will validate there once the username is known.
	switch req.AuthFlow {
	case "USER_PASSWORD_AUTH":
		if !s.checkSecretHash(w, r, c, req.AuthParameters["USERNAME"], req.AuthParameters["SECRET_HASH"]) {
			return
		}
		s.handlePasswordAuth(w, r, c, req.AuthParameters)
	case "USER_SRP_AUTH":
		if !s.checkSecretHash(w, r, c, req.AuthParameters["USERNAME"], req.AuthParameters["SECRET_HASH"]) {
			return
		}
		if !s.checkAuthFlowAllowed(w, r, c, "ALLOW_USER_SRP_AUTH") {
			return
		}
		s.handleSRPAuthStart(w, r, c, req.AuthParameters)
	case "USER_AUTH":
		if !s.checkSecretHash(w, r, c, req.AuthParameters["USERNAME"], req.AuthParameters["SECRET_HASH"]) {
			return
		}
		if !s.checkAuthFlowAllowed(w, r, c, "ALLOW_USER_AUTH") {
			return
		}
		s.handleUserAuthWithConfirmSession(w, r, c, req.AuthParameters, req.Session)
	case "CUSTOM_AUTH":
		if !s.checkSecretHash(w, r, c, req.AuthParameters["USERNAME"], req.AuthParameters["SECRET_HASH"]) {
			return
		}
		if !s.checkCustomAuthFlowAllowed(w, r, c) {
			return
		}
		resp, aerr := s.handleCustomAuthStartTyped(r.Context(), c, req.AuthParameters)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
	case "REFRESH_TOKEN_AUTH", "REFRESH_TOKEN":
		s.handleRefreshTokenAuth(w, r, c, req.AuthParameters)
	default:
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Unsupported AuthFlow: " + req.AuthFlow,
			HTTPStatus: 400,
		})
	}
}

// adminInitiateAuth — AdminInitiateAuth.
func (s *Service) adminInitiateAuth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID     string            `json:"UserPoolId"`
		ClientID       string            `json:"ClientId"`
		AuthFlow       string            `json:"AuthFlow"`
		AuthParameters map[string]string `json:"AuthParameters"`
		Session        string            `json:"Session"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}

	if _, ok := s.requirePool(r.Context(), w, r, req.UserPoolID); !ok {
		return
	}

	switch req.AuthFlow {
	case "ADMIN_USER_PASSWORD_AUTH", "USER_PASSWORD_AUTH":
		pwClient, err := s.loadClient(r.Context(), req.UserPoolID, req.ClientID)
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
		if pwClient == nil {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "ResourceNotFoundException",
				Message:    "Client " + req.ClientID + " does not exist.",
				HTTPStatus: 400,
			})
			return
		}
		if !s.checkSecretHash(w, r, pwClient, req.AuthParameters["USERNAME"], req.AuthParameters["SECRET_HASH"]) {
			return
		}
		s.handlePasswordAuth(w, r, pwClient, req.AuthParameters)
	case "USER_SRP_AUTH":
		pwClient, err := s.loadClient(r.Context(), req.UserPoolID, req.ClientID)
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
		if pwClient == nil {
			protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Client " + req.ClientID + " does not exist.", HTTPStatus: 400})
			return
		}
		if !s.checkSecretHash(w, r, pwClient, req.AuthParameters["USERNAME"], req.AuthParameters["SECRET_HASH"]) {
			return
		}
		if !s.checkAuthFlowAllowed(w, r, pwClient, "ALLOW_USER_SRP_AUTH") {
			return
		}
		s.handleSRPAuthStart(w, r, pwClient, req.AuthParameters)
	case "USER_AUTH":
		adminClient, err := s.loadClient(r.Context(), req.UserPoolID, req.ClientID)
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
		if adminClient == nil {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "ResourceNotFoundException",
				Message:    "Client " + req.ClientID + " does not exist.",
				HTTPStatus: 400,
			})
			return
		}
		if !s.checkSecretHash(w, r, adminClient, req.AuthParameters["USERNAME"], req.AuthParameters["SECRET_HASH"]) {
			return
		}
		if !s.checkAuthFlowAllowed(w, r, adminClient, "ALLOW_USER_AUTH") {
			return
		}
		s.handleUserAuthWithConfirmSession(w, r, adminClient, req.AuthParameters, req.Session)
	case "CUSTOM_AUTH":
		adminClient, err := s.loadClient(r.Context(), req.UserPoolID, req.ClientID)
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
		if adminClient == nil {
			protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "Client " + req.ClientID + " does not exist.", HTTPStatus: 400})
			return
		}
		if !s.checkSecretHash(w, r, adminClient, req.AuthParameters["USERNAME"], req.AuthParameters["SECRET_HASH"]) {
			return
		}
		if !s.checkCustomAuthFlowAllowed(w, r, adminClient) {
			return
		}
		resp, aerr := s.handleCustomAuthStartTyped(r.Context(), adminClient, req.AuthParameters)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
	case "REFRESH_TOKEN_AUTH", "REFRESH_TOKEN":
		// For admin flows, load the client record to enable SECRET_HASH validation.
		// If no ClientId is provided, treat as a secret-less client (admin callers do
		// not always supply a client ID).
		var adminClient *UserPoolClient
		if req.ClientID != "" {
			ac, err := s.loadClient(r.Context(), req.UserPoolID, req.ClientID)
			if err != nil {
				protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
				return
			}
			if ac == nil {
				protocol.WriteJSONError(w, r, &protocol.AWSError{
					Code:       "ResourceNotFoundException",
					Message:    "Client " + req.ClientID + " does not exist.",
					HTTPStatus: 400,
				})
				return
			}
			adminClient = ac
		} else {
			adminClient = &UserPoolClient{ClientID: req.ClientID, UserPoolID: req.UserPoolID}
			applyClientDefaults(adminClient)
		}
		s.handleRefreshTokenAuth(w, r, adminClient, req.AuthParameters)
	default:
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Unsupported AuthFlow: " + req.AuthFlow,
			HTTPStatus: 400,
		})
	}
}

func (s *Service) checkAuthFlowAllowed(w http.ResponseWriter, r *http.Request, client *UserPoolClient, flow string) bool {
	for _, allowed := range client.ExplicitAuthFlows {
		if allowed == flow {
			return true
		}
	}
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "UnsupportedOperationException",
		Message:    "Auth flow is not enabled for this client.",
		HTTPStatus: 400,
	})
	return false
}

func (s *Service) checkCustomAuthFlowAllowed(w http.ResponseWriter, r *http.Request, client *UserPoolClient) bool {
	if checkCustomAuthFlowAllowedTyped(client) == nil {
		return true
	}
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "UnsupportedOperationException",
		Message:    "Auth flow is not enabled for this client.",
		HTTPStatus: 400,
	})
	return false
}

func (s *Service) handleUserAuthWithConfirmSession(w http.ResponseWriter, r *http.Request, client *UserPoolClient, params map[string]string, session string) {
	username := params["USERNAME"]
	if username == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "USERNAME is required in AuthParameters.",
			HTTPStatus: 400,
		})
		return
	}
	pool, err := s.loadPool(r.Context(), client.UserPoolID)
	if err != nil || pool == nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	u, err := s.resolveUser(r.Context(), pool, username)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if u == nil {
		protocol.WriteJSONError(w, r, errUserNotFound(username))
		return
	}
	if session == "" {
		if !u.Enabled {
			s.publish(r, events.CognitoSignInFailed, events.ResourcePayload{Name: username})
			protocol.WriteJSONError(w, r, errNotAuthorized("User is disabled."))
			return
		}
		if u.Status == StatusUnconfirmed {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "UserNotConfirmedException",
				Message:    "User is not confirmed.",
				HTTPStatus: 400,
			})
			return
		}
		availableChallenges := availableUserAuthChallenges(pool, u)
		if len(availableChallenges) == 0 {
			protocol.WriteJSONError(w, r, errNoSupportedFirstAuthFactors())
			return
		}
		session, err := s.issueOpaqueToken(r.Context(), client.UserPoolID, u.Username, "userauth", 5*time.Minute)
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
		challengeName := "SELECT_CHALLENGE"
		if preferred := params["PREFERRED_CHALLENGE"]; preferred != "" && containsChallenge(availableChallenges, preferred) {
			challengeName = preferred
		}
		challengeParameters := map[string]string{"USERNAME": u.Username}
		if challengeName == "EMAIL_OTP" || challengeName == "SMS_OTP" {
			var aerr *protocol.AWSError
			challengeParameters, aerr = s.issueUserAuthOTP(pool, u, challengeName)
			if aerr != nil {
				protocol.WriteJSONError(w, r, aerr)
				return
			}
			if err := s.saveUser(r.Context(), u); err != nil {
				protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
				return
			}
		} else if challengeName == "WEB_AUTHN" {
			var aerr *protocol.AWSError
			challengeParameters, aerr = s.startWebAuthnChallengeParameters(pool, u)
			if aerr != nil {
				protocol.WriteJSONError(w, r, aerr)
				return
			}
		}
		s.writeJSON(w, r, http.StatusOK, map[string]any{
			"AvailableChallenges": availableChallenges,
			"ChallengeName":       challengeName,
			"ChallengeParameters": challengeParameters,
			"Session":             session,
		})
		return
	}
	st, err := s.loadToken(r.Context(), session)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if st == nil || st.Type != "confirm" || st.UserPoolID != client.UserPoolID || st.Username != u.Username {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid session."))
		return
	}
	if s.clk.Now().After(st.ExpiresAt) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Session has expired."))
		return
	}
	if !u.Enabled {
		s.publish(r, events.CognitoSignInFailed, events.ResourcePayload{Name: username})
		protocol.WriteJSONError(w, r, errNotAuthorized("User is disabled."))
		return
	}
	if u.Status != StatusConfirmed {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "UserNotConfirmedException",
			Message:    "User is not confirmed.",
			HTTPStatus: 400,
		})
		return
	}
	issuer := s.issuerURL(r, client.UserPoolID)
	result, err := s.issueTokens(r.Context(), u, client, issuer, "", "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	_ = s.removeToken(r.Context(), session)
	s.log.Info("user authenticated from confirm signup session",
		zap.String("poolId", client.UserPoolID), zap.String("username", username))
	s.publish(r, events.CognitoSignIn, events.ResourcePayload{Name: username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"AuthenticationResult": result})
}

func (s *Service) handleChoiceAuthChallenge(w http.ResponseWriter, r *http.Request, client *UserPoolClient, session string, responses map[string]string) {
	if session == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterException", Message: "Session is required.", HTTPStatus: 400})
		return
	}
	switch responses["ANSWER"] {
	case "PASSWORD":
		s.completeChoicePasswordChallenge(w, r, client, session, responses)
	case "PASSWORD_SRP":
		s.startChoiceSRPChallenge(w, r, client, session, responses)
	case "EMAIL_OTP", "SMS_OTP":
		s.startChoiceOTPChallenge(w, r, client, session, responses)
	case "WEB_AUTHN":
		var resp *RespondToAuthChallengeResp
		var aerr *protocol.AWSError
		if responses["CREDENTIAL"] != "" {
			resp, aerr = s.completeWebAuthnChallengeTyped(r.Context(), client, session, responses)
		} else {
			resp, aerr = s.startChoiceWebAuthnChallengeTyped(r.Context(), client, session, responses)
		}
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
	default:
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterException", Message: "Unsupported challenge answer: " + responses["ANSWER"], HTTPStatus: 400})
	}
}

func (s *Service) handleSRPAuthStart(w http.ResponseWriter, r *http.Request, client *UserPoolClient, params map[string]string) {
	username := params["USERNAME"]
	if username == "" || params["SRP_A"] == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterException", Message: "USERNAME and SRP_A are required in AuthParameters.", HTTPStatus: 400})
		return
	}
	pool, err := s.loadPool(r.Context(), client.UserPoolID)
	if err != nil || pool == nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	u, err := s.resolveUser(r.Context(), pool, username)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if u == nil {
		protocol.WriteJSONError(w, r, errUserNotFound(username))
		return
	}
	if !u.Enabled {
		protocol.WriteJSONError(w, r, errNotAuthorized("User is disabled."))
		return
	}
	session, err := s.issueOpaqueToken(r.Context(), client.UserPoolID, u.Username, "srp", 5*time.Minute)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"ChallengeName": "PASSWORD_VERIFIER", "ChallengeParameters": srpChallengeParameters(u.Username), "Session": session})
}

func (s *Service) startChoiceSRPChallenge(w http.ResponseWriter, r *http.Request, client *UserPoolClient, session string, responses map[string]string) {
	if responses["SRP_A"] == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterException", Message: "SRP_A is required in ChallengeResponses.", HTTPStatus: 400})
		return
	}
	st, err := s.loadToken(r.Context(), session)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if st == nil || st.Type != "userauth" || st.UserPoolID != client.UserPoolID {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid session."))
		return
	}
	if s.clk.Now().After(st.ExpiresAt) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Session has expired."))
		return
	}
	_ = s.removeToken(r.Context(), session)
	srpSession, err := s.issueOpaqueToken(r.Context(), client.UserPoolID, st.Username, "srp", 5*time.Minute)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"ChallengeName": "PASSWORD_VERIFIER", "ChallengeParameters": srpChallengeParameters(st.Username), "Session": srpSession})
}

func (s *Service) startChoiceOTPChallenge(w http.ResponseWriter, r *http.Request, client *UserPoolClient, session string, responses map[string]string) {
	st, err := s.loadToken(r.Context(), session)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if st == nil || st.Type != "userauth" || st.UserPoolID != client.UserPoolID {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid session."))
		return
	}
	if s.clk.Now().After(st.ExpiresAt) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Session has expired."))
		return
	}
	pool, err := s.loadPool(r.Context(), client.UserPoolID)
	if err != nil || pool == nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	u, err := s.resolveUser(r.Context(), pool, responses["USERNAME"])
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if u == nil || u.Username != st.Username || !containsChallenge(availableUserAuthChallenges(pool, u), responses["ANSWER"]) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid session."))
		return
	}
	challengeParameters, aerr := s.issueUserAuthOTP(pool, u, responses["ANSWER"])
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"ChallengeName": responses["ANSWER"], "ChallengeParameters": challengeParameters, "Session": session})
}

func (s *Service) handlePasswordChoiceChallenge(w http.ResponseWriter, r *http.Request, client *UserPoolClient, session string, responses map[string]string) {
	if session == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterException", Message: "Session is required.", HTTPStatus: 400})
		return
	}
	s.completeChoicePasswordChallenge(w, r, client, session, responses)
}

func (s *Service) completeChoicePasswordChallenge(w http.ResponseWriter, r *http.Request, client *UserPoolClient, session string, responses map[string]string) {
	password := responses["PASSWORD"]
	if password == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterException", Message: "PASSWORD is required in ChallengeResponses.", HTTPStatus: 400})
		return
	}
	st, err := s.loadToken(r.Context(), session)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if st == nil || st.Type != "userauth" || st.UserPoolID != client.UserPoolID {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid session."))
		return
	}
	if s.clk.Now().After(st.ExpiresAt) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Session has expired."))
		return
	}
	pool, err := s.loadPool(r.Context(), client.UserPoolID)
	if err != nil || pool == nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	u, err := s.resolveUser(r.Context(), pool, responses["USERNAME"])
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if u == nil || u.Username != st.Username {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid session."))
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		s.publish(r, events.CognitoSignInFailed, events.ResourcePayload{Name: responses["USERNAME"]})
		protocol.WriteJSONError(w, r, errNotAuthorized("Incorrect username or password."))
		return
	}
	issuer := s.issuerURL(r, client.UserPoolID)
	result, err := s.issueTokens(r.Context(), u, client, issuer, "", "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	_ = s.removeToken(r.Context(), session)
	s.publish(r, events.CognitoSignIn, events.ResourcePayload{Name: responses["USERNAME"]})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"AuthenticationResult": result})
}

func (s *Service) completeOTPChallenge(w http.ResponseWriter, r *http.Request, client *UserPoolClient, challengeName, session string, responses map[string]string) {
	codeKey := challengeName + "_CODE"
	code := responses[codeKey]
	if code == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterException", Message: codeKey + " is required in ChallengeResponses.", HTTPStatus: 400})
		return
	}
	st, err := s.loadToken(r.Context(), session)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if st == nil || st.Type != "userauth" || st.UserPoolID != client.UserPoolID {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid session."))
		return
	}
	if s.clk.Now().After(st.ExpiresAt) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Session has expired."))
		return
	}
	pool, err := s.loadPool(r.Context(), client.UserPoolID)
	if err != nil || pool == nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	u, err := s.resolveUser(r.Context(), pool, responses["USERNAME"])
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if u == nil || u.Username != st.Username {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid session."))
		return
	}
	stored, ok := authChallengeCode(u, challengeName)
	if !ok || stored.Code != code {
		protocol.WriteJSONError(w, r, errCodeMismatch())
		return
	}
	if !stored.ExpiresAt.IsZero() && s.clk.Now().After(stored.ExpiresAt) {
		removeAuthChallengeCode(u, challengeName)
		_ = s.saveUser(r.Context(), u)
		protocol.WriteJSONError(w, r, errExpiredCode())
		return
	}
	issuer := s.issuerURL(r, client.UserPoolID)
	result, err := s.issueTokens(r.Context(), u, client, issuer, "", "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	removeAuthChallengeCode(u, challengeName)
	_ = s.removeToken(r.Context(), session)
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.publish(r, events.CognitoSignIn, events.ResourcePayload{Name: responses["USERNAME"]})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"AuthenticationResult": result, "ChallengeParameters": map[string]string{}})
}

func (s *Service) completeSRPVerifierChallenge(w http.ResponseWriter, r *http.Request, client *UserPoolClient, session string, responses map[string]string) {
	if responses["PASSWORD_CLAIM_SIGNATURE"] == "" || responses["PASSWORD_CLAIM_SECRET_BLOCK"] == "" || responses["TIMESTAMP"] == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterException", Message: "PASSWORD_CLAIM_SIGNATURE, PASSWORD_CLAIM_SECRET_BLOCK, and TIMESTAMP are required in ChallengeResponses.", HTTPStatus: 400})
		return
	}
	st, err := s.loadToken(r.Context(), session)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if st == nil || (st.Type != "srp" && st.Type != "custom_srp") || st.UserPoolID != client.UserPoolID {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid session."))
		return
	}
	if s.clk.Now().After(st.ExpiresAt) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Session has expired."))
		return
	}
	u, err := s.loadUser(r.Context(), client.UserPoolID, st.Username)
	if err != nil || u == nil {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid session."))
		return
	}
	if st.Type == "custom_srp" {
		_ = s.removeToken(r.Context(), session)
		customSession, err := s.issueOpaqueToken(r.Context(), client.UserPoolID, u.Username, "customauth", 5*time.Minute)
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
		s.writeJSON(w, r, http.StatusOK, map[string]any{"ChallengeName": "CUSTOM_CHALLENGE", "ChallengeParameters": map[string]string{"USERNAME": u.Username}, "Session": customSession})
		return
	}
	issuer := s.issuerURL(r, client.UserPoolID)
	result, err := s.issueTokens(r.Context(), u, client, issuer, "", "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	_ = s.removeToken(r.Context(), session)
	s.publish(r, events.CognitoSignIn, events.ResourcePayload{Name: responses["USERNAME"]})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"AuthenticationResult": result, "ChallengeParameters": map[string]string{}})
}

// handlePasswordAuth is the shared USER_PASSWORD_AUTH / ADMIN_USER_PASSWORD_AUTH logic.
func (s *Service) handlePasswordAuth(w http.ResponseWriter, r *http.Request, client *UserPoolClient, params map[string]string) {
	poolID := client.UserPoolID
	username := params["USERNAME"]
	password := params["PASSWORD"]
	if username == "" || password == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "USERNAME and PASSWORD are required in AuthParameters.",
			HTTPStatus: 400,
		})
		return
	}

	// Resolve user — supports attribute-based lookup for pools using UsernameAttributes.
	pool, err := s.loadPool(r.Context(), poolID)
	if err != nil || pool == nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	u, err := s.resolveUser(r.Context(), pool, username)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if u == nil {
		protocol.WriteJSONError(w, r, errUserNotFound(username))
		return
	}
	if !u.Enabled {
		s.publish(r, events.CognitoSignInFailed, events.ResourcePayload{Name: username})
		protocol.WriteJSONError(w, r, errNotAuthorized("User is disabled."))
		return
	}
	if u.Status == StatusUnconfirmed {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "UserNotConfirmedException",
			Message:    "User is not confirmed.",
			HTTPStatus: 400,
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		s.publish(r, events.CognitoSignInFailed, events.ResourcePayload{Name: username})
		protocol.WriteJSONError(w, r, errNotAuthorized("Incorrect username or password."))
		return
	}

	// Users in FORCE_CHANGE_PASSWORD must set a permanent password first.
	if u.Status == StatusForceChangePassword {
		session, err := s.issueSession(r.Context(), poolID, username)
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
		s.writeJSON(w, r, http.StatusOK, map[string]any{
			"ChallengeName": "NEW_PASSWORD_REQUIRED",
			"Session":       session,
			"ChallengeParameters": map[string]string{
				"USER_ID_FOR_SRP": username,
				"userAttributes":  "{}",
			},
		})
		return
	}
	if resp, aerr := s.maybeStartDeviceAuthChallenge(r.Context(), pool, u, params); aerr != nil || resp != nil {
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
		return
	}

	// If the user has TOTP MFA enabled and verified, require the MFA challenge.
	if u.MFAEnabled && u.TOTPVerified {
		session, err := s.issueOpaqueToken(r.Context(), poolID, username, "mfa", 3*time.Minute)
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
		s.writeJSON(w, r, http.StatusOK, map[string]any{
			"ChallengeName":       "SOFTWARE_TOKEN_MFA",
			"Session":             session,
			"ChallengeParameters": map[string]string{},
		})
		return
	}

	issuer := s.issuerURL(r, poolID)
	result, err := s.issueTokens(r.Context(), u, client, issuer, "", "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.attachNewDeviceMetadata(r.Context(), pool, result, params)
	s.log.Info("user authenticated",
		zap.String("poolId", poolID), zap.String("username", username))
	s.publish(r, events.CognitoSignIn, events.ResourcePayload{Name: username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"AuthenticationResult": result})
}

// handleRefreshTokenAuth exchanges a refresh token for a new access/id token pair.
func (s *Service) handleRefreshTokenAuth(w http.ResponseWriter, r *http.Request, c *UserPoolClient, params map[string]string) {
	poolID := c.UserPoolID
	refreshValue := params["REFRESH_TOKEN"]
	if refreshValue == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "REFRESH_TOKEN is required in AuthParameters.",
			HTTPStatus: 400,
		})
		return
	}

	t, err := s.loadToken(r.Context(), refreshValue)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if t == nil || t.Type != "refresh" || t.UserPoolID != poolID {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid refresh token."))
		return
	}
	if s.clk.Now().After(t.ExpiresAt) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Refresh token has expired."))
		return
	}

	// Reject tokens issued before a GlobalSignOut.
	u, err := s.loadUser(r.Context(), poolID, t.Username)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if u == nil {
		protocol.WriteJSONError(w, r, errNotAuthorized("User does not exist."))
		return
	}
	if u.GlobalSignOutAt != nil && t.CreatedAt.Before(*u.GlobalSignOutAt) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Refresh token revoked by global sign-out."))
		return
	}

	// Validate SECRET_HASH now that we know the real username.
	if !s.checkSecretHash(w, r, c, u.Username, params["SECRET_HASH"]) {
		return
	}

	issuer := s.issuerURL(r, poolID)
	result, err := s.issueTokens(r.Context(), u, c, issuer, t.OriginJTI, "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	// AWS omits RefreshToken in the response for refresh-token flows.
	result.RefreshToken = ""
	s.writeJSON(w, r, http.StatusOK, map[string]any{"AuthenticationResult": result})
}

// respondToAuthChallenge — RespondToAuthChallenge.
func (s *Service) respondToAuthChallenge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID           string            `json:"ClientId"`
		ChallengeName      string            `json:"ChallengeName"`
		Session            string            `json:"Session"`
		ChallengeResponses map[string]string `json:"ChallengeResponses"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}

	c, ok := s.requireClientByID(r.Context(), w, r, req.ClientID)
	if !ok {
		return
	}
	if !s.checkSecretHash(w, r, c, req.ChallengeResponses["USERNAME"], req.ChallengeResponses["SECRET_HASH"]) {
		return
	}
	switch req.ChallengeName {
	case "SELECT_CHALLENGE":
		s.handleChoiceAuthChallenge(w, r, c, req.Session, req.ChallengeResponses)
	case "PASSWORD":
		s.handlePasswordChoiceChallenge(w, r, c, req.Session, req.ChallengeResponses)
	case "PASSWORD_VERIFIER":
		s.completeSRPVerifierChallenge(w, r, c, req.Session, req.ChallengeResponses)
	case "WEB_AUTHN":
		resp, aerr := s.completeWebAuthnChallengeTyped(r.Context(), c, req.Session, req.ChallengeResponses)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
	case "EMAIL_OTP", "SMS_OTP":
		s.completeOTPChallenge(w, r, c, req.ChallengeName, req.Session, req.ChallengeResponses)
	case "CUSTOM_CHALLENGE":
		resp, aerr := s.completeCustomAuthChallengeTyped(r.Context(), c, req.Session, req.ChallengeResponses)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
	case "DEVICE_SRP_AUTH":
		resp, aerr := s.startDeviceSRPChallengeTyped(r.Context(), c, req.Session, req.ChallengeResponses)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
	case "DEVICE_PASSWORD_VERIFIER":
		resp, aerr := s.completeDevicePasswordVerifierTyped(r.Context(), c, req.Session, req.ChallengeResponses)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
	case "NEW_PASSWORD_REQUIRED":
		s.handleNewPasswordChallenge(w, r, c, req.Session, req.ChallengeResponses)
	case "SOFTWARE_TOKEN_MFA":
		s.handleMFAChallenge(w, r, c, req.Session, req.ChallengeResponses)
	default:
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Unknown ChallengeName: " + req.ChallengeName,
			HTTPStatus: 400,
		})
	}
}

// adminRespondToAuthChallenge — AdminRespondToAuthChallenge.
func (s *Service) adminRespondToAuthChallenge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID         string            `json:"UserPoolId"`
		ClientID           string            `json:"ClientId"`
		ChallengeName      string            `json:"ChallengeName"`
		Session            string            `json:"Session"`
		ChallengeResponses map[string]string `json:"ChallengeResponses"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}
	if _, ok := s.requirePool(r.Context(), w, r, req.UserPoolID); !ok {
		return
	}

	// Resolve client for token validity configuration.
	adminClient, err := s.loadClient(r.Context(), req.UserPoolID, req.ClientID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if adminClient == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Client " + req.ClientID + " does not exist.",
			HTTPStatus: 400,
		})
		return
	}
	if !s.checkSecretHash(w, r, adminClient, req.ChallengeResponses["USERNAME"], req.ChallengeResponses["SECRET_HASH"]) {
		return
	}

	switch req.ChallengeName {
	case "SELECT_CHALLENGE":
		s.handleChoiceAuthChallenge(w, r, adminClient, req.Session, req.ChallengeResponses)
	case "PASSWORD":
		s.handlePasswordChoiceChallenge(w, r, adminClient, req.Session, req.ChallengeResponses)
	case "PASSWORD_VERIFIER":
		s.completeSRPVerifierChallenge(w, r, adminClient, req.Session, req.ChallengeResponses)
	case "WEB_AUTHN":
		resp, aerr := s.completeWebAuthnChallengeTyped(r.Context(), adminClient, req.Session, req.ChallengeResponses)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
	case "EMAIL_OTP", "SMS_OTP":
		s.completeOTPChallenge(w, r, adminClient, req.ChallengeName, req.Session, req.ChallengeResponses)
	case "CUSTOM_CHALLENGE":
		resp, aerr := s.completeCustomAuthChallengeTyped(r.Context(), adminClient, req.Session, req.ChallengeResponses)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
	case "DEVICE_SRP_AUTH":
		resp, aerr := s.startDeviceSRPChallengeTyped(r.Context(), adminClient, req.Session, req.ChallengeResponses)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
	case "DEVICE_PASSWORD_VERIFIER":
		resp, aerr := s.completeDevicePasswordVerifierTyped(r.Context(), adminClient, req.Session, req.ChallengeResponses)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		s.writeJSON(w, r, http.StatusOK, resp)
	case "NEW_PASSWORD_REQUIRED":
		s.handleNewPasswordChallenge(w, r, adminClient, req.Session, req.ChallengeResponses)
	case "SOFTWARE_TOKEN_MFA":
		s.handleMFAChallenge(w, r, adminClient, req.Session, req.ChallengeResponses)
	default:
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Unknown ChallengeName: " + req.ChallengeName,
			HTTPStatus: 400,
		})
	}
}

// handleNewPasswordChallenge resolves a NEW_PASSWORD_REQUIRED challenge.
func (s *Service) handleNewPasswordChallenge(w http.ResponseWriter, r *http.Request, client *UserPoolClient, session string, responses map[string]string) {
	poolID := client.UserPoolID
	if session == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Session is required.",
			HTTPStatus: 400,
		})
		return
	}

	st, err := s.loadToken(r.Context(), session)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if st == nil || st.Type != "session" || st.UserPoolID != poolID {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid session."))
		return
	}
	if s.clk.Now().After(st.ExpiresAt) {
		protocol.WriteJSONError(w, r, errNotAuthorized("Session has expired."))
		return
	}

	newPw := responses["NEW_PASSWORD"]
	if newPw == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "NEW_PASSWORD is required in ChallengeResponses.",
			HTTPStatus: 400,
		})
		return
	}

	u, ok := s.requireUser(r.Context(), w, r, poolID, st.Username)
	if !ok {
		return
	}

	pool, err := s.loadPool(r.Context(), poolID)
	if err != nil || pool == nil {
		protocol.WriteJSONError(w, r, errNotAuthorized("User pool not found."))
		return
	}
	if aerr := validatePassword(pool, newPw); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	hash, err := hashPassword(newPw)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	u.PasswordHash = string(hash)
	u.PlaintextPassword = newPw
	u.TempPassword = ""
	u.Status = StatusConfirmed
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	_ = s.removeToken(r.Context(), session) // consume the one-time session token

	issuer := s.issuerURL(r, poolID)
	result, err := s.issueTokens(r.Context(), u, client, issuer, "", "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.log.Info("user completed new-password challenge",
		zap.String("poolId", poolID), zap.String("username", u.Username))
	s.publish(r, events.CognitoUserConfirmed, events.ResourcePayload{Name: u.Username})
	s.publish(r, events.CognitoPasswordChanged, events.ResourcePayload{Name: u.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"AuthenticationResult": result})
}

// handleMFAChallenge resolves a SOFTWARE_TOKEN_MFA challenge.
func (s *Service) handleMFAChallenge(w http.ResponseWriter, r *http.Request, client *UserPoolClient, session string, responses map[string]string) {
	poolID := client.UserPoolID
	if session == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Session is required.",
			HTTPStatus: 400,
		})
		return
	}

	st, err := s.loadToken(r.Context(), session)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if st == nil || st.Type != "mfa" || st.UserPoolID != poolID {
		protocol.WriteJSONError(w, r, errNotAuthorized("Invalid MFA session."))
		return
	}
	if s.clk.Now().After(st.ExpiresAt) {
		protocol.WriteJSONError(w, r, errNotAuthorized("MFA session has expired."))
		return
	}

	code := responses["SOFTWARE_TOKEN_MFA_CODE"]
	if code == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "SOFTWARE_TOKEN_MFA_CODE is required in ChallengeResponses.",
			HTTPStatus: 400,
		})
		return
	}

	u, ok := s.requireUser(r.Context(), w, r, poolID, st.Username)
	if !ok {
		return
	}
	if !verifyTOTP(u.TOTPSecret, code, s.clk.Now()) {
		protocol.WriteJSONError(w, r, errCodeMismatch())
		return
	}

	_ = s.removeToken(r.Context(), session)

	issuer := s.issuerURL(r, poolID)
	result, err := s.issueTokens(r.Context(), u, client, issuer, "", "")
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.log.Info("user completed MFA challenge",
		zap.String("poolId", poolID), zap.String("username", u.Username))
	s.publish(r, events.CognitoSignIn, events.ResourcePayload{Name: u.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{"AuthenticationResult": result})
}

// forgotPassword — ForgotPassword
// Generates a password-reset code and (if email is configured) mails it to the user.
func (s *Service) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID   string `json:"ClientId"`
		Username   string `json:"Username"`
		SecretHash string `json:"SecretHash"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Username, "Username") {
		return
	}

	c, ok := s.requireClientByID(r.Context(), w, r, req.ClientID)
	if !ok {
		return
	}
	if !s.checkSecretHash(w, r, c, req.Username, req.SecretHash) {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, c.UserPoolID, req.Username)
	if !ok {
		return
	}

	code := generateCode()
	u.PasswordResetCode = code
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	if emailAddr := u.email(); emailAddr != "" {
		pool, _ := s.loadPool(r.Context(), c.UserPoolID)
		if pool != nil {
			s.sendPasswordResetEmail(pool, emailAddr, u.Username, code)
		}
	}
	if phone := u.phoneNumber(); phone != "" {
		pool, _ := s.loadPool(r.Context(), c.UserPoolID)
		if pool != nil {
			s.sendPasswordResetSMS(pool, phone, u.Username, code)
		}
	}

	// Return delivery details matching AWS wire format.
	emailAddr := u.email()
	maskedEmail := ""
	if emailAddr != "" {
		// Mask the email address (e.g. j***@example.com).
		at := -1
		for i, ch := range emailAddr {
			if ch == '@' {
				at = i
				break
			}
		}
		if at > 1 {
			maskedEmail = emailAddr[:1] + "***" + emailAddr[at:]
		} else {
			maskedEmail = "***" + emailAddr[at:]
		}
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"CodeDeliveryDetails": map[string]string{
			"DeliveryMedium": "EMAIL",
			"Destination":    maskedEmail,
			"AttributeName":  "email",
		},
	})
}

// confirmForgotPassword — ConfirmForgotPassword.
func (s *Service) confirmForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID         string `json:"ClientId"`
		Username         string `json:"Username"`
		ConfirmationCode string `json:"ConfirmationCode"`
		Password         string `json:"Password"`
		SecretHash       string `json:"SecretHash"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.ClientID, "ClientId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Username, "Username") {
		return
	}
	if !serviceutil.RequireString(w, r, req.ConfirmationCode, "ConfirmationCode") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Password, "Password") {
		return
	}

	c, ok := s.requireClientByID(r.Context(), w, r, req.ClientID)
	if !ok {
		return
	}
	if !s.checkSecretHash(w, r, c, req.Username, req.SecretHash) {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, c.UserPoolID, req.Username)
	if !ok {
		return
	}
	if u.PasswordResetCode == "" {
		protocol.WriteJSONError(w, r, errExpiredCode())
		return
	}
	if u.PasswordResetCode != req.ConfirmationCode {
		protocol.WriteJSONError(w, r, errCodeMismatch())
		return
	}

	pool, err := s.loadPool(r.Context(), c.UserPoolID)
	if err != nil || pool == nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if err := validatePassword(pool, req.Password); err != nil {
		protocol.WriteJSONError(w, r, err)
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	u.PasswordHash = string(hash)
	u.PlaintextPassword = req.Password
	u.PasswordResetCode = ""
	u.TempPassword = ""
	if u.Status == StatusForceChangePassword {
		u.Status = StatusConfirmed
	}
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.log.Info("user confirmed password reset",
		zap.String("poolId", c.UserPoolID), zap.String("username", req.Username))
	s.publish(r, events.CognitoPasswordChanged, events.ResourcePayload{Name: req.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// changePassword — ChangePassword (requires a valid access token).
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken      string `json:"AccessToken"`
		PreviousPassword string `json:"PreviousPassword"`
		ProposedPassword string `json:"ProposedPassword"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") {
		return
	}
	if !serviceutil.RequireString(w, r, req.PreviousPassword, "PreviousPassword") {
		return
	}
	if !serviceutil.RequireString(w, r, req.ProposedPassword, "ProposedPassword") {
		return
	}

	t, ok := s.validateAccessToken(r.Context(), w, r, req.AccessToken)
	if !ok {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, t.UserPoolID, t.Username)
	if !ok {
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.PreviousPassword)); err != nil {
		protocol.WriteJSONError(w, r, errNotAuthorized("Incorrect username or password."))
		return
	}

	pool, err := s.loadPool(r.Context(), t.UserPoolID)
	if err != nil || pool == nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if err := validatePassword(pool, req.ProposedPassword); err != nil {
		protocol.WriteJSONError(w, r, err)
		return
	}

	hash, err := hashPassword(req.ProposedPassword)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	u.PasswordHash = string(hash)
	u.PlaintextPassword = req.ProposedPassword
	u.TempPassword = ""
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.publish(r, events.CognitoPasswordChanged, events.ResourcePayload{Name: u.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// getUser — GetUser (requires a valid access token).
func (s *Service) getUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken string `json:"AccessToken"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") {
		return
	}

	t, ok := s.validateAccessToken(r.Context(), w, r, req.AccessToken)
	if !ok {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, t.UserPoolID, t.Username)
	if !ok {
		return
	}
	uw := toUserWire(u)
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"Username":             uw.Username,
		"UserAttributes":       uw.Attributes,
		"UserCreateDate":       uw.UserCreateDate,
		"UserLastModifiedDate": uw.UserLastModifiedDate,
	})
}

// globalSignOut — GlobalSignOut
// Marks the user's GlobalSignOutAt so all tokens issued before now are rejected.
// Also revokes the calling access token immediately by deleting its JTI record.
func (s *Service) globalSignOut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken string `json:"AccessToken"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") {
		return
	}

	t, ok := s.validateAccessToken(r.Context(), w, r, req.AccessToken)
	if !ok {
		return
	}

	// Remove the JTI record so this specific token is immediately invalid.
	_ = s.removeToken(r.Context(), t.Value)

	// Mark GlobalSignOutAt — invalidates all access tokens with iat before now,
	// and all refresh tokens created before now.
	u, err := s.loadUser(r.Context(), t.UserPoolID, t.Username)
	if err == nil && u != nil {
		now := s.clk.Now()
		u.GlobalSignOutAt = &now
		_ = s.saveUser(r.Context(), u)
	}
	s.publish(r, events.CognitoSignOut, events.ResourcePayload{Name: t.Username})

	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// revokeToken — RevokeToken (revokes a refresh token).
func (s *Service) revokeToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID string `json:"ClientId"`
		Token    string `json:"Token"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.Token, "Token") {
		return
	}
	_ = s.removeToken(r.Context(), req.Token)
	s.publish(r, events.CognitoSignOut, events.ResourcePayload{Name: req.Token})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// updateUserAttributes — UpdateUserAttributes (self-service, requires access token).
func (s *Service) updateUserAttributes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken    string          `json:"AccessToken"`
		UserAttributes []UserAttribute `json:"UserAttributes"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") {
		return
	}

	t, ok := s.validateAccessToken(r.Context(), w, r, req.AccessToken)
	if !ok {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, t.UserPoolID, t.Username)
	if !ok {
		return
	}
	pool, ok := s.requirePool(r.Context(), w, r, t.UserPoolID)
	if !ok {
		return
	}
	details, aerr := s.updateAttributesWithVerification(r.Context(), pool, u, req.UserAttributes, false)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.publish(r, events.CognitoUserUpdated, events.ResourcePayload{Name: u.Username})
	if len(details) > 0 {
		s.writeJSON(w, r, http.StatusOK, map[string]any{"CodeDeliveryDetailsList": details})
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// verifyUserAttribute — VerifyUserAttribute (self-service, requires access token).
func (s *Service) verifyUserAttribute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken   string `json:"AccessToken"`
		AttributeName string `json:"AttributeName"`
		Code          string `json:"Code"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") {
		return
	}
	if !serviceutil.RequireString(w, r, req.AttributeName, "AttributeName") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Code, "Code") {
		return
	}
	t, ok := s.validateAccessToken(r.Context(), w, r, req.AccessToken)
	if !ok {
		return
	}
	pool, ok := s.requirePool(r.Context(), w, r, t.UserPoolID)
	if !ok {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, t.UserPoolID, t.Username)
	if !ok {
		return
	}
	if aerr := s.verifyPendingAttribute(r.Context(), pool, u, req.AttributeName, req.Code); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.publish(r, events.CognitoUserUpdated, events.ResourcePayload{Name: u.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// getUserAttributeVerificationCode — GetUserAttributeVerificationCode.
func (s *Service) getUserAttributeVerificationCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken   string `json:"AccessToken"`
		AttributeName string `json:"AttributeName"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") {
		return
	}
	if !serviceutil.RequireString(w, r, req.AttributeName, "AttributeName") {
		return
	}
	t, ok := s.validateAccessToken(r.Context(), w, r, req.AccessToken)
	if !ok {
		return
	}
	pool, ok := s.requirePool(r.Context(), w, r, t.UserPoolID)
	if !ok {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, t.UserPoolID, t.Username)
	if !ok {
		return
	}
	details, aerr := s.resendAttributeVerificationCode(pool, u, req.AttributeName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"CodeDeliveryDetails": details})
}

// deleteUserAttributes — DeleteUserAttributes (self-service, requires access token).
func (s *Service) deleteUserAttributes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken        string   `json:"AccessToken"`
		UserAttributeNames []string `json:"UserAttributeNames"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") {
		return
	}

	t, ok := s.validateAccessToken(r.Context(), w, r, req.AccessToken)
	if !ok {
		return
	}
	u, ok := s.requireUser(r.Context(), w, r, t.UserPoolID, t.Username)
	if !ok {
		return
	}
	for _, name := range req.UserAttributeNames {
		u.removeAttr(name)
	}
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.publish(r, events.CognitoUserUpdated, events.ResourcePayload{Name: u.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}
