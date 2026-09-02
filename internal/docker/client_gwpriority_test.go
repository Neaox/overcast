package docker

// client_gwpriority_test.go — a network connect that ranks the network as
// the container's default-route source (EndpointSettings.GwPriority) goes out
// under the first API version that carries the field, and only to a daemon
// that speaks it (#1571).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.uber.org/zap"
)

// gwPriorityDaemon answers GET /version with a configurable API version and
// records every network connect it receives: the path (which carries the API
// version the client chose) and the body.
type gwPriorityDaemon struct {
	apiVersion string
	versionErr bool

	mu       sync.Mutex
	versions int // how many times /version was asked
	connects []struct {
		path string
		body map[string]any
	}
}

func (d *gwPriorityDaemon) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case r.URL.Path == "/version":
		d.versions++
		if d.versionErr {
			http.Error(w, `{"message":"no"}`, http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ApiVersion": d.apiVersion, "MinAPIVersion": "1.24"})
	case r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		d.connects = append(d.connects, struct {
			path string
			body map[string]any
		}{r.URL.Path, body})
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

func newGwPriorityClient(t *testing.T, d *gwPriorityDaemon) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(server.Close)
	return &Client{httpClient: server.Client(), host: server.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)}
}

func endpointOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	ep, _ := body["EndpointConfig"].(map[string]any)
	if ep == nil {
		t.Fatalf("connect body carries no EndpointConfig: %v", body)
	}
	return ep
}

func TestConnectNetworkWithConfig_gwPriorityGoesOutUnderTheVersionThatCarriesIt(t *testing.T) {
	// Given: a daemon that speaks API 1.48 (Docker 28.0), the first version
	// whose network connect accepts GwPriority.
	d := &gwPriorityDaemon{apiVersion: "1.55"}
	dc := newGwPriorityClient(t, d)

	// When: a container joins a network ranked as its default-route source.
	err := dc.ConnectNetworkWithConfig(context.Background(), "egress", "ctr", &EndpointSettings{GwPriority: 10})

	// Then: the connect went out under 1.48 with the ranking in the body —
	// under the pinned 1.45 the daemon would silently drop it.
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if len(d.connects) != 1 {
		t.Fatalf("connects = %d, want 1", len(d.connects))
	}
	if got := d.connects[0].path; got != "/v"+apiVersionGatewayPriority+"/networks/egress/connect" {
		t.Errorf("connect path = %q, want it under API %s", got, apiVersionGatewayPriority)
	}
	if got := endpointOf(t, d.connects[0].body)["GwPriority"]; got != float64(10) {
		t.Errorf("GwPriority in body = %v, want 10", got)
	}
}

func TestConnectNetworkWithConfig_gwPriorityIsDroppedForAnOlderDaemon(t *testing.T) {
	// Given: a daemon that predates gateway priorities. Asking it under 1.48
	// would be refused outright ("client version 1.48 is too new").
	d := &gwPriorityDaemon{apiVersion: "1.45"}
	dc := newGwPriorityClient(t, d)

	// When: the same ranked connect.
	err := dc.ConnectNetworkWithConfig(context.Background(), "egress", "ctr", &EndpointSettings{GwPriority: 10, Aliases: []string{"a"}})

	// Then: it went out under the pinned version without the ranking, and
	// with everything else intact — a connect the daemon can honour beats
	// one it refuses, and Docker's name-order tie-break decides the route.
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if got := d.connects[0].path; got != "/v"+apiVersionPinned+"/networks/egress/connect" {
		t.Errorf("connect path = %q, want it under the pinned API %s", got, apiVersionPinned)
	}
	ep := endpointOf(t, d.connects[0].body)
	if _, ok := ep["GwPriority"]; ok {
		t.Errorf("GwPriority was sent to a daemon that does not understand it: %v", ep)
	}
	if aliases, _ := ep["Aliases"].([]any); len(aliases) != 1 {
		t.Errorf("aliases were lost with the ranking: %v", ep)
	}
}

func TestConnectNetworkWithConfig_unrankedConnectsNeverAskTheVersion(t *testing.T) {
	// Given/When: the ordinary connect every service makes.
	d := &gwPriorityDaemon{apiVersion: "1.55"}
	dc := newGwPriorityClient(t, d)
	if err := dc.ConnectNetworkWithConfig(context.Background(), "plane", "ctr", &EndpointSettings{Aliases: []string{"db"}}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Then: it goes out under the pinned version and costs no version
	// round-trip — the negotiation is for the one field that needs it.
	if d.versions != 0 {
		t.Errorf("/version was asked %d times for a connect that carries nothing version-dependent", d.versions)
	}
	if got := d.connects[0].path; got != "/v"+apiVersionPinned+"/networks/plane/connect" {
		t.Errorf("connect path = %q", got)
	}
}

func TestAPIVersionAtLeast_readsTheDaemonOnceAndAssumesTheFloorWhenItCannot(t *testing.T) {
	// Given: a daemon at 1.48.
	d := &gwPriorityDaemon{apiVersion: "1.48"}
	dc := newGwPriorityClient(t, d)
	ctx := context.Background()

	// Then: the comparison is against what it said, and it is asked once.
	if !dc.APIVersionAtLeast(ctx, "1.48") || !dc.APIVersionAtLeast(ctx, "1.45") || dc.APIVersionAtLeast(ctx, "1.49") {
		t.Errorf("version comparisons against 1.48 are wrong")
	}
	if d.versions != 1 {
		t.Errorf("/version was asked %d times, want once — the answer does not change for the life of the client", d.versions)
	}

	// And a daemon that cannot answer is taken to speak nothing beyond the
	// floor, without caching the failure.
	failing := &gwPriorityDaemon{versionErr: true}
	fc := newGwPriorityClient(t, failing)
	if fc.APIVersionAtLeast(ctx, "1.46") {
		t.Errorf("an unreadable version was assumed newer than the pinned floor")
	}
	if !fc.APIVersionAtLeast(ctx, apiVersionPinned) {
		t.Errorf("the pinned floor itself must always be assumed supported")
	}
	if failing.versions != 2 {
		t.Errorf("/version was asked %d times after a failure, want 2 — a failure is not cached", failing.versions)
	}
}

func TestCompareAPIVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.48", "1.48", 0},
		{"1.45", "1.48", -1},
		{"1.55", "1.48", 1},
		{"2.0", "1.99", 1},
		{"v1.48", "1.48", 0},
		{"", "1.48", -1},
		{"garbage", "1.48", -1},
	} {
		if got := compareAPIVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareAPIVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
