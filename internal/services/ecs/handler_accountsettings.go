package ecs

// handler_accountsettings.go — ListAccountSettings, PutAccountSetting,
// PutAccountSettingDefault, DeleteAccountSetting handlers.
//
// Settings are metadata-only. Known names have hardcoded defaults; any other
// name is accepted as a free-form value. Overrides are stored per region.

import (
	"encoding/json"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// defaultAccountSettings maps known ECS account setting names to their default values.
var defaultAccountSettings = map[string]string{
	"containerInsights":              "disabled",
	"taskLongArnFormat":              "enabled",
	"serviceLongArnFormat":           "enabled",
	"containerInstanceLongArnFormat": "enabled",
	"awsvpcTrunking":                 "disabled",
	"dualStackIPv6":                  "disabled",
	"fargateFIPSMode":                "disabled",
	"guardDutyActivate":              "NONE",
	"tagResourceAuthorization":       "disabled",
}

// resolvedSetting returns the stored override for name, or the hardcoded default.
func (h *Handler) resolvedSetting(r *http.Request, name string) AccountSetting {
	stored, _ := h.store.getAccountSetting(r.Context(), name)
	if stored != nil {
		return *stored
	}
	defaultVal := defaultAccountSettings[name]
	return AccountSetting{Name: name, Value: defaultVal}
}

// ListAccountSettings handles AmazonEC2ContainerServiceV20141113.ListAccountSettings.
func (h *Handler) ListAccountSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name              string `json:"name"`
		Value             string `json:"value"`
		PrincipalArn      string `json:"principalArn"`
		EffectiveSettings bool   `json:"effectiveSettings"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	type settingWire struct {
		Name         string `json:"name"`
		Value        string `json:"value"`
		PrincipalArn string `json:"principalArn"`
	}

	var settings []settingWire

	if req.Name != "" {
		s := h.resolvedSetting(r, req.Name)
		settings = append(settings, settingWire(s))
	} else {
		// Return all known settings with their effective values.
		for name := range defaultAccountSettings {
			s := h.resolvedSetting(r, name)
			settings = append(settings, settingWire(s))
		}
	}

	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"settings": settings,
	})
}

// PutAccountSetting handles AmazonEC2ContainerServiceV20141113.PutAccountSetting.
func (h *Handler) PutAccountSetting(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("name"))
		return
	}

	setting := &AccountSetting{
		Name:  req.Name,
		Value: req.Value,
	}
	if aerr := h.store.putAccountSetting(r.Context(), setting); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"setting": setting,
	})
}

// PutAccountSettingDefault handles AmazonEC2ContainerServiceV20141113.PutAccountSettingDefault.
// Behaves identically to PutAccountSetting in the emulator (no per-principal scoping).
func (h *Handler) PutAccountSettingDefault(w http.ResponseWriter, r *http.Request) {
	h.PutAccountSetting(w, r)
}

// DeleteAccountSetting handles AmazonEC2ContainerServiceV20141113.DeleteAccountSetting.
func (h *Handler) DeleteAccountSetting(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		PrincipalArn string `json:"principalArn"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("name"))
		return
	}

	existing, aerr := h.store.getAccountSetting(r.Context(), req.Name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if aerr := h.store.deleteAccountSetting(r.Context(), req.Name); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Return the setting that was deleted (or default if nothing was stored).
	var returned AccountSetting
	if existing != nil {
		returned = *existing
	} else {
		returned = h.resolvedSetting(r, req.Name)
	}

	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"setting": returned,
	})
}
