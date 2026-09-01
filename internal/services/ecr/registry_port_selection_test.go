package ecr

// registry_port_selection_test.go — the port repositoryUri advertises is the
// one the daemon proves it can dial *and* proves is ours.
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
//
// Reachability alone is not enough, though. Two Overcast instances on one
// daemon can interleave their ephemeral publishes so that one instance's
// localhost:<port> reaches the *other's* registry over the other address
// family. Anonymously the two are indistinguishable — every authenticated
// registry refuses an anonymous probe alike — so the probe carries this
// instance's credentials and only a registry that accepts them is ours.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// probePassword is the registry password the service under test holds.
const probePassword = "our-registry-password"

// registryAnswer is what a fake registry on a candidate port replies to the
// identity probe.
type registryAnswer int

const (
	// answerOurs accepts our credentials and reports the probe repository
	// absent — a 404 from registry:2, which is what our own registry gives.
	answerOurs registryAnswer = iota
	// answerForeign is a sibling instance's registry: listening on the port,
	// but our password is not in its htpasswd.
	answerForeign
	// answerDead is nothing listening; the daemon's dial fails.
	answerDead
)

// selectionDaemon fakes the daemon's /distribution endpoint, answering per
// port according to answers. Ports absent from the map answer as ours.
func selectionDaemon(t *testing.T, answers map[int]registryAnswer) (*Service, *atomic.Int32) {
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

		// Whatever else it does, the probe must offer our credentials — that is
		// the only thing that can tell two registries apart.
		user, password := probeCredentials(t, r)
		if user != ecrRegistryUser || password != probePassword {
			t.Errorf("probe of :%d carried credentials %q/%q, want %q/%q",
				port, user, password, ecrRegistryUser, probePassword)
		}

		// A port absent from the map is the zero value, answerOurs.
		switch answers[port] {
		case answerDead:
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"message": fmt.Sprintf("Get \"http://localhost:%d/v2/\": dial tcp [::1]:%d: connect: connection refused", port, port),
			})
		case answerForeign:
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "unauthorized: authentication required"})
		case answerOurs:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "manifest unknown: manifest unknown"})
		}
	}))
	t.Cleanup(srv.Close)

	return &Service{
		cfg:          &config.Config{},
		log:          serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		docker:       docker.NewClient(strings.Replace(srv.URL, "http://", "tcp://", 1), zap.NewNop()),
		probeTimeout: 2 * time.Second,
	}, &probes
}

// probeCredentials decodes the X-Registry-Auth the probe carried.
func probeCredentials(t *testing.T, r *http.Request) (user, password string) {
	t.Helper()
	header := r.Header.Get("X-Registry-Auth")
	if header == "" {
		return "", ""
	}
	raw, err := base64.URLEncoding.DecodeString(header)
	if err != nil {
		t.Errorf("decode X-Registry-Auth: %v", err)
		return "", ""
	}
	var auth struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(raw, &auth); err != nil {
		t.Errorf("unmarshal X-Registry-Auth: %v", err)
		return "", ""
	}
	return auth.Username, auth.Password
}

func TestSelectDaemonReachablePort_picksThePortTheDaemonAnswersOn(t *testing.T) {
	// Given: split bindings where the first-listed port is unreachable from
	// the daemon's localhost and the second answers.
	s, _ := selectionDaemon(t, map[int]registryAnswer{32768: answerDead})

	// When: the port is selected.
	port, reachable := s.selectDaemonReachablePort([]int{32768, 32769}, probePassword)

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

func TestSelectDaemonReachablePort_ourRegistryIsProvedByAcceptedCredentials(t *testing.T) {
	// Given: our own registry, which accepts our password and reports the
	// probe repository missing.
	s, probes := selectionDaemon(t, nil)

	// When: the port is selected.
	port, reachable := s.selectDaemonReachablePort([]int{4510}, probePassword)

	// Then: that settles it on the first probe; no retry loop.
	if !reachable || port != 4510 {
		t.Fatalf("selection = (%d, %v), want (4510, true)", port, reachable)
	}
	if got := probes.Load(); got != 1 {
		t.Errorf("probes = %d, want 1", got)
	}
}

func TestSelectDaemonReachablePort_skipsARegistryThatRefusesOurCredentials(t *testing.T) {
	// Given: an interleaved dual-stack publish — our localhost:32768 reaches a
	// sibling instance's registry, while 32769 is ours. Both are listening, so
	// reachability cannot tell them apart.
	s, _ := selectionDaemon(t, map[int]registryAnswer{32768: answerForeign})

	// When: the port is selected.
	port, reachable := s.selectDaemonReachablePort([]int{32768, 32769}, probePassword)

	// Then: the sibling's port is passed over. Advertising it would mint
	// repositoryUri and tokens against a registry whose htpasswd has never
	// heard of us, so every push would fail authentication holding a token
	// that looks entirely valid.
	if !reachable {
		t.Fatal("expected a reachable verdict")
	}
	if port != 32769 {
		t.Errorf("port = %d, want 32769 — ours, not the sibling's", port)
	}
}

func TestSelectDaemonReachablePort_neverAdvertisesASiblingsPort(t *testing.T) {
	// Given: every candidate is answered by a registry that is not ours —
	// ours never came up.
	s, _ := selectionDaemon(t, map[int]registryAnswer{
		32768: answerForeign,
		32769: answerForeign,
	})

	// When: the port is selected.
	port, reachable := s.selectDaemonReachablePort([]int{32768, 32769}, probePassword)

	// Then: the sweep gives up rather than claiming a stranger's port. The
	// caller still gets a port to state, but reachable=false is what stops it
	// being reported as verified.
	if reachable {
		t.Errorf("selection reported reachable on port %d, but no candidate was ours", port)
	}
	if port != 32768 {
		t.Errorf("port = %d, want the first candidate as the stated fallback", port)
	}
}
