//go:build dev

package router

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/internal/capabilities"
	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// This file is the gate #863 and #864 ask for: for every operation Overcast
// declares it emulates, assert that a route is registered at the method and
// URI the pinned AWS model binds it to.
//
// The manifest has carried that binding for every one of 18,850 operations
// since it landed, and until now exactly one caller consulted it — capgen's
// awsapi.HasOperation, a name-only check that asks "is this a real AWS
// operation?" and never "is it registered where AWS puts it?". Ten services
// shipped working implementations behind paths and methods no SDK sends
// (#793, #815, #854-#860, #862), each of them contradicted by a file in the
// same repository.
//
// # What this gate can prove, and what it cannot
//
// The manifest carries the @http binding and nothing else. It has no member
// shapes, so a member bound to the query string does not appear in the URI
// template. This gate therefore proves the method and the path, and says
// nothing about the request *shape*. It would not have caught the second half
// of #793, where GroupName is an httpQuery member that Overcast served as a
// path segment: the path was wrong there too, so the gate would have fired,
// but a service that puts an httpQuery member in the body would pass it. See
// docs/plans/manifest-enforcement.md § Boundary for the decision on where
// member bindings should be enforced instead.

func TestModeledBindings_areServedWhereAWSBindsThem(t *testing.T) {
	// Given: every route the router registers, including the sub-routers it
	// dispatches to at request time.
	routes := inventory(t)

	// When: each declared, dispatched capability is located in the model and
	// its binding looked for among those routes.
	var unserved []bindingFault
	for _, cap := range dispatchedCapabilities() {
		for _, want := range restBindings(cap) {
			if routes.serves(want) != coverageNone {
				continue
			}
			unserved = append(unserved, bindingFault{cap: cap, want: want, otherMethods: routes.methodsFor(want)})
		}
	}

	// Then: only the bindings the ledger already accounts for are unserved,
	// and every ledger entry is still needed.
	assertLedger(t, unserved)
	assertUnreachableRowsDoNotClaimSupport(t, unserved)
}

// assertUnreachableRowsDoNotClaimSupport is #864's fourth enforcement point,
// tied to the third so it cannot drift: StatusSupported has to mean reachable.
//
// The support matrix is what a user consults to find out whether Overcast can
// do a thing. A ✅ against an operation no SDK can call is worse than a ❌,
// because it is acted on. #861 found ElastiCache advertising two operations
// that always answered 501, and #862 found SESv2 advertising one that answered
// 405 to every client — and in both cases the emulator's own records said so
// plainly enough for a machine to have caught it.
//
// The honest grades for an unserved row are WIP (it exists but is known
// incorrect) or Unsupported (it answers 501). Supported and Inert both promise
// the caller gets through.
func assertUnreachableRowsDoNotClaimSupport(t *testing.T, unserved []bindingFault) {
	t.Helper()

	var overclaimed []string
	for _, fault := range unserved {
		switch fault.cap.Status {
		case capabilities.StatusSupported, capabilities.StatusInert:
			overclaimed = append(overclaimed, fmt.Sprintf("%s declares %s but %s is unserved (%s)",
				fault.key(), fault.cap.Status, fault.want, unservedBindings[fault.key()]))
		case capabilities.StatusUnsupported, capabilities.StatusWIP, capabilities.StatusPartial:
			// Unsupported rows never reach here, and WIP and Partial both
			// already tell the reader not to rely on the operation.
		}
	}
	sort.Strings(overclaimed)

	if len(overclaimed) > 0 {
		t.Errorf("%d operation(s) advertise support for a binding no client can reach:\n\t%s\n\n"+
			"Register the modeled route, or declare the row WIP or Unsupported. A ✅ in the support matrix is "+
			"read as a promise that the call works.", len(overclaimed), strings.Join(overclaimed, "\n\t"))
	}
}

// TestWeaklyServedBindings_areRecorded names every operation whose only proof
// of being served is weaker than a route registered at its modeled binding.
//
// #864 asks explicitly not to over-claim, and "the router delivers this
// request somewhere" is not "this service serves this operation". Two things
// produce the gap: a subtree wildcard, where a handler parses the rest of the
// path itself and may still answer 501; and API Gateway's fallback tag store,
// which answers /tags for every ARN nobody claims. The second is exactly the
// shape of #854's worst symptom — a plausible 200 from a service that was not
// asked. Recording the set makes it a reviewable number instead of a silent
// pass, and makes it fail when it grows.
func TestWeaklyServedBindings_areRecorded(t *testing.T) {
	// Given: the router's registered surface.
	routes := inventory(t)

	// When: every declared, dispatched capability's binding is graded.
	weak := map[string]string{}
	for _, cap := range dispatchedCapabilities() {
		for _, want := range restBindings(cap) {
			got := routes.serves(want)
			if got == coverageNone || got == coverageExact {
				continue
			}
			weak[want.service+"/"+want.operation] = got.String()
		}
	}

	// Then: the graded-weak set is the one recorded, entry for entry.
	assertWeaklyServed(t, weak)
}

// dispatchedCapabilities returns the capabilities that claim to answer a
// request. StatusUnsupported rows are excluded because 501 is what they
// promise; every other status — Supported, Partial, WIP, Inert — claims the
// operation is dispatched, and an operation that is dispatched has to be
// dispatched from somewhere a client will call.
//
// DocOnly rows are deliberately included. Excluding them is what hid #862 for
// 33 releases: SESv2's CreateEmailIdentity was DocOnly and dispatched, so the
// capgen cross-check skipped it and the reachability probe in #861 never sent
// it a request. capgen now rejects a DocOnly row that has an implementation
// (see checkDocOnlyRowsAreNotDispatched), which makes the flag a claim this
// gate is entitled to test rather than an exemption from being tested.
func dispatchedCapabilities() []capabilities.Capability {
	all := capabilities.Default.All()
	out := make([]capabilities.Capability, 0, len(all))
	for _, cap := range all {
		if cap.Status == capabilities.StatusUnsupported {
			continue
		}
		out = append(out, cap)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Operation < out[j].Operation
	})
	return out
}

// binding is one modeled REST binding: the method and URI template AWS serves
// an operation on.
type binding struct {
	service   string
	operation string
	method    string
	uri       string
}

func (b binding) String() string {
	return fmt.Sprintf("%s %s", b.method, b.uri)
}

// restBindings returns the REST bindings a capability must be served at.
//
// Only REST-bound operations are checkable from a route table: an AWS JSON or
// Query operation is modeled as POST / and is dispatched from the X-Amz-Target
// header or the Action parameter, which no route pattern records. Those are
// covered by TestDeclaredProtocols_areAllDispatched instead.
//
// One Overcast key can front several modeled identities, and two of them can
// model the same operation name at different bindings — API Gateway v1 puts
// CreateModel at /restapis/{restapi_id}/models and v2 at /v2/apis/{ApiId}/models.
// modeledIdentity picks the one the declaration means, so a v1 capability is
// held to v1's binding rather than to both.
func restBindings(cap capabilities.Capability) []binding {
	identity := modeledIdentity(cap)
	var out []binding
	for _, op := range awsapi.Operations(cap.Service, modeledOperationName(cap)) {
		if op.Protocol != awsapi.ProtocolRESTJSON && op.Protocol != awsapi.ProtocolRESTXML {
			continue
		}
		if identity != "" && op.Service != identity {
			continue
		}
		out = append(out, binding{service: cap.Service, operation: cap.Operation, method: op.HTTPMethod, uri: op.URI})
	}
	return out
}

// modeledIdentity names the modeled service a capability declaration belongs
// to, or "" where the key fronts a single identity for this operation.
//
// API Gateway is the only key whose two identities model overlapping operation
// names, and Overcast already distinguishes them in the declaration: v2 rows
// carry V2 in the operation name (CreateV2Api, GetV2Route) and v1 rows do not.
// That is the same convention capgen's modeledOperationName strips to reach
// the AWS name, read here for the other half of the answer.
func modeledIdentity(cap capabilities.Capability) string {
	if cap.Service != "apigateway" {
		return ""
	}
	if strings.Contains(cap.Operation, "V2") {
		return "apigatewayv2"
	}
	return "api-gateway"
}

// modeledOperationName resolves a capability's declared operation name to the
// AWS operation name it stands for. DisplayName exists for exactly this — its
// documented purpose is "the internal operation identifier differs from the
// AWS API name (e.g. V2SendEmail -> SendEmail)" — and honouring it here is
// what lets SESv2's rows drop the DocOnly flag they were carrying only to
// silence a name mismatch.
//
// It intentionally mirrors capgen's function of the same name; both resolve
// the same declaration to the same model row, and a test that resolved names
// differently from the generator would certify a name the generator rejects.
func modeledOperationName(cap capabilities.Capability) string {
	if cap.DisplayName != "" {
		return cap.DisplayName
	}
	if cap.Service == "apigateway" {
		return strings.Replace(cap.Operation, "V2", "", 1)
	}
	return cap.Operation
}

// routeSet is the router's registered surface, indexed for binding lookups.
type routeSet struct {
	routes []registeredRoute
	// dispatchStubs are the patterns that only choose a sub-router — the
	// "/*" and "/" a runtime dispatcher is registered on, plus the root
	// fallback. Matching a binding against one of those would certify every
	// path beneath it, which is the opposite of what this gate is for: the
	// whole of #854's appconfig surface sits under a prefix that something
	// answers.
	dispatchStubs map[string]bool
}

func inventory(t *testing.T) *routeSet {
	t.Helper()
	return inventoryWith(t, func(*config.Config) {})
}

// inventoryWith is inventory with a chance to adjust the config first, for
// gates that need routes the default build does not register. Only the
// namespace gate uses it so far, to switch Debug on and make the /_overcast/debug/*
// namespace visible; the model-binding gates want the default surface, since a
// modeled AWS binding never hides behind a debug flag.
func inventoryWith(t *testing.T, configure func(*config.Config)) *routeSet {
	t.Helper()

	cfg := &config.Config{
		Host:      "127.0.0.1",
		Port:      0,
		Region:    "us-east-1",
		AccountID: "000000000000",
		State:     config.StateBackendMemory,
		LogLevel:  "error",
		DataDir:   t.TempDir(),
	}
	configure(cfg)
	handler, preShutdown, cleanup, _ := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	t.Cleanup(func() {
		preShutdown()
		cleanup(t.Context())
	})

	mux, ok := handler.(*inspectableMux)
	if !ok {
		t.Fatalf("New() returned %T, want *inspectableMux — the dev build must wrap the mux so dispatched sub-routers are visible", handler)
	}
	routes, err := walkRegisteredRoutes(handler)
	if err != nil {
		t.Fatalf("walking the router: %v", err)
	}

	stubs := map[string]bool{"/": true, "/*": true}
	for _, mount := range mux.mounts {
		if mount.Prefix == "" {
			continue
		}
		stubs[mount.Prefix+"/*"] = true
		stubs[mount.Prefix+"/"] = true
		stubs[mount.Prefix] = true
	}
	return &routeSet{routes: routes, dispatchStubs: stubs}
}

// coverage grades how strongly a route proves a binding is served. The
// ordering is meaningful: a stronger grade is a stronger proof, and serves
// reports the strongest one any route offers.
type coverage int

const (
	// coverageNone: no route would match a request at that method and path.
	coverageNone coverage = iota
	// coverageFallback: the request is delivered, but to the sub-router a
	// dispatcher falls back to for ARNs no service claims — a different
	// service's handler. It is delivery, not emulation by the right owner,
	// and #854 is what that looks like when it goes wrong.
	coverageFallback
	// coverageBroad: a route matches the modeled request and more besides —
	// a subtree wildcard over further modeled structure, or a parameter where
	// the model binds a literal. The router delivers the request to the right
	// service; which operation it reaches is decided inside the handler, so
	// the route table no longer records it.
	coverageBroad
	// coverageExact: a route is registered at this method and path shape.
	coverageExact
)

func (c coverage) String() string {
	switch c {
	case coverageFallback:
		return "another service's fallback handler"
	case coverageBroad:
		return "a route broader than the modeled binding"
	case coverageExact:
		return "an exact route"
	case coverageNone:
		return "nothing"
	default:
		return "nothing"
	}
}

// serves returns the strongest coverage any registered route gives a binding.
func (rs *routeSet) serves(want binding) coverage {
	segments := uriSegments(want.uri)
	best := coverageNone
	for _, route := range rs.routes {
		if route.Method != want.method {
			continue
		}
		if got := rs.covers(route, want, segments); got > best {
			best = got
		}
	}
	return best
}

// methodsFor returns the methods some route does serve at the binding's URI.
// A non-empty result is the #862 signature: the path is right and the method
// is wrong, which reads as a 405 to a client and as "unimplemented" to
// everyone reading the support matrix.
func (rs *routeSet) methodsFor(want binding) []string {
	segments := uriSegments(want.uri)
	seen := map[string]bool{}
	for _, route := range rs.routes {
		if route.Method == want.method || rs.covers(route, want, segments) == coverageNone {
			continue
		}
		seen[route.Method] = true
	}
	methods := make([]string, 0, len(seen))
	for method := range seen {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return methods
}

func (rs *routeSet) covers(route registeredRoute, want binding, uri []string) coverage {
	// A dispatched sub-router answers for its own service, and a fallback one
	// also answers for any service the dispatcher could not attribute. S3
	// makes the ownership rule load-bearing rather than tidy: its
	// bucket/object routes are deliberately broad enough to cover almost any
	// path, and they are consulted last precisely so they do not answer for
	// another service. A gate that let them count would report every unrouted
	// operation in the emulator as served.
	grade := coverageExact
	switch {
	case route.Owner == "":
		// A pattern registered directly on the mux serves whoever registered
		// it, except where it is only the dispatcher choosing a sub-router.
		if rs.dispatchStubs[route.Pattern] {
			return coverageNone
		}
	case route.Owner == want.service:
	case route.Fallback:
		grade = coverageFallback
	default:
		return coverageNone
	}

	if got := patternCovers(patternSegments(route.Pattern), uri); got < grade {
		grade = got
	}
	return grade
}

// patternCovers reports whether a chi pattern's segments would match the
// concrete path a modeled URI template names.
//
// Both sides are compared structurally rather than by string equality:
// parameter names differ freely between the model and the route ({Name} vs
// {scheduleName}) and carry no routing meaning, while a literal segment where
// the model binds a parameter is a real difference — it serves one value where
// AWS serves all of them.
//
// A route that matches more than the model binds is graded rather than
// accepted or rejected outright, because both readings are wrong. Rejecting it
// fails MSK, whose /v1/clusters/* handlers really do serve their subtree, and
// S3, for which /{bucket}/* is the only way to bind {Key+}. Accepting it
// silently is what let a shared prefix answer for a service that was never
// asked — #854's POST /applications returning an AppRegistry ARN with a 200.
// So a broader route counts as served and lands in the recorded set instead.
func patternCovers(pattern, uri []string) coverage {
	grade := coverageExact
	broaden := func() { grade = coverageBroad }

	for i, segment := range pattern {
		switch {
		case segment == "*":
			// A wildcard standing in for the model's last remaining segment
			// binds exactly what the model binds; over further modeled
			// structure it takes the routing decision into the handler.
			if i != len(uri)-1 {
				broaden()
			}
			return grade
		case i >= len(uri):
			return coverageNone
		case isConstrainedLabel(segment):
			// chi lets a label carry a regular expression
			// ({accountID:[0-9]+}), which admits a subset of segment values.
			// SQS's queue-URL route is the one in this tree, and without this
			// case it matches the first segment of every modeled URI in the
			// corpus — reporting the entire emulator as served.
			if isLabel(uri[i]) || !labelConstraintAccepts(segment, uri[i]) {
				return coverageNone
			}
			broaden()
		case isLabel(segment):
			// A greedy model label spans one or more segments, so only a
			// wildcard covers it, which the first case already returned for.
			if isGreedyLabel(uri[i]) {
				return coverageNone
			}
			if !isLabel(uri[i]) {
				broaden()
			}
		case isLabel(uri[i]):
			// The model binds a parameter here; a literal route serves one of
			// its values and 404s the rest.
			return coverageNone
		case segment != uri[i]:
			return coverageNone
		}
	}
	if len(pattern) != len(uri) {
		return coverageNone
	}
	return grade
}

func patternSegments(pattern string) []string { return strings.Split(pattern, "/") }

// labelConstraintAccepts reports whether a constrained chi label admits a
// literal path segment. chi anchors the expression to the whole segment.
func labelConstraintAccepts(segment, literal string) bool {
	expression := segment[strings.IndexByte(segment, ':')+1 : len(segment)-1]
	matched, err := regexp.MatchString(`^(?:`+expression+`)$`, literal)
	return err == nil && matched
}

func isConstrainedLabel(segment string) bool {
	return isLabel(segment) && strings.ContainsRune(segment, ':')
}

// uriSegments splits a modeled URI template into path segments, dropping the
// literal query string some templates carry.
//
// 139 modeled URIs pin a query parameter (/2020-05-31/distribution?WithTags,
// /apikeys?mode=import). chi routes on the path alone, so two operations that
// differ only in the query reach one route and the handler branches on the
// parameter. This gate can prove the path is served and cannot prove the
// branch exists; the query is dropped rather than silently failing every such
// binding. docs/plans/manifest-enforcement.md § Boundary records it.
func uriSegments(uri string) []string {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri = uri[:i]
	}
	if uri != "/" {
		uri = strings.TrimSuffix(uri, "/")
	}
	return strings.Split(uri, "/")
}

func isLabel(segment string) bool {
	return strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
}

func isGreedyLabel(segment string) bool {
	return isLabel(segment) && strings.HasSuffix(segment, "+}")
}

type bindingFault struct {
	cap          capabilities.Capability
	want         binding
	otherMethods []string
}

func (f bindingFault) key() string { return f.cap.Service + "/" + f.cap.Operation }

func (f bindingFault) String() string {
	detail := "no route registered at that path"
	if len(f.otherMethods) > 0 {
		detail = "registered at that path only as " + strings.Join(f.otherMethods, ", ")
	}
	return fmt.Sprintf("%s: AWS binds %s — %s", f.key(), f.want, detail)
}
