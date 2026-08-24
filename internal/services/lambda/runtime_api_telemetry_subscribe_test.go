package lambda

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// runtime_api_telemetry_subscribe_test.go — the Telemetry API subscription
// surface (PUT /2022-07-01/telemetry), which superseded the Logs API and is
// the endpoint current observability extensions actually call
// (https://docs.aws.amazon.com/lambda/latest/dg/telemetry-api.html). Before it
// existed here, such an extension got a 404 and silently received nothing.

// subscribeVia sends one subscription request to path and returns the
// response status and body.
func subscribeVia(t *testing.T, addr, path, extID string, body map[string]any) (int, string) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, "http://"+addr+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Identifier", extID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// telemetryDestination is an HTTP destination that records every body POSTed
// to it.
type telemetryDestination struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newTelemetryDestination(t *testing.T) *telemetryDestination {
	t.Helper()
	d := &telemetryDestination{}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.bodies = append(d.bodies, string(body))
		d.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(d.Close)
	return d
}

// received waits for a body satisfying match, returning it, or fails the test.
func (d *telemetryDestination) received(t *testing.T, match func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		d.mu.Lock()
		for _, body := range d.bodies {
			if match(body) {
				d.mu.Unlock()
				return body
			}
		}
		all := strings.Join(d.bodies, "\n")
		d.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("the destination never received a matching body; it saw:\n%s", all)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func subscribeBody(destURI, schemaVersion string, types ...string) map[string]any {
	body := map[string]any{
		"types":       types,
		"destination": map[string]string{"protocol": "HTTP", "URI": destURI},
	}
	if schemaVersion != "" {
		body["schemaVersion"] = schemaVersion
	}
	return body
}

func newTelemetryTestServer(t *testing.T, logFormat string) (*RuntimeAPIServer, string) {
	t.Helper()
	srv, addr := newRuntimeAPITestServer(t)
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:demo",
		FunctionName: "demo",
		Handler:      "index.handler",
		LogFormat:    logFormat,
	})
	return srv, addr
}

func TestTelemetryAPI_subscribeDeliversPlatformRecords(t *testing.T) {
	srv, addr := newTelemetryTestServer(t, logFormatText)
	dest := newTelemetryDestination(t)
	extID := registerExtension(t, http.DefaultClient, addr, "collector")

	// A record published before the subscription exists — the retained
	// replay must reach a Telemetry API subscriber exactly as it does a
	// Logs API one.
	srv.PublishInitPhaseRecord("127.0.0.1", `{"time":"2026-08-24T00:00:00.000Z","type":"platform.initStart","record":{"initializationType":"on-demand","phase":"init"}}`)

	status, body := subscribeVia(t, addr, "/2022-07-01/telemetry", extID,
		subscribeBody(dest.URL, "2022-12-13", "platform"))
	if status != http.StatusOK {
		t.Fatalf("subscribe status = %d, want 200 (body %q)", status, body)
	}
	if body != `"OK"` {
		// The documented success body is the JSON string "OK".
		t.Fatalf("subscribe body = %q, want %q", body, `"OK"`)
	}

	dest.received(t, func(b string) bool { return strings.Contains(b, "platform.initStart") })
}

func TestTelemetryAPI_trailingSlashSpellingWorks(t *testing.T) {
	// AWS's own reference constructs the URL by appending "telemetry/", so
	// the trailing slash is a request real extensions make.
	_, addr := newTelemetryTestServer(t, logFormatText)
	dest := newTelemetryDestination(t)
	extID := registerExtension(t, http.DefaultClient, addr, "collector")

	status, _ := subscribeVia(t, addr, "/2022-07-01/telemetry/", extID,
		subscribeBody(dest.URL, "2025-01-29", "platform"))
	if status != http.StatusOK {
		t.Fatalf("subscribe via trailing-slash path = %d, want 200", status)
	}
}

func TestTelemetryAPI_rejectsAnUnknownSchemaVersion(t *testing.T) {
	_, addr := newTelemetryTestServer(t, logFormatText)
	dest := newTelemetryDestination(t)
	extID := registerExtension(t, http.DefaultClient, addr, "collector")

	// A version this build does not know would be half-honoured at best;
	// refusing it loudly and locally is the preferred failure direction.
	status, _ := subscribeVia(t, addr, "/2022-07-01/telemetry", extID,
		subscribeBody(dest.URL, "2031-01-01", "platform"))
	if status != http.StatusBadRequest {
		t.Fatalf("unknown schemaVersion = %d, want 400", status)
	}
}

func TestTelemetryAPI_crossAPISubscriptionIsRefused(t *testing.T) {
	// "After subscribing using one of these APIs, any attempt to subscribe
	// using the other API returns an error."
	// (https://docs.aws.amazon.com/lambda/latest/dg/telemetry-api.html)
	for _, tc := range []struct{ name, first, second string }{
		{name: "logs then telemetry", first: "/2020-08-15/logs", second: "/2022-07-01/telemetry"},
		{name: "telemetry then logs", first: "/2022-07-01/telemetry", second: "/2020-08-15/logs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, addr := newTelemetryTestServer(t, logFormatText)
			dest := newTelemetryDestination(t)
			extID := registerExtension(t, http.DefaultClient, addr, "collector")

			if status, _ := subscribeVia(t, addr, tc.first, extID, subscribeBody(dest.URL, "", "platform")); status != http.StatusOK {
				t.Fatalf("first subscribe = %d, want 200", status)
			}
			status, body := subscribeVia(t, addr, tc.second, extID, subscribeBody(dest.URL, "", "platform"))
			if status == http.StatusOK {
				t.Fatalf("second subscribe via the other API succeeded, want an error (body %q)", body)
			}
		})
	}
}

// TestTelemetryAPI_recordShapeFollowsSchemaAndLogFormat pins the rule from the
// schema reference: from schemaVersion 2022-12-13, a JSON-format function's
// log line is embedded as the object it already is; every other combination —
// an older schema, a Text-format function, a line that is not a JSON object —
// stays a string.
func TestTelemetryAPI_recordShapeFollowsSchemaAndLogFormat(t *testing.T) {
	jsonLine := `{"timestamp":"2026-08-24T00:00:00.000Z","level":"INFO","message":"hello"}`
	for _, tc := range []struct {
		name       string
		logFormat  string
		schema     string
		line       string
		wantObject bool
	}{
		{name: "modern schema, JSON format, JSON line", logFormat: logFormatJSON, schema: "2022-12-13", line: jsonLine, wantObject: true},
		{name: "latest schema behaves the same", logFormat: logFormatJSON, schema: "2025-01-29", line: jsonLine, wantObject: true},
		{name: "oldest schema keeps the string", logFormat: logFormatJSON, schema: "2022-07-01", line: jsonLine, wantObject: false},
		{name: "text format keeps the string", logFormat: logFormatText, schema: "2022-12-13", line: jsonLine, wantObject: false},
		{name: "a plain line stays a string", logFormat: logFormatJSON, schema: "2022-12-13", line: "plain text", wantObject: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, addr := newTelemetryTestServer(t, tc.logFormat)
			dest := newTelemetryDestination(t)
			extID := registerExtension(t, http.DefaultClient, addr, "collector")
			if status, _ := subscribeVia(t, addr, "/2022-07-01/telemetry", extID,
				subscribeBody(dest.URL, tc.schema, "function")); status != http.StatusOK {
				t.Fatal("subscribe failed")
			}

			srv.PublishExtensionLog("127.0.0.1", "function", tc.line)

			body := dest.received(t, func(b string) bool { return strings.Contains(b, `"function"`) })
			var events []struct {
				Record json.RawMessage `json:"record"`
			}
			if err := json.Unmarshal([]byte(body), &events); err != nil || len(events) != 1 {
				t.Fatalf("body %q did not decode to one event: %v", body, err)
			}
			isObject := strings.HasPrefix(strings.TrimSpace(string(events[0].Record)), "{")
			if isObject != tc.wantObject {
				t.Fatalf("record = %s, wantObject = %v", events[0].Record, tc.wantObject)
			}
		})
	}
}

// TestTelemetryAPI_lifecycleRecords pins the two records that tell the
// environment's own extension story: platform.extension at registration
// ({events, name, state}) and the subscription event at subscribe — named
// platform.telemetrySubscription or platform.logsSubscription for the surface
// it came through, per each API's documented example. The registration record
// is retained with the init phase, so the subscriber that registered before
// anything could listen still receives it.
func TestTelemetryAPI_lifecycleRecords(t *testing.T) {
	for _, tc := range []struct{ name, path, eventType string }{
		{name: "telemetry api", path: "/2022-07-01/telemetry", eventType: "platform.telemetrySubscription"},
		{name: "logs api", path: "/2020-08-15/logs", eventType: "platform.logsSubscription"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, addr := newTelemetryTestServer(t, logFormatText)
			dest := newTelemetryDestination(t)
			extID := registerExtension(t, http.DefaultClient, addr, "collector")

			status, _ := subscribeVia(t, addr, tc.path, extID, map[string]any{
				"types":       []string{"platform"},
				"buffering":   map[string]any{"timeoutMs": 25},
				"destination": map[string]string{"protocol": "HTTP", "URI": dest.URL},
			})
			if status != http.StatusOK {
				t.Fatalf("subscribe = %d", status)
			}

			// Its own registration, replayed from the init-phase retention.
			body := dest.received(t, func(b string) bool { return strings.Contains(b, "platform.extension") })
			if !strings.Contains(body, `"name":"collector"`) || !strings.Contains(body, `"state":"Ready"`) {
				t.Errorf("platform.extension = %s", body)
			}
			// Its own subscription, under the surface's documented name.
			body = dest.received(t, func(b string) bool { return strings.Contains(b, tc.eventType) })
			if !strings.Contains(body, `"state":"Subscribed"`) || !strings.Contains(body, `"platform"`) {
				t.Errorf("%s = %s", tc.eventType, body)
			}
		})
	}
}
