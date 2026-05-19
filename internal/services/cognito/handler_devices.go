package cognito

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func (s *Service) confirmDevice(w http.ResponseWriter, r *http.Request) {
	var req ConfirmDeviceReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") || !serviceutil.RequireString(w, r, req.DeviceKey, "DeviceKey") {
		return
	}
	resp, aerr := s.ConfirmDeviceTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Service) listDevices(w http.ResponseWriter, r *http.Request) {
	var req ListDevicesReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") {
		return
	}
	resp, aerr := s.ListDevicesTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Service) getDevice(w http.ResponseWriter, r *http.Request) {
	var req DeviceKeyAccessReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.DeviceKey, "DeviceKey") {
		return
	}
	resp, aerr := s.GetDeviceTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Service) updateDeviceStatus(w http.ResponseWriter, r *http.Request) {
	var req UpdateDeviceStatusReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.AccessToken, "AccessToken") || !serviceutil.RequireString(w, r, req.DeviceKey, "DeviceKey") {
		return
	}
	resp, aerr := s.UpdateDeviceStatusTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Service) forgetDevice(w http.ResponseWriter, r *http.Request) {
	var req DeviceKeyAccessReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.DeviceKey, "DeviceKey") {
		return
	}
	resp, aerr := s.ForgetDeviceTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Service) adminGetDevice(w http.ResponseWriter, r *http.Request) {
	var req AdminDeviceReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") || !serviceutil.RequireString(w, r, req.Username, "Username") || !serviceutil.RequireString(w, r, req.DeviceKey, "DeviceKey") {
		return
	}
	resp, aerr := s.AdminGetDeviceTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Service) adminListDevices(w http.ResponseWriter, r *http.Request) {
	var req AdminListDevicesReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") || !serviceutil.RequireString(w, r, req.Username, "Username") {
		return
	}
	resp, aerr := s.AdminListDevicesTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Service) adminUpdateDeviceStatus(w http.ResponseWriter, r *http.Request) {
	var req AdminUpdateDeviceStatusReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") || !serviceutil.RequireString(w, r, req.Username, "Username") || !serviceutil.RequireString(w, r, req.DeviceKey, "DeviceKey") {
		return
	}
	resp, aerr := s.AdminUpdateDeviceStatusTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *Service) adminForgetDevice(w http.ResponseWriter, r *http.Request) {
	var req AdminDeviceReq
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if !serviceutil.RequireString(w, r, req.UserPoolID, "UserPoolId") || !serviceutil.RequireString(w, r, req.Username, "Username") || !serviceutil.RequireString(w, r, req.DeviceKey, "DeviceKey") {
		return
	}
	resp, aerr := s.AdminForgetDeviceTyped(r.Context(), &req)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}
