package cloudformation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

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

	// When: CloudFormation creates and updates the key
	if _, _, err := h.Create(context.Background(), router, nil, props, &resolveContext{Region: "us-east-1"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, _, err := h.Update(context.Background(), router, nil, "key-id", props, props, &resolveContext{Region: "us-east-1"}); err != nil {
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
