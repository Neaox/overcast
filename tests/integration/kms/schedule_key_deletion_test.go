package kms_test

// ScheduleKeyDeletion's response shape, per
// https://docs.aws.amazon.com/kms/latest/APIReference/API_ScheduleKeyDeletion.html.
// The response syntax is
//
//	{"DeletionDate": number, "KeyId": "string", "KeyState": "string",
//	 "PendingWindowInDays": number}
//
// and PendingWindowInDays is "The waiting period before the KMS key is
// deleted", Integer, Valid Range 7-30. The documented example response carries
// it beside KeyId and DeletionDate:
//
//	{"DeletionDate": 1.4820192E9,
//	 "KeyId": "arn:aws:kms:us-east-2:111122223333:key/1234abcd-…",
//	 "PendingWindowInDays": 7}
//
// Overcast returned KeyId, KeyArn, DeletionDate and KeyState but omitted the
// window that applied, so a caller could not read back what it asked for or
// discover the 30-day default (#1844).

import (
	"math"
	"net/http"
	"testing"
	"time"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// assertDeletionDate checks the epoch-seconds DeletionDate lands the given
// number of days out, which is also what fixes its shape: AWS models it as a
// Timestamp and its example renders 1.4820192E9, a JSON number of seconds
// rather than an ISO-8601 string.
func assertDeletionDate(t *testing.T, deletionDate float64, days int) {
	t.Helper()
	want := float64(time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix())
	if math.Abs(deletionDate-want) > 300 {
		t.Errorf("DeletionDate = %v, want about %v (epoch seconds, %d days out)", deletionDate, want, days)
	}
}

func TestScheduleKeyDeletion_returnsPendingWindowInDays(t *testing.T) {
	// Given: a key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "sched-window-echo")

	// When: deletion is scheduled with an explicit waiting period
	resp := kmsCall(t, srv, "ScheduleKeyDeletion", map[string]any{
		"KeyId":               keyID,
		"PendingWindowInDays": 14,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the response carries the window that applied, alongside the other
	// three documented elements
	var out struct {
		KeyId               string  `json:"KeyId"`
		KeyState            string  `json:"KeyState"`
		DeletionDate        float64 `json:"DeletionDate"`
		PendingWindowInDays int     `json:"PendingWindowInDays"`
	}
	decodeJSON(t, resp, &out)
	if out.PendingWindowInDays != 14 {
		t.Errorf("PendingWindowInDays = %d, want 14", out.PendingWindowInDays)
	}
	if !hasKeyARNSuffix(out.KeyId, keyID) {
		t.Errorf("KeyId = %q, want the key ARN ending in :key/%s", out.KeyId, keyID)
	}
	if out.KeyState != "PendingDeletion" {
		t.Errorf("KeyState = %q, want PendingDeletion", out.KeyState)
	}
	assertDeletionDate(t, out.DeletionDate, 14)
}

func TestScheduleKeyDeletion_omittedWindowReportsThe30DayDefault(t *testing.T) {
	// Given: a key
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "sched-window-default")

	// When: no waiting period is supplied — "If you do not include a value, it
	// defaults to 30"
	resp := kmsCall(t, srv, "ScheduleKeyDeletion", map[string]any{"KeyId": keyID})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the response reports the default that applied, not nothing
	var out struct {
		DeletionDate        float64 `json:"DeletionDate"`
		PendingWindowInDays int     `json:"PendingWindowInDays"`
	}
	decodeJSON(t, resp, &out)
	if out.PendingWindowInDays != 30 {
		t.Errorf("PendingWindowInDays = %d, want the 30-day default", out.PendingWindowInDays)
	}
	assertDeletionDate(t, out.DeletionDate, 30)
}

func TestRPCv2CBOR_ScheduleKeyDeletion_returnsPendingWindowInDays(t *testing.T) {
	// Given: a key, over CBOR
	srv := helpers.NewTestServer(t)
	keyID := createKeyCBOR(t, srv, "cbor-sched-window")

	// When: deletion is scheduled with an explicit waiting period
	resp := kmsCBORCall(t, srv, "ScheduleKeyDeletion", map[string]any{
		"KeyId":               keyID,
		"PendingWindowInDays": 7,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the typed path carries it too
	var out struct {
		PendingWindowInDays int `cbor:"PendingWindowInDays"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode CBOR ScheduleKeyDeletion response: %v", err)
	}
	if out.PendingWindowInDays != 7 {
		t.Errorf("PendingWindowInDays = %d, want 7", out.PendingWindowInDays)
	}
}
