package kms

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

type keyIDRequest struct {
	KeyId string `json:"KeyId" cbor:"KeyId"`
}

type createKeyRequest struct {
	Description string `json:"Description" cbor:"Description"`
	KeySpec     string `json:"KeySpec" cbor:"KeySpec"`
	KeyUsage    string `json:"KeyUsage" cbor:"KeyUsage"`
}

type keyMetadataResponse struct {
	KeyMetadata keyMetadataWire `json:"KeyMetadata" cbor:"KeyMetadata"`
}

type listKeysRequest struct{}

type keyListEntry struct {
	KeyId  string `json:"KeyId" cbor:"KeyId"`
	KeyArn string `json:"KeyArn" cbor:"KeyArn"`
}

type listKeysResponse struct {
	Keys      []keyListEntry `json:"Keys" cbor:"Keys"`
	Truncated bool           `json:"Truncated" cbor:"Truncated"`
}

type scheduleKeyDeletionRequest struct {
	KeyId               string `json:"KeyId" cbor:"KeyId"`
	PendingWindowInDays int    `json:"PendingWindowInDays" cbor:"PendingWindowInDays"`
}

type scheduleKeyDeletionResponse struct {
	KeyId        string  `json:"KeyId" cbor:"KeyId"`
	KeyArn       string  `json:"KeyArn" cbor:"KeyArn"`
	DeletionDate float64 `json:"DeletionDate" cbor:"DeletionDate"`
	KeyState     string  `json:"KeyState" cbor:"KeyState"`
}

type cancelKeyDeletionResponse struct {
	KeyId    string `json:"KeyId" cbor:"KeyId"`
	KeyArn   string `json:"KeyArn" cbor:"KeyArn"`
	KeyState string `json:"KeyState" cbor:"KeyState"`
}

type createAliasRequest struct {
	AliasName   string `json:"AliasName" cbor:"AliasName"`
	TargetKeyId string `json:"TargetKeyId" cbor:"TargetKeyId"`
}

type deleteAliasRequest struct {
	AliasName string `json:"AliasName" cbor:"AliasName"`
}

type aliasEntry struct {
	AliasName    string  `json:"AliasName" cbor:"AliasName"`
	AliasArn     string  `json:"AliasArn" cbor:"AliasArn"`
	TargetKeyId  string  `json:"TargetKeyId" cbor:"TargetKeyId"`
	CreationDate float64 `json:"CreationDate" cbor:"CreationDate"`
}

type listAliasesResponse struct {
	Aliases   []aliasEntry `json:"Aliases" cbor:"Aliases"`
	Truncated bool         `json:"Truncated" cbor:"Truncated"`
}

type encryptRequest struct {
	KeyId     string `json:"KeyId" cbor:"KeyId"`
	Plaintext []byte `json:"Plaintext" cbor:"Plaintext"`
}

type encryptResponse struct {
	CiphertextBlob      []byte `json:"CiphertextBlob" cbor:"CiphertextBlob"`
	KeyId               string `json:"KeyId" cbor:"KeyId"`
	EncryptionAlgorithm string `json:"EncryptionAlgorithm" cbor:"EncryptionAlgorithm"`
}

type decryptRequest struct {
	KeyId          string `json:"KeyId" cbor:"KeyId"`
	CiphertextBlob []byte `json:"CiphertextBlob" cbor:"CiphertextBlob"`
}

type decryptResponse struct {
	Plaintext           []byte `json:"Plaintext" cbor:"Plaintext"`
	KeyId               string `json:"KeyId" cbor:"KeyId"`
	EncryptionAlgorithm string `json:"EncryptionAlgorithm" cbor:"EncryptionAlgorithm"`
}

type generateDataKeyRequest struct {
	KeyId   string `json:"KeyId" cbor:"KeyId"`
	KeySpec string `json:"KeySpec" cbor:"KeySpec"`
}

type generateDataKeyResponse struct {
	Plaintext      []byte `json:"Plaintext" cbor:"Plaintext"`
	CiphertextBlob []byte `json:"CiphertextBlob" cbor:"CiphertextBlob"`
	KeyId          string `json:"KeyId" cbor:"KeyId"`
}

type generateDataKeyWithoutPlaintextResponse struct {
	CiphertextBlob []byte `json:"CiphertextBlob" cbor:"CiphertextBlob"`
	KeyId          string `json:"KeyId" cbor:"KeyId"`
}

type signRequest struct {
	KeyId            string `json:"KeyId" cbor:"KeyId"`
	Message          []byte `json:"Message" cbor:"Message"`
	MessageType      string `json:"MessageType" cbor:"MessageType"`
	SigningAlgorithm string `json:"SigningAlgorithm" cbor:"SigningAlgorithm"`
}

type signResponse struct {
	KeyId            string `json:"KeyId" cbor:"KeyId"`
	Signature        []byte `json:"Signature" cbor:"Signature"`
	SigningAlgorithm string `json:"SigningAlgorithm" cbor:"SigningAlgorithm"`
}

type verifyRequest struct {
	KeyId            string `json:"KeyId" cbor:"KeyId"`
	Message          []byte `json:"Message" cbor:"Message"`
	MessageType      string `json:"MessageType" cbor:"MessageType"`
	Signature        []byte `json:"Signature" cbor:"Signature"`
	SigningAlgorithm string `json:"SigningAlgorithm" cbor:"SigningAlgorithm"`
}

type verifyResponse struct {
	KeyId            string `json:"KeyId" cbor:"KeyId"`
	SignatureValid   bool   `json:"SignatureValid" cbor:"SignatureValid"`
	SigningAlgorithm string `json:"SigningAlgorithm" cbor:"SigningAlgorithm"`
}

type tagResourceRequest struct {
	KeyId string `json:"KeyId" cbor:"KeyId"`
	Tags  []Tag  `json:"Tags" cbor:"Tags"`
}

type untagResourceRequest struct {
	KeyId   string   `json:"KeyId" cbor:"KeyId"`
	TagKeys []string `json:"TagKeys" cbor:"TagKeys"`
}

type listResourceTagsResponse struct {
	Tags      []Tag `json:"Tags" cbor:"Tags"`
	Truncated bool  `json:"Truncated" cbor:"Truncated"`
}

func (h *Handler) publishCtx(ctx context.Context, t events.Type, payload any) {
	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{Type: t, Payload: payload})
	}
}

func (h *Handler) createKeyTyped(ctx context.Context, req *createKeyRequest) (*keyMetadataResponse, *protocol.AWSError) {
	if req.KeySpec == "" {
		req.KeySpec = "SYMMETRIC_DEFAULT"
	}
	if req.KeyUsage == "" {
		req.KeyUsage = "ENCRYPT_DECRYPT"
	}

	keyID := uuid.NewString()
	k := &Key{
		KeyID:       keyID,
		ARN:         fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", h.cfg.Region, h.cfg.AccountID, keyID),
		Description: req.Description,
		KeySpec:     req.KeySpec,
		KeyUsage:    req.KeyUsage,
		Enabled:     true,
		KeyState:    "Enabled",
		CreatedAt:   h.clk.Now(),
	}
	if err := generateKeyMaterial(k); err != nil {
		h.log.Warn("kms: generate key material", zap.Error(err))
		return nil, protocol.ErrInternalError
	}
	if err := h.store.PutKey(ctx, k); err != nil {
		return nil, protocol.ErrInternalError
	}
	h.publishCtx(ctx, events.KMSKeyCreated, events.ResourcePayload{Name: k.KeyID})
	return &keyMetadataResponse{KeyMetadata: h.toMeta(k)}, nil
}

func (h *Handler) describeKeyTyped(ctx context.Context, req *keyIDRequest) (*keyMetadataResponse, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	return &keyMetadataResponse{KeyMetadata: h.toMeta(k)}, nil
}

func (h *Handler) listKeysTyped(ctx context.Context, _ *listKeysRequest) (*listKeysResponse, *protocol.AWSError) {
	keys, err := h.store.ScanKeys(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	entries := make([]keyListEntry, 0, len(keys))
	for _, k := range keys {
		if k.KeyState != "PendingDeletion" {
			entries = append(entries, keyListEntry{KeyId: k.KeyID, KeyArn: k.ARN})
		}
	}
	return &listKeysResponse{Keys: entries, Truncated: false}, nil
}

func (h *Handler) disableKeyTyped(ctx context.Context, req *keyIDRequest) (*struct{}, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	k.Enabled = false
	k.KeyState = "Disabled"
	if err := h.store.PutKey(ctx, k); err != nil {
		return nil, protocol.ErrInternalError
	}
	h.publishCtx(ctx, events.KMSKeyStateChanged, events.ResourcePayload{Name: k.KeyID})
	return &struct{}{}, nil
}

func (h *Handler) enableKeyTyped(ctx context.Context, req *keyIDRequest) (*struct{}, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	k.Enabled = true
	k.KeyState = "Enabled"
	if err := h.store.PutKey(ctx, k); err != nil {
		return nil, protocol.ErrInternalError
	}
	h.publishCtx(ctx, events.KMSKeyStateChanged, events.ResourcePayload{Name: k.KeyID})
	return &struct{}{}, nil
}

func (h *Handler) scheduleKeyDeletionTyped(ctx context.Context, req *scheduleKeyDeletionRequest) (*scheduleKeyDeletionResponse, *protocol.AWSError) {
	if req.PendingWindowInDays <= 0 {
		req.PendingWindowInDays = 30
	}
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	deletionDate := h.clk.Now().Add(time.Duration(req.PendingWindowInDays) * 24 * time.Hour)
	k.Enabled = false
	k.KeyState = "PendingDeletion"
	k.DeletionDate = &deletionDate
	if err := h.store.PutKey(ctx, k); err != nil {
		return nil, protocol.ErrInternalError
	}
	h.publishCtx(ctx, events.KMSKeyDeleted, events.ResourcePayload{Name: k.KeyID})
	return &scheduleKeyDeletionResponse{
		KeyId:        k.KeyID,
		KeyArn:       k.ARN,
		DeletionDate: float64(deletionDate.UnixMilli()) / 1000.0,
		KeyState:     k.KeyState,
	}, nil
}

func (h *Handler) cancelKeyDeletionTyped(ctx context.Context, req *keyIDRequest) (*cancelKeyDeletionResponse, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	k.Enabled = false
	k.KeyState = "Disabled"
	k.DeletionDate = nil
	if err := h.store.PutKey(ctx, k); err != nil {
		return nil, protocol.ErrInternalError
	}
	h.publishCtx(ctx, events.KMSKeyStateChanged, events.ResourcePayload{Name: k.KeyID})
	return &cancelKeyDeletionResponse{KeyId: k.KeyID, KeyArn: k.ARN, KeyState: k.KeyState}, nil
}

func (h *Handler) createAliasTyped(ctx context.Context, req *createAliasRequest) (*struct{}, *protocol.AWSError) {
	if req.AliasName == "" {
		return nil, protocol.ErrMissingParameter("AliasName")
	}
	k, aerr := h.resolveKeyForTyped(ctx, req.TargetKeyId)
	if aerr != nil {
		return nil, aerr
	}
	a := &Alias{
		AliasName:   req.AliasName,
		AliasARN:    fmt.Sprintf("arn:aws:kms:%s:%s:%s", h.cfg.Region, h.cfg.AccountID, req.AliasName),
		TargetKeyID: k.KeyID,
		CreatedAt:   h.clk.Now(),
	}
	if err := h.store.PutAlias(ctx, a); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (h *Handler) deleteAliasTyped(ctx context.Context, req *deleteAliasRequest) (*struct{}, *protocol.AWSError) {
	if err := h.store.DeleteAlias(ctx, req.AliasName); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (h *Handler) listAliasesTyped(ctx context.Context, req *keyIDRequest) (*listAliasesResponse, *protocol.AWSError) {
	filterKeyID := ""
	if req.KeyId != "" {
		k, err := h.resolveKey(ctx, req.KeyId)
		if err != nil {
			return nil, protocol.ErrInternalError
		}
		if k != nil {
			filterKeyID = k.KeyID
		}
	}
	aliases, err := h.store.ScanAliases(ctx, filterKeyID)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	entries := make([]aliasEntry, 0, len(aliases))
	for _, a := range aliases {
		entries = append(entries, aliasEntry{
			AliasName:    a.AliasName,
			AliasArn:     a.AliasARN,
			TargetKeyId:  a.TargetKeyID,
			CreationDate: float64(a.CreatedAt.UnixMilli()) / 1000.0,
		})
	}
	return &listAliasesResponse{Aliases: entries, Truncated: false}, nil
}

func (h *Handler) encryptTyped(ctx context.Context, req *encryptRequest) (*encryptResponse, *protocol.AWSError) {
	k, aerr := h.resolveEnabledKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	ciphertext, err := aesGCMEncrypt(k.AESKey, req.Plaintext, k.KeyID)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &encryptResponse{
		CiphertextBlob:      ciphertext,
		KeyId:               k.ARN,
		EncryptionAlgorithm: "SYMMETRIC_DEFAULT",
	}, nil
}

func (h *Handler) decryptTyped(ctx context.Context, req *decryptRequest) (*decryptResponse, *protocol.AWSError) {
	keyID, nonce, ct, err := parseEnvelope(req.CiphertextBlob)
	if err != nil {
		return nil, errInvalidCiphertext()
	}
	k, err := h.store.GetKey(ctx, keyID)
	if err != nil || k == nil {
		return nil, errNotFound(keyID)
	}
	if !k.Enabled {
		return nil, errDisabled(k.KeyID)
	}
	plaintext, err := aesGCMDecryptRaw(k.AESKey, nonce, ct)
	if err != nil {
		return nil, errInvalidCiphertext()
	}
	return &decryptResponse{
		Plaintext:           plaintext,
		KeyId:               k.ARN,
		EncryptionAlgorithm: "SYMMETRIC_DEFAULT",
	}, nil
}

func (h *Handler) generateDataKeyTyped(ctx context.Context, req *generateDataKeyRequest) (*generateDataKeyResponse, *protocol.AWSError) {
	k, dataKey, ciphertext, aerr := h.generateDataKeyParts(ctx, req)
	if aerr != nil {
		return nil, aerr
	}
	return &generateDataKeyResponse{Plaintext: dataKey, CiphertextBlob: ciphertext, KeyId: k.ARN}, nil
}

func (h *Handler) generateDataKeyWithoutPlaintextTyped(ctx context.Context, req *generateDataKeyRequest) (*generateDataKeyWithoutPlaintextResponse, *protocol.AWSError) {
	k, _, ciphertext, aerr := h.generateDataKeyParts(ctx, req)
	if aerr != nil {
		return nil, aerr
	}
	return &generateDataKeyWithoutPlaintextResponse{CiphertextBlob: ciphertext, KeyId: k.ARN}, nil
}

func (h *Handler) signTyped(ctx context.Context, req *signRequest) (*signResponse, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	privKey, err := parseRSAPrivateKey(k.RSAPrivKey)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	digest := sha256.Sum256(req.Message)
	sig, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, digest[:])
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &signResponse{KeyId: k.ARN, Signature: sig, SigningAlgorithm: req.SigningAlgorithm}, nil
}

func (h *Handler) verifyTyped(ctx context.Context, req *verifyRequest) (*verifyResponse, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	privKey, err := parseRSAPrivateKey(k.RSAPrivKey)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	digest := sha256.Sum256(req.Message)
	verifyErr := rsa.VerifyPKCS1v15(&privKey.PublicKey, crypto.SHA256, digest[:], req.Signature)
	return &verifyResponse{
		KeyId:            k.ARN,
		SignatureValid:   verifyErr == nil,
		SigningAlgorithm: req.SigningAlgorithm,
	}, nil
}

func (h *Handler) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (*struct{}, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	for _, t := range req.Tags {
		replaced := false
		for i, existing := range k.Tags {
			if existing.TagKey == t.TagKey {
				k.Tags[i].TagValue = t.TagValue
				replaced = true
				break
			}
		}
		if !replaced {
			k.Tags = append(k.Tags, t)
		}
	}
	if err := h.store.PutKey(ctx, k); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (h *Handler) untagResourceTyped(ctx context.Context, req *untagResourceRequest) (*struct{}, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	remove := make(map[string]bool, len(req.TagKeys))
	for _, key := range req.TagKeys {
		remove[key] = true
	}
	filtered := k.Tags[:0]
	for _, t := range k.Tags {
		if !remove[t.TagKey] {
			filtered = append(filtered, t)
		}
	}
	k.Tags = filtered
	if err := h.store.PutKey(ctx, k); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (h *Handler) listResourceTagsTyped(ctx context.Context, req *keyIDRequest) (*listResourceTagsResponse, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	tags := k.Tags
	if tags == nil {
		tags = []Tag{}
	}
	return &listResourceTagsResponse{Tags: tags, Truncated: false}, nil
}

func (h *Handler) resolveKeyForTyped(ctx context.Context, keyID string) (*Key, *protocol.AWSError) {
	k, err := h.resolveKey(ctx, keyID)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if k == nil {
		return nil, errNotFound(keyID)
	}
	return k, nil
}

func (h *Handler) resolveEnabledKeyForTyped(ctx context.Context, keyID string) (*Key, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, keyID)
	if aerr != nil {
		return nil, aerr
	}
	if !k.Enabled {
		return nil, errDisabled(k.KeyID)
	}
	return k, nil
}

func (h *Handler) generateDataKeyParts(ctx context.Context, req *generateDataKeyRequest) (*Key, []byte, []byte, *protocol.AWSError) {
	k, aerr := h.resolveEnabledKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, nil, nil, aerr
	}
	keyLen := 32
	if req.KeySpec == "AES_128" {
		keyLen = 16
	}
	dataKey := make([]byte, keyLen)
	if _, err := rand.Read(dataKey); err != nil {
		return nil, nil, nil, protocol.ErrInternalError
	}
	ciphertext, err := aesGCMEncrypt(k.AESKey, dataKey, k.KeyID)
	if err != nil {
		return nil, nil, nil, protocol.ErrInternalError
	}
	return k, dataKey, ciphertext, nil
}

func errInvalidCiphertext() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidCiphertextException",
		Message:    "The ciphertext is not valid.",
		HTTPStatus: 400,
	}
}

// ── GetPublicKey typed ──────────────────────────────────────────────────────

type getPublicKeyResponse struct {
	CustomerMasterKeySpec  string   `json:"CustomerMasterKeySpec" cbor:"CustomerMasterKeySpec"`
	KeyId                  string   `json:"KeyId" cbor:"KeyId"`
	PublicKey              []byte   `json:"PublicKey" cbor:"PublicKey"`
	KeySpec                string   `json:"KeySpec" cbor:"KeySpec"`
	KeyUsage               string   `json:"KeyUsage" cbor:"KeyUsage"`
	SigningAlgorithms      []string `json:"SigningAlgorithms,omitempty" cbor:"SigningAlgorithms,omitempty"`
	EncryptionAlgorithms   []string `json:"EncryptionAlgorithms,omitempty" cbor:"EncryptionAlgorithms,omitempty"`
	KeyAgreementAlgorithms []string `json:"KeyAgreementAlgorithms,omitempty" cbor:"KeyAgreementAlgorithms,omitempty"`
}

func (h *Handler) getPublicKeyTyped(ctx context.Context, req *keyIDRequest) (*getPublicKeyResponse, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	if k.RSAPrivKey == nil {
		return nil, &protocol.AWSError{
			Code: "UnsupportedOperationException", Message: "GetPublicKey is only supported for asymmetric keys.",
			HTTPStatus: 400,
		}
	}
	privKey, err := parseRSAPrivateKey(k.RSAPrivKey)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	resp := &getPublicKeyResponse{
		CustomerMasterKeySpec: k.KeySpec,
		KeyId:                 k.ARN,
		PublicKey:             pubDER,
		KeySpec:               k.KeySpec,
		KeyUsage:              k.KeyUsage,
	}
	if k.KeyUsage == "SIGN_VERIFY" {
		resp.SigningAlgorithms = []string{
			"RSASSA_PKCS1_V1_5_SHA_256", "RSASSA_PKCS1_V1_5_SHA_384", "RSASSA_PKCS1_V1_5_SHA_512",
			"RSASSA_PSS_SHA_256", "RSASSA_PSS_SHA_384", "RSASSA_PSS_SHA_512",
		}
	} else if k.KeyUsage == "ENCRYPT_DECRYPT" {
		resp.EncryptionAlgorithms = []string{"RSAES_OAEP_SHA_1", "RSAES_OAEP_SHA_256"}
	}
	return resp, nil
}

// ── UpdateAlias typed ────────────────────────────────────────────────────────

type updateAliasRequest struct {
	AliasName   string `json:"AliasName" cbor:"AliasName"`
	TargetKeyId string `json:"TargetKeyId" cbor:"TargetKeyId"`
}

func (h *Handler) updateAliasTyped(ctx context.Context, req *updateAliasRequest) (*struct{}, *protocol.AWSError) {
	if req.AliasName == "" {
		return nil, protocol.ErrMissingParameter("AliasName")
	}
	a, err := h.store.GetAlias(ctx, req.AliasName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if a == nil {
		return nil, errNotFound(req.AliasName)
	}
	k, aerr := h.resolveKeyForTyped(ctx, req.TargetKeyId)
	if aerr != nil {
		return nil, aerr
	}
	a.TargetKeyID = k.KeyID
	if err := h.store.PutAlias(ctx, a); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

// ── ReEncrypt typed ─────────────────────────────────────────────────────────

type reEncryptRequest struct {
	CiphertextBlob                 []byte `json:"CiphertextBlob" cbor:"CiphertextBlob"`
	DestinationKeyId               string `json:"DestinationKeyId" cbor:"DestinationKeyId"`
	SourceKeyId                    string `json:"SourceKeyId" cbor:"SourceKeyId"`
	SourceEncryptionAlgorithm      string `json:"SourceEncryptionAlgorithm" cbor:"SourceEncryptionAlgorithm"`
	DestinationEncryptionAlgorithm string `json:"DestinationEncryptionAlgorithm" cbor:"DestinationEncryptionAlgorithm"`
}

type reEncryptResponse struct {
	CiphertextBlob                 []byte `json:"CiphertextBlob" cbor:"CiphertextBlob"`
	KeyId                          string `json:"KeyId" cbor:"KeyId"`
	SourceKeyId                    string `json:"SourceKeyId" cbor:"SourceKeyId"`
	SourceEncryptionAlgorithm      string `json:"SourceEncryptionAlgorithm" cbor:"SourceEncryptionAlgorithm"`
	DestinationEncryptionAlgorithm string `json:"DestinationEncryptionAlgorithm" cbor:"DestinationEncryptionAlgorithm"`
}

func (h *Handler) reEncryptTyped(ctx context.Context, req *reEncryptRequest) (*reEncryptResponse, *protocol.AWSError) {
	keyID, nonce, ct, err := parseEnvelope(req.CiphertextBlob)
	if err != nil {
		return nil, errInvalidCiphertext()
	}
	srcKey, err := h.store.GetKey(ctx, keyID)
	if err != nil || srcKey == nil {
		return nil, errNotFound(keyID)
	}
	if !srcKey.Enabled {
		return nil, errDisabled(srcKey.KeyID)
	}
	plaintext, decErr := aesGCMDecryptRaw(srcKey.AESKey, nonce, ct)
	if decErr != nil {
		return nil, errInvalidCiphertext()
	}
	dstKey, aerr := h.resolveEnabledKeyForTyped(ctx, req.DestinationKeyId)
	if aerr != nil {
		return nil, aerr
	}
	newCT, encErr := aesGCMEncrypt(dstKey.AESKey, plaintext, dstKey.KeyID)
	if encErr != nil {
		return nil, protocol.ErrInternalError
	}
	return &reEncryptResponse{
		CiphertextBlob:                 newCT,
		KeyId:                          dstKey.ARN,
		SourceKeyId:                    srcKey.ARN,
		SourceEncryptionAlgorithm:      "SYMMETRIC_DEFAULT",
		DestinationEncryptionAlgorithm: "SYMMETRIC_DEFAULT",
	}, nil
}

// ── GenerateDataKeyPair typed ───────────────────────────────────────────────

type generateDataKeyPairRequest struct {
	KeyId       string `json:"KeyId" cbor:"KeyId"`
	KeyPairSpec string `json:"KeyPairSpec" cbor:"KeyPairSpec"` // "RSA_2048", "RSA_3072", "RSA_4096"
}

type generateDataKeyPairResponse struct {
	KeyId                    string `json:"KeyId" cbor:"KeyId"`
	KeyPairSpec              string `json:"KeyPairSpec" cbor:"KeyPairSpec"`
	PrivateKeyCiphertextBlob []byte `json:"PrivateKeyCiphertextBlob" cbor:"PrivateKeyCiphertextBlob"`
	PrivateKeyPlaintext      []byte `json:"PrivateKeyPlaintext" cbor:"PrivateKeyPlaintext"`
	PublicKey                []byte `json:"PublicKey" cbor:"PublicKey"`
}

func (h *Handler) generateDataKeyPairTyped(ctx context.Context, req *generateDataKeyPairRequest) (*generateDataKeyPairResponse, *protocol.AWSError) {
	if req.KeyPairSpec == "" {
		req.KeyPairSpec = "RSA_2048"
	}
	k, aerr := h.resolveEnabledKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	bits := 2048
	switch req.KeyPairSpec {
	case "RSA_3072":
		bits = 3072
	case "RSA_4096":
		bits = 4096
	}
	priv, genErr := rsa.GenerateKey(rand.Reader, bits)
	if genErr != nil {
		return nil, protocol.ErrInternalError
	}
	privDER := x509.MarshalPKCS1PrivateKey(priv)
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privDER})
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	privateCT, encErr := aesGCMEncrypt(k.AESKey, privPEM, k.KeyID)
	if encErr != nil {
		return nil, protocol.ErrInternalError
	}
	return &generateDataKeyPairResponse{
		KeyId: k.ARN, KeyPairSpec: req.KeyPairSpec,
		PrivateKeyCiphertextBlob: privateCT, PrivateKeyPlaintext: privPEM, PublicKey: pubPEM,
	}, nil
}

// ── VerifyMac typed ─────────────────────────────────────────────────────────

type verifyMacRequest struct {
	KeyId        string `json:"KeyId" cbor:"KeyId"`
	Message      []byte `json:"Message" cbor:"Message"`
	Mac          []byte `json:"Mac" cbor:"Mac"`
	MacAlgorithm string `json:"MacAlgorithm" cbor:"MacAlgorithm"`
}

type verifyMacResponse struct {
	KeyId        string `json:"KeyId" cbor:"KeyId"`
	MacValid     bool   `json:"MacValid" cbor:"MacValid"`
	MacAlgorithm string `json:"MacAlgorithm" cbor:"MacAlgorithm"`
}

func (h *Handler) verifyMacTyped(ctx context.Context, req *verifyMacRequest) (*verifyMacResponse, *protocol.AWSError) {
	k, aerr := h.resolveEnabledKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	var hash crypto.Hash
	switch req.MacAlgorithm {
	case "HMAC_SHA_256":
		hash = crypto.SHA256
	case "HMAC_SHA_384":
		hash = crypto.SHA384
	case "HMAC_SHA_512":
		hash = crypto.SHA512
	default:
		hash = crypto.SHA256
	}
	mac := hmac.New(sha256.New, k.AESKey)
	if hash == crypto.SHA384 {
		mac = hmac.New(sha512.New384, k.AESKey)
	} else if hash == crypto.SHA512 {
		mac = hmac.New(sha512.New, k.AESKey)
	}
	mac.Write(req.Message)
	expected := mac.Sum(nil)
	return &verifyMacResponse{
		KeyId: k.ARN, MacValid: hmac.Equal(expected, req.Mac), MacAlgorithm: req.MacAlgorithm,
	}, nil
}

// ── Key policies typed ──────────────────────────────────────────────────────

type keyPolicyRequest struct {
	KeyId      string `json:"KeyId" cbor:"KeyId"`
	PolicyName string `json:"PolicyName" cbor:"PolicyName"`
}

type getKeyPolicyResponse struct {
	Policy     string `json:"Policy" cbor:"Policy"`
	PolicyName string `json:"PolicyName" cbor:"PolicyName"`
}

func (h *Handler) getKeyPolicyTyped(ctx context.Context, req *keyPolicyRequest) (*getKeyPolicyResponse, *protocol.AWSError) {
	if req.PolicyName == "" {
		req.PolicyName = "default"
	}
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	policy := k.Policy
	if policy == "" {
		policy = fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Sid":"Enable IAM User Permissions","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::%s:root"},"Action":"kms:*","Resource":"%s"}]}`, h.cfg.AccountID, k.ARN)
	}
	return &getKeyPolicyResponse{Policy: policy, PolicyName: req.PolicyName}, nil
}

type putKeyPolicyRequest struct {
	KeyId      string `json:"KeyId" cbor:"KeyId"`
	PolicyName string `json:"PolicyName" cbor:"PolicyName"`
	Policy     string `json:"Policy" cbor:"Policy"`
}

func (h *Handler) putKeyPolicyTyped(ctx context.Context, req *putKeyPolicyRequest) (*struct{}, *protocol.AWSError) {
	if req.PolicyName == "" {
		req.PolicyName = "default"
	}
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	k.Policy = req.Policy
	if err := h.store.PutKey(ctx, k); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

type listKeyPoliciesResponse struct {
	PolicyNames []string `json:"PolicyNames" cbor:"PolicyNames"`
	Truncated   bool     `json:"Truncated" cbor:"Truncated"`
}

func (h *Handler) listKeyPoliciesTyped(ctx context.Context, req *keyIDRequest) (*listKeyPoliciesResponse, *protocol.AWSError) {
	_, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	return &listKeyPoliciesResponse{PolicyNames: []string{"default"}, Truncated: false}, nil
}

// ── Grants typed ────────────────────────────────────────────────────────────

type createGrantRequest struct {
	KeyId             string           `json:"KeyId" cbor:"KeyId"`
	GranteePrincipal  string           `json:"GranteePrincipal" cbor:"GranteePrincipal"`
	RetiringPrincipal string           `json:"RetiringPrincipal" cbor:"RetiringPrincipal"`
	Operations        []string         `json:"Operations" cbor:"Operations"`
	Constraints       *GrantConstraint `json:"Constraints" cbor:"Constraints"`
	GrantTokens       []string         `json:"GrantTokens" cbor:"GrantTokens"`
	Name              string           `json:"Name" cbor:"Name"`
}

type createGrantResponse struct {
	GrantId    string `json:"GrantId" cbor:"GrantId"`
	GrantToken string `json:"GrantToken" cbor:"GrantToken"`
}

func (h *Handler) createGrantTyped(ctx context.Context, req *createGrantRequest) (*createGrantResponse, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	grantID := uuid.NewString()
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, protocol.ErrInternalError
	}
	grantToken := hex.EncodeToString(tokenBytes)
	g := &Grant{
		GrantID: grantID, GrantToken: grantToken, KeyID: k.KeyID,
		GranteePrincipal: req.GranteePrincipal, RetiringPrincipal: req.RetiringPrincipal,
		Operations: req.Operations, Constraints: req.Constraints,
		Name: req.Name, CreationDate: h.clk.Now(),
	}
	if err := h.store.PutGrant(ctx, g); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &createGrantResponse{GrantId: grantID, GrantToken: grantToken}, nil
}

type listGrantsRequest struct {
	KeyId            string `json:"KeyId" cbor:"KeyId"`
	GrantId          string `json:"GrantId" cbor:"GrantId"`
	GranteePrincipal string `json:"GranteePrincipal" cbor:"GranteePrincipal"`
}

type grantEntry struct {
	GrantID           string           `json:"GrantId" cbor:"GrantId"`
	GranteePrincipal  string           `json:"GranteePrincipal" cbor:"GranteePrincipal"`
	RetiringPrincipal string           `json:"RetiringPrincipal,omitempty" cbor:"RetiringPrincipal,omitempty"`
	Operations        []string         `json:"Operations" cbor:"Operations"`
	Constraints       *GrantConstraint `json:"Constraints,omitempty" cbor:"Constraints,omitempty"`
	Name              string           `json:"Name,omitempty" cbor:"Name,omitempty"`
	CreationDate      float64          `json:"CreationDate" cbor:"CreationDate"`
	KeyId             string           `json:"KeyId" cbor:"KeyId"`
}

type listGrantsResponse struct {
	Grants    []grantEntry `json:"Grants" cbor:"Grants"`
	Truncated bool         `json:"Truncated" cbor:"Truncated"`
}

func (h *Handler) listGrantsTyped(ctx context.Context, req *listGrantsRequest) (*listGrantsResponse, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	grants, err := h.store.ScanGrantsByKey(ctx, k.KeyID)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if req.GrantId != "" {
		filtered := grants[:0]
		for _, g := range grants {
			if g.GrantID == req.GrantId {
				filtered = append(filtered, g)
			}
		}
		grants = filtered
	}
	if req.GranteePrincipal != "" {
		filtered := grants[:0]
		for _, g := range grants {
			if g.GranteePrincipal == req.GranteePrincipal {
				filtered = append(filtered, g)
			}
		}
		grants = filtered
	}
	entries := make([]grantEntry, 0, len(grants))
	for _, g := range grants {
		entries = append(entries, grantEntry{
			GrantID: g.GrantID, GranteePrincipal: g.GranteePrincipal,
			RetiringPrincipal: g.RetiringPrincipal, Operations: g.Operations,
			Constraints: g.Constraints, Name: g.Name,
			CreationDate: float64(g.CreationDate.UnixMilli()) / 1000.0, KeyId: k.ARN,
		})
	}
	return &listGrantsResponse{Grants: entries, Truncated: false}, nil
}

type revokeGrantRequest struct {
	KeyId   string `json:"KeyId" cbor:"KeyId"`
	GrantId string `json:"GrantId" cbor:"GrantId"`
}

func (h *Handler) revokeGrantTyped(ctx context.Context, req *revokeGrantRequest) (*struct{}, *protocol.AWSError) {
	k, aerr := h.resolveKeyForTyped(ctx, req.KeyId)
	if aerr != nil {
		return nil, aerr
	}
	g, err := h.store.GetGrant(ctx, req.GrantId)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if g == nil || g.KeyID != k.KeyID {
		return nil, errNotFound(req.GrantId)
	}
	if err := h.store.DeleteGrant(ctx, req.GrantId); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

type retireGrantRequest struct {
	KeyId      string `json:"KeyId" cbor:"KeyId"`
	GrantId    string `json:"GrantId" cbor:"GrantId"`
	GrantToken string `json:"GrantToken" cbor:"GrantToken"`
}

func (h *Handler) retireGrantTyped(ctx context.Context, req *retireGrantRequest) (*struct{}, *protocol.AWSError) {
	if req.GrantToken != "" {
		grants, err := h.store.ScanAllGrants(ctx)
		if err != nil {
			return nil, protocol.ErrInternalError
		}
		found := false
		for _, g := range grants {
			if g.GrantToken == req.GrantToken {
				req.GrantId = g.GrantID
				found = true
				break
			}
		}
		if !found {
			return nil, errNotFound(req.GrantToken)
		}
	}
	g, err := h.store.GetGrant(ctx, req.GrantId)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if g == nil {
		return nil, errNotFound(req.GrantId)
	}
	if err := h.store.DeleteGrant(ctx, req.GrantId); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

type listRetirableGrantsRequest struct {
	RetiringPrincipal string `json:"RetiringPrincipal" cbor:"RetiringPrincipal"`
}

type retirableGrantEntry struct {
	GrantID          string   `json:"GrantId" cbor:"GrantId"`
	GranteePrincipal string   `json:"GranteePrincipal" cbor:"GranteePrincipal"`
	Operations       []string `json:"Operations" cbor:"Operations"`
	CreationDate     float64  `json:"CreationDate" cbor:"CreationDate"`
	Name             string   `json:"Name,omitempty" cbor:"Name,omitempty"`
}

type listRetirableGrantsResponse struct {
	Grants    []retirableGrantEntry `json:"Grants" cbor:"Grants"`
	Truncated bool                  `json:"Truncated" cbor:"Truncated"`
}

func (h *Handler) listRetirableGrantsTyped(ctx context.Context, req *listRetirableGrantsRequest) (*listRetirableGrantsResponse, *protocol.AWSError) {
	grants, err := h.store.ScanGrantsByPrincipal(ctx, req.RetiringPrincipal)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	entries := make([]retirableGrantEntry, 0, len(grants))
	for _, g := range grants {
		entries = append(entries, retirableGrantEntry{
			GrantID: g.GrantID, GranteePrincipal: g.GranteePrincipal,
			Operations: g.Operations, Name: g.Name,
			CreationDate: float64(g.CreationDate.UnixMilli()) / 1000.0,
		})
	}
	return &listRetirableGrantsResponse{Grants: entries, Truncated: false}, nil
}
