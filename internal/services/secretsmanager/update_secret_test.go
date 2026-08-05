package secretsmanager

import (
	"context"
	"encoding/json"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"
)

func TestUpdateSecret_metadataOnlyOmitsVersionIDFromJSONAndCBOR(t *testing.T) {
	h, _ := newRotationTestHandler(t)
	seedSecret(t, h, "metadata-only", "original")
	description := "changed"

	out, aerr := h.updateSecretTyped(context.Background(), &updateSecretRequest{
		SecretId: "metadata-only", Description: &description,
	})
	if aerr != nil {
		t.Fatalf("UpdateSecret: %v", aerr)
	}
	assertVersionIDPresence(t, out, false)
}

func TestUpdateSecret_newVersionIncludesVersionIDInJSONAndCBOR(t *testing.T) {
	h, _ := newRotationTestHandler(t)
	seedSecret(t, h, "new-version", "original")

	out, aerr := h.updateSecretTyped(context.Background(), &updateSecretRequest{
		SecretId: "new-version", SecretString: "replacement",
	})
	if aerr != nil {
		t.Fatalf("UpdateSecret: %v", aerr)
	}
	if out.VersionId == "" {
		t.Fatal("VersionId is empty for a new-version update")
	}
	assertVersionIDPresence(t, out, true)
}

func assertVersionIDPresence(t *testing.T, out *updateSecretResponse, want bool) {
	t.Helper()
	jsonBody, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	var jsonFields map[string]any
	if err := json.Unmarshal(jsonBody, &jsonFields); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}
	if _, got := jsonFields["VersionId"]; got != want {
		t.Errorf("JSON VersionId presence = %v, want %v (%s)", got, want, jsonBody)
	}

	cborBody, err := cborlib.Marshal(out)
	if err != nil {
		t.Fatalf("marshal CBOR: %v", err)
	}
	var cborFields map[string]any
	if err := cborlib.Unmarshal(cborBody, &cborFields); err != nil {
		t.Fatalf("unmarshal CBOR: %v", err)
	}
	if _, got := cborFields["VersionId"]; got != want {
		t.Errorf("CBOR VersionId presence = %v, want %v", got, want)
	}
}

func TestUpdateSecret_emptyKMSKeyResetsToDefaultSemantics(t *testing.T) {
	h, _ := newRotationTestHandler(t)
	ctx := context.Background()
	if _, aerr := h.createSecretTyped(ctx, &createSecretRequest{
		Name: "kms-reset", SecretString: "value", KmsKeyId: "alias/customer-key",
	}); aerr != nil {
		t.Fatalf("CreateSecret: %v", aerr)
	}
	empty := ""
	if _, aerr := h.updateSecretTyped(ctx, &updateSecretRequest{SecretId: "kms-reset", KmsKeyId: &empty}); aerr != nil {
		t.Fatalf("UpdateSecret: %v", aerr)
	}
	described, aerr := h.describeSecretTyped(ctx, &secretIDRequest{SecretId: "kms-reset"})
	if aerr != nil {
		t.Fatalf("DescribeSecret: %v", aerr)
	}
	if described.KmsKeyId != "" {
		t.Fatalf("KmsKeyId = %q, want default/reset empty value", described.KmsKeyId)
	}
	encoded, err := json.Marshal(described)
	if err != nil {
		t.Fatalf("marshal DescribeSecret: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal DescribeSecret: %v", err)
	}
	if _, exists := fields["KmsKeyId"]; exists {
		t.Fatalf("DescribeSecret exposed reset KmsKeyId: %s", encoded)
	}
}
