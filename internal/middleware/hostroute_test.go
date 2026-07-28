package middleware

import (
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

func TestHostRouteServiceFor(t *testing.T) {
	// Given: hosts that do and don't match the host-route grammar
	cases := []struct {
		host    string
		wantSvc string
		wantOK  bool
	}{
		{host: "abc123.execute-api.us-east-1.amazonaws.com", wantSvc: "apigateway", wantOK: true},
		{host: "xyz.lambda-url.us-east-1.on.aws", wantSvc: "lambda", wantOK: true},
		{host: "api1.appsync-api.us-east-1.amazonaws.com", wantSvc: "appsync", wantOK: true},
		{host: "mybucket.s3.us-east-1.amazonaws.com", wantOK: false}, // S3 is not a host-route label
		{host: "localhost:4566", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			// When: the grammar is parsed and its owner looked up
			m, matched := ParseHostRoute(tc.host)
			svc, ok := "", false
			if matched {
				svc, ok = HostRouteServiceFor(m)
			}

			// Then: it matches ParseHostRoute's positive/negative verdict
			if ok != tc.wantOK {
				t.Fatalf("HostRouteServiceFor(%q) ok = %v, want %v", tc.host, ok, tc.wantOK)
			}
			if ok && svc != tc.wantSvc {
				t.Errorf("HostRouteServiceFor(%q) = %q, want %q", tc.host, svc, tc.wantSvc)
			}
		})
	}
}

// The middleware that applies HostRouteRows is HostAddressing; its dispatch
// and pass-through behaviour is specified in hostaddressing_test.go, where it
// is tested together with S3 virtual-hosted addressing because the two share
// one precedence decision.
