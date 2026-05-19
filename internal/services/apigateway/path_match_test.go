package apigateway

import (
	"testing"
)

// ---- pathMatchScore --------------------------------------------------------

func TestPathMatchScore_exactSegments(t *testing.T) {
	// Given a template with exact segments
	// When scoring against a matching path
	score := pathMatchScore("/users/list", "/users/list")
	// Then the score should be positive (2 points per exact segment)
	if score != 4 {
		t.Errorf("expected score 4, got %d", score)
	}
}

func TestPathMatchScore_singleParam(t *testing.T) {
	// Given a template with a path parameter
	// When scoring against a matching path
	score := pathMatchScore("/users/{id}", "/users/42")
	// Then score = exact(2) + param(1) = 3
	if score != 3 {
		t.Errorf("expected score 3, got %d", score)
	}
}

func TestPathMatchScore_greedyParam(t *testing.T) {
	// Given a greedy template
	// When scoring against a multi-segment path
	score := pathMatchScore("/{proxy+}", "/a/b/c")
	// Then it should match with score 1 (greedy)
	if score != 1 {
		t.Errorf("expected score 1, got %d", score)
	}
}

func TestPathMatchScore_greedyWithPrefix(t *testing.T) {
	// Given a template with exact prefix + greedy
	score := pathMatchScore("/api/{proxy+}", "/api/foo/bar")
	// Then score = exact(2) + greedy(1) = 3
	if score != 3 {
		t.Errorf("expected score 3, got %d", score)
	}
}

func TestPathMatchScore_greedyRequiresSegment(t *testing.T) {
	// Given a greedy template with a prefix
	// When the request path has no segment after the prefix
	score := pathMatchScore("/api/{proxy+}", "/api")
	// Then it should NOT match (-1)
	if score != -1 {
		t.Errorf("expected score -1 for greedy with no remaining segments, got %d", score)
	}
}

func TestPathMatchScore_greedyRootNoSegments(t *testing.T) {
	// Given a root-level greedy template
	// When the request path is just "/"
	score := pathMatchScore("/{proxy+}", "/")
	// Then it should NOT match (-1)
	if score != -1 {
		t.Errorf("expected score -1 for /{proxy+} against /, got %d", score)
	}
}

func TestPathMatchScore_mismatch(t *testing.T) {
	// Given a template that doesn't match
	score := pathMatchScore("/users/list", "/orders/list")
	// Then score should be -1
	if score != -1 {
		t.Errorf("expected score -1, got %d", score)
	}
}

func TestPathMatchScore_tooManySegments(t *testing.T) {
	// Given a request path with more segments than template (no greedy)
	score := pathMatchScore("/users/{id}", "/users/42/orders")
	// Then score should be -1
	if score != -1 {
		t.Errorf("expected score -1, got %d", score)
	}
}

func TestPathMatchScore_rootTemplate(t *testing.T) {
	// Given a "/" template
	score := pathMatchScore("/", "/anything")
	// Then score should be 0 (handled by exact match in matchResource)
	if score != 0 {
		t.Errorf("expected score 0 for root template, got %d", score)
	}
}

// ---- matchResource ---------------------------------------------------------

func TestMatchResource_exactMatch(t *testing.T) {
	resources := []*Resource{
		{ID: "root", Path: "/"},
		{ID: "users", Path: "/users"},
		{ID: "proxy", Path: "/{proxy+}"},
	}
	res := matchResource(resources, "/users")
	if res == nil || res.ID != "users" {
		t.Errorf("expected exact match 'users', got %v", res)
	}
}

func TestMatchResource_paramMatchOverGreedy(t *testing.T) {
	// Given both a specific param route and a greedy route
	resources := []*Resource{
		{ID: "proxy", Path: "/{proxy+}"},
		{ID: "userID", Path: "/users/{id}"},
	}
	// When matching /users/42
	res := matchResource(resources, "/users/42")
	// Then the more specific route wins (score 3 vs score 1)
	if res == nil || res.ID != "userID" {
		t.Errorf("expected 'userID', got %v", res)
	}
}

func TestMatchResource_greedyMatchesMultiSegment(t *testing.T) {
	resources := []*Resource{
		{ID: "root", Path: "/"},
		{ID: "proxy", Path: "/{proxy+}"},
	}
	res := matchResource(resources, "/a/b/c")
	if res == nil || res.ID != "proxy" {
		t.Errorf("expected 'proxy', got %v", res)
	}
}

func TestMatchResource_greedyDoesNotMatchRoot(t *testing.T) {
	resources := []*Resource{
		{ID: "root", Path: "/"},
		{ID: "proxy", Path: "/{proxy+}"},
	}
	// "/" should be an exact match to root, not the greedy param
	res := matchResource(resources, "/")
	if res == nil || res.ID != "root" {
		t.Errorf("expected exact match 'root' for /, got %v", res)
	}
}

// ---- extractPathParams -----------------------------------------------------

func TestExtractPathParams_singleParam(t *testing.T) {
	params := extractPathParams("/users/{id}", "/users/42")
	if params["id"] != "42" {
		t.Errorf("expected id=42, got %q", params["id"])
	}
}

func TestExtractPathParams_multipleParams(t *testing.T) {
	params := extractPathParams("/users/{userId}/orders/{orderId}", "/users/abc/orders/123")
	if params["userId"] != "abc" {
		t.Errorf("expected userId=abc, got %q", params["userId"])
	}
	if params["orderId"] != "123" {
		t.Errorf("expected orderId=123, got %q", params["orderId"])
	}
}

func TestExtractPathParams_greedyParam(t *testing.T) {
	params := extractPathParams("/{proxy+}", "/a/b/c")
	if params["proxy"] != "a/b/c" {
		t.Errorf("expected proxy=a/b/c, got %q", params["proxy"])
	}
}

func TestExtractPathParams_greedyWithPrefix(t *testing.T) {
	params := extractPathParams("/api/{path+}", "/api/v1/users/list")
	if params["path"] != "v1/users/list" {
		t.Errorf("expected path=v1/users/list, got %q", params["path"])
	}
}

// ---- routeV2MatchScore -----------------------------------------------------

func TestRouteV2MatchScore_exactMethod(t *testing.T) {
	score := routeV2MatchScore("GET /users", "GET", "/users")
	// exact segment: 2
	if score != 2 {
		t.Errorf("expected score 2, got %d", score)
	}
}

func TestRouteV2MatchScore_anyMethod(t *testing.T) {
	score := routeV2MatchScore("ANY /users", "DELETE", "/users")
	if score != 2 {
		t.Errorf("expected score 2, got %d", score)
	}
}

func TestRouteV2MatchScore_wrongMethod(t *testing.T) {
	score := routeV2MatchScore("POST /users", "GET", "/users")
	if score != -1 {
		t.Errorf("expected -1 for wrong method, got %d", score)
	}
}

func TestRouteV2MatchScore_paramRoute(t *testing.T) {
	score := routeV2MatchScore("GET /users/{id}", "GET", "/users/42")
	// exact(2) + param(1) = 3
	if score != 3 {
		t.Errorf("expected score 3, got %d", score)
	}
}

func TestRouteV2MatchScore_greedyRoute(t *testing.T) {
	score := routeV2MatchScore("GET /{proxy+}", "GET", "/a/b/c")
	if score != 1 {
		t.Errorf("expected score 1, got %d", score)
	}
}

func TestRouteV2MatchScore_greedyRequiresSegment(t *testing.T) {
	// Greedy route should NOT match when there are no remaining segments
	score := routeV2MatchScore("GET /api/{proxy+}", "GET", "/api")
	if score != -1 {
		t.Errorf("expected -1 for greedy with no remaining segments, got %d", score)
	}
}

func TestRouteV2MatchScore_greedyRootNoMatch(t *testing.T) {
	score := routeV2MatchScore("GET /{proxy+}", "GET", "/")
	if score != -1 {
		t.Errorf("expected -1 for /{proxy+} against /, got %d", score)
	}
}

// ---- matchV2Route (best-match selection) -----------------------------------

func TestMatchV2Route_prefersSpecificOverGreedy(t *testing.T) {
	routes := []*RouteV2{
		{RouteKey: "GET /{proxy+}", RouteID: "greedy"},
		{RouteKey: "GET /users/{id}", RouteID: "specific"},
	}
	route := matchV2Route(routes, "GET", "/users/42")
	if route == nil || route.RouteID != "specific" {
		t.Errorf("expected 'specific' route, got %v", route)
	}
}

func TestMatchV2Route_prefersSpecificOverGreedy_reverseOrder(t *testing.T) {
	// Same test but routes stored in reverse order — should still pick specific
	routes := []*RouteV2{
		{RouteKey: "GET /users/{id}", RouteID: "specific"},
		{RouteKey: "GET /{proxy+}", RouteID: "greedy"},
	}
	route := matchV2Route(routes, "GET", "/users/42")
	if route == nil || route.RouteID != "specific" {
		t.Errorf("expected 'specific' route, got %v", route)
	}
}

func TestMatchV2Route_fallsBackToDefault(t *testing.T) {
	routes := []*RouteV2{
		{RouteKey: "GET /specific", RouteID: "specific"},
		{RouteKey: "$default", RouteID: "default"},
	}
	route := matchV2Route(routes, "GET", "/unmatched")
	if route == nil || route.RouteID != "default" {
		t.Errorf("expected '$default' route, got %v", route)
	}
}

func TestMatchV2Route_exactMatchWins(t *testing.T) {
	routes := []*RouteV2{
		{RouteKey: "GET /{proxy+}", RouteID: "greedy"},
		{RouteKey: "GET /users/list", RouteID: "exact"},
		{RouteKey: "GET /users/{id}", RouteID: "param"},
	}
	route := matchV2Route(routes, "GET", "/users/list")
	if route == nil || route.RouteID != "exact" {
		t.Errorf("expected 'exact' route, got %v", route)
	}
}

// ---- extractV2PathParams ---------------------------------------------------

func TestExtractV2PathParams_singleParam(t *testing.T) {
	params := extractV2PathParams("GET /users/{id}", "/users/42")
	if params["id"] != "42" {
		t.Errorf("expected id=42, got %q", params["id"])
	}
}

func TestExtractV2PathParams_greedyParam(t *testing.T) {
	params := extractV2PathParams("ANY /{proxy+}", "/a/b/c")
	if params["proxy"] != "a/b/c" {
		t.Errorf("expected proxy=a/b/c, got %q", params["proxy"])
	}
}

func TestExtractV2PathParams_greedyWithPrefix(t *testing.T) {
	params := extractV2PathParams("POST /api/{path+}", "/api/v1/docs/readme")
	if params["path"] != "v1/docs/readme" {
		t.Errorf("expected path=v1/docs/readme, got %q", params["path"])
	}
}
