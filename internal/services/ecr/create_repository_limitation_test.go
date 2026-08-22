package ecr

// create_repository_limitation_test.go — CreateRepository must say, in its own
// response, when the address it just handed out does not reach a working
// registry.
//
// Before this, the only signal was a Warn log line inside registryEndpoint:
// CreateRepository still answered 200 with a repositoryUri naming Overcast's
// own API port (or, worse, a port the daemon never proved it could reach), and
// the failure only surfaced later as an opaque `docker push … 405 Method Not
// Allowed`, or an ECS/Lambda pull of that image failing to start — in a
// different place, at a different time, with no link back to the repository
// that caused it. This is issue #1107 / W1's shape: a create that returns
// terminal success before what it depends on is actually there.
//
// The fix reuses the existing x-overcast-emulation-limitation channel
// (protocol.MarkLimitation / EmulationLimitationHeader) rather than inventing
// a new one — CloudFormation already copies it onto ResourceStatusReason (see
// cloudformation/limitation.go), so a repository created inside a `cdk deploy`
// carries the same sentence a reader would find in docs/services/ecr.md.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
)

// limitationService is uriService's twin: no Docker configured, so the
// registry never starts. Kept separate so a future edit to uriService (used
// by the repositoryUri tests) cannot silently change what this test exercises.
func limitationService(t *testing.T) *Service {
	t.Helper()
	return New(
		&config.Config{Hostname: "localhost.overcast.sh", Port: 4566, Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(),
		zap.NewNop(),
		clock.New(),
	)
}

func TestCreateRepository_marksLimitationWhenNoRegistryIsRunning(t *testing.T) {
	s := limitationService(t)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"repositoryName":"no-registry"}`))
	rec := httptest.NewRecorder()

	s.createRepository(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("CreateRepository status = %d, want 200 (metadata still succeeds): %s", rec.Code, rec.Body.String())
	}
	got := protocol.Limitations(rec.Header())
	if len(got) != 1 {
		t.Fatalf("limitation headers = %v, want exactly one — CreateRepository just handed out a repositoryUri nothing backs", got)
	}
	if !strings.Contains(got[0], "405") {
		t.Errorf("limitation = %q, want it to name the 405 a docker push against this address gets", got[0])
	}
}

func TestCreateRepository_marksLimitationWhenTheRegistryWasNeverProvenReachable(t *testing.T) {
	s := limitationService(t)
	// Given: a registry container started, but the daemon-reachability probe
	// never confirmed it answers (adoptRegistryAddress(_, false) is exactly
	// what selectDaemonReachablePort records on that outcome).
	s.adoptRegistryAddress(4510, false)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"repositoryName":"unproven"}`))
	rec := httptest.NewRecorder()

	s.createRepository(rec, req)

	got := protocol.Limitations(rec.Header())
	if len(got) != 1 {
		t.Fatalf("limitation headers = %v, want exactly one for an unproven registry", got)
	}
	if !strings.Contains(got[0], "never confirmed") {
		t.Errorf("limitation = %q, want it to say the daemon never confirmed reachability", got[0])
	}
}

func TestCreateRepository_noLimitationWhenTheRegistryIsProven(t *testing.T) {
	s := limitationService(t)
	// Given: the daemon itself answered the probe at this address — the only
	// state selectDaemonReachablePort records as proved.
	s.adoptRegistryAddress(4510, true)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"repositoryName":"proven"}`))
	rec := httptest.NewRecorder()

	s.createRepository(rec, req)

	if got := protocol.Limitations(rec.Header()); len(got) != 0 {
		t.Errorf("limitation headers = %v, want none — the registry was demonstrated to work", got)
	}
}

// The RPCv2-CBOR wire path (createRepositoryTyped, dispatched through
// op.Typed) does not get an http.ResponseWriter — see op.Limitationer's doc
// comment for why — so it is exercised directly here rather than through
// Dispatch, matching how the legacy-path tests above call createRepository
// directly.
func TestCreateRepositoryTyped_reportsTheSameLimitationAsTheLegacyPath(t *testing.T) {
	s := limitationService(t)
	ctx := context.Background()

	resp, aerr := s.createRepositoryTyped(ctx, &createRepositoryRequest{RepositoryName: "typed-no-registry"})
	if aerr != nil {
		t.Fatalf("CreateRepository (typed): %v", aerr)
	}
	lims := resp.EmulationLimitations()
	if len(lims) != 1 || !strings.Contains(lims[0], "405") {
		t.Fatalf("EmulationLimitations() = %v, want the same no-registry sentence the legacy path marks", lims)
	}

	s.adoptRegistryAddress(4511, true)
	resp2, aerr := s.createRepositoryTyped(ctx, &createRepositoryRequest{RepositoryName: "typed-proven"})
	if aerr != nil {
		t.Fatalf("CreateRepository (typed, proven): %v", aerr)
	}
	if lims := resp2.EmulationLimitations(); len(lims) != 0 {
		t.Errorf("EmulationLimitations() = %v, want none once the registry is proven", lims)
	}
}
