package cognito

import (
	"net/http"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// associateSoftwareToken — AssociateSoftwareToken
// Generates a TOTP secret for the caller and stores it on the user record
// (pending verification via VerifySoftwareToken). Supports AccessToken only;
// session-based association (mid-flow MFA setup) is not yet implemented.
func (s *Service) associateSoftwareToken(w http.ResponseWriter, r *http.Request) {
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

	secret := generateTOTPSecret()
	u.TOTPSecret = secret
	u.TOTPVerified = false // pending verification
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.log.Info("TOTP secret generated",
		zap.String("poolId", t.UserPoolID), zap.String("username", t.Username))
	s.publish(r, events.CognitoUserUpdated, events.ResourcePayload{Name: t.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"SecretCode": secret,
	})
}

// verifySoftwareToken — VerifySoftwareToken
// Verifies that the user can produce a valid TOTP code for their stored secret.
// On success, marks TOTPVerified = true so MFA can be enabled.
func (s *Service) verifySoftwareToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken  string `json:"AccessToken"`
		UserCode     string `json:"UserCode"`
		FriendlyName string `json:"FriendlyDeviceName"` // ignored, stored for completeness
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserCode, "UserCode") {
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
	if u.TOTPSecret == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "SoftwareTokenMFANotFoundException",
			Message:    "Software token TOTP MFA not found.",
			HTTPStatus: 400,
		})
		return
	}
	if !verifyTOTP(u.TOTPSecret, req.UserCode, s.clk.Now()) {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "EnableSoftwareTokenMFAException",
			Message:    "Code mismatch and fail enable Software Token MFA.",
			HTTPStatus: 400,
		})
		return
	}
	u.TOTPVerified = true
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.log.Info("TOTP verified",
		zap.String("poolId", t.UserPoolID), zap.String("username", t.Username))
	s.publish(r, events.CognitoUserUpdated, events.ResourcePayload{Name: t.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"Status": "SUCCESS",
	})
}

// setUserMFAPreference — SetUserMFAPreference
// Enables or disables TOTP MFA for the calling user (identified by AccessToken).
func (s *Service) setUserMFAPreference(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken              string `json:"AccessToken"`
		SoftwareTokenMfaSettings *struct {
			Enabled      bool `json:"Enabled"`
			PreferredMfa bool `json:"PreferredMfa"`
		} `json:"SoftwareTokenMfaSettings"`
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

	if req.SoftwareTokenMfaSettings != nil {
		if req.SoftwareTokenMfaSettings.Enabled && !u.TOTPVerified {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "InvalidParameterException",
				Message:    "You must verify your software token before enabling MFA.",
				HTTPStatus: 400,
			})
			return
		}
		u.MFAEnabled = req.SoftwareTokenMfaSettings.Enabled
	}
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.publish(r, events.CognitoUserUpdated, events.ResourcePayload{Name: u.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}

// adminSetUserMFAPreference — AdminSetUserMFAPreference
// Like SetUserMFAPreference but identified by UserPoolId + Username.
func (s *Service) adminSetUserMFAPreference(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPoolID               string `json:"UserPoolId"`
		Username                 string `json:"Username"`
		SoftwareTokenMfaSettings *struct {
			Enabled      bool `json:"Enabled"`
			PreferredMfa bool `json:"PreferredMfa"`
		} `json:"SoftwareTokenMfaSettings"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	if !serviceutil.RequireString(w, r, req.Username, "Username") {
		return
	}
	if _, ok := s.requirePool(r.Context(), w, r, req.UserPoolID); !ok {
		return
	}

	u, ok := s.requireUser(r.Context(), w, r, req.UserPoolID, req.Username)
	if !ok {
		return
	}

	if req.SoftwareTokenMfaSettings != nil {
		if req.SoftwareTokenMfaSettings.Enabled && !u.TOTPVerified {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "InvalidParameterException",
				Message:    "You must verify your software token before enabling MFA.",
				HTTPStatus: 400,
			})
			return
		}
		u.MFAEnabled = req.SoftwareTokenMfaSettings.Enabled
	}
	if err := s.saveUser(r.Context(), u); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	s.publish(r, events.CognitoUserUpdated, events.ResourcePayload{Name: u.Username})
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}
