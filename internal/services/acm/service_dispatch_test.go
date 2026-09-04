package acm

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/state"
)

// newTestService returns an ACM Service wired for tests.
func newTestService(t *testing.T) *Service {
	t.Helper()
	store := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	return New(cfg, store, zap.NewNop(), clock.NewMock())
}

// TestDispatch_UnimplementedVsUnknownOperation pins the house rule that a
// real AWS operation Overcast has not implemented gets an honest 501 rather
// than the 400 UnknownOperationException that would wrongly claim AWS has no
// such operation — the same distinction cloudwatch/service.go's dispatchJSON
// makes for PutCompositeAlarm et al. (#1645).
//
// ImportCertificate is a real, documented ACM operation
// (internal/awsapi/manifest.gen.go) that Overcast does not implement — it is
// absent from both Handler.ops and Handler.typedOp. A made-up name is not in
// the AWS model at all, so it keeps the 400.
func TestDispatch_UnimplementedVsUnknownOperation(t *testing.T) {
	tests := []struct {
		name          string
		operation     string
		wantStatus    int
		wantBodyHas   string
		wantBodyLacks string
	}{
		{
			name:          "real unimplemented operation gets 501",
			operation:     "ImportCertificate",
			wantStatus:    http.StatusNotImplemented,
			wantBodyLacks: "UnknownOperationException",
		},
		{
			name:        "operation AWS has no such name for gets 400",
			operation:   "NotARealAcmOperation",
			wantStatus:  http.StatusBadRequest,
			wantBodyHas: "UnknownOperationException",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			if _, ok := svc.handler.ops[tt.operation]; ok {
				t.Fatalf("test setup: %q is implemented, pick an unimplemented one", tt.operation)
			}
			if _, ok := svc.handler.typedOp[tt.operation]; ok {
				t.Fatalf("test setup: %q is implemented, pick an unimplemented one", tt.operation)
			}

			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
			r.Header.Set("Content-Type", "application/x-amz-json-1.1")
			r = r.WithContext(codec.WithDispatch(r.Context(), codec.JSON11, tt.operation))

			w := httptest.NewRecorder()
			svc.Dispatch(w, r)

			if w.Code != tt.wantStatus {
				t.Fatalf("Dispatch: status = %d, want %d; body = %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantBodyHas != "" && !bytes.Contains(w.Body.Bytes(), []byte(tt.wantBodyHas)) {
				t.Fatalf("expected %q in body, got: %s", tt.wantBodyHas, w.Body.String())
			}
			if tt.wantBodyLacks != "" && bytes.Contains(w.Body.Bytes(), []byte(tt.wantBodyLacks)) {
				t.Fatalf("expected %q absent from body, got: %s", tt.wantBodyLacks, w.Body.String())
			}
		})
	}
}
