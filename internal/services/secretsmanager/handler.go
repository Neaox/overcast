package secretsmanager

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Handler holds Secrets Manager handler dependencies.
type Handler struct {
	cfg   *config.Config
	store *smStore
	log   *serviceutil.ServiceLogger
	clk   clock.Clock
	bus   *events.Bus
	ops   map[string]http.HandlerFunc

	typedOp map[string]op.Operation
}

// newHandler builds a Handler from the raw dependencies.
func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{
		cfg:   cfg,
		store: newSMStore(store, clk, cfg.Region),
		log:   log,
		clk:   clk,
	}
	h.initOps()
	return h
}

// initOps registers every known Secrets Manager operation to its handler.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		// Implemented
		"CreateSecret":         h.CreateSecret,
		"GetSecretValue":       h.GetSecretValue,
		"DescribeSecret":       h.DescribeSecret,
		"PutSecretValue":       h.PutSecretValue,
		"UpdateSecret":         h.UpdateSecret,
		"ListSecrets":          h.ListSecrets,
		"ListSecretVersionIds": h.ListSecretVersionIds,
		"DeleteSecret":         h.DeleteSecret,
		"TagResource":          h.TagResource,
		"RotateSecret":         h.RotateSecret,
		"CancelRotateSecret":   h.CancelRotateSecret,
		// Stubs (handler_stubs.go)
		"UntagResource":                h.UntagResource,
		"RestoreSecret":                h.RestoreSecret,
		"GetResourcePolicy":            h.GetResourcePolicy,
		"PutResourcePolicy":            h.PutResourcePolicy,
		"DeleteResourcePolicy":         h.DeleteResourcePolicy,
		"ReplicateSecretToRegions":     h.ReplicateSecretToRegions,
		"RemoveRegionsFromReplication": h.RemoveRegionsFromReplication,
		"ValidateResourcePolicy":       h.ValidateResourcePolicy,
		"GetRandomPassword":            h.GetRandomPassword,
		"BatchGetSecretValue":          h.BatchGetSecretValue,
	}
	h.typedOp = h.typedOps()
}

// ─── CreateSecret ──────────────────────────────────────────────────────────

// publish emits an event if the bus is wired.
func (h *Handler) publish(r *http.Request, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(r.Context(), events.Event{Type: t, Payload: payload})
	}
}

func (h *Handler) CreateSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"Name"`
		SecretString string `json:"SecretString"`
		SecretBinary string `json:"SecretBinary"`
		Description  string `json:"Description"`
		Tags         []Tag  `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, errInvalidParameter("You must provide a value for the Name parameter."))
		return
	}

	ctx := r.Context()
	// Check for duplicate
	if _, aerr := h.store.getSecret(ctx, req.Name); aerr == nil {
		protocol.WriteJSONError(w, r, errResourceExists(req.Name))
		return
	}

	now := h.store.now()
	versionId := uuid.New().String()
	arn := protocol.ARN(middleware.RegionFromContext(r.Context(), h.cfg.Region), h.cfg.AccountID, "secretsmanager", fmt.Sprintf("secret:%s", req.Name))

	version := SecretVersion{
		VersionId:    versionId,
		SecretString: req.SecretString,
		SecretBinary: req.SecretBinary,
		Stages:       []string{"AWSCURRENT"},
		CreatedDate:  float64(now.Unix()),
	}

	sec := &Secret{
		ARN:              arn,
		Name:             req.Name,
		Description:      req.Description,
		Tags:             req.Tags,
		Versions:         []SecretVersion{version},
		CurrentVersionId: versionId,
		CreatedDate:      float64(now.Unix()),
		LastChangedDate:  float64(now.Unix()),
	}

	if aerr := h.store.putSecret(ctx, sec); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.publish(r, events.SecretCreated, events.ResourcePayload{Name: req.Name})

	h.log.Info("secret created", zap.String("name", req.Name))
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"ARN":       arn,
		"Name":      req.Name,
		"VersionId": versionId,
	})
}

// ─── GetSecretValue ────────────────────────────────────────────────────────

func (h *Handler) GetSecretValue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId     string `json:"SecretId"`
		VersionId    string `json:"VersionId"`
		VersionStage string `json:"VersionStage"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	sec, aerr := h.store.resolveSecret(r.Context(), req.SecretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Find the requested version (default: AWSCURRENT)
	var version *SecretVersion
	if req.VersionId != "" {
		for i := range sec.Versions {
			if sec.Versions[i].VersionId == req.VersionId {
				version = &sec.Versions[i]
				break
			}
		}
	} else {
		for i := range sec.Versions {
			if sec.Versions[i].VersionId == sec.CurrentVersionId {
				version = &sec.Versions[i]
				break
			}
		}
	}

	if version == nil {
		protocol.WriteJSONError(w, r, errResourceNotFound(req.SecretId))
		return
	}

	resp := map[string]any{
		"ARN":           sec.ARN,
		"Name":          sec.Name,
		"VersionId":     version.VersionId,
		"VersionStages": version.Stages,
		"CreatedDate":   version.CreatedDate,
	}
	if version.SecretString != "" {
		resp["SecretString"] = version.SecretString
	}
	if version.SecretBinary != "" {
		resp["SecretBinary"] = version.SecretBinary
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

// ─── DescribeSecret ────────────────────────────────────────────────────────

func (h *Handler) DescribeSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId string `json:"SecretId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	sec, aerr := h.store.resolveSecret(r.Context(), req.SecretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Build VersionIdsToStages map
	versionMap := make(map[string][]string)
	for _, v := range sec.Versions {
		versionMap[v.VersionId] = v.Stages
	}

	resp := map[string]any{
		"ARN":                sec.ARN,
		"Name":               sec.Name,
		"Description":        sec.Description,
		"CreatedDate":        sec.CreatedDate,
		"LastChangedDate":    sec.LastChangedDate,
		"Tags":               sec.Tags,
		"VersionIdsToStages": versionMap,
	}
	if sec.RotationEnabled {
		resp["RotationEnabled"] = true
		if sec.RotationRules != nil {
			resp["RotationRules"] = sec.RotationRules
		}
		if sec.RotationLambdaARN != "" {
			resp["RotationLambdaARN"] = sec.RotationLambdaARN
		}
	}
	protocol.WriteJSON(w, r, http.StatusOK, resp)
}

// ─── PutSecretValue ────────────────────────────────────────────────────────

func (h *Handler) PutSecretValue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId     string `json:"SecretId"`
		SecretString string `json:"SecretString"`
		SecretBinary string `json:"SecretBinary"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	now := h.store.now()
	versionId := uuid.New().String()

	// Demote current version
	for i := range sec.Versions {
		for j, stage := range sec.Versions[i].Stages {
			if stage == "AWSCURRENT" {
				sec.Versions[i].Stages[j] = "AWSPREVIOUS"
			}
		}
	}

	version := SecretVersion{
		VersionId:    versionId,
		SecretString: req.SecretString,
		SecretBinary: req.SecretBinary,
		Stages:       []string{"AWSCURRENT"},
		CreatedDate:  float64(now.Unix()),
	}
	sec.Versions = append(sec.Versions, version)
	const maxVersions = 3
	if len(sec.Versions) > maxVersions {
		sec.Versions = sec.Versions[len(sec.Versions)-maxVersions:]
	}
	sec.CurrentVersionId = versionId
	sec.LastChangedDate = float64(now.Unix())

	if aerr := h.store.putSecret(ctx, sec); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.publish(r, events.SecretUpdated, events.ResourcePayload{Name: sec.Name})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"ARN":           sec.ARN,
		"Name":          sec.Name,
		"VersionId":     versionId,
		"VersionStages": []string{"AWSCURRENT"},
	})
}

// ─── UpdateSecret ──────────────────────────────────────────────────────────

func (h *Handler) UpdateSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId     string `json:"SecretId"`
		Description  string `json:"Description"`
		SecretString string `json:"SecretString"`
		SecretBinary string `json:"SecretBinary"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if req.Description != "" {
		sec.Description = req.Description
	}
	sec.LastChangedDate = float64(h.store.now().Unix())

	// If a new value is provided, create a new version
	versionId := sec.CurrentVersionId
	if req.SecretString != "" || req.SecretBinary != "" {
		versionId = uuid.New().String()
		for i := range sec.Versions {
			for j, stage := range sec.Versions[i].Stages {
				if stage == "AWSCURRENT" {
					sec.Versions[i].Stages[j] = "AWSPREVIOUS"
				}
			}
		}
		version := SecretVersion{
			VersionId:    versionId,
			SecretString: req.SecretString,
			SecretBinary: req.SecretBinary,
			Stages:       []string{"AWSCURRENT"},
			CreatedDate:  float64(h.store.now().Unix()),
		}
		sec.Versions = append(sec.Versions, version)
		sec.CurrentVersionId = versionId
	}

	if aerr := h.store.putSecret(ctx, sec); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.publish(r, events.SecretUpdated, events.ResourcePayload{Name: sec.Name})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"ARN":       sec.ARN,
		"Name":      sec.Name,
		"VersionId": versionId,
	})
}

// ─── ListSecrets ───────────────────────────────────────────────────────────

func (h *Handler) ListSecrets(w http.ResponseWriter, r *http.Request) {
	secrets, aerr := h.store.listSecrets(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	list := make([]map[string]any, 0, len(secrets))
	for _, sec := range secrets {
		entry := map[string]any{
			"ARN":             sec.ARN,
			"Name":            sec.Name,
			"Description":     sec.Description,
			"CreatedDate":     sec.CreatedDate,
			"LastChangedDate": sec.LastChangedDate,
		}
		if len(sec.Tags) > 0 {
			entry["Tags"] = sec.Tags
		}
		list = append(list, entry)
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"SecretList": list,
	})
}

// ─── ListSecretVersionIds ──────────────────────────────────────────────────

func (h *Handler) ListSecretVersionIds(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId string `json:"SecretId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	sec, aerr := h.store.resolveSecret(r.Context(), req.SecretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	versions := make([]map[string]any, 0, len(sec.Versions))
	for _, v := range sec.Versions {
		versions = append(versions, map[string]any{
			"VersionId":     v.VersionId,
			"VersionStages": v.Stages,
			"CreatedDate":   v.CreatedDate,
		})
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"ARN":      sec.ARN,
		"Name":     sec.Name,
		"Versions": versions,
	})
}

// ─── DeleteSecret ──────────────────────────────────────────────────────────

func (h *Handler) DeleteSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId                   string `json:"SecretId"`
		ForceDeleteWithoutRecovery bool   `json:"ForceDeleteWithoutRecovery"`
		RecoveryWindowInDays       int64  `json:"RecoveryWindowInDays"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// For emulator simplicity, always delete immediately
	if aerr := h.store.deleteSecret(ctx, sec.Name); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.publish(r, events.SecretDeleted, events.ResourcePayload{Name: sec.Name})

	h.log.Info("secret deleted", zap.String("name", sec.Name))
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"ARN":          sec.ARN,
		"Name":         sec.Name,
		"DeletionDate": float64(h.store.now().Unix()),
	})
}

// ─── TagResource ───────────────────────────────────────────────────────────

func (h *Handler) TagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId string `json:"SecretId"`
		Tags     []Tag  `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Merge tags (overwrite existing keys)
	tagMap := make(map[string]string)
	for _, t := range sec.Tags {
		tagMap[t.Key] = t.Value
	}
	for _, t := range req.Tags {
		tagMap[t.Key] = t.Value
	}
	merged := make([]Tag, 0, len(tagMap))
	for k, v := range tagMap {
		merged = append(merged, Tag{Key: k, Value: v})
	}
	sec.Tags = merged

	if aerr := h.store.putSecret(ctx, sec); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

// ─── RotateSecret ──────────────────────────────────────────────────────────

func (h *Handler) RotateSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId          string         `json:"SecretId"`
		RotationLambdaARN string         `json:"RotationLambdaARN"`
		RotationRules     *RotationRules `json:"RotationRules"`
		RotateImmediately *bool          `json:"RotateImmediately"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	sec.RotationEnabled = true
	if req.RotationRules != nil {
		sec.RotationRules = req.RotationRules
	}
	if req.RotationLambdaARN != "" {
		sec.RotationLambdaARN = req.RotationLambdaARN
	}
	sec.LastChangedDate = float64(h.store.now().Unix())

	if aerr := h.store.putSecret(ctx, sec); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.publish(r, events.SecretRotated, events.ResourcePayload{Name: sec.Name})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"ARN":       sec.ARN,
		"Name":      sec.Name,
		"VersionId": sec.CurrentVersionId,
	})
}

// ─── CancelRotateSecret ────────────────────────────────────────────────────

func (h *Handler) CancelRotateSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId string `json:"SecretId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	sec.RotationEnabled = false
	sec.RotationRules = nil
	sec.RotationLambdaARN = ""
	sec.LastChangedDate = float64(h.store.now().Unix())

	if aerr := h.store.putSecret(ctx, sec); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"ARN":  sec.ARN,
		"Name": sec.Name,
	})
}

// ─── UntagResource ─────────────────────────────────────────────────────────

func (h *Handler) UntagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretId string   `json:"SecretId"`
		TagKeys  []string `json:"TagKeys"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Build a set of keys to remove for O(1) lookup.
	remove := make(map[string]struct{}, len(req.TagKeys))
	for _, k := range req.TagKeys {
		remove[k] = struct{}{}
	}
	filtered := sec.Tags[:0]
	for _, t := range sec.Tags {
		if _, drop := remove[t.Key]; !drop {
			filtered = append(filtered, t)
		}
	}
	sec.Tags = filtered

	if aerr := h.store.putSecret(ctx, sec); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

// ─── GetRandomPassword ─────────────────────────────────────────────────────

// defaultPasswordLength matches the AWS Secrets Manager default.
const defaultPasswordLength = 32

// defaultPasswordChars is the full default character set (upper, lower, digit, symbol).
const defaultPasswordChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

func (h *Handler) GetRandomPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PasswordLength     int64  `json:"PasswordLength"`
		ExcludeCharacters  string `json:"ExcludeCharacters"`
		ExcludeNumbers     bool   `json:"ExcludeNumbers"`
		ExcludePunctuation bool   `json:"ExcludePunctuation"`
		ExcludeUppercase   bool   `json:"ExcludeUppercase"`
		ExcludeLowercase   bool   `json:"ExcludeLowercase"`
		IncludeSpace       bool   `json:"IncludeSpace"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	length := req.PasswordLength
	if length <= 0 {
		length = defaultPasswordLength
	}

	// Build allowed charset by filtering the defaults.
	excluded := make(map[rune]struct{})
	for _, c := range req.ExcludeCharacters {
		excluded[c] = struct{}{}
	}
	digits := "0123456789"
	punctuation := "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
	uppercase := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lowercase := "abcdefghijklmnopqrstuvwxyz"

	var charset []rune
	for _, c := range defaultPasswordChars {
		if _, skip := excluded[c]; skip {
			continue
		}
		if req.ExcludeNumbers && containsRune(digits, c) {
			continue
		}
		if req.ExcludePunctuation && containsRune(punctuation, c) {
			continue
		}
		if req.ExcludeUppercase && containsRune(uppercase, c) {
			continue
		}
		if req.ExcludeLowercase && containsRune(lowercase, c) {
			continue
		}
		charset = append(charset, c)
	}
	if req.IncludeSpace {
		charset = append(charset, ' ')
	}

	if len(charset) == 0 {
		protocol.WriteJSONError(w, r, errInvalidParameter("No characters available with the given exclusion settings."))
		return
	}

	password := make([]rune, length)
	for i := range password {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
			return
		}
		password[i] = charset[n.Int64()]
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"RandomPassword": string(password),
	})
}

// containsRune reports whether s contains r.
func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// ─── BatchGetSecretValue ───────────────────────────────────────────────────

func (h *Handler) BatchGetSecretValue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SecretIdList []string `json:"SecretIdList"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	type secretEntry struct {
		ARN           string   `json:"ARN"`
		Name          string   `json:"Name"`
		VersionId     string   `json:"VersionId"`
		VersionStages []string `json:"VersionStages"`
		SecretString  string   `json:"SecretString,omitempty"`
		SecretBinary  string   `json:"SecretBinary,omitempty"`
		CreatedDate   float64  `json:"CreatedDate"`
	}
	type errorEntry struct {
		SecretId  string `json:"SecretId"`
		ErrorCode string `json:"ErrorCode"`
		Message   string `json:"Message"`
	}

	secretValues := make([]secretEntry, 0)
	errors := make([]errorEntry, 0)

	for _, id := range req.SecretIdList {
		sec, aerr := h.store.resolveSecret(ctx, id)
		if aerr != nil {
			errors = append(errors, errorEntry{
				SecretId:  id,
				ErrorCode: aerr.Code,
				Message:   aerr.Message,
			})
			continue
		}
		// Find AWSCURRENT version.
		var version *SecretVersion
		for i := range sec.Versions {
			if sec.Versions[i].VersionId == sec.CurrentVersionId {
				version = &sec.Versions[i]
				break
			}
		}
		if version == nil {
			errors = append(errors, errorEntry{
				SecretId:  id,
				ErrorCode: "ResourceNotFoundException",
				Message:   fmt.Sprintf("secret %q has no current version", id),
			})
			continue
		}
		entry := secretEntry{
			ARN:           sec.ARN,
			Name:          sec.Name,
			VersionId:     version.VersionId,
			VersionStages: version.Stages,
			SecretString:  version.SecretString,
			SecretBinary:  version.SecretBinary,
			CreatedDate:   version.CreatedDate,
		}
		secretValues = append(secretValues, entry)
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"SecretValues": secretValues,
		"Errors":       errors,
	})
}
