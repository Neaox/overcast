package ec2

// vpc_reconcile_regions_test.go — the startup reconcile covers every region
// the store holds, not only the default one.
//
// The EC2 store keys VPCs per region, and reconcileNetworks runs with no
// request context. Resolving the region from that context gave the default
// region, so a VPC created in ap-southeast-2 — three subnets, an attached
// gateway, persisted across the restart — came back with no Docker network,
// was absent from /_overcast/health, and stayed that way until something in
// that region happened to touch it. The us-east-1 VPC beside it was healed.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/dataplane"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/middleware"
)

const (
	otherRegion = "ap-southeast-2"
	otherVPCID  = "vpc-bac19e35"
)

func regionCtx(region string) context.Context {
	return middleware.ContextWithRegion(context.Background(), region)
}

// putStaleVPC stores a VPC under region whose record names a network the
// daemon no longer has — what a restart leaves behind when the networks went
// with the daemon (or `overcast network reset`) and the store did not.
func putStaleVPC(t *testing.T, h *Handler, region, vpcID, cidr string) {
	t.Helper()
	vpc := &VPC{VpcID: vpcID, CidrBlock: cidr, State: "available",
		DockerNetworkID: "net-gone-" + vpcID, NetworkStatus: vpcNetworkStatusOK}
	if aerr := h.store.putVPC(regionCtx(region), vpc); aerr != nil {
		t.Fatalf("putVPC(%s): %s", region, aerr.Message)
	}
}

// createVPCIn is createVPC under a request region.
func createVPCIn(t *testing.T, h *Handler, region, cidr string) string {
	t.Helper()
	params := url.Values{"CidrBlock": {cidr}}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(regionCtx(region))
	rec := httptest.NewRecorder()
	h.CreateVpc(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateVpc(%s): %d %s", region, rec.Code, rec.Body.String())
	}
	return xmlValue(t, rec.Body.String(), "vpcId")
}

func storedVPCIn(t *testing.T, h *Handler, region, vpcID string) *VPC {
	t.Helper()
	vpc, aerr := h.store.getVPC(regionCtx(region), vpcID)
	if aerr != nil {
		t.Fatalf("getVPC %s in %s: %s", vpcID, region, aerr.Message)
	}
	return vpc
}

// summaries is the daemon-wide snapshot the router hands ReconcileNetworks:
// every network the fake holds, as the list endpoint would report it.
func (f *fakeVPCDocker) summaries() []docker.NetworkSummary {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]docker.NetworkSummary, 0, len(f.networks))
	for _, n := range f.networks {
		out = append(out, docker.NetworkSummary{
			ID: n.id, Name: n.name, Labels: n.labels, Internal: n.internal, Driver: n.driver,
			IPAM: docker.NetworkIPAM{Config: []docker.NetworkIPAMConfig{{Subnet: n.subnet}}},
		})
	}
	return out
}

func healthReports(tracker *docker.Tracker, name string) bool {
	for _, n := range tracker.Snapshot().Networks {
		if n.Name == name {
			return true
		}
	}
	return false
}

func TestReconcileNetworks_recreatesAMissingNetworkInANonDefaultRegion(t *testing.T) {
	for _, strategy := range []string{"shared", "strict", "remapped"} {
		t.Run(strategy, func(t *testing.T) {
			// Given: an Overcast whose default region is us-east-1, reporting
			// its networks to health, with a VPC persisted in ap-southeast-2
			// whose Docker network did not survive the restart.
			f := newFakeVPCDocker(t)
			h := vpcDockerHandler(t, f, strategy)
			tracker := docker.NewTracker()
			h.SetNetworkReporter(tracker)
			putStaleVPC(t, h, otherRegion, otherVPCID, "10.0.0.0/16")
			mine := h.instances.Resolve(context.Background())
			if mine == "" {
				t.Fatal("the handler has no instance identity; the label below could not be checked")
			}

			// When: the startup reconcile runs over a daemon with no EC2 networks.
			h.reconcileNetworks(context.Background(), nil)

			// Then: the VPC has its network back, stamped as this instance's.
			name := h.cfg.VPCNetwork(otherVPCID)
			n := f.network(name)
			if n == nil {
				t.Fatalf("no network %q after reconcile: the VPC in %s was not reconciled", name, otherRegion)
			}
			if got := n.labels[docker.LabelInstance]; got != mine {
				t.Errorf("recreated network carries instance label %q, want %q", got, mine)
			}
			// And: the record names it and is launchable.
			got := storedVPCIn(t, h, otherRegion, otherVPCID)
			if got.DockerNetworkID != n.id {
				t.Errorf("stored VPC names network %q, want %q", got.DockerNetworkID, n.id)
			}
			if !dataplane.Launchable(got.NetworkStatus) {
				t.Errorf("stored VPC status %q is not launchable", got.NetworkStatus)
			}
			// And: health lists it beside the planes.
			if !healthReports(tracker, name) {
				t.Errorf("health reports %+v, want %q among them", tracker.Snapshot().Networks, name)
			}
		})
	}
}

func TestReconcileNetworks_keepsAnotherRegionsLiveNetwork(t *testing.T) {
	for _, strategy := range []string{"shared", "strict", "remapped"} {
		t.Run(strategy, func(t *testing.T) {
			// Given: one VPC in each of two regions, each backed by a network
			// the daemon has.
			f := newFakeVPCDocker(t)
			h := vpcDockerHandler(t, f, strategy)
			usVPC := createVPCIn(t, h, "us-east-1", "10.1.0.0/16")
			apVPC := createVPCIn(t, h, otherRegion, "10.2.0.0/16")
			usNet := storedVPCIn(t, h, "us-east-1", usVPC).DockerNetworkID
			apNet := storedVPCIn(t, h, otherRegion, apVPC).DockerNetworkID
			if usNet == "" || apNet == "" {
				t.Fatalf("VPCs are not backed: us=%q ap=%q", usNet, apNet)
			}

			// When: the startup reconcile runs over the daemon's snapshot.
			h.reconcileNetworks(context.Background(), f.summaries())

			// Then: neither network was taken for an orphan — a network is
			// unclaimed only when no VPC in *any* region names it.
			if !f.has(usNet) || !f.has(apNet) {
				t.Fatalf("after reconcile: us network present=%t, ap network present=%t; want both", f.has(usNet), f.has(apNet))
			}
			if got := storedVPCIn(t, h, otherRegion, apVPC); got.DockerNetworkID != apNet {
				t.Errorf("the %s VPC names %q after reconcile, want %q", otherRegion, got.DockerNetworkID, apNet)
			}
		})
	}
}

func TestReconcileNetworks_staleRecordDoesNotTakeAnotherRegionsNetworkOnItsSubnet(t *testing.T) {
	for _, strategy := range []string{"shared", "strict", "remapped"} {
		t.Run(strategy, func(t *testing.T) {
			// Given: the same CIDR in two regions, as CDK's default gives every
			// region. The us-east-1 VPC is backed; the ap-southeast-2 record
			// names a network that is gone — and ap-southeast-2 sorts first.
			f := newFakeVPCDocker(t)
			h := vpcDockerHandler(t, f, strategy)
			usVPC := createVPCIn(t, h, "us-east-1", "10.0.0.0/16")
			usNet := storedVPCIn(t, h, "us-east-1", usVPC).DockerNetworkID
			putStaleVPC(t, h, otherRegion, otherVPCID, "10.0.0.0/16")

			// When: the startup reconcile runs.
			h.reconcileNetworks(context.Background(), f.summaries())

			// Then: the live VPC keeps its network. Adoption by subnet is for
			// label drift and for networks whose VPC is gone, not for taking a
			// network out from under the VPC it is labelled for.
			if !f.has(usNet) {
				t.Fatal("the us-east-1 network was removed")
			}
			if got := storedVPCIn(t, h, "us-east-1", usVPC); got.DockerNetworkID != usNet {
				t.Errorf("us-east-1 VPC names %q after reconcile, want its own %q", got.DockerNetworkID, usNet)
			}
			// And: the record that lost its network does not name that one
			// either — Docker refused the overlapping pool, and the VPC is
			// honestly unbacked rather than quietly on the wrong bridge.
			if got := storedVPCIn(t, h, otherRegion, otherVPCID); got.DockerNetworkID == usNet {
				t.Errorf("the %s VPC adopted the us-east-1 network %q", otherRegion, usNet)
			} else if got.NetworkStatus != vpcNetworkStatusUnbacked {
				t.Errorf("the %s VPC status = %q (network %q), want unbacked", otherRegion, got.NetworkStatus, got.DockerNetworkID)
			}
		})
	}
}

func TestReconcileNetworks_sameRegionSameCIDRStillAdoptsBySubnet(t *testing.T) {
	// Given: in one region, VPC A created while Docker was down (unbacked)
	// and VPC B created on the same CIDR after Docker came back — B, finding
	// no live sharer, owns the network. A sorts first.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	a := &VPC{VpcID: "vpc-00000000", CidrBlock: "10.0.0.0/16", State: "available", NetworkStatus: vpcNetworkStatusUnbacked}
	if aerr := h.store.putVPC(context.Background(), a); aerr != nil {
		t.Fatalf("putVPC: %s", aerr.Message)
	}
	b := createVPCIn(t, h, "us-east-1", "10.0.0.0/16")
	bNet := storedVPC(t, h, b).DockerNetworkID

	// When: the startup reconcile runs.
	h.reconcileNetworks(context.Background(), f.summaries())

	// Then: as before regions were iterated, A adopts the network on its
	// subnet and B shares it — the cross-region guard does not apply within
	// a region, where the strategy's own sharing rules decide.
	gotA, gotB := storedVPC(t, h, a.VpcID), storedVPC(t, h, b)
	if gotA.DockerNetworkID != bNet || !dataplane.Launchable(gotA.NetworkStatus) {
		t.Errorf("A: network %q status %q, want %q and launchable", gotA.DockerNetworkID, gotA.NetworkStatus, bNet)
	}
	if gotB.DockerNetworkID != bNet || gotB.NetworkStatus != vpcNetworkStatusShared {
		t.Errorf("B: network %q status %q, want %q and shared", gotB.DockerNetworkID, gotB.NetworkStatus, bNet)
	}
}

func TestReconcileNetworks_recreateInAnotherRegionWaitsForTheNetworkLock(t *testing.T) {
	// Given: a VPC in ap-southeast-2 whose network is gone, and the lock on
	// that network's name held — as `overcast network reset` holds it.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	putStaleVPC(t, h, otherRegion, otherVPCID, "10.0.0.0/16")
	name := h.cfg.VPCNetwork(otherVPCID)
	unlock := docker.LockNetwork(name)

	// When: the startup reconcile runs.
	done := make(chan struct{})
	go func() {
		h.reconcileNetworks(context.Background(), nil)
		close(done)
	}()

	// Then: the recreate waits for the lock rather than racing its holder.
	select {
	case <-done:
		t.Fatal("reconcile finished while the network's lock was held")
	case <-time.After(100 * time.Millisecond):
	}
	if f.network(name) != nil {
		t.Fatal("the network was created under a lock held by someone else")
	}
	unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile never finished after the lock was released")
	}
	if f.network(name) == nil {
		t.Fatalf("no network %q once the lock was released", name)
	}
}

func TestDockerNetworkForVpc_reconcilesARegionTheStartupPassDidNotCover(t *testing.T) {
	// Given: a VPC in ap-southeast-2 whose network is gone, on an instance
	// whose startup reconcile has not reached that region (it ran before the
	// record was written, or could not read the store); and a live VPC in the
	// default region.
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	svc := &Service{handler: h, log: h.log}
	usVPC := createVPCIn(t, h, "us-east-1", "10.1.0.0/16")
	usNet := storedVPCIn(t, h, "us-east-1", usVPC).DockerNetworkID
	putStaleVPC(t, h, otherRegion, otherVPCID, "10.0.0.0/16")

	// When: a placement asks for the VPC's network under its region.
	if status := svc.VPCNetworkStatus(regionCtx(otherRegion), otherVPCID); !dataplane.Launchable(status) {
		t.Fatalf("VPCNetworkStatus = %q, want launchable", status)
	}
	got := svc.DockerNetworkForVpc(regionCtx(otherRegion), otherVPCID)
	lists := 0
	for _, c := range f.calls {
		if c == "GET /v1.45/networks" {
			lists++
		}
	}
	if lists != 1 {
		t.Errorf("the daemon was listed %d times across two placements in one region, want once", lists)
	}

	// Then: the network was recreated first, and the answer names it — not
	// the network that is gone.
	n := f.network(h.cfg.VPCNetwork(otherVPCID))
	if n == nil {
		t.Fatal("first use of the region did not recreate the VPC's network")
	}
	if got != n.id {
		t.Errorf("DockerNetworkForVpc = %q, want the recreated network %q", got, n.id)
	}
	// And: the other region's network was not mistaken for an orphan.
	if !f.has(usNet) {
		t.Error("the lazy pass removed the default region's live network")
	}
}
