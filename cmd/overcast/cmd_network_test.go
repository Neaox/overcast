package main

// cmd_network_test.go — `overcast network status|reset` against a fake daemon.
//
// The behaviour worth pinning is the part that is easy to get wrong under
// pressure: --dry-run must change nothing, and reset must stop Overcast's own
// containers while leaving somebody else's compose service running.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/dataplane"
)

// mustConfig loads the configuration these tests run against, so a spec built
// here is the one the command under test builds.
func mustConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// cliDaemon is a fake Docker daemon holding a fixed set of networks and
// containers, recording every mutating call.
type cliDaemon struct {
	mu         sync.Mutex
	networks   map[string]map[string]any
	containers []map[string]any
	calls      []string

	// instance is what /_overcast/health reports as docker.instance — the
	// daemon's sweep-domain identity. Empty models a daemon that is not
	// running, where the command cannot establish its own identity at all.
	instance string
}

func newCLIDaemon(t *testing.T) *cliDaemon {
	t.Helper()
	d := &cliDaemon{networks: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(d.handle))
	t.Cleanup(srv.Close)
	t.Setenv("LAMBDA_DOCKER_SOCKET", "tcp://"+srv.Listener.Addr().String())

	// The Overcast API the command reads its own identity from. A separate
	// server on its own port, as in a real setup.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_overcast/health" {
			http.NotFound(w, r)
			return
		}
		d.mu.Lock()
		id := d.instance
		d.mu.Unlock()
		if id == "" {
			// A daemon that has not resolved an identity reports none, which is
			// the same thing the command sees when nothing is listening.
			_, _ = w.Write([]byte(`{"docker":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"docker":{"instance":"` + id + `"}}`))
	}))
	t.Cleanup(api.Close)
	t.Setenv("OVERCAST_PORT", api.Listener.Addr().String()[strings.LastIndex(api.Listener.Addr().String(), ":")+1:])
	return d
}

// setInstance makes the fake daemon report an identity, as a running Overcast
// that has resolved one does.
func (d *cliDaemon) setInstance(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.instance = id
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

	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1.45/networks/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1.45/networks/")
		d.mu.Lock()
		for name, n := range d.networks {
			if name == id || n["Id"] == id {
				delete(d.networks, name)
				break
			}
		}
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

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
	// Drift is a non-zero exit by design, so a CI caller has something to
	// assert on besides the prose.
	if err := cmd.Execute(); !errors.Is(err, errDriftFound) {
		t.Fatalf("network status error = %v, want errDriftFound", err)
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

// ─── the gateway fact, --force, and ownership ───────────────────────────────

// vpcNetwork builds a per-VPC network exactly as the daemon would have created
// it — same spec, same labels, same hash — so a test that expects "ok" is
// asserting the CLI agrees with the daemon rather than with a hand-written
// fixture.
func vpcNetwork(t *testing.T, vpcID string, hasGateway bool, instance string) map[string]any {
	t.Helper()
	cfg := mustConfig(t)
	spec := dataplane.VPCNetworkSpec(cfg, dataplane.VPCNetwork{
		VPCID:              vpcID,
		Owner:              instance,
		Internal:           dataplane.VPCNetworkInternal(cfg, hasGateway),
		HasInternetGateway: hasGateway,
	}).Resolve(context.Background(), nil)
	opts := spec.CreateOptions()
	return map[string]any{
		"Id": "net-" + vpcID, "Name": opts.Name, "Driver": opts.Driver,
		"Internal": opts.Internal, "Scope": "local", "Labels": opts.Labels,
	}
}

// planeNetwork is the same for the control plane.
func planeNetwork(t *testing.T) map[string]any {
	t.Helper()
	opts := dataplane.PlaneSpecs(mustConfig(t))[1].
		Resolve(context.Background(), nil).CreateOptions()
	return map[string]any{
		"Id": "net-ctl", "Name": opts.Name, "Driver": opts.Driver,
		"Internal": opts.Internal, "Scope": "local", "Labels": opts.Labels,
	}
}

// B1: under `open` a VPC with an internet gateway has a routable network, and
// the CLI has to know that. It cannot ask a store, so it reads the fact the
// daemon recorded — and a CLI that assumed "no gateway" would report a mismatch
// on every gateway-attached VPC, rebuild it into a state contradicting the
// template, and be flipped straight back by the next reconcile.
func TestNetworkStatus_readsTheRecordedGatewayFact(t *testing.T) {
	cleanNetworkEnv(t)
	d := newCLIDaemon(t)
	// A gateway-attached VPC: routable, exactly as the daemon made it.
	d.addNetwork(vpcNetwork(t, "vpc-gw", true, ""))
	// And a gateway-less one: internal, also exactly as the daemon made it.
	d.addNetwork(vpcNetwork(t, "vpc-iso", false, ""))

	var out bytes.Buffer
	cmd := newNetworkCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"status"})
	_ = cmd.Execute()

	got := out.String()
	for _, want := range []string{
		"octest-vpc-vpc-gw: ok",
		"octest-vpc-vpc-iso: ok",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output does not contain %q — the CLI disagreed with the daemon about a "+
				"network the daemon created correctly:\n%s", want, got)
		}
	}
	// The ruling's condition: say where an isolated VPC network's egress comes
	// from, at the place the flag is read.
	if !strings.Contains(got, "egress via octest_control") {
		t.Errorf("an --internal VPC network's line does not say where its egress comes from:\n%s", got)
	}
}

// A network created before the gateway fact was recorded is not guessed at:
// isolation and the spec hash are dropped from the comparison and the drop is
// printed, so a clean report never silently means "I did not look".
func TestNetworkStatus_declinesToJudgeAnUnrecordedGatewayFact(t *testing.T) {
	cleanNetworkEnv(t)
	d := newCLIDaemon(t)
	n := vpcNetwork(t, "vpc-old", false, "")
	delete(n["Labels"].(map[string]string), "overcast.network.gateway")
	d.addNetwork(n)

	var out bytes.Buffer
	cmd := newNetworkCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"status"})
	_ = cmd.Execute()

	got := out.String()
	if strings.Contains(got, "internal: want") {
		t.Errorf("the CLI judged an isolation it cannot know:\n%s", got)
	}
	if !strings.Contains(got, "not compared") {
		t.Errorf("the CLI dropped the comparison without saying so:\n%s", got)
	}
}

// B2: --force is printed in the plan, so it has to actually rebuild. Before
// this it was accepted, planned, and then dropped by the re-diff under the lock.
func TestNetworkReset_forceRebuildsAMatchingNetwork(t *testing.T) {
	cleanNetworkEnv(t)
	d := newCLIDaemon(t)
	// A plane in exactly the state this configuration asks for.
	plane := planeNetwork(t)
	d.addNetwork(plane)
	name := plane["Name"].(string)

	var out bytes.Buffer
	cmd := newNetworkCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"reset", name, "--force", "--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("network reset --force: %v", err)
	}

	if !d.saw("DELETE") || !d.saw("POST /v1.45/networks/create") {
		t.Fatalf("--force did not rebuild the network; calls: %v\noutput:\n%s", d.calls, out.String())
	}
	if !strings.Contains(out.String(), "rebuilt "+name) {
		t.Errorf("output does not report the rebuild:\n%s", out.String())
	}
}

// Without --force the same network is left alone, which is the behaviour
// --force exists to override.
func TestNetworkReset_withoutForceLeavesAMatchingNetworkAlone(t *testing.T) {
	cleanNetworkEnv(t)
	d := newCLIDaemon(t)
	plane := planeNetwork(t)
	d.addNetwork(plane)
	name := plane["Name"].(string)

	cmd := newNetworkCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"reset", name, "--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("network reset: %v", err)
	}
	if d.saw("DELETE") {
		t.Fatalf("a matching network was rebuilt without --force; calls: %v", d.calls)
	}
}

// S9: a network belonging to another Overcast instance is skipped by a bare
// invocation and refused when named. Before this the guard compared the live
// network's owner against a spec built from that same label, so it could never
// fire, and a bare reset would rebuild a neighbour's live VPC network in
// silence.
func TestNetworkReset_refusesAnotherInstancesNetwork(t *testing.T) {
	cleanNetworkEnv(t)
	d := newCLIDaemon(t)
	d.setInstance("this-instance")
	d.addNetwork(vpcNetwork(t, "vpc-theirs", false, "another-instance"))

	var out bytes.Buffer
	cmd := newNetworkCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"reset", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("network reset --dry-run: %v", err)
	}
	if d.saw("DELETE") {
		t.Fatal("a bare reset touched another instance's network")
	}

	// Naming it explicitly is refused, with the owner named.
	cmd = newNetworkCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"reset", "octest-vpc-vpc-theirs", "--dry-run"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "another Overcast instance") {
		t.Fatalf("error = %v, want a refusal naming the other instance", err)
	}
}

// When the daemon is not running the command cannot establish its own identity,
// and must say that rather than assert the network is somebody else's. The two
// are different facts and only one of them is about the network.
func TestNetworkReset_saysWhenItCannotEstablishOwnership(t *testing.T) {
	cleanNetworkEnv(t)
	d := newCLIDaemon(t)
	d.addNetwork(vpcNetwork(t, "vpc-mine", false, "some-instance"))

	cmd := newNetworkCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"reset", "octest-vpc-vpc-mine", "--dry-run"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "could not establish this daemon's own identity") {
		t.Fatalf("error = %v, want it to say the identity could not be established", err)
	}
	if strings.Contains(err.Error(), "belongs to another") {
		t.Errorf("the message asserts ownership it could not establish: %v", err)
	}
}

// The gateway fact recorded on a network is the one behind its isolation, even
// when the flip that set it ran before the attachment was recorded. A label
// that contradicts the isolation beside it is worse than no label: the CLI
// computes its desired state from it, so it would report a mismatch that is not
// there — which is B1 arriving by another route.
func TestNetworkStatus_gatewayLabelAgreesWithIsolation(t *testing.T) {
	cleanNetworkEnv(t)
	d := newCLIDaemon(t)
	d.setInstance("this-instance")
	// A gateway-attached VPC: routable, and labelled as having a gateway.
	d.addNetwork(vpcNetwork(t, "vpc-gw", true, "this-instance"))

	var out bytes.Buffer
	cmd := newNetworkCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("network status: %v", err)
	}
	if !strings.Contains(out.String(), "octest-vpc-vpc-gw: ok") {
		t.Errorf("a correctly-created gateway-attached network was reported as drifted:\n%s", out.String())
	}
}

// --force overrides "it already matches". It does not override "I do not know
// what this should be".
//
// A VPC network created before the gateway fact was recorded has no
// `overcast.network.gateway` label, so the CLI's spec carries Internal=true —
// not because anything decided that, but because "unknown" reads as "no
// gateway" through VPCNetworkInternal. Rebuilding from it would take a
// routable, gateway-attached network and bring it back `--internal`, stamped
// with a spec hash for a state nothing ever chose. The existing --force test
// uses a plane, where the gateway fact is never in question.
func TestNetworkReset_forceNeverRebuildsWhatItCannotJudge(t *testing.T) {
	cleanNetworkEnv(t)
	d := newCLIDaemon(t)
	d.setInstance("this-instance")

	// As the daemon really made it: gateway attached, so routable — but from
	// before the fact was written down.
	n := vpcNetwork(t, "vpc-old", true, "this-instance")
	delete(n["Labels"].(map[string]string), "overcast.network.gateway")
	d.addNetwork(n)
	name := n["Name"].(string)

	// A bare --force, and the same network named explicitly: neither may touch it.
	for _, args := range [][]string{
		{"reset", "--force", "--yes"},
		{"reset", name, "--force", "--yes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			d.mu.Lock()
			d.calls = nil
			d.mu.Unlock()

			var out bytes.Buffer
			cmd := newNetworkCmd()
			cmd.SetOut(&out)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("network reset: %v", err)
			}
			if d.saw("DELETE") || d.saw("POST /v1.45/networks/create") {
				t.Fatalf("--force rebuilt a network whose isolation is unknown; calls: %v\ncalls would "+
					"have written internal=true over a routable network", d.calls)
			}
			if !strings.Contains(out.String(), "predates the recorded gateway state") &&
				!strings.Contains(out.String(), "not compared") &&
				!strings.Contains(out.String(), "Nothing to do") {
				t.Errorf("the command did not say why it declined:\n%s", out.String())
			}
		})
	}
}

// Under OVERCAST_VPC_EGRESS=routed a VPC has two networks, and `status` has to
// tell them apart: the plane is `--internal` however the gateway stands, and
// the egress network beside it is routable by definition. A CLI that judged
// the second by the first's rules would report a mismatch on every correctly
// built egress network and offer to rebuild it into an isolated one.
func TestNetworkStatus_routedReportsThePlaneAndItsEgressNetwork(t *testing.T) {
	cleanNetworkEnv(t)
	t.Setenv("OVERCAST_VPC_EGRESS", "routed")
	cfg := mustConfig(t)
	d := newCLIDaemon(t)

	// The plane, exactly as the daemon made it under routed: internal, with a
	// gateway attached — which decides nothing about the plane in this mode.
	d.addNetwork(vpcNetwork(t, "vpc-r", true, ""))

	// And the VPC's egress network, on its /24 from the pool.
	egressOpts := dataplane.VPCEgressNetworkSpec(cfg, dataplane.VPCEgressNetwork{
		VPCID: "vpc-r", Subnet: "198.18.0.0/24",
	}).Resolve(context.Background(), nil).CreateOptions()
	d.addNetwork(map[string]any{
		"Id": "net-vpc-r-egress", "Name": egressOpts.Name, "Driver": egressOpts.Driver,
		"Internal": egressOpts.Internal, "Scope": "local", "Labels": egressOpts.Labels,
		"IPAM":    map[string]any{"Config": []map[string]any{{"Subnet": "198.18.0.0/24"}}},
		"Options": egressOpts.Options,
	})

	var out bytes.Buffer
	cmd := newNetworkCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"status"})
	_ = cmd.Execute()

	got := out.String()
	for _, want := range []string{
		"octest-vpc-vpc-r: ok",
		"octest-vpc-vpc-r-egress: ok",
		// The plane's line says where a container in it would get a route out.
		"octest-vpc-vpc-r-egress (OVERCAST_VPC_EGRESS=routed)",
		// The egress network's line says what it is for.
		"the VPC's route out",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "egress via octest_control") {
		t.Errorf("routed's plane was described as taking egress from the control plane:\n%s", got)
	}
}
