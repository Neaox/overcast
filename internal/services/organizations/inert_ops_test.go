package organizations

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/inert"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/op"
	"github.com/overcast-sh/overcast/internal/state"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	st := state.NewMemoryStore()
	t.Cleanup(func() { _ = st.Close() })
	return New(&config.Config{Region: "us-east-1", AccountID: "000000000000"}, st, zap.NewNop(), clock.NewMock())
}

// TestInertBindings_AreSorted holds inert.Lookup's precondition. The
// invariant is invisible at the call site, so a table emitted (or edited)
// out of order would otherwise resolve some operations to the wrong handler
// in silence rather than failing.
func TestInertBindings_AreSorted(t *testing.T) {
	bindings := newTestService(t).inertBindings()
	if !inert.Sorted(bindings) {
		names := make([]string, 0, len(bindings))
		for _, b := range bindings {
			names = append(names, b.Op)
		}
		t.Fatalf("inertBindings is not strictly sorted by Op: %v", names)
	}
	if len(bindings) == 0 {
		t.Fatal("inertBindings is empty")
	}
}

// TestInertBindings_EveryBindingIsReachableByName is the other half of the
// sorted-slice contract: sorted-ness alone does not prove the search finds
// every entry.
func TestInertBindings_EveryBindingIsReachableByName(t *testing.T) {
	bindings := newTestService(t).inertBindings()
	for _, want := range bindings {
		got, ok := inert.Lookup(bindings, want.Op)
		if !ok {
			t.Fatalf("Lookup(%q) reported not found", want.Op)
		}
		if got.Invoke.Name() != want.Op {
			t.Fatalf("Lookup(%q) resolved to the handler named %q", want.Op, got.Invoke.Name())
		}
	}
}

// TestInertBindings_CollisionsAreDeclared is §4.5(3): a binding shadowed by a
// hand-written operation has to be listed in inert_overrides.txt, so a
// collision is something a reviewer sees rather than dead code that looks
// live.
func TestInertBindings_CollisionsAreDeclared(t *testing.T) {
	bindings := newTestService(t).inertBindings()
	declared := readOverrides(t)

	if got := inert.Collisions(bindings, handWrittenOps, declared); len(got) > 0 {
		t.Fatalf("Tier 1 bindings %v are shadowed by a hand-written implementation and are not listed in "+
			"inert_overrides.txt — dispatch will never reach them, so either drop the binding or declare it", got)
	}

	// An override that no longer collides is stale: it says a review decision
	// still applies when it does not.
	for _, name := range declared {
		colliding := slices.ContainsFunc(bindings, func(b inert.Binding) bool { return b.Op == name })
		if !colliding || !slices.Contains(handWrittenOps, name) {
			t.Fatalf("inert_overrides.txt lists %q, which is no longer a collision — remove the line", name)
		}
	}
}

// TestHandWrittenOps_MatchTheTypedTable keeps the declared precedence list
// honest. handWrittenOps is what dispatch and the collision check both read;
// if it drifts from the actual hand-written table, precedence stops meaning
// what §4.5 says it means.
func TestHandWrittenOps_MatchTheTypedTable(t *testing.T) {
	typed := newTestService(t).typedOps()
	if len(typed) != len(handWrittenOps) {
		t.Fatalf("handWrittenOps has %d entries, typedOps has %d", len(handWrittenOps), len(typed))
	}
	for _, name := range handWrittenOps {
		if _, ok := typed[name]; !ok {
			t.Fatalf("handWrittenOps names %q, which typedOps does not implement", name)
		}
	}
}

// TestDispatch_HandWrittenWinsOverAnInertBinding is §4.5's rule exercised
// through the real dispatcher rather than asserted about the tables: with a
// binding deliberately installed on top of a hand-written operation, the
// hand-written response is the one that comes back.
func TestDispatch_HandWrittenWinsOverAnInertBinding(t *testing.T) {
	// Given: a service whose binding table shadows DescribeOrganization.
	s := newTestService(t)
	shadow := op.NewTyped("DescribeOrganization", func(context.Context, *describeOrganizationRequest) (*map[string]string, *protocol.AWSError) {
		return &map[string]string{"ServedBy": "inert-binding"}, nil
	})
	s.bindings = append(s.bindings, inert.Binding{Op: "DescribeOrganization", Class: inert.ClassRead, Resource: "organization", Invoke: shadow})
	slices.SortFunc(s.bindings, func(a, b inert.Binding) int { return strings.Compare(a.Op, b.Op) })

	// When: DescribeOrganization is dispatched.
	body := dispatchJSON(t, s, "DescribeOrganization", map[string]any{})

	// Then: the hand-written implementation answered, not the binding.
	if _, servedByShadow := body["ServedBy"]; servedByShadow {
		t.Fatalf("the inert binding answered DescribeOrganization: %v — hand-written must always win (§4.5)", body)
	}
	if _, ok := body["Organization"]; !ok {
		t.Fatalf("DescribeOrganization returned %v, want the hand-written Organization response", body)
	}
}

// TestDispatch_UnboundOperationStaysTier0 is §3.6's default: an operation
// with no binding returns the protocol-correct 501, not a fabricated
// success. AttachPolicy is the case that matters here — it is the verb the
// policy resource's whole point would tempt someone to fake — and MoveAccount
// is its counterpart for the organizational-unit tree: an OU exists to hold
// accounts, and this emulator holds none.
func TestDispatch_UnboundOperationStaysTier0(t *testing.T) {
	s := newTestService(t)
	for _, name := range []string{"AttachPolicy", "CreateAccount", "MoveAccount", "ListParents"} {
		rec := dispatch(t, s, name, map[string]any{})
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s returned %d, want 501", name, rec.Code)
		}
	}
}

// readOverrides parses inert_overrides.txt: one operation name per line,
// blank lines and #-comments ignored.
func readOverrides(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("inert_overrides.txt")
	if err != nil {
		t.Fatalf("reading inert_overrides.txt: %v", err)
	}
	var out []string
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanning inert_overrides.txt: %v", err)
	}
	return out
}

// dispatch drives one operation through Service.Dispatch the way an AWS JSON
// 1.1 SDK client would: a POST to / carrying X-Amz-Target.
func dispatch(t *testing.T, s *Service, opName string, fields map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshalling %s input: %v", opName, err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", targetPrefix+opName)
	rec := httptest.NewRecorder()
	s.Dispatch(rec, req)
	return rec
}

func dispatchJSON(t *testing.T, s *Service, opName string, fields map[string]any) map[string]any {
	t.Helper()
	rec := dispatch(t, s, opName, fields)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s returned %d with an unparseable body %q: %v", opName, rec.Code, rec.Body.String(), err)
		}
	}
	return out
}
