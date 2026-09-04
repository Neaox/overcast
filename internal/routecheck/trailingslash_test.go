//go:build dev

package routecheck

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/awsapi"
)

// A trailing slash is part of a path, not decoration — chi matches "/rrset"
// and "/rrset/" with two separate patterns — so a sweep that normalises it
// away certifies a binding as reachable using a request no client sends. That
// is the failure mode this file exists to pin: the sweep passing while
// proving nothing, which a green test looks exactly like.
//
// #1413 is the worked example. 114 manifest bindings end in a slash; before
// the fix every one of them was probed without it.

func TestConcretePath_keepsAModeledTrailingSlash(t *testing.T) {
	cases := []struct {
		name, uri, wantPath, wantQuery string
	}{
		{"trailing slash kept", "/backup/plans/", "/backup/plans/", ""},
		{"after a label", "/agents/{agentId}/", "/agents/probevalue/", ""},
		{"no slash unchanged", "/backup/plans", "/backup/plans", ""},
		{"root unchanged", "/", "/", ""},
		{"slash before the query", "/2015-01-01/tags/?arn=x", "/2015-01-01/tags/", "arn=x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the modeled template is turned into a routable path
			path, query := ConcretePath(tc.uri)

			// Then: it is addressed the way the model spells it
			if path != tc.wantPath || query != tc.wantQuery {
				t.Errorf("ConcretePath(%q) = %q, %q; want %q, %q", tc.uri, path, query, tc.wantPath, tc.wantQuery)
			}
		})
	}
}

func TestBuildRequest_addressesAModeledTrailingSlash(t *testing.T) {
	// Given: a REST binding AWS models with a trailing slash
	op := awsapi.Operation{
		Service: "backup", Name: "ListBackupPlans", Protocol: awsapi.ProtocolRESTJSON,
		HTTPMethod: "GET", URI: "/backup/plans/",
	}

	// When: the probe request is built
	req, binding, err := BuildRequest(context.Background(), "http://127.0.0.1:1", awsapi.NewRegistry(), op)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	// Then: both the request and the binding it is reported under carry the slash
	if got := req.URL.Path; got != "/backup/plans/" {
		t.Errorf("probe path = %q, want %q — the sweep is probing a path no SDK sends", got, "/backup/plans/")
	}
	if binding != "GET /backup/plans/" {
		t.Errorf("binding = %q, want %q", binding, "GET /backup/plans/")
	}
}
