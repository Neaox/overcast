package cloudformation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/trace"
)

// Internal dispatch hop bodies must be bounded like top-level trace entries:
// an oversized request or response routed through the provisioner must not be
// pinned whole in the trace ring buffer.
func TestInternalRequest_oversizedHopBodiesAreCapped(t *testing.T) {
	// Given: a debug trace recorder and a dispatch with >1 MiB bodies both ways
	rec := trace.NewRecorder("req-1", time.Unix(0, 0), "POST", "/", "localhost", "", http.Header{})
	ctx := trace.ContextWithRecorder(context.Background(), rec)
	big := bytes.Repeat([]byte("x"), trace.MaxHopBody+4096)
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(big)
	})

	// When: the provisioner dispatches the request internally
	if _, err := internalRequest(ctx, router, "us-east-1", http.MethodPut, "/bucket/key", "application/octet-stream", big); err != nil {
		t.Fatalf("internalRequest returned error: %v", err)
	}

	// Then: the recorded hop stores capped bodies, not the originals
	hops := rec.Entry().Hops
	if len(hops) != 1 {
		t.Fatalf("hops = %d, want 1", len(hops))
	}
	if got := len(hops[0].RequestBody); got != trace.MaxHopBody {
		t.Errorf("len(hop RequestBody) = %d, want %d", got, trace.MaxHopBody)
	}
	if got := len(hops[0].ResponseBody); got != trace.MaxHopBody {
		t.Errorf("len(hop ResponseBody) = %d, want %d", got, trace.MaxHopBody)
	}
	if hops[0].RequestBodyOmitted != trace.OmitSize || hops[0].ResponseBodyOmitted != trace.OmitSize {
		t.Errorf("omission reasons = (%q, %q), want both %q",
			hops[0].RequestBodyOmitted, hops[0].ResponseBodyOmitted, trace.OmitSize)
	}
}

func TestKMSKeyHandler_forwardsSerializedPolicyAndLockoutBypass(t *testing.T) {
	// Given: a CloudFormation key policy object and explicit bypass setting
	policy := map[string]any{
		"Version": "2012-10-17",
		"Statement": []any{map[string]any{
			"Effect": "Allow", "Principal": map[string]any{"AWS": "arn:aws:iam::000000000000:root"},
			"Action": "kms:*", "Resource": "*",
		}},
	}
	var requests []map[string]any
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s request: %v", r.Header.Get("X-Amz-Target"), err)
		}
		requests = append(requests, body)
		switch r.Header.Get("X-Amz-Target") {
		case "TrentService.CreateKey":
			_, _ = w.Write([]byte(`{"KeyMetadata":{"KeyId":"key-id","Arn":"arn:aws:kms:us-east-1:000000000000:key/key-id"}}`))
		case "TrentService.GetKeyPolicy":
			_, _ = w.Write([]byte(`{"Policy":"{\"Version\":\"2012-10-17\",\"Statement\":[]}"}`))
		}
	})
	h := &kmsKeyHandler{}
	props := map[string]any{"KeyPolicy": policy, "BypassPolicyLockoutSafetyCheck": true}
	oldProps := map[string]any{"KeyPolicy": map[string]any{"Version": "2012-10-17"}, "BypassPolicyLockoutSafetyCheck": true}

	// When: CloudFormation creates the key and an update changes the policy
	if _, _, err := h.Create(context.Background(), router, nil, props, &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, _, err := h.Update(context.Background(), router, nil, "key-id", props, oldProps, &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	// Then: both service calls receive a JSON policy string and the bypass flag
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(requests))
	}
	for _, i := range []int{0, 2} {
		body := requests[i]
		policyString, ok := body["Policy"].(string)
		if !ok {
			t.Fatalf("request %d Policy = %#v, want string", i, body["Policy"])
		}
		var roundTrip map[string]any
		if err := json.Unmarshal([]byte(policyString), &roundTrip); err != nil {
			t.Fatalf("request %d Policy is not JSON: %v", i, err)
		}
		if body["BypassPolicyLockoutSafetyCheck"] != true {
			t.Fatalf("request %d bypass = %#v, want true", i, body["BypassPolicyLockoutSafetyCheck"])
		}
	}
}

// Real CloudFormation calls PutKeyPolicy only when the KeyPolicy property
// changed. Re-putting an unchanged policy is not just churn: a caller-locking
// policy created with BypassPolicyLockoutSafetyCheck would be re-validated
// without bypass on every unrelated update and fail the whole stack update.
func TestKMSKeyHandlerUpdate_unchangedPolicyIsNotReapplied(t *testing.T) {
	// Given: a policy identical in old and new properties
	policy := map[string]any{
		"Version": "2012-10-17",
		"Statement": []any{map[string]any{
			"Effect": "Allow", "Principal": map[string]any{"AWS": "arn:aws:iam::000000000000:user/other"},
			"Action": "kms:PutKeyPolicy", "Resource": "*",
		}},
	}
	var targets []string
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targets = append(targets, r.Header.Get("X-Amz-Target"))
		_, _ = w.Write([]byte(`{}`))
	})
	props := map[string]any{"KeyPolicy": policy, "BypassPolicyLockoutSafetyCheck": true}
	oldProps := map[string]any{"KeyPolicy": policy, "BypassPolicyLockoutSafetyCheck": true}

	// When: an update runs with no KeyPolicy change
	if _, _, err := (&kmsKeyHandler{}).Update(context.Background(), router, nil, "key-id", props, oldProps, &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	// Then: no key-policy calls are dispatched
	for _, target := range targets {
		if target == "TrentService.GetKeyPolicy" || target == "TrentService.PutKeyPolicy" {
			t.Fatalf("dispatched %s for an unchanged KeyPolicy", target)
		}
	}
}

// CloudFormation's KeyPolicy is Type: Json, which accepts either an object or
// a JSON string. A string must be forwarded as-is: marshalling it again
// produces a double-encoded quoted string that KMS policy validation rejects.
func TestKMSKeyHandler_stringKeyPolicyIsForwardedVerbatim(t *testing.T) {
	// Given: a KeyPolicy supplied as a JSON string
	policyString := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"kms:*","Resource":"*"}]}`
	var policies []any
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s request: %v", r.Header.Get("X-Amz-Target"), err)
		}
		switch r.Header.Get("X-Amz-Target") {
		case "TrentService.CreateKey":
			policies = append(policies, body["Policy"])
			_, _ = w.Write([]byte(`{"KeyMetadata":{"KeyId":"key-id","Arn":"arn:aws:kms:us-east-1:000000000000:key/key-id"}}`))
		case "TrentService.GetKeyPolicy":
			_, _ = w.Write([]byte(`{"Policy":"{}"}`))
		case "TrentService.PutKeyPolicy":
			policies = append(policies, body["Policy"])
			_, _ = w.Write([]byte(`{}`))
		}
	})
	h := &kmsKeyHandler{}
	props := map[string]any{"KeyPolicy": policyString}

	// When: CloudFormation creates the key and an update changes the policy
	if _, _, err := h.Create(context.Background(), router, nil, props, &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, _, err := h.Update(context.Background(), router, nil, "key-id", props, map[string]any{}, &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	// Then: both dispatches carry the string untouched, not re-encoded
	if len(policies) != 2 {
		t.Fatalf("captured policies = %d, want 2", len(policies))
	}
	for i, got := range policies {
		if got != policyString {
			t.Errorf("dispatch %d Policy = %#v, want the original string", i, got)
		}
	}
}

// A changed Description must dispatch UpdateKeyDescription; CloudFormation
// silently ignoring it leaves the key describing its old purpose.
func TestKMSKeyHandlerUpdate_descriptionChangeDispatchesUpdateKeyDescription(t *testing.T) {
	// Given: an update that only changes Description
	var bodies []map[string]any
	var targets []string
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targets = append(targets, r.Header.Get("X-Amz-Target"))
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s request: %v", r.Header.Get("X-Amz-Target"), err)
		}
		bodies = append(bodies, body)
		_, _ = w.Write([]byte(`{}`))
	})

	// When: the update runs
	if _, _, err := (&kmsKeyHandler{}).Update(context.Background(), router, nil, "key-id", map[string]any{
		"Description": "new purpose",
	}, map[string]any{"Description": "old purpose"}, &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	// Then: exactly one UpdateKeyDescription call carries the new description
	if len(targets) != 1 || targets[0] != "TrentService.UpdateKeyDescription" {
		t.Fatalf("targets = %v, want [TrentService.UpdateKeyDescription]", targets)
	}
	if bodies[0]["KeyId"] != "key-id" || bodies[0]["Description"] != "new purpose" {
		t.Fatalf("UpdateKeyDescription body = %#v", bodies[0])
	}
}

// An unchanged Description must not dispatch anything.
func TestKMSKeyHandlerUpdate_unchangedDescriptionIsNotReapplied(t *testing.T) {
	var targets []string
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targets = append(targets, r.Header.Get("X-Amz-Target"))
		_, _ = w.Write([]byte(`{}`))
	})

	if _, _, err := (&kmsKeyHandler{}).Update(context.Background(), router, nil, "key-id", map[string]any{
		"Description": "same",
	}, map[string]any{"Description": "same"}, &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("targets = %v, want none", targets)
	}
}

func TestKMSKeyHandlerUpdate_compensatesPolicyWhenEnabledTransitionFails(t *testing.T) {
	oldPolicy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"kms:*","Resource":"*"}]}`
	newPolicy := map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{
		"Effect": "Allow", "Principal": "*", "Action": "kms:PutKeyPolicy", "Resource": "*",
	}}}
	var putBodies []map[string]any
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s request: %v", r.Header.Get("X-Amz-Target"), err)
		}
		switch r.Header.Get("X-Amz-Target") {
		case "TrentService.GetKeyPolicy":
			encoded, _ := json.Marshal(map[string]any{"Policy": oldPolicy})
			_, _ = w.Write(encoded)
		case "TrentService.PutKeyPolicy":
			putBodies = append(putBodies, body)
			_, _ = w.Write([]byte(`{}`))
		case "TrentService.DisableKey":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"__type":"InternalError","message":"failed"}`))
		default:
			t.Fatalf("unexpected target %q", r.Header.Get("X-Amz-Target"))
		}
	})

	physicalID, _, err := (&kmsKeyHandler{}).Update(context.Background(), router, nil, "key-id", map[string]any{
		"KeyPolicy": newPolicy, "Enabled": false,
	}, map[string]any{"Enabled": true}, &resolveContext{Region: "us-east-1"})

	if physicalID != "key-id" {
		t.Fatalf("physical ID = %q, want key-id", physicalID)
	}
	var failed updateFailure
	if !errors.As(err, &failed) || failed.dirty || errors.Is(err, errReplacementRequired) {
		t.Fatalf("Update error = %#v, want clean terminal update failure", err)
	}
	if len(putBodies) != 2 {
		t.Fatalf("PutKeyPolicy calls = %d, want apply and compensation", len(putBodies))
	}
	if putBodies[1]["Policy"] != oldPolicy || putBodies[1]["BypassPolicyLockoutSafetyCheck"] != true {
		t.Fatalf("compensation body = %#v, want exact old policy with bypass", putBodies[1])
	}
}

func TestKMSKeyHandlerUpdate_failedPolicyCompensationIsDirty(t *testing.T) {
	putCalls := 0
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Amz-Target") {
		case "TrentService.GetKeyPolicy":
			_, _ = w.Write([]byte(`{"Policy":"old-policy"}`))
		case "TrentService.PutKeyPolicy":
			putCalls++
			if putCalls == 2 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"__type":"InternalError","message":"restore failed"}`))
			}
		case "TrentService.DisableKey":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"__type":"InternalError","message":"disable failed"}`))
		}
	})

	physicalID, _, err := (&kmsKeyHandler{}).Update(context.Background(), router, nil, "key-id", map[string]any{
		"KeyPolicy": map[string]any{"Version": "2012-10-17"}, "Enabled": false,
	}, map[string]any{"Enabled": true}, &resolveContext{Region: "us-east-1"})

	if physicalID != "key-id" {
		t.Fatalf("physical ID = %q, want key-id", physicalID)
	}
	if !isDirtyUpdateFailure(err) || errors.Is(err, errReplacementRequired) {
		t.Fatalf("Update error = %#v, want dirty terminal update failure", err)
	}
}

func TestKMSKeyHandlerUpdate_enabledFailureIsCleanAndPreservesPhysicalID(t *testing.T) {
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"__type":"InternalError","message":"disable failed"}`))
	})

	physicalID, _, err := (&kmsKeyHandler{}).Update(context.Background(), router, nil, "key-id", map[string]any{
		"Enabled": false,
	}, map[string]any{"Enabled": true}, &resolveContext{Region: "us-east-1"})

	var failed updateFailure
	if physicalID != "key-id" || !errors.As(err, &failed) || failed.dirty || errors.Is(err, errReplacementRequired) {
		t.Fatalf("Update result = (%q, %#v), want key-id and clean terminal failure", physicalID, err)
	}
}

func TestKMSKeyHandlerCreate_disableFailureSchedulesDeletion(t *testing.T) {
	// Given: CreateKey succeeds but applying Enabled=false fails
	var targets []string
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		targets = append(targets, target)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		switch target {
		case "TrentService.CreateKey":
			_, _ = w.Write([]byte(`{"KeyMetadata":{"KeyId":"key-id","Arn":"arn:aws:kms:us-east-1:000000000000:key/key-id"}}`))
		case "TrentService.DisableKey":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"__type":"InternalError","message":"failed"}`))
		case "TrentService.ScheduleKeyDeletion":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected target %q", target)
		}
	})
	h := &kmsKeyHandler{}

	// When: CloudFormation creates the disabled key
	_, _, err := h.Create(context.Background(), router, nil, map[string]any{"Enabled": false}, &resolveContext{Region: "us-east-1"})

	// Then: it reports the failure after scheduling cleanup of the partial key
	if err == nil || !strings.Contains(err.Error(), "DisableKey") {
		t.Fatalf("Create error = %v, want DisableKey failure", err)
	}
	want := []string{"TrentService.CreateKey", "TrentService.DisableKey", "TrentService.ScheduleKeyDeletion"}
	if strings.Join(targets, ",") != strings.Join(want, ",") {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
}

func TestKMSKeyHandlerUpdate_removedEnabledRestoresDefault(t *testing.T) {
	// Given: a key that was explicitly disabled by the previous template
	var target string
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target = r.Header.Get("X-Amz-Target")
	})
	h := &kmsKeyHandler{}

	// When: the new template omits Enabled
	_, _, err := h.Update(context.Background(), router, nil, "key-id", map[string]any{}, map[string]any{
		"Enabled": false,
	}, &resolveContext{Region: "us-east-1"})

	// Then: CloudFormation restores the documented default enabled state
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if target != "TrentService.EnableKey" {
		t.Fatalf("target = %q, want TrentService.EnableKey", target)
	}
}
