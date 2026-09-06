package kms_test

// CancelKeyDeletion's response shape, per
// https://docs.aws.amazon.com/kms/latest/APIReference/API_CancelKeyDeletion.html:
// the KeyId element is "The Amazon Resource Name (key ARN) of the KMS key whose
// deletion is canceled", and the documented example response is
// {"KeyId":"arn:aws:kms:us-east-2:111122223333:key/1234abcd-…"}. It is the
// same contract ScheduleKeyDeletion carries (#1710), and the one response that
// was still answering the bare key id after that fix (#1844).

import (
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestCancelKeyDeletion_returnsKeyArn(t *testing.T) {
	// Given: a key whose deletion is scheduled
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "cancel-arn")
	sched := kmsCall(t, srv, "ScheduleKeyDeletion", map[string]any{
		"KeyId":               keyID,
		"PendingWindowInDays": 7,
	})
	sched.Body.Close()
	helpers.AssertStatus(t, sched, http.StatusOK)

	// When: the deletion is cancelled
	resp := kmsCall(t, srv, "CancelKeyDeletion", map[string]any{"KeyId": keyID})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: KeyId is the full key ARN, not the bare UUID, and the key is back
	// to Disabled as AWS leaves it
	var out struct {
		KeyId    string `json:"KeyId"`
		KeyState string `json:"KeyState"`
	}
	decodeJSON(t, resp, &out)
	if !strings.HasPrefix(out.KeyId, "arn:aws:kms:") || !strings.HasSuffix(out.KeyId, ":key/"+keyID) {
		t.Errorf("KeyId = %q, want the key ARN ending in :key/%s", out.KeyId, keyID)
	}
	if out.KeyState != "Disabled" {
		t.Errorf("KeyState = %q, want Disabled", out.KeyState)
	}
}
