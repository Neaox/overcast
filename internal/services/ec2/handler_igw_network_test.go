package ec2

// handler_igw_network_test.go — what attaching or detaching an internet
// gateway does to a VPC's Docker network (#1569).
//
// Docker fixes --internal when a network is created, and refuses to remove a
// network with endpoints. Every VPC a stack creates starts internal (the
// gateway is attached after CreateVpc), so the flip has to succeed under
// whatever is already on the network, or fail the call — never report success
// over a network it did not change.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/state"
)

// fakeVPCDocker is an httptest Docker daemon holding just enough state for the
// gateway flip: networks with their --internal flag and endpoints, and each
// container's side of its attachments. It refuses to remove a network with
// endpoints, as the real daemon does.
type fakeVPCDocker struct {
	server *httptest.Server

	mu       sync.Mutex
	nextID   int
	networks map[string]*fakeNetwork // by ID
	calls    []string

	// refuseRemove makes every network removal fail, endpoints or not — a
	// daemon refusing for a reason the flip cannot fix.
	refuseRemove bool
	// failCreate makes every network create fail — the recreate half of a
	// flip failing after the old network is gone.
	failCreate bool
	// failConnect names containers whose reconnect the daemon refuses — a
	// recreate that succeeded but cannot take every container back.
	failConnect map[string]bool
}

type fakeNetwork struct {
	id, name string
	internal bool
	// driver is echoed back by inspect because the isolation check compares the
	// whole spec, not just the isolation flag — a fake that reported no driver
	// would make every network look drifted and every test recreate one.
	driver    string
	subnet    string
	labels    map[string]string
	endpoints map[string]fakeEndpoint // by container ID
}

type fakeEndpoint struct {
	ip      string
	aliases []string
}

func newFakeVPCDocker(t *testing.T) *fakeVPCDocker {
	t.Helper()
	f := &fakeVPCDocker{networks: map[string]*fakeNetwork{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeVPCDocker) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)

	path := strings.TrimPrefix(r.URL.Path, "/v1.45")
	switch {
	case r.Method == http.MethodPost && path == "/networks/create":
		var req struct {
			Name     string
			Driver   string
			Internal bool
			Labels   map[string]string
			IPAM     *docker.NetworkIPAM
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if f.failCreate {
			http.Error(w, `{"message":"could not find an available, non-overlapping IPv4 address pool"}`, http.StatusInternalServerError)
			return
		}
		// As the daemon does: two bridges cannot claim one subnet. This is
		// what a flip racing another flip of the same network runs into.
		for _, n := range f.networks {
			if req.IPAM != nil && len(req.IPAM.Config) > 0 && n.subnet == req.IPAM.Config[0].Subnet {
				http.Error(w, `{"message":"Pool overlaps with other one on this address space"}`, http.StatusForbidden)
				return
			}
		}
		f.nextID++
		n := &fakeNetwork{
			id: fmt.Sprintf("net-%d", f.nextID), name: req.Name, internal: req.Internal,
			driver: req.Driver, labels: req.Labels, endpoints: map[string]fakeEndpoint{},
		}
		if req.IPAM != nil && len(req.IPAM.Config) > 0 {
			n.subnet = req.IPAM.Config[0].Subnet
		}
		f.networks[n.id] = n
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": n.id})

	case strings.HasPrefix(path, "/networks/"):
		rest := strings.TrimPrefix(path, "/networks/")
		id, action, _ := strings.Cut(rest, "/")
		n := f.networks[id]
		if n == nil {
			http.Error(w, `{"message":"network `+id+` not found"}`, http.StatusNotFound)
			return
		}
		switch {
		case r.Method == http.MethodGet && action == "":
			containers := map[string]docker.NetworkEndpoint{}
			for cid := range n.endpoints {
				containers[cid] = docker.NetworkEndpoint{Name: cid, IPv4Address: n.endpoints[cid].ip + "/16"}
			}
			_ = json.NewEncoder(w).Encode(docker.NetworkInspect{
				ID: n.id, Name: n.name, Internal: n.internal, Labels: n.labels, Driver: n.driver,
				IPAM:       docker.NetworkIPAM{Config: []docker.NetworkIPAMConfig{{Subnet: n.subnet}}},
				Containers: containers,
			})
		case r.Method == http.MethodDelete && action == "":
			if f.refuseRemove || len(n.endpoints) > 0 {
				http.Error(w, `{"message":"error while removing network: network `+n.name+` has active endpoints"}`, http.StatusForbidden)
				return
			}
			delete(f.networks, id)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && action == "connect":
			var req struct {
				Container      string
				EndpointConfig *docker.EndpointSettings
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if f.failConnect[req.Container] {
				http.Error(w, `{"message":"cannot join network of a non running container: `+req.Container+`"}`, http.StatusForbidden)
				return
			}
			ep := fakeEndpoint{}
			if req.EndpointConfig != nil {
				ep.aliases = req.EndpointConfig.Aliases
				if req.EndpointConfig.IPAMConfig != nil {
					ep.ip = req.EndpointConfig.IPAMConfig.IPv4Address
				}
			}
			if ep.ip == "" {
				ep.ip = fmt.Sprintf("10.255.0.%d", len(n.endpoints)+2)
			}
			n.endpoints[req.Container] = ep
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && action == "disconnect":
			var req struct{ Container string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			if _, ok := n.endpoints[req.Container]; !ok {
				http.Error(w, `{"message":"container `+req.Container+` is not connected to network `+n.name+`"}`, http.StatusForbidden)
				return
			}
			delete(n.endpoints, req.Container)
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}

	case r.Method == http.MethodGet && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
		cid := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
		var info docker.ContainerInspect
		info.ID = cid
		info.NetworkSettings.Networks = map[string]docker.ContainerNetwork{}
		for _, n := range f.networks {
			if ep, ok := n.endpoints[cid]; ok {
				info.NetworkSettings.Networks[n.name] = docker.ContainerNetwork{NetworkID: n.id, IPAddress: ep.ip, Aliases: ep.aliases}
			}
		}
		_ = json.NewEncoder(w).Encode(info)

	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

// attach puts a container on a network the way a workload would be — an ECS
// task, an RDS instance, a Lambda function — with the address and DNS aliases
// its endpoint carries.
func (f *fakeVPCDocker) attach(netID, containerID, ip string, aliases ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.networks[netID].endpoints[containerID] = fakeEndpoint{ip: ip, aliases: aliases}
}

// network returns the one network with the given name, or nil.
func (f *fakeVPCDocker) network(name string) *fakeNetwork {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.networks {
		if n.name == name {
			return n
		}
	}
	return nil
}

func (f *fakeVPCDocker) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.networks[id]
	return ok
}

// callIndex returns the position of the first call matching the prefix, or -1.
func (f *fakeVPCDocker) callIndex(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

// vpcDockerHandler returns a handler on the host (not containerised — Overcast
// itself is then never one of the endpoints), wired to f, under the named
// strategy.
func vpcDockerHandler(t *testing.T, f *fakeVPCDocker, strategy string) *Handler {
	t.Helper()
	origInContainer := runningInContainer
	t.Cleanup(func() { runningInContainer = origInContainer })
	runningInContainer = func() bool { return false }

	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", Network: "overcast", EC2VPCNetworkStrategy: strategy}
	h := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.NewMock()).handler
	h.docker = docker.NewClient(strings.Replace(f.server.URL, "http://", "tcp://", 1), zap.NewNop())
	h.dockerReady.Store(true)
	return h
}

// ec2Call drives one Query-protocol handler and returns the recorder.
func ec2Call(t *testing.T, fn http.HandlerFunc, params url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

func xmlValue(t *testing.T, body, tag string) string {
	t.Helper()
	m := regexp.MustCompile("<" + tag + ">([^<]+)</" + tag + ">").FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no <%s> in response: %s", tag, body)
	}
	return m[1]
}

func createVPC(t *testing.T, h *Handler, cidr string) string {
	t.Helper()
	rec := ec2Call(t, h.CreateVpc, url.Values{"CidrBlock": {cidr}})
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateVpc: %d %s", rec.Code, rec.Body.String())
	}
	return xmlValue(t, rec.Body.String(), "vpcId")
}

func createIGW(t *testing.T, h *Handler) string {
	t.Helper()
	rec := ec2Call(t, h.CreateInternetGateway, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateInternetGateway: %d %s", rec.Code, rec.Body.String())
	}
	return xmlValue(t, rec.Body.String(), "internetGatewayId")
}

func gatewayParams(igwID, vpcID string) url.Values {
	return url.Values{"InternetGatewayId": {igwID}, "VpcId": {vpcID}}
}

func storedVPC(t *testing.T, h *Handler, vpcID string) *VPC {
	t.Helper()
	vpc, aerr := h.store.getVPC(context.Background(), vpcID)
	if aerr != nil {
		t.Fatalf("getVPC %s: %s", vpcID, aerr.Message)
	}
	return vpc
}

func attachedVPCs(t *testing.T, h *Handler, igwID string) []string {
	t.Helper()
	igw, aerr := h.store.getInternetGateway(context.Background(), igwID)
	if aerr != nil {
		t.Fatalf("getInternetGateway %s: %s", igwID, aerr.Message)
	}
	var out []string
	for _, a := range igw.Attachments {
		out = append(out, a.VpcID)
	}
	return out
}

func TestAttachInternetGateway_createThenAttach(t *testing.T) {
	// Given: a VPC created before any gateway exists — the order every
	// CloudFormation template produces — so its network is --internal.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	before := f.network("overcast-vpc-" + vpcID)
	if before == nil || !before.internal {
		t.Fatalf("expected the fresh VPC network to be --internal, got %+v", before)
	}
	igwID := createIGW(t, h)

	// When: the gateway is attached.
	rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwID, vpcID))

	// Then: the call succeeds, the network now backing the VPC is external,
	// the old one is gone, and the record names the new one.
	if rec.Code != http.StatusOK {
		t.Fatalf("AttachInternetGateway: %d %s", rec.Code, rec.Body.String())
	}
	after := f.network("overcast-vpc-" + vpcID)
	if after == nil || after.internal {
		t.Fatalf("expected an external network after attach, got %+v", after)
	}
	if after.id == before.id || f.has(before.id) {
		t.Errorf("expected the internal network %s to be replaced, still present", before.id)
	}
	if got := storedVPC(t, h, vpcID); got.DockerNetworkID != after.id || got.NetworkStatus != vpcNetworkStatusOK {
		t.Errorf("stored VPC = network %q status %q, want %q / ok", got.DockerNetworkID, got.NetworkStatus, after.id)
	}
	if got := attachedVPCs(t, h, igwID); len(got) != 1 || got[0] != vpcID {
		t.Errorf("gateway attachments = %v, want [%s]", got, vpcID)
	}
}

func TestAttachInternetGateway_movesAttachedContainers(t *testing.T) {
	// Given: a VPC whose --internal network already carries two workload
	// containers with their own addresses and DNS aliases — a database and a
	// task placed before the gateway arrived.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	before := f.network("overcast-vpc-" + vpcID)
	f.attach(before.id, "ctr-db", "10.9.0.5", "mydb.rds.local")
	f.attach(before.id, "ctr-task", "10.9.0.9")
	igwID := createIGW(t, h)

	// When: the gateway is attached.
	rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwID, vpcID))

	// Then: the call succeeds — Docker's refusal to remove a network with
	// endpoints is not the caller's problem — and both containers sit on the
	// recreated, external network with the address and aliases they had.
	if rec.Code != http.StatusOK {
		t.Fatalf("AttachInternetGateway: %d %s", rec.Code, rec.Body.String())
	}
	after := f.network("overcast-vpc-" + vpcID)
	if after == nil || after.internal || after.id == before.id {
		t.Fatalf("expected a recreated external network, got %+v", after)
	}
	if got := after.endpoints["ctr-db"]; got.ip != "10.9.0.5" || len(got.aliases) != 1 || got.aliases[0] != "mydb.rds.local" {
		t.Errorf("database endpoint after the flip = %+v, want 10.9.0.5 with alias mydb.rds.local", got)
	}
	if got := after.endpoints["ctr-task"]; got.ip != "10.9.0.9" {
		t.Errorf("task endpoint after the flip = %+v, want 10.9.0.9", got)
	}
	// The order is the only one Docker accepts: every endpoint off, remove,
	// create, every endpoint back on.
	disconnect := f.callIndex("POST /v1.45/networks/" + before.id + "/disconnect")
	remove := f.callIndex("DELETE /v1.45/networks/" + before.id)
	reconnect := f.callIndex("POST /v1.45/networks/" + after.id + "/connect")
	if !(disconnect >= 0 && disconnect < remove && remove < reconnect) {
		t.Errorf("call order disconnect=%d remove=%d reconnect=%d, want disconnect < remove < reconnect: %v", disconnect, remove, reconnect, f.calls)
	}
	if got := storedVPC(t, h, vpcID); got.DockerNetworkID != after.id {
		t.Errorf("stored VPC names network %q, want %q", got.DockerNetworkID, after.id)
	}
}

func TestAttachInternetGateway_dockerRefusesRemoval(t *testing.T) {
	// Given: a daemon that will not remove the VPC network no matter what
	// (a container attached from outside Overcast, say), with a workload
	// container on it.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	before := f.network("overcast-vpc-" + vpcID)
	f.attach(before.id, "ctr-db", "10.9.0.5", "mydb.rds.local")
	igwID := createIGW(t, h)
	f.refuseRemove = true

	// When: the gateway is attached.
	rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwID, vpcID))

	// Then: the call fails, saying what Docker refused, and nothing is
	// recorded — a retry is possible, and DescribeInternetGateways does not
	// claim a gateway the network does not reflect.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("AttachInternetGateway: %d %s, want 500", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<Code>InternalError</Code>") || !strings.Contains(body, "has active endpoints") {
		t.Errorf("error body should carry InternalError and Docker's reason: %s", body)
	}
	if got := attachedVPCs(t, h, igwID); len(got) != 0 {
		t.Errorf("gateway attachments = %v, want none after a failed attach", got)
	}
	// And the network is as it was: still the same internal network, with
	// the container back on it at its old address.
	after := f.network("overcast-vpc-" + vpcID)
	if after == nil || after.id != before.id || !after.internal {
		t.Fatalf("expected the original internal network to survive, got %+v", after)
	}
	if got := after.endpoints["ctr-db"]; got.ip != "10.9.0.5" || len(got.aliases) != 1 {
		t.Errorf("container was not put back on the network: %+v", got)
	}
	if got := storedVPC(t, h, vpcID); got.DockerNetworkID != before.id {
		t.Errorf("stored VPC names network %q, want the untouched %q", got.DockerNetworkID, before.id)
	}
	// Nothing changed, so nothing is reported as broken.
	if got := h.networkProblems(); len(got) != 0 {
		t.Errorf("network problems = %+v, want none for a flip that changed nothing", got)
	}
}

func TestAttachInternetGateway_recreateFailsAfterRemoval(t *testing.T) {
	// Given: a daemon that removes the old network and then cannot create
	// the new one — the one failure with no way back.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	igwID := createIGW(t, h)
	f.failCreate = true

	// When: the gateway is attached.
	rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwID, vpcID))

	// Then: the call fails, the VPC is recorded as unbacked rather than
	// pointing at a network that no longer exists, and the state is reported
	// for the health advisories, since nothing about it heals on its own.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("AttachInternetGateway: %d %s, want 500", rec.Code, rec.Body.String())
	}
	if got := storedVPC(t, h, vpcID); got.DockerNetworkID != "" || got.NetworkStatus != vpcNetworkStatusUnbacked {
		t.Errorf("stored VPC = network %q status %q, want unbacked with no network", got.DockerNetworkID, got.NetworkStatus)
	}
	problems := h.networkProblems()
	if len(problems) != 1 || problems[0].VpcID != vpcID || !strings.Contains(problems[0].Detail, "external") {
		t.Errorf("network problems = %+v, want one for %s wanting an external network", problems, vpcID)
	}
}

func TestDetachInternetGateway_sharedNetworkFollowsItsLastGateway(t *testing.T) {
	// Given: two VPCs with the same CIDR sharing one Docker network under the
	// shared strategy, each with its own gateway attached.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcA := createVPC(t, h, "10.9.0.0/16")
	igwA := createIGW(t, h)
	if rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwA, vpcA)); rec.Code != http.StatusOK {
		t.Fatalf("attach A: %d %s", rec.Code, rec.Body.String())
	}
	vpcB := createVPC(t, h, "10.9.0.0/16")
	if got := storedVPC(t, h, vpcB); got.NetworkStatus != vpcNetworkStatusShared || got.DockerNetworkID != storedVPC(t, h, vpcA).DockerNetworkID {
		t.Fatalf("expected B to share A's network, got %+v", got)
	}
	igwB := createIGW(t, h)
	if rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwB, vpcB)); rec.Code != http.StatusOK {
		t.Fatalf("attach B: %d %s", rec.Code, rec.Body.String())
	}
	shared := f.network("overcast-vpc-" + vpcA)
	if shared == nil || shared.internal {
		t.Fatalf("expected the shared network to be external, got %+v", shared)
	}

	// When: B's gateway is detached while A still has one.
	if rec := ec2Call(t, h.DetachInternetGateway, gatewayParams(igwB, vpcB)); rec.Code != http.StatusOK {
		t.Fatalf("detach B: %d %s", rec.Code, rec.Body.String())
	}

	// Then: the network stays external, untouched — a sharer's detach must
	// not cut off a VPC that still has its gateway.
	if got := f.network("overcast-vpc-" + vpcA); got == nil || got.id != shared.id || got.internal {
		t.Errorf("expected the shared network to stay as it was, got %+v", got)
	}

	// When: A's gateway, the last one, is detached.
	if rec := ec2Call(t, h.DetachInternetGateway, gatewayParams(igwA, vpcA)); rec.Code != http.StatusOK {
		t.Fatalf("detach A: %d %s", rec.Code, rec.Body.String())
	}

	// Then: the network is recreated --internal, and both records follow it —
	// a sharer left pointing at the removed network would place its next
	// container nowhere.
	after := f.network("overcast-vpc-" + vpcA)
	if after == nil || !after.internal || after.id == shared.id {
		t.Fatalf("expected a recreated internal network, got %+v", after)
	}
	a, b := storedVPC(t, h, vpcA), storedVPC(t, h, vpcB)
	if a.DockerNetworkID != after.id || b.DockerNetworkID != after.id {
		t.Errorf("records name networks A=%q B=%q, want both %q", a.DockerNetworkID, b.DockerNetworkID, after.id)
	}
	if a.NetworkStatus != vpcNetworkStatusOK || b.NetworkStatus != vpcNetworkStatusShared {
		t.Errorf("statuses A=%q B=%q, want ok / shared", a.NetworkStatus, b.NetworkStatus)
	}
}

func TestReconcileNetworks_repairsStaleIsolation(t *testing.T) {
	// Given: a VPC whose stored gateway is attached, backed by a network that
	// is still --internal — created by a run that could not flip it — with
	// a container on it.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	stale := f.network("overcast-vpc-" + vpcID)
	f.attach(stale.id, "ctr-db", "10.9.0.5", "mydb.rds.local")
	igw := &InternetGateway{InternetGatewayID: "igw-stale", Attachments: []IGWAttachment{{VpcID: vpcID, State: "attached"}}}
	if aerr := h.store.putInternetGateway(context.Background(), igw); aerr != nil {
		t.Fatal(aerr.Message)
	}

	// When: startup reconcile runs over the networks Docker reports.
	h.reconcileNetworks(context.Background(), []docker.NetworkSummary{{
		ID: stale.id, Name: stale.name, Labels: stale.labels,
		IPAM: docker.NetworkIPAM{Config: []docker.NetworkIPAMConfig{{Subnet: stale.subnet}}},
	}})

	// Then: the network is recreated external, the container moved with it,
	// and the record names the new network.
	after := f.network("overcast-vpc-" + vpcID)
	if after == nil || after.internal || after.id == stale.id {
		t.Fatalf("expected reconcile to recreate the network external, got %+v", after)
	}
	if got := after.endpoints["ctr-db"]; got.ip != "10.9.0.5" {
		t.Errorf("container after reconcile = %+v, want it back at 10.9.0.5", got)
	}
	if got := storedVPC(t, h, vpcID); got.DockerNetworkID != after.id {
		t.Errorf("stored VPC names network %q, want %q", got.DockerNetworkID, after.id)
	}
	if got := h.networkProblems(); len(got) != 0 {
		t.Errorf("network problems = %+v, want none after a successful repair", got)
	}
}

func TestReconcileNetworks_reportsIsolationItCannotRepair(t *testing.T) {
	// Given: the same stale network, on a daemon that refuses to remove it.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	stale := f.network("overcast-vpc-" + vpcID)
	igw := &InternetGateway{InternetGatewayID: "igw-stale", Attachments: []IGWAttachment{{VpcID: vpcID, State: "attached"}}}
	if aerr := h.store.putInternetGateway(context.Background(), igw); aerr != nil {
		t.Fatal(aerr.Message)
	}
	f.refuseRemove = true

	// When: startup reconcile runs.
	h.reconcileNetworks(context.Background(), []docker.NetworkSummary{{
		ID: stale.id, Name: stale.name, Labels: stale.labels,
		IPAM: docker.NetworkIPAM{Config: []docker.NetworkIPAMConfig{{Subnet: stale.subnet}}},
	}})

	// Then: the mismatch is on record for the health advisories — a startup
	// log line is not somewhere anyone looks after the fact — naming the VPC
	// and the state it should be in.
	problems := New(&config.Config{Region: "us-east-1"}, state.NewMemoryStore(), zap.NewNop(), clock.NewMock()).NetworkProblems()
	if len(problems) != 0 {
		t.Fatalf("a fresh service reports problems: %+v", problems)
	}
	problems = h.networkProblems()
	if len(problems) != 1 || problems[0].VpcID != vpcID || problems[0].NetworkID != stale.id {
		t.Fatalf("network problems = %+v, want one for %s on %s", problems, vpcID, stale.id)
	}
	if !strings.Contains(problems[0].Detail, "external") || !strings.Contains(problems[0].Detail, "has active endpoints") {
		t.Errorf("problem detail should say what was wanted and what Docker said: %q", problems[0].Detail)
	}

	// And: once the flip succeeds, the entry clears.
	f.refuseRemove = false
	if rec := ec2Call(t, h.DetachInternetGateway, gatewayParams("igw-stale", vpcID)); rec.Code != http.StatusOK {
		t.Fatalf("detach: %d %s", rec.Code, rec.Body.String())
	}
	if got := h.networkProblems(); len(got) != 0 {
		t.Errorf("network problems = %+v, want none once the network matches again", got)
	}
}

func TestNetworkProblems_orderedByVPC(t *testing.T) {
	// Given: problems recorded in no particular order.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	h.noteNetworkProblem("vpc-b", "net-b", "bang")
	h.noteNetworkProblem("vpc-a", "net-a", "boom")

	// When/Then: they come back ordered, so the advisory reads the same on
	// every refresh.
	got := h.networkProblems()
	ids := make([]string, len(got))
	for i, p := range got {
		ids[i] = p.VpcID
	}
	if !sort.StringsAreSorted(ids) || len(ids) != 2 {
		t.Errorf("problems ordered %v, want sorted by VPC ID", ids)
	}
}

// The typed dispatch path is what DispatchQuery actually routes these
// operations through (typed_dispatch.go); the handlers above are its legacy
// twin. Both must flip before they record.

func TestAttachIGWTyped_movesAttachedContainers(t *testing.T) {
	// Given: a VPC whose --internal network carries a workload container.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	before := f.network("overcast-vpc-" + vpcID)
	f.attach(before.id, "ctr-db", "10.9.0.5", "mydb.rds.local")
	igwID := createIGW(t, h)

	// When: the gateway is attached through the typed operation.
	resp, aerr := h.attachIGWTyped(context.Background(), &attachIGWReq{InternetGatewayID: igwID, VpcID: vpcID})

	// Then: same outcome as the handler — external network, container moved,
	// attachment recorded.
	if aerr != nil {
		t.Fatalf("attachIGWTyped: %s: %s", aerr.Code, aerr.Message)
	}
	if !resp.Return {
		t.Error("expected return=true")
	}
	after := f.network("overcast-vpc-" + vpcID)
	if after == nil || after.internal || after.id == before.id {
		t.Fatalf("expected a recreated external network, got %+v", after)
	}
	if got := after.endpoints["ctr-db"]; got.ip != "10.9.0.5" {
		t.Errorf("container after the flip = %+v, want it at 10.9.0.5", got)
	}
	if got := attachedVPCs(t, h, igwID); len(got) != 1 || got[0] != vpcID {
		t.Errorf("gateway attachments = %v, want [%s]", got, vpcID)
	}
}

func TestAttachIGWTyped_dockerRefusesRemoval(t *testing.T) {
	// Given: a daemon that refuses to remove the network.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	igwID := createIGW(t, h)
	f.refuseRemove = true

	// When: the gateway is attached through the typed operation.
	_, aerr := h.attachIGWTyped(context.Background(), &attachIGWReq{InternetGatewayID: igwID, VpcID: vpcID})

	// Then: InternalError with Docker's reason, and no attachment recorded.
	if aerr == nil || aerr.Code != "InternalError" || aerr.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("attachIGWTyped error = %+v, want InternalError/500", aerr)
	}
	if !strings.Contains(aerr.Message, "has active endpoints") {
		t.Errorf("message should carry Docker's reason: %s", aerr.Message)
	}
	if got := attachedVPCs(t, h, igwID); len(got) != 0 {
		t.Errorf("gateway attachments = %v, want none", got)
	}
}

func TestDetachIGWTyped_dockerRefusesRemoval(t *testing.T) {
	// Given: an attached gateway on a daemon that then refuses removals.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	igwID := createIGW(t, h)
	if _, aerr := h.attachIGWTyped(context.Background(), &attachIGWReq{InternetGatewayID: igwID, VpcID: vpcID}); aerr != nil {
		t.Fatalf("attach: %s", aerr.Message)
	}
	f.refuseRemove = true

	// When: the gateway is detached through the typed operation.
	_, aerr := h.detachIGWTyped(context.Background(), &detachIGWReq{InternetGatewayID: igwID, VpcID: vpcID})

	// Then: the call fails and the attachment stays, matching the network
	// that is still external.
	if aerr == nil || aerr.Code != "InternalError" {
		t.Fatalf("detachIGWTyped error = %+v, want InternalError", aerr)
	}
	if got := attachedVPCs(t, h, igwID); len(got) != 1 {
		t.Errorf("gateway attachments = %v, want the attachment kept", got)
	}
	if got := f.network("overcast-vpc-" + vpcID); got == nil || got.internal {
		t.Errorf("network = %+v, want it still external", got)
	}
}

func TestAttachInternetGateway_containerCannotRejoinRecreatedNetwork(t *testing.T) {
	// Given: two containers on the --internal network, one of which the
	// daemon will refuse to reconnect.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	before := f.network("overcast-vpc-" + vpcID)
	f.attach(before.id, "ctr-db", "10.9.0.5", "mydb.rds.local")
	f.attach(before.id, "ctr-stuck", "10.9.0.9")
	f.failConnect = map[string]bool{"ctr-stuck": true}
	igwID := createIGW(t, h)

	// When: the gateway is attached.
	rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwID, vpcID))

	// Then: the recreate is the point of no return — the network is external,
	// so the attachment is recorded and the call succeeds; the container that
	// could not rejoin is what is reported, through the advisories.
	if rec.Code != http.StatusOK {
		t.Fatalf("AttachInternetGateway: %d %s", rec.Code, rec.Body.String())
	}
	after := f.network("overcast-vpc-" + vpcID)
	if after == nil || after.internal || after.id == before.id {
		t.Fatalf("expected a recreated external network, got %+v", after)
	}
	if got := attachedVPCs(t, h, igwID); len(got) != 1 || got[0] != vpcID {
		t.Errorf("gateway attachments = %v, want [%s] — the network is external, the record must say so", got, vpcID)
	}
	if _, ok := after.endpoints["ctr-db"]; !ok {
		t.Error("the container that could rejoin should be on the new network")
	}
	if _, ok := after.endpoints["ctr-stuck"]; ok {
		t.Error("the refused container cannot be on the new network")
	}
	problems := h.networkProblems()
	if len(problems) != 1 || problems[0].VpcID != vpcID || problems[0].NetworkID != after.id ||
		!strings.Contains(problems[0].Detail, "ctr-stuck") || !strings.Contains(problems[0].Detail, "recreated external") {
		t.Errorf("network problems = %+v, want one naming ctr-stuck on the recreated external network", problems)
	}

	// And: once a later flip takes every container along, the entry clears.
	f.failConnect = nil
	if rec := ec2Call(t, h.DetachInternetGateway, gatewayParams(igwID, vpcID)); rec.Code != http.StatusOK {
		t.Fatalf("detach: %d %s", rec.Code, rec.Body.String())
	}
	if got := h.networkProblems(); len(got) != 0 {
		t.Errorf("network problems = %+v, want none after a clean flip", got)
	}
}

func TestChangeVPCGateway_concurrentSharersTakeTurns(t *testing.T) {
	// Given: several VPCs sharing one Docker network under the shared
	// strategy, with a container on it, and a gateway created for each.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	const sharers = 4
	vpcIDs := make([]string, sharers)
	igwIDs := make([]string, sharers)
	for i := range sharers {
		vpcIDs[i] = createVPC(t, h, "10.9.0.0/16")
		igwIDs[i] = createIGW(t, h)
	}
	shared := f.network("overcast-vpc-" + vpcIDs[0])
	f.attach(shared.id, "ctr-db", "10.9.0.5", "mydb.rds.local")

	// When: every sharer attaches its gateway at once. Without serialisation
	// each would disconnect the same endpoint, one would remove the network,
	// the others' removes would read as success, and their recreates would
	// collide on the subnet — marking every record unbacked.
	var wg sync.WaitGroup
	codes := make([]int, sharers)
	for i := range sharers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = ec2Call(t, h.AttachInternetGateway, gatewayParams(igwIDs[i], vpcIDs[i])).Code
		}()
	}
	wg.Wait()

	// Then: every call succeeds, exactly one external network backs all of
	// them, every record names it, the container is on it, and nothing is
	// reported as broken.
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("attach %d: status %d", i, code)
		}
	}
	after := f.network("overcast-vpc-" + vpcIDs[0])
	if after == nil || after.internal {
		t.Fatalf("expected one external network, got %+v", after)
	}
	for _, id := range vpcIDs {
		if got := storedVPC(t, h, id); got.DockerNetworkID != after.id || got.NetworkStatus == vpcNetworkStatusUnbacked {
			t.Errorf("record %s = network %q status %q, want %q and not unbacked", id, got.DockerNetworkID, got.NetworkStatus, after.id)
		}
	}
	if got := after.endpoints["ctr-db"]; got.ip != "10.9.0.5" {
		t.Errorf("container after the concurrent flips = %+v, want it at 10.9.0.5", got)
	}
	if got := h.networkProblems(); len(got) != 0 {
		t.Errorf("network problems = %+v, want none", got)
	}

	// When: every sharer detaches at once.
	for i := range sharers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = ec2Call(t, h.DetachInternetGateway, gatewayParams(igwIDs[i], vpcIDs[i])).Code
		}()
	}
	wg.Wait()

	// Then: the last one out turns the light off, and only once.
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("detach %d: status %d", i, code)
		}
	}
	final := f.network("overcast-vpc-" + vpcIDs[0])
	if final == nil || !final.internal {
		t.Fatalf("expected one internal network after every gateway left, got %+v", final)
	}
	for _, id := range vpcIDs {
		if got := storedVPC(t, h, id); got.DockerNetworkID != final.id {
			t.Errorf("record %s names network %q, want %q", id, got.DockerNetworkID, final.id)
		}
	}
}

func TestChangeVPCGateway_reconcileWaitsForAnInFlightFlip(t *testing.T) {
	// Given: a VPC whose network lock is held, as it is during a flip.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	vpc, unlock, aerr := h.lockVPCNetwork(context.Background(), vpcID)
	if aerr != nil {
		t.Fatal(aerr.Message)
	}

	// When: the startup isolation pass runs against that network.
	done := make(chan struct{})
	go func() {
		h.reconcileVPCNetworkIsolation(context.Background())
		close(done)
	}()

	// Then: it does not touch the network until the flip is over.
	select {
	case <-done:
		t.Fatal("reconcile ran over a network whose flip lock was held")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile never ran after the lock was released")
	}
	if got := storedVPC(t, h, vpcID); got.DockerNetworkID != vpc.DockerNetworkID {
		t.Errorf("reconcile moved a network that matched its gateway state: %q -> %q", vpc.DockerNetworkID, got.DockerNetworkID)
	}
}

func TestAttachInternetGateway_defaultVPCIsRecordedNotFlipped(t *testing.T) {
	// Given: the region's default VPC, whose network is the shared data plane.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpc, aerr := h.ensureDefaultVPC(context.Background())
	if aerr != nil {
		t.Fatal(aerr.Message)
	}
	igwID := createIGW(t, h)
	before := len(f.calls)

	// When: a gateway is attached to it.
	rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwID, vpc.VpcID))

	// Then: the attachment is recorded and the data plane is left alone —
	// recreating it would take every container Overcast started with it.
	if rec.Code != http.StatusOK {
		t.Fatalf("AttachInternetGateway: %d %s", rec.Code, rec.Body.String())
	}
	if got := attachedVPCs(t, h, igwID); len(got) != 1 || got[0] != vpc.VpcID {
		t.Errorf("gateway attachments = %v, want [%s]", got, vpc.VpcID)
	}
	f.mu.Lock()
	extra := f.calls[before:]
	f.mu.Unlock()
	if len(extra) != 0 {
		t.Errorf("expected no Docker calls for the default VPC, saw %v", extra)
	}
}

func TestDeleteVpc_clearsItsRecordedProblem(t *testing.T) {
	// Given: a VPC with a problem on record.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	h.noteNetworkProblem(vpcID, "net-x", "stuck")

	// When: the VPC is deleted.
	rec := ec2Call(t, h.DeleteVpc, url.Values{"VpcId": {vpcID}})

	// Then: the entry goes with the record — a deleted VPC cannot keep an
	// advisory alive.
	if rec.Code != http.StatusOK {
		t.Fatalf("DeleteVpc: %d %s", rec.Code, rec.Body.String())
	}
	if got := h.networkProblems(); len(got) != 0 {
		t.Errorf("network problems = %+v, want none after delete", got)
	}
}
