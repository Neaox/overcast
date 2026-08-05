package cloudformation

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

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
