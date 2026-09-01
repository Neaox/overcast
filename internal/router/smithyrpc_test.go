package router

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/awsapi"
)

// stubRPCService is the smallest thing newSmithyRPCService accepts. Its
// identity is all these tests read: they assert *which* dispatcher was
// selected, never what it wrote.
type stubRPCService struct{ name string }

func (s *stubRPCService) Name() string                                    { return s.name }
func (s *stubRPCService) RegisterRoutes(_ chi.Router)                     {}
func (s *stubRPCService) TargetPrefix() string                            { return s.name + "." }
func (s *stubRPCService) Dispatch(_ http.ResponseWriter, _ *http.Request) {}

func newStubDispatchers(t *testing.T, names ...string) (map[string]*smithyRPCService, map[string]*smithyRPCService) {
	t.Helper()
	dispatchers := make(map[string]*smithyRPCService, len(names))
	byName := make(map[string]*smithyRPCService, len(names))
	for _, name := range names {
		stub := &stubRPCService{name: name}
		entry := newSmithyRPCService(stub, stub)
		dispatchers[name] = entry
		byName[name] = entry
	}
	return dispatchers, byName
}

// TestSmithyRPCServiceFor_ambiguousClaimResolvesFromThePathSegment is the
// guard this file exists for, and it has to synthesise its subject: the pinned
// models contain no ambiguous RPC binding today, so a test driven from real
// manifest data would pass whether or not the guard exists.
//
// awsmodelgen blanks Service on an ambiguous claim. A request whose service
// shape is spelled as an absolute Smithy shape ID therefore has exactly one
// usable signal left — the path segment — and refusing it would answer 501 for
// a service Overcast implements.
func TestSmithyRPCServiceFor_ambiguousClaimResolvesFromThePathSegment(t *testing.T) {
	// Given: a service registered under its target-prefix key, and a modeled
	// claim the generator could not attribute to any single service.
	dispatchers, byName := newStubDispatchers(t, "example_20200101")
	claim := awsapi.Claim{Operation: "GetThing", Ambiguous: true}

	// When: the caller spells the shape as an absolute shape ID, which Smithy
	// permits and ClaimRPC already normalises away before matching.
	service := smithyRPCServiceFor(dispatchers, "com.amazonaws.example#Example_20200101", claim, true)

	// Then: the path segment resolves the implemented service.
	if service != byName["example_20200101"] {
		t.Fatalf("ambiguous claim with a qualified shape ID resolved to %v; want the example_20200101 dispatcher", service)
	}
}

// TestSmithyRPCServiceFor_ambiguousClaimNeverIndexesTheEmptyKey pins the other
// half of the same defect. dispatchers[claim.Service] is not a failed lookup
// when Service is blank — it is a lookup for "", which would attribute an
// unattributable request to whatever happened to be registered under the empty
// key.
func TestSmithyRPCServiceFor_ambiguousClaimNeverIndexesTheEmptyKey(t *testing.T) {
	// Given: a dispatcher map that has acquired an empty key.
	dispatchers, byName := newStubDispatchers(t, "", "example")
	claim := awsapi.Claim{Operation: "GetThing", Ambiguous: true}

	// When: an ambiguous claim arrives for a shape nothing is registered under.
	service := smithyRPCServiceFor(dispatchers, "SomeOtherShape", claim, true)

	// Then: nothing is selected, so the caller gets an honest 501 rather than
	// another service's handler.
	if service != nil {
		t.Fatalf("ambiguous claim selected a dispatcher (empty key: %v); want none", service == byName[""])
	}
}

// TestSmithyRPCServiceFor_attributedClaimBridgesShapeToService keeps the
// existing bridge working: a modeled shape name that is neither a service name
// nor a target prefix is resolvable only through the claim's attribution.
func TestSmithyRPCServiceFor_attributedClaimBridgesShapeToService(t *testing.T) {
	// Given: a service registered under its Overcast key only.
	dispatchers, byName := newStubDispatchers(t, "cloudwatch")
	claim := awsapi.Claim{Service: "cloudwatch", Operation: "PutMetricData"}

	// When: the request names the modeled service shape.
	service := smithyRPCServiceFor(dispatchers, "GraniteServiceVersion20100801", claim, true)

	// Then: the attributed claim bridges it to the implementation.
	if service != byName["cloudwatch"] {
		t.Fatalf("attributed claim resolved to %v; want the cloudwatch dispatcher", service)
	}
}

// TestSmithyRPCServiceFor_unmodeledRequestUsesOnlyThePathSegment asserts the
// claim is not consulted at all when the registry did not match, which is what
// keeps an unmodeled shape from borrowing a zero Claim's blank service.
func TestSmithyRPCServiceFor_unmodeledRequestUsesOnlyThePathSegment(t *testing.T) {
	// Given: a dispatcher map with an empty key and no matching claim.
	dispatchers, byName := newStubDispatchers(t, "", "example")

	// When: an unmodeled shape that is registered is looked up, and one that is not.
	registered := smithyRPCServiceFor(dispatchers, "Example", awsapi.Claim{}, false)
	unregistered := smithyRPCServiceFor(dispatchers, "SomeOtherShape", awsapi.Claim{}, false)

	// Then: only the path segment decides.
	if registered != byName["example"] {
		t.Errorf("registered shape resolved to %v; want the example dispatcher", registered)
	}
	if unregistered != nil {
		t.Errorf("unregistered shape resolved to a dispatcher; want none")
	}
}
