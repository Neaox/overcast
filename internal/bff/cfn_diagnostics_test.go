package bff

// cfn_diagnostics_test.go — the console's route to the deploy-diagnostics
// journal.
//
// The 404 is the interesting case and the reason this has its own test. It is
// not an error here: a stack that has never failed has no journal, that is the
// ordinary answer, and the console decides whether to show its Diagnostics tab
// on exactly this status. A proxy that turned it into a 502, or into an empty
// 200, would either flag a healthy stack as broken or offer a tab with nothing
// behind it.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCFNStackDiagnostics_proxiesThePayloadAndItsStatus(t *testing.T) {
	const payload = `{"stackName":"MyStack","operation":"CREATE","resources":[]}`

	cases := []struct {
		name       string
		upstream   int
		body       string
		wantStatus int
		wantBody   string
	}{
		{
			name:     "a journalled failure is passed through verbatim",
			upstream: http.StatusOK, body: payload,
			wantStatus: http.StatusOK, wantBody: payload,
		},
		{
			// The ordinary case for a healthy stack, and the one the console
			// keys the tab's existence on.
			name:     "no journal stays a 404",
			upstream: http.StatusNotFound, body: `{"error":"no deploy diagnostics for MyStack"}`,
			wantStatus: http.StatusNotFound, wantBody: `{"error":"no deploy diagnostics for MyStack"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotRegion string
			_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotRegion = r.Header.Get("X-Overcast-Region")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.upstream)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer restore()

			req := httptest.NewRequest(http.MethodGet, "/api/cloudformation/stacks/MyStack/diagnostics", nil)
			req.Header.Set("X-Overcast-Region", "ap-southeast-2")
			rec := httptest.NewRecorder()
			NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tc.wantBody {
				t.Errorf("body = %s, want %s", got, tc.wantBody)
			}
			if gotPath != "/_overcast/cloudformation/stacks/MyStack/diagnostics" {
				t.Errorf("proxied to %q, want the emulator's diagnostics path", gotPath)
			}
			// The journal is region-scoped like every other CloudFormation
			// record, so a console pointed elsewhere must not be answered from
			// the default region.
			if gotRegion != "ap-southeast-2" {
				t.Errorf("forwarded region = %q, want ap-southeast-2", gotRegion)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want JSON", ct)
			}
			if !json.Valid(rec.Body.Bytes()) {
				t.Errorf("body is not valid JSON: %s", rec.Body.String())
			}
		})
	}
}

func TestCFNStackDiagnostics_unreachableEmulatorIsABadGateway(t *testing.T) {
	emulator, restore := stubEmulatorForProxyTests(t, http.NotFoundHandler())
	emulator.Close() // nothing is listening now
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/cloudformation/stacks/MyStack/diagnostics", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}
