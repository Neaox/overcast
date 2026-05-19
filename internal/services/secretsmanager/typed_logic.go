package secretsmanager

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

type secretIDRequest struct {
	SecretId string `json:"SecretId" cbor:"SecretId"`
}

type createSecretRequest struct {
	Name         string `json:"Name" cbor:"Name"`
	SecretString string `json:"SecretString" cbor:"SecretString"`
	SecretBinary string `json:"SecretBinary" cbor:"SecretBinary"`
	Description  string `json:"Description" cbor:"Description"`
	Tags         []Tag  `json:"Tags" cbor:"Tags"`
}

type createSecretResponse struct {
	ARN       string `json:"ARN" cbor:"ARN"`
	Name      string `json:"Name" cbor:"Name"`
	VersionId string `json:"VersionId" cbor:"VersionId"`
}

type getSecretValueRequest struct {
	SecretId     string `json:"SecretId" cbor:"SecretId"`
	VersionId    string `json:"VersionId" cbor:"VersionId"`
	VersionStage string `json:"VersionStage" cbor:"VersionStage"`
}

type secretValueResponse struct {
	ARN           string   `json:"ARN" cbor:"ARN"`
	Name          string   `json:"Name" cbor:"Name"`
	VersionId     string   `json:"VersionId" cbor:"VersionId"`
	VersionStages []string `json:"VersionStages" cbor:"VersionStages"`
	SecretString  string   `json:"SecretString,omitempty" cbor:"SecretString,omitempty"`
	SecretBinary  string   `json:"SecretBinary,omitempty" cbor:"SecretBinary,omitempty"`
	CreatedDate   float64  `json:"CreatedDate" cbor:"CreatedDate"`
}

type describeSecretResponse struct {
	ARN                string              `json:"ARN" cbor:"ARN"`
	Name               string              `json:"Name" cbor:"Name"`
	Description        string              `json:"Description" cbor:"Description"`
	CreatedDate        float64             `json:"CreatedDate" cbor:"CreatedDate"`
	LastChangedDate    float64             `json:"LastChangedDate" cbor:"LastChangedDate"`
	Tags               []Tag               `json:"Tags" cbor:"Tags"`
	VersionIdsToStages map[string][]string `json:"VersionIdsToStages" cbor:"VersionIdsToStages"`
	RotationEnabled    bool                `json:"RotationEnabled,omitempty" cbor:"RotationEnabled,omitempty"`
	RotationRules      *RotationRules      `json:"RotationRules,omitempty" cbor:"RotationRules,omitempty"`
	RotationLambdaARN  string              `json:"RotationLambdaARN,omitempty" cbor:"RotationLambdaARN,omitempty"`
}

type putSecretValueRequest struct {
	SecretId     string `json:"SecretId" cbor:"SecretId"`
	SecretString string `json:"SecretString" cbor:"SecretString"`
	SecretBinary string `json:"SecretBinary" cbor:"SecretBinary"`
}

type putSecretValueResponse struct {
	ARN           string   `json:"ARN" cbor:"ARN"`
	Name          string   `json:"Name" cbor:"Name"`
	VersionId     string   `json:"VersionId" cbor:"VersionId"`
	VersionStages []string `json:"VersionStages" cbor:"VersionStages"`
}

type updateSecretRequest struct {
	SecretId     string `json:"SecretId" cbor:"SecretId"`
	Description  string `json:"Description" cbor:"Description"`
	SecretString string `json:"SecretString" cbor:"SecretString"`
	SecretBinary string `json:"SecretBinary" cbor:"SecretBinary"`
}

type updateSecretResponse struct {
	ARN       string `json:"ARN" cbor:"ARN"`
	Name      string `json:"Name" cbor:"Name"`
	VersionId string `json:"VersionId" cbor:"VersionId"`
}

type listSecretsRequest struct{}

type secretListEntry struct {
	ARN             string  `json:"ARN" cbor:"ARN"`
	Name            string  `json:"Name" cbor:"Name"`
	Description     string  `json:"Description" cbor:"Description"`
	CreatedDate     float64 `json:"CreatedDate" cbor:"CreatedDate"`
	LastChangedDate float64 `json:"LastChangedDate" cbor:"LastChangedDate"`
	Tags            []Tag   `json:"Tags,omitempty" cbor:"Tags,omitempty"`
}

type listSecretsResponse struct {
	SecretList []secretListEntry `json:"SecretList" cbor:"SecretList"`
}

type secretVersionEntry struct {
	VersionId     string   `json:"VersionId" cbor:"VersionId"`
	VersionStages []string `json:"VersionStages" cbor:"VersionStages"`
	CreatedDate   float64  `json:"CreatedDate" cbor:"CreatedDate"`
}

type listSecretVersionIdsResponse struct {
	ARN      string               `json:"ARN" cbor:"ARN"`
	Name     string               `json:"Name" cbor:"Name"`
	Versions []secretVersionEntry `json:"Versions" cbor:"Versions"`
}

type deleteSecretRequest struct {
	SecretId                   string `json:"SecretId" cbor:"SecretId"`
	ForceDeleteWithoutRecovery bool   `json:"ForceDeleteWithoutRecovery" cbor:"ForceDeleteWithoutRecovery"`
	RecoveryWindowInDays       int64  `json:"RecoveryWindowInDays" cbor:"RecoveryWindowInDays"`
}

type deleteSecretResponse struct {
	ARN          string  `json:"ARN" cbor:"ARN"`
	Name         string  `json:"Name" cbor:"Name"`
	DeletionDate float64 `json:"DeletionDate" cbor:"DeletionDate"`
}

type tagResourceRequest struct {
	SecretId string `json:"SecretId" cbor:"SecretId"`
	Tags     []Tag  `json:"Tags" cbor:"Tags"`
}

type rotateSecretRequest struct {
	SecretId          string         `json:"SecretId" cbor:"SecretId"`
	RotationLambdaARN string         `json:"RotationLambdaARN" cbor:"RotationLambdaARN"`
	RotationRules     *RotationRules `json:"RotationRules" cbor:"RotationRules"`
	RotateImmediately *bool          `json:"RotateImmediately" cbor:"RotateImmediately"`
}

type rotateSecretResponse struct {
	ARN       string `json:"ARN" cbor:"ARN"`
	Name      string `json:"Name" cbor:"Name"`
	VersionId string `json:"VersionId" cbor:"VersionId"`
}

type cancelRotateSecretResponse struct {
	ARN  string `json:"ARN" cbor:"ARN"`
	Name string `json:"Name" cbor:"Name"`
}

type untagResourceRequest struct {
	SecretId string   `json:"SecretId" cbor:"SecretId"`
	TagKeys  []string `json:"TagKeys" cbor:"TagKeys"`
}

type getRandomPasswordRequest struct {
	PasswordLength     int64  `json:"PasswordLength" cbor:"PasswordLength"`
	ExcludeCharacters  string `json:"ExcludeCharacters" cbor:"ExcludeCharacters"`
	ExcludeNumbers     bool   `json:"ExcludeNumbers" cbor:"ExcludeNumbers"`
	ExcludePunctuation bool   `json:"ExcludePunctuation" cbor:"ExcludePunctuation"`
	ExcludeUppercase   bool   `json:"ExcludeUppercase" cbor:"ExcludeUppercase"`
	ExcludeLowercase   bool   `json:"ExcludeLowercase" cbor:"ExcludeLowercase"`
	IncludeSpace       bool   `json:"IncludeSpace" cbor:"IncludeSpace"`
}

type getRandomPasswordResponse struct {
	RandomPassword string `json:"RandomPassword" cbor:"RandomPassword"`
}

type batchGetSecretValueRequest struct {
	SecretIdList []string `json:"SecretIdList" cbor:"SecretIdList"`
}

type batchGetSecretValueResponse struct {
	SecretValues []secretValueResponse `json:"SecretValues" cbor:"SecretValues"`
	Errors       []batchSecretError    `json:"Errors" cbor:"Errors"`
}

type batchSecretError struct {
	SecretId  string `json:"SecretId" cbor:"SecretId"`
	ErrorCode string `json:"ErrorCode" cbor:"ErrorCode"`
	Message   string `json:"Message" cbor:"Message"`
}

func (h *Handler) publishCtx(ctx context.Context, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: t, Payload: payload})
	}
}

func (h *Handler) createSecretTyped(ctx context.Context, req *createSecretRequest) (*createSecretResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, errInvalidParameter("You must provide a value for the Name parameter.")
	}
	if _, aerr := h.store.getSecret(ctx, req.Name); aerr == nil {
		return nil, errResourceExists(req.Name)
	}

	now := h.store.now()
	versionId := uuid.New().String()
	arn := protocol.ARN(h.store.region(ctx), h.cfg.AccountID, "secretsmanager", fmt.Sprintf("secret:%s", req.Name))

	sec := &Secret{
		ARN:         arn,
		Name:        req.Name,
		Description: req.Description,
		Tags:        req.Tags,
		Versions: []SecretVersion{{
			VersionId:    versionId,
			SecretString: req.SecretString,
			SecretBinary: req.SecretBinary,
			Stages:       []string{"AWSCURRENT"},
			CreatedDate:  float64(now.Unix()),
		}},
		CurrentVersionId: versionId,
		CreatedDate:      float64(now.Unix()),
		LastChangedDate:  float64(now.Unix()),
	}
	if aerr := h.store.putSecret(ctx, sec); aerr != nil {
		return nil, aerr
	}

	h.publishCtx(ctx, events.SecretCreated, events.ResourcePayload{Name: req.Name})
	h.log.Info("secret created", zap.String("name", req.Name))
	return &createSecretResponse{ARN: arn, Name: req.Name, VersionId: versionId}, nil
}

func (h *Handler) getSecretValueTyped(ctx context.Context, req *getSecretValueRequest) (*secretValueResponse, *protocol.AWSError) {
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		return nil, aerr
	}
	version := findSecretVersion(sec, req.VersionId)
	if version == nil {
		return nil, errResourceNotFound(req.SecretId)
	}
	return secretValueOut(sec, version), nil
}

func (h *Handler) describeSecretTyped(ctx context.Context, req *secretIDRequest) (*describeSecretResponse, *protocol.AWSError) {
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		return nil, aerr
	}

	versionMap := make(map[string][]string, len(sec.Versions))
	for _, v := range sec.Versions {
		versionMap[v.VersionId] = v.Stages
	}
	return &describeSecretResponse{
		ARN:                sec.ARN,
		Name:               sec.Name,
		Description:        sec.Description,
		CreatedDate:        sec.CreatedDate,
		LastChangedDate:    sec.LastChangedDate,
		Tags:               sec.Tags,
		VersionIdsToStages: versionMap,
		RotationEnabled:    sec.RotationEnabled,
		RotationRules:      sec.RotationRules,
		RotationLambdaARN:  sec.RotationLambdaARN,
	}, nil
}

func (h *Handler) putSecretValueTyped(ctx context.Context, req *putSecretValueRequest) (*putSecretValueResponse, *protocol.AWSError) {
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		return nil, aerr
	}
	versionId := h.appendCurrentVersion(sec, req.SecretString, req.SecretBinary)
	if aerr := h.store.putSecret(ctx, sec); aerr != nil {
		return nil, aerr
	}
	h.publishCtx(ctx, events.SecretUpdated, events.ResourcePayload{Name: sec.Name})
	return &putSecretValueResponse{
		ARN:           sec.ARN,
		Name:          sec.Name,
		VersionId:     versionId,
		VersionStages: []string{"AWSCURRENT"},
	}, nil
}

func (h *Handler) updateSecretTyped(ctx context.Context, req *updateSecretRequest) (*updateSecretResponse, *protocol.AWSError) {
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		return nil, aerr
	}
	if req.Description != "" {
		sec.Description = req.Description
	}
	sec.LastChangedDate = float64(h.store.now().Unix())

	versionId := sec.CurrentVersionId
	if req.SecretString != "" || req.SecretBinary != "" {
		versionId = h.appendCurrentVersion(sec, req.SecretString, req.SecretBinary)
	}
	if aerr := h.store.putSecret(ctx, sec); aerr != nil {
		return nil, aerr
	}
	h.publishCtx(ctx, events.SecretUpdated, events.ResourcePayload{Name: sec.Name})
	return &updateSecretResponse{ARN: sec.ARN, Name: sec.Name, VersionId: versionId}, nil
}

func (h *Handler) listSecretsTyped(ctx context.Context, _ *listSecretsRequest) (*listSecretsResponse, *protocol.AWSError) {
	secrets, aerr := h.store.listSecrets(ctx)
	if aerr != nil {
		return nil, aerr
	}
	out := make([]secretListEntry, 0, len(secrets))
	for _, sec := range secrets {
		out = append(out, secretListEntry{
			ARN:             sec.ARN,
			Name:            sec.Name,
			Description:     sec.Description,
			CreatedDate:     sec.CreatedDate,
			LastChangedDate: sec.LastChangedDate,
			Tags:            sec.Tags,
		})
	}
	return &listSecretsResponse{SecretList: out}, nil
}

func (h *Handler) listSecretVersionIdsTyped(ctx context.Context, req *secretIDRequest) (*listSecretVersionIdsResponse, *protocol.AWSError) {
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		return nil, aerr
	}
	versions := make([]secretVersionEntry, 0, len(sec.Versions))
	for _, v := range sec.Versions {
		versions = append(versions, secretVersionEntry{
			VersionId:     v.VersionId,
			VersionStages: v.Stages,
			CreatedDate:   v.CreatedDate,
		})
	}
	return &listSecretVersionIdsResponse{ARN: sec.ARN, Name: sec.Name, Versions: versions}, nil
}

func (h *Handler) deleteSecretTyped(ctx context.Context, req *deleteSecretRequest) (*deleteSecretResponse, *protocol.AWSError) {
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := h.store.deleteSecret(ctx, sec.Name); aerr != nil {
		return nil, aerr
	}
	h.publishCtx(ctx, events.SecretDeleted, events.ResourcePayload{Name: sec.Name})
	h.log.Info("secret deleted", zap.String("name", sec.Name))
	return &deleteSecretResponse{
		ARN:          sec.ARN,
		Name:         sec.Name,
		DeletionDate: float64(h.store.now().Unix()),
	}, nil
}

func (h *Handler) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (*struct{}, *protocol.AWSError) {
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		return nil, aerr
	}

	tagMap := make(map[string]string, len(sec.Tags)+len(req.Tags))
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
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) rotateSecretTyped(ctx context.Context, req *rotateSecretRequest) (*rotateSecretResponse, *protocol.AWSError) {
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		return nil, aerr
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
		return nil, aerr
	}
	h.publishCtx(ctx, events.SecretRotated, events.ResourcePayload{Name: sec.Name})
	return &rotateSecretResponse{ARN: sec.ARN, Name: sec.Name, VersionId: sec.CurrentVersionId}, nil
}

func (h *Handler) cancelRotateSecretTyped(ctx context.Context, req *secretIDRequest) (*cancelRotateSecretResponse, *protocol.AWSError) {
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		return nil, aerr
	}
	sec.RotationEnabled = false
	sec.RotationRules = nil
	sec.RotationLambdaARN = ""
	sec.LastChangedDate = float64(h.store.now().Unix())
	if aerr := h.store.putSecret(ctx, sec); aerr != nil {
		return nil, aerr
	}
	return &cancelRotateSecretResponse{ARN: sec.ARN, Name: sec.Name}, nil
}

func (h *Handler) untagResourceTyped(ctx context.Context, req *untagResourceRequest) (*struct{}, *protocol.AWSError) {
	sec, aerr := h.store.resolveSecret(ctx, req.SecretId)
	if aerr != nil {
		return nil, aerr
	}
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
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) getRandomPasswordTyped(_ context.Context, req *getRandomPasswordRequest) (*getRandomPasswordResponse, *protocol.AWSError) {
	length := req.PasswordLength
	if length <= 0 {
		length = defaultPasswordLength
	}
	charset := allowedPasswordCharset(req)
	if len(charset) == 0 {
		return nil, errInvalidParameter("No characters available with the given exclusion settings.")
	}

	password := make([]rune, int(length))
	for i := range password {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, err)
		}
		password[i] = charset[n.Int64()]
	}
	return &getRandomPasswordResponse{RandomPassword: string(password)}, nil
}

func (h *Handler) batchGetSecretValueTyped(ctx context.Context, req *batchGetSecretValueRequest) (*batchGetSecretValueResponse, *protocol.AWSError) {
	out := &batchGetSecretValueResponse{
		SecretValues: make([]secretValueResponse, 0),
		Errors:       make([]batchSecretError, 0),
	}
	for _, id := range req.SecretIdList {
		sec, aerr := h.store.resolveSecret(ctx, id)
		if aerr != nil {
			out.Errors = append(out.Errors, batchSecretError{
				SecretId:  id,
				ErrorCode: aerr.Code,
				Message:   aerr.Message,
			})
			continue
		}
		version := findSecretVersion(sec, "")
		if version == nil {
			out.Errors = append(out.Errors, batchSecretError{
				SecretId:  id,
				ErrorCode: "ResourceNotFoundException",
				Message:   fmt.Sprintf("secret %q has no current version", id),
			})
			continue
		}
		out.SecretValues = append(out.SecretValues, *secretValueOut(sec, version))
	}
	return out, nil
}

func (h *Handler) appendCurrentVersion(sec *Secret, secretString, secretBinary string) string {
	now := h.store.now()
	versionId := uuid.New().String()
	for i := range sec.Versions {
		for j, stage := range sec.Versions[i].Stages {
			if stage == "AWSCURRENT" {
				sec.Versions[i].Stages[j] = "AWSPREVIOUS"
			}
		}
	}
	sec.Versions = append(sec.Versions, SecretVersion{
		VersionId:    versionId,
		SecretString: secretString,
		SecretBinary: secretBinary,
		Stages:       []string{"AWSCURRENT"},
		CreatedDate:  float64(now.Unix()),
	})
	const maxVersions = 3
	if len(sec.Versions) > maxVersions {
		sec.Versions = sec.Versions[len(sec.Versions)-maxVersions:]
	}
	sec.CurrentVersionId = versionId
	sec.LastChangedDate = float64(now.Unix())
	return versionId
}

func findSecretVersion(sec *Secret, versionId string) *SecretVersion {
	if versionId == "" {
		versionId = sec.CurrentVersionId
	}
	for i := range sec.Versions {
		if sec.Versions[i].VersionId == versionId {
			return &sec.Versions[i]
		}
	}
	return nil
}

func secretValueOut(sec *Secret, version *SecretVersion) *secretValueResponse {
	return &secretValueResponse{
		ARN:           sec.ARN,
		Name:          sec.Name,
		VersionId:     version.VersionId,
		VersionStages: version.Stages,
		SecretString:  version.SecretString,
		SecretBinary:  version.SecretBinary,
		CreatedDate:   version.CreatedDate,
	}
}

func allowedPasswordCharset(req *getRandomPasswordRequest) []rune {
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
	return charset
}
