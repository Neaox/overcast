package kms_test

// Alias naming rules and response shape.
//
// AWS reference:
//   - https://docs.aws.amazon.com/kms/latest/APIReference/API_CreateAlias.html
//     AliasName "must begin with alias/ followed by a name", Pattern
//     `^alias/[a-zA-Z0-9/_-]+$`, and "The alias name cannot begin with
//     alias/aws/. The alias/aws/ prefix is reserved for AWS managed keys."
//     Errors: InvalidAliasNameException and AlreadyExistsException, both 400.
//   - https://docs.aws.amazon.com/kms/latest/APIReference/API_ScheduleKeyDeletion.html
//     Response KeyId is "The Amazon Resource Name (key ARN) of the KMS key
//     whose deletion is scheduled."

import (
	"net/http"
	"strings"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestCreateAlias_withoutAliasPrefix(t *testing.T) {
	// Given: a key to point an alias at
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "alias-prefix")

	for _, name := range []string{"my-alias", "aliases/app", "alias/", "alias/bad name"} {
		// When: creating an alias whose name is not a valid AWS alias name
		resp := kmsCall(t, srv, "CreateAlias", map[string]any{
			"AliasName":   name,
			"TargetKeyId": keyID,
		})

		// Then: AWS rejects it
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertJSONError(t, resp, "InvalidAliasNameException")
		resp.Body.Close()
	}

	// And: no alias was created
	listResp := kmsCall(t, srv, "ListAliases", map[string]any{})
	defer listResp.Body.Close()
	var listOut struct {
		Aliases []struct {
			AliasName string `json:"AliasName"`
		} `json:"Aliases"`
	}
	decodeJSON(t, listResp, &listOut)
	if len(listOut.Aliases) != 0 {
		t.Errorf("ListAliases = %+v, want no aliases", listOut.Aliases)
	}
}

func TestCreateAlias_reservedAwsPrefix(t *testing.T) {
	// Given: a customer key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "alias-reserved")

	// When: claiming a name under the prefix AWS reserves for managed keys
	resp := kmsCall(t, srv, "CreateAlias", map[string]any{
		"AliasName":   "alias/aws/s3",
		"TargetKeyId": keyID,
	})
	defer resp.Body.Close()

	// Then: AWS rejects it
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidAliasNameException")
}

func TestCreateAlias_duplicateName(t *testing.T) {
	// Given: an alias that already exists
	srv := helpers.NewTestServer(t)
	keyA := createKey(t, srv, "alias-dup-a")
	keyB := createKey(t, srv, "alias-dup-b")
	first := kmsCall(t, srv, "CreateAlias", map[string]any{
		"AliasName":   "alias/duplicated",
		"TargetKeyId": keyA,
	})
	first.Body.Close()
	helpers.AssertStatus(t, first, http.StatusOK)

	// When: creating it again, pointing somewhere else
	resp := kmsCall(t, srv, "CreateAlias", map[string]any{
		"AliasName":   "alias/duplicated",
		"TargetKeyId": keyB,
	})
	defer resp.Body.Close()

	// Then: AWS rejects it — re-pointing goes through UpdateAlias
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "AlreadyExistsException")

	// And: the alias still points at the original key
	listResp := kmsCall(t, srv, "ListAliases", map[string]any{})
	defer listResp.Body.Close()
	var listOut struct {
		Aliases []struct {
			AliasName   string `json:"AliasName"`
			TargetKeyId string `json:"TargetKeyId"`
		} `json:"Aliases"`
	}
	decodeJSON(t, listResp, &listOut)
	for _, a := range listOut.Aliases {
		if a.AliasName == "alias/duplicated" && a.TargetKeyId != keyA {
			t.Errorf("alias/duplicated targets %q, want %q", a.TargetKeyId, keyA)
		}
	}
}

func TestUpdateAlias_stillRepointsExistingAlias(t *testing.T) {
	// Given: an alias pointing at key A
	srv := helpers.NewTestServer(t)
	keyA := createKey(t, srv, "alias-update-a")
	keyB := createKey(t, srv, "alias-update-b")
	create := kmsCall(t, srv, "CreateAlias", map[string]any{
		"AliasName":   "alias/repointed",
		"TargetKeyId": keyA,
	})
	create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	// When: re-pointing it at key B through UpdateAlias
	resp := kmsCall(t, srv, "UpdateAlias", map[string]any{
		"AliasName":   "alias/repointed",
		"TargetKeyId": keyB,
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the alias resolves to key B
	descResp := kmsCall(t, srv, "DescribeKey", map[string]any{"KeyId": "alias/repointed"})
	defer descResp.Body.Close()
	var meta struct {
		KeyMetadata struct {
			KeyId string `json:"KeyId"`
		} `json:"KeyMetadata"`
	}
	decodeJSON(t, descResp, &meta)
	if meta.KeyMetadata.KeyId != keyB {
		t.Errorf("alias/repointed resolves to %q, want %q", meta.KeyMetadata.KeyId, keyB)
	}
}

func TestScheduleKeyDeletion_returnsKeyArn(t *testing.T) {
	// Given: a key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "sched-arn")

	// When: scheduling its deletion
	resp := kmsCall(t, srv, "ScheduleKeyDeletion", map[string]any{
		"KeyId":               keyID,
		"PendingWindowInDays": 7,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: KeyId is the full key ARN, not the bare UUID
	var out struct {
		KeyId string `json:"KeyId"`
	}
	decodeJSON(t, resp, &out)
	if !strings.HasPrefix(out.KeyId, "arn:aws:kms:") || !strings.HasSuffix(out.KeyId, ":key/"+keyID) {
		t.Errorf("KeyId = %q, want the key ARN ending in :key/%s", out.KeyId, keyID)
	}
}

func TestRPCv2CBOR_CreateAlias_withoutAliasPrefix(t *testing.T) {
	// Given: a key created over CBOR
	srv := helpers.NewTestServer(t)
	keyID := createKeyCBOR(t, srv, "cbor-alias")

	// When: creating an alias without the alias/ prefix
	resp := kmsCBORCall(t, srv, "CreateAlias", map[string]any{
		"AliasName":   "no-prefix",
		"TargetKeyId": keyID,
	})
	defer resp.Body.Close()

	// Then: the typed path rejects it too
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var errBody map[string]string
	if err := cborlib.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode CBOR error body: %v", err)
	}
	if errBody["__type"] != "InvalidAliasNameException" {
		t.Errorf("__type = %q, want InvalidAliasNameException", errBody["__type"])
	}
}
