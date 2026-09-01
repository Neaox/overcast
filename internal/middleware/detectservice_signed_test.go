package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/internal/middleware"
)

// TestDetectServiceClassifiesEverySignedRouteByItsSigningName is the signed
// counterpart of TestDetectServiceClassifiesEveryRegisteredRouteFamily, and it
// closes what that test's own doc comment leaves open. The unsigned test
// records "s3" for every family the prefix switch does not claim and calls it
// correct because "the credential scope does the work". That is true only when
// the scope's service component is a name the rest of the emulator recognises,
// and for MSK and AppRegistry it was not. (CloudWatch and ELBv2 had the same
// defect but reach no REST binding, so they are covered by the invariant
// below rather than by this walk.)
//
// The models say what a real SDK signs with: every REST binding carries the
// signing name of the service that declares it, alongside the Overcast key that
// owns it. So the assertion needs no table — for each route the router
// registers, sign it the way AWS signs it and require the classifier to name
// the service that binds it.
//
// Failing this test means a signed SDK call to that route is classified as
// something the generated registry cannot key on, so no operation is named, so
// IAMEnforce's unnamed-action branch lets it through unauthorized.
func TestDetectServiceClassifiesEverySignedRouteByItsSigningName(t *testing.T) {
	// Given: every route the router registers, paired with the modeled binding
	// it matches and the signing name that binding declares.
	registry := awsapi.NewRegistry()
	served := implementedServices(t)
	type probe struct {
		route   string
		path    string
		signing string
		want    string
	}
	var probes []probe

	walkRegisteredRoutes(t, func(method, route, path string) {
		claim, ok := registry.ClaimRESTQuery(method, path, "")
		switch {
		case !ok, claim.Ambiguous:
			// Nothing modeled here, or several services declare the shape and
			// the models decline to attribute it. Either way there is no
			// single signing name to sign with.
			return
		case claim.Service == "" || claim.SigningName == "":
			return
		case claim.SigningName == claim.Service:
			// The two names agree, so this route cannot exercise the mapping.
			return
		case !served[claim.Service]:
			// A modeled binding whose shape a route of ours happens to match,
			// belonging to a service Overcast does not implement. Asserting
			// here would be asserting about someone else's paths.
			return
		}
		probes = append(probes, probe{route: route, path: path, signing: claim.SigningName, want: claim.Service})
	})

	if len(probes) == 0 {
		t.Fatal("walked no route whose signing name differs from its service key; " +
			"either the walk broke or the models changed shape")
	}

	// When: each is classified from the credential scope a real SDK would send.
	var wrong []string
	seen := map[string]bool{}
	for _, p := range probes {
		request := signedGet(p.path, p.signing)
		got := middleware.DetectServiceForTest(request)
		if got == p.want {
			continue
		}
		// One line per route, not per method: the classifier reads the path.
		line := p.route + " (signed " + p.signing + ") -> " + got + ", want " + p.want
		if seen[line] {
			continue
		}
		seen[line] = true
		wrong = append(wrong, line)
	}
	sort.Strings(wrong)

	// Then: every one of them names the service that owns the binding.
	if len(wrong) > 0 {
		t.Errorf("signed requests classified as their signing name rather than their service key,\n"+
			"so restOperation names no operation and IAM enforcement fails open on them:\n\t%s",
			strings.Join(wrong, "\n\t"))
	}
}

// TestDetectServiceAlwaysAnswersWithAServiceKey is the same defect stated as an
// invariant rather than a route list, so a service added later is covered
// without anyone remembering to extend a table. Whatever detectService returns
// is used as an Overcast service key by every one of its callers — the
// generated registry lookup, the resource resolvers, the error-envelope switch
// — so an answer that is really some service's signing name is a bug by
// construction, whether or not a route exists to reach it today.
func TestDetectServiceAlwaysAnswersWithAServiceKey(t *testing.T) {
	// Given: every signing name the pinned models attach to a REST binding,
	// against every route family the router registers plus the bare root that
	// the Query and JSON protocols use.
	paths := append(registeredRoutePaths(t), "/")

	var wrong []string
	seen := map[string]bool{}
	for _, signingName := range modeledSigningNames() {
		for _, path := range paths {
			got := middleware.DetectServiceForTest(signedGet(path, signingName))

			// When: the answer is fed back through the mapping.
			// Then: it is already a service key, so it does not move.
			key := middleware.ServiceKeyForSigningNameForTest(got)
			if key == got {
				continue
			}
			// Reported once per (scope, answer) with one example path. The
			// path is almost never the interesting part — it decides only
			// whether the prefix switch answered before the scope was read —
			// and a violation otherwise repeats for every route in the table.
			violation := "scope " + signingName + " -> " + got + ", which is " + key + "'s signing name"
			if seen[violation] {
				continue
			}
			seen[violation] = true
			wrong = append(wrong, violation+" (e.g. "+path+")")
		}
	}
	sort.Strings(wrong)

	if len(wrong) > 0 {
		t.Errorf("detectService returned a signing name where a service key is required:\n\t%s",
			strings.Join(wrong, "\n\t"))
	}
}

// signedGet builds a GET carrying nothing but a SigV4 credential scope, which
// is the only service signal an SDK sends on a path with no distinguishing
// prefix.
func signedGet(path, signingName string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=AKID/20260811/us-east-1/"+signingName+
			"/aws4_request, SignedHeaders=host;x-amz-date, Signature=deadbeef")
	return r
}

// implementedServices is the set of Overcast service keys that have a service
// package. The signed walk asserts only about these: a modeled binding
// belonging to a service Overcast does not serve carries no obligation for how
// a request to it is labelled, and asserting about one would mean mapping every
// signing name in AWS.
//
// Read from the tree rather than listed, so adding a service brings it into
// this guard without anyone remembering to. Two keys are not directory names —
// EventBridge's package is "eventbridge" and answers to "events", and
// CloudWatch Logs has no package of its own — and neither declares a REST
// binding whose signing name differs from its key, so neither reaches the
// filter. They are named here so that stays a fact someone checked rather than
// one nobody noticed.
func implementedServices(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "services"))
	if err != nil {
		t.Fatalf("reading the service packages: %v", err)
	}
	services := map[string]bool{"events": true, "logs": true}
	for _, entry := range entries {
		if entry.IsDir() {
			services[entry.Name()] = true
		}
	}
	if len(services) < 40 {
		t.Fatalf("found only %d service packages; the walk is looking in the wrong place", len(services))
	}
	return services
}

// modeledSigningNames returns every signing name the pinned models attach to a
// REST binding. It is deliberately not filtered to the services Overcast
// implements: a signing name Overcast does not serve maps to itself, so it
// satisfies the invariant trivially and costs nothing to sweep, while a filter
// would quietly stop covering a service the day its package appeared.
func modeledSigningNames() []string {
	names := map[string]bool{}
	awsapi.WalkSigningNames(func(_, signingName string) bool {
		names[signingName] = true
		return true
	})
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// walkRegisteredRoutes visits every method and route the real router registers,
// with the route pattern reduced to a concrete request path.
func walkRegisteredRoutes(t *testing.T, visit func(method, route, path string)) {
	t.Helper()
	routes := newTestRouterRoutes(t)
	if err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		visit(method, route, concreteRoutePath(route))
		return nil
	}); err != nil {
		t.Fatalf("walking the router: %v", err)
	}
}

// registeredRoutePaths returns one concrete path per registered route.
func registeredRoutePaths(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	walkRegisteredRoutes(t, func(_, _, path string) { seen[path] = true })
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
