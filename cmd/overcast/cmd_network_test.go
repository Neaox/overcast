package main

// cmd_network_test.go — `overcast network status|reset` against a fake daemon.
//
// The behaviour worth pinning is the part that is easy to get wrong under
// pressure: --dry-run must change nothing, and reset must stop Overcast's own
// containers while leaving somebody else's compose service running.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// cliDaemon is a fake Docker daemon holding a fixed set of networks and
// containers, recording every mutating call.
type cliDaemon struct {
	mu         sync.Mutex
	networks   map[string]map[string]any
	containers []map[string]any
	calls      []string
}

func newCLIDaemon(t *testing.T) *cliDaemon {
	t.Helper()
	d := &cliDaemon{networks: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(d.handle))
	t.Cleanup(srv.Close)
	t.Setenv("LAMBDA_DOCKER_SOCKET", "tcp://"+srv.Listener.Addr().String())
	return d
}

func (d *cliDaemon) handle(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	if r.Method != http.MethodGet {
		d.calls = append(d.calls, r.Method+" "+r.URL.Path)
	}
	d.mu.Unlock()

	switch {
	case r.URL.Path == "/_ping":
		w.WriteHeader(http.StatusOK)

	case r.URL.Path == "/v1.45/networks" || strings.HasPrefix(r.URL.Path, "/v1.45/networks?"):
		d.mu.Lock()
		list := make([]map[string]any, 0, len(d.networks))
		for _, n := range d.networks {
			list = append(list, n)
		}
		d.mu.Unlock()
		_ = json.NewEncoder(w).Encode(list)

	case strings.HasPrefix(r.URL.Path, "/v1.45/containers/json"):
		d.mu.Lock()
		list := append([]map[string]any(nil), d.containers...)
		d.mu.Unlock()
		_ = json.NewEncoder(w).Encode(list)

	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1.45/networks/"):
		name := strings.TrimPrefix(r.URL.Path, "/v1.45/networks/")
		d.mu.Lock()
		n, ok := d.networks[name]
		d.mu.Unlock()
		if !ok {
			http.Error(w, `{"message":"no such network"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(n)

	case r.Method == http.MethodPost && r.URL.Path == "/v1.45/networks/create":
		_, _ = w.Write([]byte(`{"Id":"net-new"}`))

	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (d *cliDaemon) addNetwork(n map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.networks[n["Name"].(string)] = n
}

func (d *cliDaemon) saw(prefix string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// cleanNetworkEnv clears the variables that would otherwise let a developer's
// own environment decide what the command under test does.
func cleanNetworkEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"OVERCAST_NETWORK", "OVERCAST_VPC_EGRESS", "OVERCAST_CONTROL_PLANE_INTERNAL"} {
		t.Setenv(k, "")
	}
	t.Setenv("OVERCAST_NETWORK", "octest")
}

// A network created before Overcast labelled its networks carries no spec hash,
// which is exactly the population #1564 was about — so `status` has to call it
// out rather than assume it is fine, and name the field.
func TestNetworkStatus_reportsAnUnlabelledNetworkAsMismatched(t *testing.T) {
	cleanNetworkEnv(t)
	d := newCLIDaemon(t)
	d.addNetwork(map[string]any{
		"Id": "net-ctl", "Name": "octest_control", "Driver": "bridge",
		"Internal": true, "Scope": "local", "Labels": map[string]string{},
	})

	var out bytes.Buffer
	cmd := newNetworkCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("network status: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "octest_control: NOT in the configured state") {
		t.Errorf("output does not flag the stale network:\n%s", got)
	}
	if !strings.Contains(got, "internal: want false, got true") {
		t.Errorf("output does not name the differing field:\n%s", got)
	}
	if !strings.Contains(got, "spec-hash") {
		t.Errorf("output does not report the missing spec label:\n%s", got)
	}
}

// --dry-run has to change nothing at all. It is the command an operator runs to
// find out whether it is safe to run the other one.
func TestNetworkReset_dryRunChangesNothing(t *testing.T) {
	cleanNetworkEnv(t)
	d := newCLIDaemon(t)
	d.addNetwork(map[string]any{
		"Id": "net-ctl", "Name": "octest_control", "Driver": "bridge",
		"Internal": true, "Scope": "local", "Labels": map[string]string{},
		"Containers": map[string]any{
			"c1": map[string]any{"Name": "overcast-lambda-orders"},
			"c2": map[string]any{"Name": "my-compose-db"},
		},
	})
	d.mu.Lock()
	d.containers = []map[string]any{{
		"Id": "c1", "Names": []string{"/overcast-lambda-orders"}, "State": "running",
		"Labels": map[string]string{"overcast.managed": "true", "overcast.service": "lambda"},
	}}
	d.mu.Unlock()

	var out bytes.Buffer
	cmd := newNetworkCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"reset", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("network reset --dry-run: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "octest_control") {
		t.Errorf("the plan does not name the network:\n%s", got)
	}
	// Overcast's own container is stopped; somebody else's is taken off the
	// network and left running. Stopping a compose service is not this
	// command's business.
	if !strings.Contains(got, "stop       overcast-lambda-orders") {
		t.Errorf("the plan does not say it would stop Overcast's own container:\n%s", got)
	}
	if !strings.Contains(got, "disconnect my-compose-db") {
		t.Errorf("the plan does not say it would only disconnect the unmanaged container:\n%s", got)
	}
	if !strings.Contains(got, "--dry-run: nothing was changed.") {
		t.Errorf("the output does not say it changed nothing:\n%s", got)
	}

	for _, call := range []string{"DELETE", "POST /v1.45/networks/create", "POST /v1.45/containers"} {
		if d.saw(call) {
			t.Fatalf("--dry-run made a mutating call (%s); calls: %v", call, d.calls)
		}
	}
}

// A network already in the configured state is not rebuilt. Churning a plane
// that is correct would drop every container on it for nothing.
func TestNetworkReset_skipsNetworksThatAlreadyMatch(t *testing.T) {
	cleanNetworkEnv(t)
	newCLIDaemon(t)

	var out bytes.Buffer
	cmd := newNetworkCmd()
	cmd.SetOut(&out)
	// Both planes are absent from the fake daemon, which is the "nothing to
	// repair" case: the daemon creates them correctly on its next start.
	cmd.SetArgs([]string{"reset", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("network reset --dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "Nothing to do.") {
		t.Errorf("output = %q, want it to report nothing to do", out.String())
	}
}

// Naming a network Overcast does not manage is an error, not a silent success.
// A command that reports success having done nothing is how somebody concludes
// the drift is fixed when it is not.
func TestNetworkReset_refusesAnUnmanagedName(t *testing.T) {
	cleanNetworkEnv(t)
	newCLIDaemon(t)

	cmd := newNetworkCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"reset", "bridge", "--dry-run"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error naming the network as unmanaged")
	}
	if !strings.Contains(err.Error(), "not a network this configuration manages") {
		t.Errorf("error = %q, want it to say the name is not managed", err)
	}
}
