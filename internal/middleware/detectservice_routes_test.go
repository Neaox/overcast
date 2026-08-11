package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/router"
	"github.com/Neaox/overcast/internal/state"
)

// registeredRouteClassification records how detectService labels an *unsigned*
// request to every family of route the router registers. It is the committed
// answer to "which paths does the prefix switch actually cover", and the whole
// point of writing it down is that it cannot be read off the switch: the switch
// says what it claims, this says what the routing table needs.
//
// Unsigned is the case under test because it is the only one the prefix switch
// decides. A request carrying a SigV4 credential scope is classified from that
// scope at step 3 for everything the switch does not claim, which is why most
// of the entries below read "s3" without anything being broken — see the
// per-group notes. Asserting the signed case as well would mostly assert that
// serviceFromAuthCredential echoes its input.
//
// A family is a route's first path segment, or its first two when that segment
// is a shared version prefix (/v1, /v2). Values are the classification, with
// "|" joining the set when one family produces more than one.
//
// **When this test fails, the fix is not automatically to edit this map.**
// A new entry means a service registered routes on a path nothing classified
// before. Decide which of these it is:
//
//   - The route is served, and unsigned callers reach it (a browser, a webhook,
//     an SDK with no credentials). Then "s3" here is a real defect: the request
//     is labelled s3 in logs, and IAM enforcement authorises it as an `s3:`
//     action. Add the prefix to detectService.
//   - The route is only ever reached by signed AWS SDK traffic. Then "s3" is
//     the fall-through and the credential scope does the work. Record it here
//     with a note saying so, and change nothing else.
//
// Changing an existing entry is the louder signal: a path that used to be
// classified as one service and is now another has moved which IAM action a
// policy is evaluated against.
var registeredRouteClassification = map[string]string{
	// S3's catch-alls. Everything unclaimed lands here by design; "/" and "/*"
	// are the restFallback registrations, not S3 routes as such.
	"/":            "s3",
	"/*":           "s3",
	"/favicon.ico": "s3",

	// Dated API-version prefixes, all claimed explicitly by detectService.
	// TestDetectServiceCoversModeledLambdaVersions guards Lambda's list against
	// a model refresh adding one; the other three services bind a single
	// version each.
	"/2013-04-01": "route53",
	"/2015-02-01": "efs",
	"/2015-03-31": "lambda",
	"/2017-03-31": "lambda",
	"/2017-10-31": "lambda",
	"/2018-10-31": "lambda",
	"/2019-09-30": "lambda",
	"/2020-04-22": "lambda",
	"/2020-05-31": "cloudfront",
	"/2020-06-30": "lambda",
	"/2021-01-01": "opensearch",
	"/2021-10-31": "lambda",
	"/2021-11-15": "lambda",

	// Emulator-internal paths. S3 bucket names cannot begin with "_", so these
	// never reach the S3 fallback; internalService maps the known owners and
	// calls the rest "internal". The multi-valued entries are directories with
	// several owners under them (/_overcast/cognito, /_overcast/inbox, …).
	"/_":              "internal",
	"/_apigateway":    "internal",
	"/_appconfig":     "internal",
	"/_appconfigdata": "internal",
	"/_appsync":       "appsync",
	"/_cloudfront":    "cloudfront",
	"/_cognito":       "cognito",
	"/_ecs":           "ecs",
	"/_elb":           "internal",
	"/_events":        "events|internal",
	"/_health":        "internal",
	"/_internal":      "internal",
	"/_lambda":        "lambda",
	"/_mcp":           "internal",
	"/_metrics":       "metrics",
	"/_overcast":      "cognito|internal|secretsmanager|ses",
	"/_rds":           "internal",
	"/_topology":      "internal",

	// Undated literals the prefix switch claims.
	"/apikeys":      "apigateway",
	"/applications": "appregistry",
	// Bedrock Runtime's inference bindings. Claimed by the whole path shape
	// rather than by the "/model" prefix, because that segment is also a legal
	// S3 bucket name — see isBedrockRuntimeInferencePath.
	"/model":    "bedrock",
	"/restapis": "apigateway",
	// EventBridge Scheduler serves both of these to unsigned callers as well as
	// signed ones, and its own tag operations are reached through the shared
	// /tags dispatcher rather than a prefix of its own.
	"/schedule-groups": "scheduler",
	"/schedules":       "scheduler",
	"/usageplans":      "apigateway",
	"/v1/apis":         "appsync",
	"/v1/domainnames":  "appsync",
	"/v1/mergedApis":   "appsync",
	"/v1/pipes":        "pipes",
	"/v1/sourceApis":   "appsync",
	"/v1/tags":         "appsync",
	"/v2/email":        "ses",

	// /v2/apis is shared by API Gateway v2 and AppSync, and is the one place the
	// switch already consults the credential scope before answering. Unsigned it
	// resolves to API Gateway.
	"/v2/apis": "apigateway",

	// Everything below is served by a real route but classified from the SigV4
	// credential scope, not from the path — "s3" is what an unsigned request
	// gets, and every AWS SDK signs. Adding prefixes for them would not be free:
	// each of these first segments is also a legal S3 bucket name, and the
	// prefix switch runs ahead of the credential scope, so claiming one takes
	// the path away from a genuine S3 request to a bucket of that name.
	//
	//   API Gateway: /@connections (WebSocket management), /account,
	//   /domainnames, /tags, /vpclinks, and their /v2 forms.
	"/@connections":   "s3",
	"/account":        "s3",
	"/domainnames":    "s3",
	"/tags":           "s3",
	"/vpclinks":       "s3",
	"/v2/domainnames": "s3",
	"/v2/tags":        "s3",
	"/v2/vpclinks":    "s3",
	//   App Registry: /applications is claimed above, its sibling is not.
	"/attribute-groups": "s3",
	//   EKS, which is why the credential scope has to stay the primary signal:
	//   it is fully served with no prefix entry at all.
	"/access-policies":  "s3",
	"/addons":           "s3",
	"/cluster-versions": "s3",
	"/clusters":         "s3",
	//   MSK.
	"/v1/clusters":       "s3",
	"/v1/configurations": "s3",
	"/v1/kafka-versions": "s3",
	"/v2/clusters":       "s3",
	//   Smithy RPC v2 (/service/{service}/operation/{operation}); the dispatcher
	//   reads the service from the path itself and delegates to S3 without a
	//   Smithy-Protocol header, so the s3 label matches what happens.
	"/service": "s3",
	//   Label-rooted routes, indistinguishable from a bucket path by shape:
	//   SQS's path-style queue URLs and Cognito's per-pool OIDC discovery
	//   documents, the latter fetched unsigned by JWT libraries.
	"/{accountID:[0-9]+}": "s3",
	"/{region}":           "s3",
}

// buildTagGatedRouteFamilies are registered only in some build configurations,
// so their absence is not drift. /_mcp is a !slim route (see mcp_routes.go and
// mcp_routes_slim.go), and CI runs the suite under -tags slim,dev.
var buildTagGatedRouteFamilies = map[string]bool{"/_mcp": true}

// TestDetectServiceClassifiesEveryRegisteredRouteFamily walks the real router
// and checks every route family against the table above.
//
// detectService's prefix switch is hand-written, and this is what stops that
// being silent. A service added with a new path root, or an existing service
// gaining an API version, changes the routing table without changing the
// switch — and nothing about that is visible until someone reads a log line
// labelled s3 for a request that plainly is not S3, or an IAM policy denies an
// `s3:` action nobody wrote. Enumerating the routes turns that into a failure
// at the commit that introduces it.
func TestDetectServiceClassifiesEveryRegisteredRouteFamily(t *testing.T) {
	// Given: every route family the router registers, classified unsigned.
	got := classifyRegisteredRouteFamilies(t)
	if len(got) == 0 {
		t.Fatal("walked no routes")
	}

	// When: each is compared with the committed classification.
	var unexpected, changed, missing []string
	for _, family := range sortedFamilies(got) {
		want, recorded := registeredRouteClassification[family]
		switch {
		case !recorded:
			unexpected = append(unexpected, family+" -> "+got[family])
		case got[family] != want:
			changed = append(changed, family+": was "+want+", now "+got[family])
		}
	}
	for family := range registeredRouteClassification {
		if _, found := got[family]; !found && !buildTagGatedRouteFamilies[family] {
			missing = append(missing, family)
		}
	}
	sort.Strings(missing)

	// Then: the switch and the routing table still agree about every path.
	if len(unexpected) > 0 {
		t.Errorf("routes registered on paths nothing classifies:\n\t%s\n"+
			"Read registeredRouteClassification's doc comment before adding these to it.",
			strings.Join(unexpected, "\n\t"))
	}
	if len(changed) > 0 {
		t.Errorf("classification moved, which moves the IAM action these paths are authorised as:\n\t%s",
			strings.Join(changed, "\n\t"))
	}
	if len(missing) > 0 {
		t.Errorf("recorded families no longer registered — delete them, or add them to buildTagGatedRouteFamilies:\n\t%s",
			strings.Join(missing, "\n\t"))
	}
}

func classifyRegisteredRouteFamilies(t *testing.T) map[string]string {
	t.Helper()
	cfg := &config.Config{
		Host:      "127.0.0.1",
		Port:      0,
		Region:    "us-east-1",
		AccountID: "000000000000",
		State:     config.StateBackendMemory,
		LogLevel:  "error",
	}
	handler, preShutdown, cleanup, _ := router.New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	t.Cleanup(func() {
		preShutdown()
		cleanup(t.Context())
	})
	routes, ok := handler.(chi.Routes)
	if !ok {
		t.Fatal("router.New did not return a chi.Routes")
	}

	seen := map[string]map[string]bool{}
	if err := chi.Walk(routes, func(_, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// detectService reads the path, not the method, so one probe per route
		// is enough and GET is as good as any.
		request := httptest.NewRequest(http.MethodGet, concreteRoutePath(route), nil)
		family := routeFamily(route)
		if seen[family] == nil {
			seen[family] = map[string]bool{}
		}
		seen[family][middleware.DetectServiceForTest(request)] = true
		return nil
	}); err != nil {
		t.Fatalf("walking the router: %v", err)
	}

	classification := make(map[string]string, len(seen))
	for family, services := range seen {
		names := make([]string, 0, len(services))
		for name := range services {
			names = append(names, name)
		}
		sort.Strings(names)
		classification[family] = strings.Join(names, "|")
	}
	return classification
}

func sortedFamilies(classification map[string]string) []string {
	families := make([]string, 0, len(classification))
	for family := range classification {
		families = append(families, family)
	}
	sort.Strings(families)
	return families
}

var routeLabel = regexp.MustCompile(`\{[^}]*\}`)

// concreteRoutePath turns a chi route pattern into a request path: labels and
// wildcards become an ordinary segment, which is all detectService needs since
// it matches prefixes rather than parsing the path.
func concreteRoutePath(route string) string {
	path := strings.ReplaceAll(routeLabel.ReplaceAllString(route, "x"), "*", "x")
	if path == "" {
		return "/"
	}
	return path
}

// routeFamily reduces a route to the unit detectService actually decides on:
// its first path segment, or its first two where the first is a version prefix
// several services share.
func routeFamily(route string) string {
	segments := strings.SplitN(strings.TrimPrefix(route, "/"), "/", 3)
	family := "/" + segments[0]
	if (family == "/v1" || family == "/v2") && len(segments) > 1 {
		family += "/" + segments[1]
	}
	return family
}
