package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseHostRoute(t *testing.T) {
	// Given: a table of Host headers covering the grammar's edge cases
	tests := []struct {
		name       string
		host       string
		wantOK     bool
		wantLabel  string
		wantID     string
		wantRegion string
	}{
		{name: "execute-api with region", host: "abc123.execute-api.us-east-1.amazonaws.com", wantOK: true, wantLabel: "execute-api", wantID: "abc123", wantRegion: "us-east-1"},
		{name: "execute-api with port", host: "abc123.execute-api.us-east-1.amazonaws.com:4566", wantOK: true, wantLabel: "execute-api", wantID: "abc123", wantRegion: "us-east-1"},
		{name: "execute-api no region (bare localhost base)", host: "abc123.execute-api.localhost", wantOK: true, wantLabel: "execute-api", wantID: "abc123", wantRegion: ""},
		{name: "execute-api gov region", host: "abc123.execute-api.us-gov-west-1.amazonaws.com", wantOK: true, wantLabel: "execute-api", wantID: "abc123", wantRegion: "us-gov-west-1"},
		{name: "lambda-url", host: "deadbeefdeadbeefdeadbeefdeadbeef.lambda-url.us-east-1.on.aws", wantOK: true, wantLabel: "lambda-url", wantID: "deadbeefdeadbeefdeadbeefdeadbeef", wantRegion: "us-east-1"},
		{name: "appsync-api", host: "myapi456.appsync-api.ap-southeast-2.amazonaws.com", wantOK: true, wantLabel: "appsync-api", wantID: "myapi456", wantRegion: "ap-southeast-2"},
		{name: "dotted id before label", host: "foo.bar.execute-api.us-east-1.amazonaws.com", wantOK: true, wantLabel: "execute-api", wantID: "foo.bar", wantRegion: "us-east-1"},
		{name: "label with no id segment does not match", host: "execute-api.us-east-1.amazonaws.com", wantOK: false},
		{name: "unrecognised label", host: "abc123.made-up-service.us-east-1.amazonaws.com", wantOK: false},
		{name: "plain path-style host", host: "localhost:4566", wantOK: false},
		{name: "IP literal bypasses grammar", host: "127.0.0.1:4566", wantOK: false},
		{name: "IPv6 literal bypasses grammar", host: "[::1]:4566", wantOK: false},
		{name: "empty host", host: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: the host is parsed
			m, ok := ParseHostRoute(tt.host)

			// Then: match result and fields are as expected
			if ok != tt.wantOK {
				t.Fatalf("ParseHostRoute(%q) ok = %v, want %v", tt.host, ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if m.Label != tt.wantLabel {
				t.Errorf("Label = %q, want %q", m.Label, tt.wantLabel)
			}
			if m.ID != tt.wantID {
				t.Errorf("ID = %q, want %q", m.ID, tt.wantID)
			}
			if m.Region != tt.wantRegion {
				t.Errorf("Region = %q, want %q", m.Region, tt.wantRegion)
			}
		})
	}
}

func TestHostRouteService(t *testing.T) {
	// Given: hosts that do and don't match the host-route grammar
	cases := []struct {
		host    string
		wantSvc string
		wantOK  bool
	}{
		{host: "abc123.execute-api.us-east-1.amazonaws.com", wantSvc: "apigateway", wantOK: true},
		{host: "xyz.lambda-url.us-east-1.on.aws", wantSvc: "lambda", wantOK: true},
		{host: "api1.appsync-api.us-east-1.amazonaws.com", wantSvc: "appsync", wantOK: true},
		{host: "mybucket.s3.us-east-1.amazonaws.com", wantOK: false}, // S3 is not in this table (parallel branch)
		{host: "localhost:4566", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			// When: the label->service lookup runs
			svc, ok := HostRouteService(tc.host)

			// Then: it matches ParseHostRoute's positive/negative verdict
			if ok != tc.wantOK {
				t.Fatalf("HostRouteService(%q) ok = %v, want %v", tc.host, ok, tc.wantOK)
			}
			if ok && svc != tc.wantSvc {
				t.Errorf("HostRouteService(%q) = %q, want %q", tc.host, svc, tc.wantSvc)
			}
		})
	}
}

func TestHostDispatch_rewritesMatchingRow(t *testing.T) {
	// Given: a single registered row for "execute-api" that records the
	// match it received and stamps a marker path
	var gotMatch HostRouteMatch
	rows := []HostRouteRow{
		{Label: "execute-api", Rewrite: func(r *http.Request, m HostRouteMatch) {
			gotMatch = m
			r.URL.Path = "/_rewritten/" + m.ID
		}},
	}
	var finalPath string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finalPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	mw := HostDispatch(&rows)(next)

	// When: a request with a matching Host reaches the middleware
	r := httptest.NewRequest(http.MethodGet, "/pets/1", nil)
	r.Host = "abc123.execute-api.us-east-1.amazonaws.com"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	// Then: the row's Rewrite ran with the parsed match, and the downstream
	// handler saw the rewritten path
	if gotMatch.ID != "abc123" || gotMatch.Region != "us-east-1" {
		t.Errorf("Rewrite received match %+v, want ID=abc123 Region=us-east-1", gotMatch)
	}
	if finalPath != "/_rewritten/abc123" {
		t.Errorf("downstream path = %q, want /_rewritten/abc123", finalPath)
	}
}

func TestHostDispatch_passesThroughUnmatchedHost(t *testing.T) {
	// Given: a row for "execute-api" only
	rows := []HostRouteRow{
		{Label: "execute-api", Rewrite: func(r *http.Request, m HostRouteMatch) {
			t.Fatalf("Rewrite should not run for a non-matching host")
		}},
	}
	var finalPath string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finalPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	mw := HostDispatch(&rows)(next)

	// When: a request with an unrelated Host (e.g. a plain S3-style request)
	// reaches the middleware
	r := httptest.NewRequest(http.MethodGet, "/my-bucket/key", nil)
	r.Host = "localhost:4566"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	// Then: the path is untouched — the request falls through unchanged
	if finalPath != "/my-bucket/key" {
		t.Errorf("path = %q, want unchanged /my-bucket/key", finalPath)
	}
}
