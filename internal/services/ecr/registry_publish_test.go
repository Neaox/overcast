package ecr

// registry_publish_test.go — the shape of the registry's port publication.
//
// Two facts about that shape were measured on Docker Desktop (WSL2) and drive
// these tests. First: a port binding with an explicit HostIP publishes
// IPv4-only, and Docker Desktop does not wire an ephemeral v4-only binding
// into the VM the daemon runs in — the daemon then cannot dial its own
// registry at localhost, which surfaces as `docker push` timing out and every
// ECS/Lambda pull of an ECR image dying with CannotPullContainerError. An
// empty HostIP binds dual-stack, which both Desktop's forwarding and native
// Linux serve correctly. Second: a fixed, well-known port keeps repositoryUri
// stable across restarts and matches the port LocalStack serves, but the
// registry must degrade to an ephemeral port rather than stay down when
// something else holds it.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// publishDaemon fakes the container lifecycle the registry startup drives:
// create, credential copy, start, inspect. It records every create request and
// can refuse to bind a given host port.
type publishDaemon struct {
	takenPort string // host port that answers "already allocated" on start
	creates   []docker.CreateContainerRequest
	started   atomic.Int32
}

func newPublishService(t *testing.T, d *publishDaemon, port int) *Service {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/containers/create"):
			var req docker.CreateContainerRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			d.creates = append(d.creates, req)
			_ = json.NewEncoder(w).Encode(docker.CreateContainerResponse{ID: "reg-1"})
		case strings.HasSuffix(r.URL.Path, "/archive"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/start"):
			last := d.creates[len(d.creates)-1]
			if p := last.HostConfig.PortBindings[ecrRegistryPortKey][0].HostPort; p != "" && p == d.takenPort {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"driver failed programming external connectivity: Bind for 0.0.0.0:` + p + ` failed: port is already allocated"}`))
				return
			}
			d.started.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/reg-1/json"):
			_, _ = w.Write([]byte(`{"Id":"reg-1","NetworkSettings":{
				"Ports":{"5000/tcp":[{"HostIp":"0.0.0.0","HostPort":"39001"}]},
				"Networks":{"bridge":{"IPAddress":"172.17.0.9","Gateway":"172.17.0.1"}}}}`))
		case r.Method == http.MethodDelete, strings.HasSuffix(r.URL.Path, "/stop"):
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	return &Service{
		cfg:    &config.Config{Hostname: "localhost", AccountID: "000000000000", Region: "us-east-1", ECRRegistryPort: port},
		log:    serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		docker: docker.NewClient(strings.Replace(srv.URL, "http://", "tcp://", 1), zap.NewNop()),
	}
}

func TestStartRegistry_publishesDualStackOnTheConfiguredPort(t *testing.T) {
	// Given: a daemon with the configured port free.
	d := &publishDaemon{}
	s := newPublishService(t, d, 4510)

	// When: the registry is started.
	_, inspect, err := s.startRegistry(context.Background(), []byte("htpasswd"), "4510")
	if err != nil {
		t.Fatalf("startRegistry: %v", err)
	}

	// Then: the binding asks for the fixed port with no HostIP — an explicit
	// one narrows the publish to IPv4, which Docker Desktop does not wire into
	// the VM the daemon dials from.
	bindings := d.creates[0].HostConfig.PortBindings[ecrRegistryPortKey]
	if len(bindings) != 1 {
		t.Fatalf("port bindings = %#v, want exactly one", bindings)
	}
	if bindings[0].HostIP != "" {
		t.Errorf("HostIP = %q, want empty: an explicit host IP publishes v4-only, which Docker Desktop's daemon cannot reach", bindings[0].HostIP)
	}
	if bindings[0].HostPort != "4510" {
		t.Errorf("HostPort = %q, want the configured 4510", bindings[0].HostPort)
	}
	if inspect == nil || d.started.Load() != 1 {
		t.Fatalf("registry not started (started=%d)", d.started.Load())
	}
}

func TestInitRegistry_fallsBackToAnEphemeralPortWhenTheFixedOneIsTaken(t *testing.T) {
	// Given: something else already holds the configured port.
	d := &publishDaemon{takenPort: "4510"}
	s := newPublishService(t, d, 4510)

	// When: the registry is started the way initRegistryDocker does — fixed
	// port first, ephemeral on port exhaustion.
	_, _, err := s.startRegistry(context.Background(), []byte("htpasswd"), "4510")
	if err == nil || !docker.IsPortUnavailable(err) {
		t.Fatalf("expected a port-unavailable error, got %v", err)
	}
	_, _, err = s.startRegistry(context.Background(), []byte("htpasswd"), "")

	// Then: the retry publishes an ephemeral dual-stack binding and starts —
	// a registry on an unexpected port beats no registry at all.
	if err != nil {
		t.Fatalf("ephemeral retry: %v", err)
	}
	last := d.creates[len(d.creates)-1].HostConfig.PortBindings[ecrRegistryPortKey][0]
	if last.HostIP != "" || last.HostPort != "" {
		t.Errorf("fallback binding = %+v, want empty HostIP and HostPort", last)
	}
	if d.started.Load() != 1 {
		t.Fatalf("started = %d, want 1", d.started.Load())
	}
}

func TestRegistryClientCandidates_coverEveryVantageOvercastRunsFrom(t *testing.T) {
	// Given: the started registry's inspect result.
	var inspect docker.ContainerInspect
	if err := json.Unmarshal([]byte(`{"NetworkSettings":{
		"Networks":{"bridge":{"IPAddress":"172.17.0.9","Gateway":"172.17.0.1"}}}}`), &inspect); err != nil {
		t.Fatalf("unmarshal inspect: %v", err)
	}

	// When: the candidate list is built.
	got := registryClientCandidates(4510, &inspect)

	// Then: it offers loopback first (Overcast on the daemon's host), then the
	// registry's own address and the bridge gateway (Overcast in a container,
	// whose 127.0.0.1 is not the daemon's).
	want := []string{
		"http://127.0.0.1:4510",
		"http://172.17.0.9:5000",
		"http://172.17.0.1:4510",
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
