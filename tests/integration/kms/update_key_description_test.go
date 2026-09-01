package kms_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ---- UpdateKeyDescription ---------------------------------------------------

func TestUpdateKeyDescription_success(t *testing.T) {
	// Given: a key with an initial description
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "before")

	// When: UpdateKeyDescription changes it
	resp := kmsCall(t, srv, "UpdateKeyDescription", map[string]any{
		"KeyId":       keyID,
		"Description": "after",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: DescribeKey reflects the new description
	desc := kmsCall(t, srv, "DescribeKey", map[string]any{"KeyId": keyID})
	defer desc.Body.Close()
	helpers.AssertStatus(t, desc, http.StatusOK)
	var out struct {
		KeyMetadata struct {
			Description string `json:"Description"`
		} `json:"KeyMetadata"`
	}
	if err := json.NewDecoder(desc.Body).Decode(&out); err != nil {
		t.Fatalf("decode DescribeKey: %v", err)
	}
	if out.KeyMetadata.Description != "after" {
		t.Errorf("Description = %q, want %q", out.KeyMetadata.Description, "after")
	}
}

func TestUpdateKeyDescription_notFound(t *testing.T) {
	// Given: no keys exist
	srv := helpers.NewTestServer(t)

	// When: UpdateKeyDescription targets an unknown key
	resp := kmsCall(t, srv, "UpdateKeyDescription", map[string]any{
		"KeyId":       "00000000-0000-0000-0000-000000000000",
		"Description": "whatever",
	})
	defer resp.Body.Close()

	// Then: NotFoundException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "NotFoundException")
}
