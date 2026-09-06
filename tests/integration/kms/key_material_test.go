package kms_test

// Key material length tests for GenerateDataKey, GenerateDataKeyWithoutPlaintext
// and GenerateRandom.
//
// AWS reference:
//   - https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateDataKey.html
//     "You must specify either the KeySpec or the NumberOfBytes parameter (but
//     not both) in every GenerateDataKey request." NumberOfBytes valid range
//     1–1024; the returned Plaintext is exactly that many bytes.
//   - https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateRandom.html
//     NumberOfBytes valid range 1–1024; "There is no default value for string
//     length."

import (
	"net/http"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestGenerateDataKey_numberOfBytes(t *testing.T) {
	// Given: a symmetric key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "gdk-nob")

	for _, n := range []int{1, 16, 64, 128, 1024} {
		// When: a data key is requested by byte count
		resp := kmsCall(t, srv, "GenerateDataKey", map[string]any{
			"KeyId":         keyID,
			"NumberOfBytes": n,
		})
		helpers.AssertStatus(t, resp, http.StatusOK)
		var out struct {
			Plaintext      []byte `json:"Plaintext"`
			CiphertextBlob []byte `json:"CiphertextBlob"`
		}
		decodeJSON(t, resp, &out)
		resp.Body.Close()

		// Then: the plaintext is exactly that long
		if len(out.Plaintext) != n {
			t.Errorf("NumberOfBytes=%d: Plaintext is %d bytes, want %d", n, len(out.Plaintext), n)
		}
		if len(out.CiphertextBlob) == 0 {
			t.Errorf("NumberOfBytes=%d: empty CiphertextBlob", n)
		}
	}
}

func TestGenerateDataKey_keySpecLengths(t *testing.T) {
	// Given: a symmetric key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "gdk-spec")

	for spec, want := range map[string]int{"AES_256": 32, "AES_128": 16} {
		// When: a data key is requested by key spec
		resp := kmsCall(t, srv, "GenerateDataKey", map[string]any{
			"KeyId":   keyID,
			"KeySpec": spec,
		})
		helpers.AssertStatus(t, resp, http.StatusOK)
		var out struct {
			Plaintext []byte `json:"Plaintext"`
		}
		decodeJSON(t, resp, &out)
		resp.Body.Close()

		// Then: the documented length comes back
		if len(out.Plaintext) != want {
			t.Errorf("KeySpec=%s: Plaintext is %d bytes, want %d", spec, len(out.Plaintext), want)
		}
	}
}

func TestGenerateDataKey_bothKeySpecAndNumberOfBytes(t *testing.T) {
	// Given: a symmetric key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "gdk-both")

	// When: both length parameters are supplied
	resp := kmsCall(t, srv, "GenerateDataKey", map[string]any{
		"KeyId":         keyID,
		"KeySpec":       "AES_256",
		"NumberOfBytes": 32,
	})
	defer resp.Body.Close()

	// Then: AWS rejects the request
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestGenerateDataKey_neitherKeySpecNorNumberOfBytes(t *testing.T) {
	// Given: a symmetric key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "gdk-neither")

	// When: no length parameter is supplied
	resp := kmsCall(t, srv, "GenerateDataKey", map[string]any{"KeyId": keyID})
	defer resp.Body.Close()

	// Then: AWS rejects the request
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestGenerateDataKey_numberOfBytesOutOfRange(t *testing.T) {
	// Given: a symmetric key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "gdk-range")

	for _, n := range []int{0, -1, 1025, 4096} {
		// When: NumberOfBytes falls outside 1–1024
		resp := kmsCall(t, srv, "GenerateDataKey", map[string]any{
			"KeyId":         keyID,
			"NumberOfBytes": n,
		})

		// Then: AWS rejects the request
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertJSONError(t, resp, "ValidationException")
		resp.Body.Close()
	}
}

func TestGenerateDataKeyWithoutPlaintext_numberOfBytes(t *testing.T) {
	// Given: a symmetric key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "gdkwp-nob")

	// When: 64 bytes of key material are requested
	resp := kmsCall(t, srv, "GenerateDataKeyWithoutPlaintext", map[string]any{
		"KeyId":         keyID,
		"NumberOfBytes": 64,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		CiphertextBlob []byte `json:"CiphertextBlob"`
	}
	decodeJSON(t, resp, &out)
	resp.Body.Close()

	// Then: decrypting the blob yields exactly 64 bytes
	decResp := kmsCall(t, srv, "Decrypt", map[string]any{"CiphertextBlob": out.CiphertextBlob})
	defer decResp.Body.Close()
	helpers.AssertStatus(t, decResp, http.StatusOK)
	var decOut struct {
		Plaintext []byte `json:"Plaintext"`
	}
	decodeJSON(t, decResp, &decOut)
	if len(decOut.Plaintext) != 64 {
		t.Errorf("data key is %d bytes, want 64", len(decOut.Plaintext))
	}
}

func TestGenerateDataKeyWithoutPlaintext_bothKeySpecAndNumberOfBytes(t *testing.T) {
	// Given: a symmetric key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "gdkwp-both")

	// When: both length parameters are supplied
	resp := kmsCall(t, srv, "GenerateDataKeyWithoutPlaintext", map[string]any{
		"KeyId":         keyID,
		"KeySpec":       "AES_128",
		"NumberOfBytes": 16,
	})
	defer resp.Body.Close()

	// Then: AWS rejects the request
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestGenerateRandom_numberOfBytes(t *testing.T) {
	// Given: a running emulator (GenerateRandom needs no key)
	srv := helpers.NewTestServer(t)

	for _, n := range []int{1, 32, 1024} {
		// When: random bytes are requested
		resp := kmsCall(t, srv, "GenerateRandom", map[string]any{"NumberOfBytes": n})
		helpers.AssertStatus(t, resp, http.StatusOK)
		var out struct {
			Plaintext []byte `json:"Plaintext"`
		}
		decodeJSON(t, resp, &out)
		resp.Body.Close()

		// Then: exactly that many bytes come back
		if len(out.Plaintext) != n {
			t.Errorf("NumberOfBytes=%d: Plaintext is %d bytes, want %d", n, len(out.Plaintext), n)
		}
	}
}

func TestGenerateRandom_numberOfBytesOutOfRange(t *testing.T) {
	// Given: a running emulator
	srv := helpers.NewTestServer(t)

	for _, body := range []map[string]any{
		{"NumberOfBytes": 0},
		{"NumberOfBytes": 1025},
		{}, // omitted entirely — AWS has no default length
	} {
		// When: NumberOfBytes is missing or outside 1–1024
		resp := kmsCall(t, srv, "GenerateRandom", body)

		// Then: AWS rejects the request
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertJSONError(t, resp, "ValidationException")
		resp.Body.Close()
	}
}

func TestRPCv2CBOR_GenerateDataKey_numberOfBytes(t *testing.T) {
	// Given: a symmetric key created over the CBOR protocol
	srv := helpers.NewTestServer(t)
	keyID := createKeyCBOR(t, srv, "gdk-cbor")

	// When: 48 bytes of key material are requested
	resp := kmsCBORCall(t, srv, "GenerateDataKey", map[string]any{
		"KeyId":         keyID,
		"NumberOfBytes": 48,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the plaintext is 48 bytes
	var out struct {
		Plaintext []byte `cbor:"Plaintext"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode CBOR GenerateDataKey response: %v", err)
	}
	if len(out.Plaintext) != 48 {
		t.Errorf("Plaintext is %d bytes, want 48", len(out.Plaintext))
	}
}
