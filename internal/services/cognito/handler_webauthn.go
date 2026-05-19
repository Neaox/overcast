package cognito

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func (s *Service) setUserPoolMfaConfig(w http.ResponseWriter, r *http.Request) {
	var req UserPoolMfaConfigReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	resp, aerr := s.SetUserPoolMfaConfigTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Service) getUserPoolMfaConfig(w http.ResponseWriter, r *http.Request) {
	var req UserPoolIDReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") {
		return
	}
	resp, aerr := s.GetUserPoolMfaConfigTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Service) startWebAuthnRegistration(w http.ResponseWriter, r *http.Request) {
	var req AccessTokenReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") {
		return
	}
	resp, aerr := s.StartWebAuthnRegistrationTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Service) completeWebAuthnRegistration(w http.ResponseWriter, r *http.Request) {
	var req CompleteWebAuthnRegistrationReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") {
		return
	}
	if req.Credential == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{Code: "InvalidParameterException", Message: "Credential is required.", HTTPStatus: 400})
		return
	}
	_, aerr := s.CompleteWebAuthnRegistrationTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{})
}
