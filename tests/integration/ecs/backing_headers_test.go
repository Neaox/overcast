package ecs_test

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// DescribeServices and DescribeTasks must carry the x-overcast-backing
// headers like the other Docker-backed describe operations. Setting them
// after the JSON body has been written is a silent no-op.

func TestDescribeServices_setsBackingHeaders(t *testing.T) {
	// Given: a running emulator
	srv := helpers.NewTestServer(t)

	// When: DescribeServices is called
	resp := ecsCall(t, srv, "DescribeServices", map[string]any{
		"services": []string{"no-such-service"},
	})
	defer resp.Body.Close()

	// Then: the response carries backing metadata headers
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("x-overcast-backing"); got == "" {
		t.Error("expected x-overcast-backing header on DescribeServices response")
	}
	if got := resp.Header.Get("x-overcast-backing-reason"); got == "" {
		t.Error("expected x-overcast-backing-reason header on DescribeServices response")
	}
}

func TestDescribeTasks_setsBackingHeaders(t *testing.T) {
	// Given: a running emulator
	srv := helpers.NewTestServer(t)

	// When: DescribeTasks is called
	resp := ecsCall(t, srv, "DescribeTasks", map[string]any{
		"tasks": []string{"11112222-3333-4444-5555-666677778888"},
	})
	defer resp.Body.Close()

	// Then: the response carries backing metadata headers
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("x-overcast-backing"); got == "" {
		t.Error("expected x-overcast-backing header on DescribeTasks response")
	}
	if got := resp.Header.Get("x-overcast-backing-reason"); got == "" {
		t.Error("expected x-overcast-backing-reason header on DescribeTasks response")
	}
}
