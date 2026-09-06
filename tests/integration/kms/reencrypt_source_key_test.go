package kms_test

// ReEncrypt binds SourceKeyId to the ciphertext, per
// https://docs.aws.amazon.com/kms/latest/APIReference/API_ReEncrypt.html:
// "Enter a key ID of the KMS key that was used to encrypt the ciphertext. If
// you identify a different KMS key, the ReEncrypt operation throws an
// IncorrectKeyException." The parameter stays optional for symmetric
// ciphertext — "AWS KMS can get this information from metadata that it adds to
// the symmetric ciphertext blob" — so an omitted SourceKeyId must still
// succeed, and an alias or ARN of the producing key must still match.
//
// It is the same rule #1710 added to Decrypt; IncorrectKeyException is the one
// exception whose documented description names both operations (#1844).

import (
	"net/http"
	"strings"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// hasKeyARNSuffix reports whether value is a KMS key ARN for keyID rather than
// the bare key id.
func hasKeyARNSuffix(value, keyID string) bool {
	return strings.HasPrefix(value, "arn:aws:kms:") && strings.HasSuffix(value, ":key/"+keyID)
}

func TestReEncrypt_sourceKeyIdMatchesProducingKey(t *testing.T) {
	// Given: a ciphertext produced by key A, and a destination key
	srv := helpers.NewTestServer(t)
	keyA := createKey(t, srv, "reencrypt-src-a")
	dst := createKey(t, srv, "reencrypt-dst")
	blob := encryptUnder(t, srv, keyA, []byte("day 1 for the internet"))

	// When: re-encrypting while naming key A as the source
	resp := kmsCall(t, srv, "ReEncrypt", map[string]any{
		"CiphertextBlob":   blob,
		"SourceKeyId":      keyA,
		"DestinationKeyId": dst,
	})
	defer resp.Body.Close()

	// Then: it succeeds and reports both keys
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		CiphertextBlob []byte `json:"CiphertextBlob"`
		KeyId          string `json:"KeyId"`
		SourceKeyId    string `json:"SourceKeyId"`
	}
	decodeJSON(t, resp, &out)
	if len(out.CiphertextBlob) == 0 {
		t.Error("expected a non-empty CiphertextBlob")
	}
	// Both are documented as the key ARN in the reference's example response.
	if !hasKeyARNSuffix(out.SourceKeyId, keyA) {
		t.Errorf("SourceKeyId = %q, want the key ARN ending in :key/%s", out.SourceKeyId, keyA)
	}
	if !hasKeyARNSuffix(out.KeyId, dst) {
		t.Errorf("KeyId = %q, want the key ARN ending in :key/%s", out.KeyId, dst)
	}
}

func TestReEncrypt_sourceKeyIdFromDifferentKey(t *testing.T) {
	// Given: a ciphertext produced by key A, and an unrelated key B
	srv := helpers.NewTestServer(t)
	keyA := createKey(t, srv, "reencrypt-src-a")
	keyB := createKey(t, srv, "reencrypt-src-b")
	dst := createKey(t, srv, "reencrypt-dst")
	blob := encryptUnder(t, srv, keyA, []byte("secret"))

	// When: re-encrypting the blob while naming key B as the source
	resp := kmsCall(t, srv, "ReEncrypt", map[string]any{
		"CiphertextBlob":   blob,
		"SourceKeyId":      keyB,
		"DestinationKeyId": dst,
	})
	defer resp.Body.Close()

	// Then: AWS rejects it rather than re-encrypting anyway
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "IncorrectKeyException")
}

func TestReEncrypt_sourceKeyIdAliasForProducingKey(t *testing.T) {
	// Given: a ciphertext produced by a key that also has an alias
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "reencrypt-alias-key")
	dst := createKey(t, srv, "reencrypt-alias-dst")
	aliasResp := kmsCall(t, srv, "CreateAlias", map[string]any{
		"AliasName":   "alias/reencrypt-source",
		"TargetKeyId": keyID,
	})
	aliasResp.Body.Close()
	helpers.AssertStatus(t, aliasResp, http.StatusOK)
	blob := encryptUnder(t, srv, keyID, []byte("secret"))

	// When: re-encrypting with the alias as SourceKeyId
	resp := kmsCall(t, srv, "ReEncrypt", map[string]any{
		"CiphertextBlob":   blob,
		"SourceKeyId":      "alias/reencrypt-source",
		"DestinationKeyId": dst,
	})
	defer resp.Body.Close()

	// Then: the alias resolves to the producing key and the call succeeds
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestReEncrypt_withoutSourceKeyIdStillResolvesFromCiphertext(t *testing.T) {
	// Given: a ciphertext produced by a key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "reencrypt-no-sourcekeyid")
	dst := createKey(t, srv, "reencrypt-no-sourcekeyid-dst")
	blob := encryptUnder(t, srv, keyID, []byte("secret"))

	// When: re-encrypting without naming a source key at all
	resp := kmsCall(t, srv, "ReEncrypt", map[string]any{
		"CiphertextBlob":   blob,
		"DestinationKeyId": dst,
	})
	defer resp.Body.Close()

	// Then: the ciphertext metadata still identifies the source key —
	// SourceKeyId is optional for symmetric ciphertext
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestReEncrypt_sourceKeyIdUnknownKey(t *testing.T) {
	// Given: a ciphertext produced by a key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "reencrypt-unknown-source")
	dst := createKey(t, srv, "reencrypt-unknown-source-dst")
	blob := encryptUnder(t, srv, keyID, []byte("secret"))

	// When: SourceKeyId names a key that does not exist
	resp := kmsCall(t, srv, "ReEncrypt", map[string]any{
		"CiphertextBlob":   blob,
		"SourceKeyId":      "00000000-0000-0000-0000-000000000000",
		"DestinationKeyId": dst,
	})
	defer resp.Body.Close()

	// Then: it is a NotFoundException, not a silent success
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "NotFoundException")
}

func TestRPCv2CBOR_ReEncrypt_sourceKeyIdFromDifferentKey(t *testing.T) {
	// Given: a ciphertext produced by key A, over CBOR
	srv := helpers.NewTestServer(t)
	keyA := createKeyCBOR(t, srv, "cbor-reencrypt-a")
	keyB := createKeyCBOR(t, srv, "cbor-reencrypt-b")
	dst := createKeyCBOR(t, srv, "cbor-reencrypt-dst")

	encResp := kmsCBORCall(t, srv, "Encrypt", map[string]any{
		"KeyId":     keyA,
		"Plaintext": []byte("secret"),
	})
	helpers.AssertStatus(t, encResp, http.StatusOK)
	var enc struct {
		CiphertextBlob []byte `cbor:"CiphertextBlob"`
	}
	if err := cborlib.NewDecoder(encResp.Body).Decode(&enc); err != nil {
		t.Fatalf("decode CBOR Encrypt response: %v", err)
	}
	encResp.Body.Close()

	// When: re-encrypting the blob while naming key B as the source
	resp := kmsCBORCall(t, srv, "ReEncrypt", map[string]any{
		"CiphertextBlob":   enc.CiphertextBlob,
		"SourceKeyId":      keyB,
		"DestinationKeyId": dst,
	})
	defer resp.Body.Close()

	// Then: the typed path rejects it too
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var errBody map[string]string
	if err := cborlib.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode CBOR error body: %v", err)
	}
	if errBody["__type"] != "IncorrectKeyException" {
		t.Errorf("__type = %q, want IncorrectKeyException", errBody["__type"])
	}
}
