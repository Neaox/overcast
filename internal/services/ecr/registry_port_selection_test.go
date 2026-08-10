package ecr

// registry_port_selection_test.go — the port repositoryUri advertises is the
// one the daemon proves it can dial, not the first one the inspect happens to
// list.
//
// A dual-stack ephemeral publish can give the IPv4 and IPv6 bindings different
// host ports, and which family "localhost" resolves to is the daemon's
// business. Advertising bindings[0] blind produced, on CI:
//
//	docker login http://localhost:32768: Error response from daemon:
//	Get "http://localhost:32768/v2/": dial tcp [::1]:32768: connection refused
//
// — a port verified from Overcast's own (IPv4) vantage that the daemon's
// localhost could not reach. The daemon performs every push, pull and login,
// so the daemon selects the port, through the same distribution-inspect dial
// login uses.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// selectionDaemon fakes the daemon's /distribution endpoint: candidates in
// dead refuse with a dial error, everything else answers with an
// authorization refusal — an answer.
func selectionDaemon(t *testing.T, dead map[int]bool) (*Service, *atomic.Int32) {
	t.Helper()
	var probes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/distribution/") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			return
		}
		probes.Add(1)
		ref, _ := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.45/distribution/"), "/json"))
		var port int
		if _, err := fmt.Sscanf(ref, "localhost:%d/", &port); err != nil {
			t.Errorf("unparseable probe ref %q", ref)
		}
		w.WriteHeader(http.StatusInternalServerError)
		if dead[port] {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"message": fmt.Sprintf("Get \"http://localhost:%d/v2/\": dial tcp [::1]:%d: connect: connection refused", port, port),
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "unauthorized: authentication required"})
	}))
	t.Cleanup(srv.Close)

	return &Service{
		cfg:    &config.Config{},
		log:    serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		docker: docker.NewClient(strings.Replace(srv.URL, "http://", "tcp://", 1), zap.NewNop()),
	}, &probes
}

func TestSelectDaemonReachablePort_picksThePortTheDaemonAnswersOn(t *testing.T) {
	// Given: split bindings where the first-listed port is unreachable from
	// the daemon's localhost and the second answers.
	s, _ := selectionDaemon(t, map[int]bool{32768: true})

	// When: the port is selected.
	port, reachable := s.selectDaemonReachablePort([]int{32768, 32769})

	// Then: the daemon's answer decides, not list order.
	if !reachable {
		t.Fatal("expected a reachable verdict")
	}
	if port != 32769 {
		t.Errorf("port = %d, want 32769 — the one the daemon reached", port)
	}
}

func TestPublishedHostPorts_readsEveryDistinctBinding(t *testing.T) {
	// Given: the inspect shape of a split dual-stack ephemeral publish.
	var inspect docker.ContainerInspect
	if err := json.Unmarshal([]byte(`{"NetworkSettings":{"Ports":{"5000/tcp":[
		{"HostIp":"0.0.0.0","HostPort":"32768"},
		{"HostIp":"::","HostPort":"32769"},
		{"HostIp":"::","HostPort":"32768"}
	]}}}`), &inspect); err != nil {
		t.Fatalf("unmarshal inspect: %v", err)
	}

	// When: the published ports are read.
	got := publishedHostPorts(&inspect)

	// Then: both ports surface, deduplicated, in listed order — each is a
	// candidate the daemon may be able to reach when the other is not.
	if len(got) != 2 || got[0] != 32768 || got[1] != 32769 {
		t.Errorf("ports = %v, want [32768 32769]", got)
	}
}

func TestSelectDaemonReachablePort_authorizationRefusalIsAnAnswer(t *testing.T) {
	// Given: a registry that refuses the anonymous probe, as ours always does.
	s, probes := selectionDaemon(t, nil)

	// When: the port is selected.
	port, reachable := s.selectDaemonReachablePort([]int{4510})

	// Then: 401 proves the path on the first probe; no retry loop.
	if !reachable || port != 4510 {
		t.Fatalf("selection = (%d, %v), want (4510, true)", port, reachable)
	}
	if got := probes.Load(); got != 1 {
		t.Errorf("probes = %d, want 1", got)
	}
}
