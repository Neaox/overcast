package kms_test

// Request validation and ciphertext/key binding on the crypto operations.
//
// AWS reference:
//   - https://docs.aws.amazon.com/kms/latest/APIReference/API_Decrypt.html
//     "If you identify a different KMS key, the Decrypt operation throws an
//     IncorrectKeyException" (HTTP 400).
//   - https://docs.aws.amazon.com/kms/latest/APIReference/API_Encrypt.html
//     Plaintext Length Constraints: "Minimum length of 1. Maximum length of
//     4096."
//   - https://docs.aws.amazon.com/kms/latest/APIReference/API_ScheduleKeyDeletion.html
//     PendingWindowInDays Valid Range: "Minimum value of 7. Maximum value of
//     30", defaulting to 30 when omitted.

import (
	"bytes"
	"net/http"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// encryptUnder encrypts plaintext under keyID and returns the ciphertext blob.
func encryptUnder(t *testing.T, srv *helpers.TestServer, keyID string, plaintext []byte) []byte {
	t.Helper()
	resp := kmsCall(t, srv, "Encrypt", map[string]any{"KeyId": keyID, "Plaintext": plaintext})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		CiphertextBlob []byte `json:"CiphertextBlob"`
	}
	decodeJSON(t, resp, &out)
	return out.CiphertextBlob
}

func TestDecrypt_keyIdMatchesProducingKey(t *testing.T) {
	// Given: a ciphertext produced by key A
	srv := helpers.NewTestServer(t)
	keyA := createKey(t, srv, "decrypt-key-a")
	blob := encryptUnder(t, srv, keyA, []byte("day 1 for the internet"))

	// When: decrypting with that same key named explicitly
	resp := kmsCall(t, srv, "Decrypt", map[string]any{
		"KeyId":          keyA,
		"CiphertextBlob": blob,
	})
	defer resp.Body.Close()

	// Then: the plaintext comes back
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Plaintext []byte `json:"Plaintext"`
	}
	decodeJSON(t, resp, &out)
	if string(out.Plaintext) != "day 1 for the internet" {
		t.Errorf("Plaintext = %q, want %q", out.Plaintext, "day 1 for the internet")
	}
}

func TestDecrypt_keyIdFromDifferentKey(t *testing.T) {
	// Given: a ciphertext produced by key A, and an unrelated key B
	srv := helpers.NewTestServer(t)
	keyA := createKey(t, srv, "decrypt-key-a")
	keyB := createKey(t, srv, "decrypt-key-b")
	blob := encryptUnder(t, srv, keyA, []byte("secret"))

	// When: decrypting the blob while naming key B
	resp := kmsCall(t, srv, "Decrypt", map[string]any{
		"KeyId":          keyB,
		"CiphertextBlob": blob,
	})
	defer resp.Body.Close()

	// Then: AWS rejects it rather than decrypting anyway
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "IncorrectKeyException")
}

func TestDecrypt_keyIdAliasForProducingKey(t *testing.T) {
	// Given: a ciphertext produced by a key that also has an alias
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "decrypt-alias-key")
	aliasResp := kmsCall(t, srv, "CreateAlias", map[string]any{
		"AliasName":   "alias/decrypt-target",
		"TargetKeyId": keyID,
	})
	aliasResp.Body.Close()
	helpers.AssertStatus(t, aliasResp, http.StatusOK)
	blob := encryptUnder(t, srv, keyID, []byte("secret"))

	// When: decrypting with the alias as KeyId
	resp := kmsCall(t, srv, "Decrypt", map[string]any{
		"KeyId":          "alias/decrypt-target",
		"CiphertextBlob": blob,
	})
	defer resp.Body.Close()

	// Then: the alias resolves to the producing key and decryption succeeds
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestDecrypt_withoutKeyIdStillResolvesFromCiphertext(t *testing.T) {
	// Given: a ciphertext produced by a key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "decrypt-no-keyid")
	blob := encryptUnder(t, srv, keyID, []byte("secret"))

	// When: decrypting without naming a key at all
	resp := kmsCall(t, srv, "Decrypt", map[string]any{"CiphertextBlob": blob})
	defer resp.Body.Close()

	// Then: the ciphertext metadata still identifies the key
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestEncrypt_plaintextAtLimit(t *testing.T) {
	// Given: a symmetric key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "encrypt-limit")

	// When: encrypting exactly 4096 bytes
	resp := kmsCall(t, srv, "Encrypt", map[string]any{
		"KeyId":     keyID,
		"Plaintext": bytes.Repeat([]byte("a"), 4096),
	})
	defer resp.Body.Close()

	// Then: it is accepted
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestEncrypt_plaintextOverLimit(t *testing.T) {
	// Given: a symmetric key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "encrypt-over-limit")

	// When: encrypting 4097 bytes
	resp := kmsCall(t, srv, "Encrypt", map[string]any{
		"KeyId":     keyID,
		"Plaintext": bytes.Repeat([]byte("a"), 4097),
	})
	defer resp.Body.Close()

	// Then: AWS rejects it
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestScheduleKeyDeletion_pendingWindowOutOfRange(t *testing.T) {
	// Given: a key
	srv := helpers.NewTestServer(t)

	for _, days := range []int{0, 2, 6, 31, 365} {
		keyID := createKey(t, srv, "sched-range")

		// When: PendingWindowInDays falls outside 7–30
		resp := kmsCall(t, srv, "ScheduleKeyDeletion", map[string]any{
			"KeyId":               keyID,
			"PendingWindowInDays": days,
		})

		// Then: AWS rejects the request
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertJSONError(t, resp, "ValidationException")
		resp.Body.Close()

		// And: the key is untouched
		descResp := kmsCall(t, srv, "DescribeKey", map[string]any{"KeyId": keyID})
		var meta struct {
			KeyMetadata struct {
				KeyState string `json:"KeyState"`
			} `json:"KeyMetadata"`
		}
		decodeJSON(t, descResp, &meta)
		descResp.Body.Close()
		if meta.KeyMetadata.KeyState != "Enabled" {
			t.Errorf("PendingWindowInDays=%d: KeyState = %q, want Enabled", days, meta.KeyMetadata.KeyState)
		}
	}
}

func TestScheduleKeyDeletion_pendingWindowAtBounds(t *testing.T) {
	// Given: keys to delete
	srv := helpers.NewTestServer(t)

	for _, days := range []int{7, 30} {
		keyID := createKey(t, srv, "sched-bounds")

		// When: PendingWindowInDays is at a boundary of the valid range
		resp := kmsCall(t, srv, "ScheduleKeyDeletion", map[string]any{
			"KeyId":               keyID,
			"PendingWindowInDays": days,
		})

		// Then: it is accepted
		helpers.AssertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
}

func TestScheduleKeyDeletion_pendingWindowOmittedDefaultsTo30(t *testing.T) {
	// Given: a key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "sched-default")

	// When: no waiting period is supplied
	resp := kmsCall(t, srv, "ScheduleKeyDeletion", map[string]any{"KeyId": keyID})
	defer resp.Body.Close()

	// Then: the key is scheduled with AWS's 30-day default
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		KeyState string `json:"KeyState"`
	}
	decodeJSON(t, resp, &out)
	if out.KeyState != "PendingDeletion" {
		t.Errorf("KeyState = %q, want PendingDeletion", out.KeyState)
	}
}

func TestRPCv2CBOR_Decrypt_keyIdFromDifferentKey(t *testing.T) {
	// Given: a ciphertext produced by key A, over CBOR
	srv := helpers.NewTestServer(t)
	keyA := createKeyCBOR(t, srv, "cbor-decrypt-a")
	keyB := createKeyCBOR(t, srv, "cbor-decrypt-b")

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

	// When: decrypting the blob while naming key B
	resp := kmsCBORCall(t, srv, "Decrypt", map[string]any{
		"KeyId":          keyB,
		"CiphertextBlob": enc.CiphertextBlob,
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
