package ecs

// awsvpc_eni_test.go — the address a task reports and the address its container
// actually holds have to be the same one.
//
// The ENI's privateIPv4Address is what DescribeTasks reports and what an
// ip-type target group is registered with, so a load balancer dials it. It was
// allocated by EC2's counter, while Docker's IPAM hands out addresses of its
// own from the same subnet — nothing made the two agree.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const (
	testVPCNetworkID = "net-abc123"
	testDockerID     = "container-1"
)

// fakeDaemon serves the two Docker endpoints attachTaskENI uses: connecting a
// container to a network, and inspecting the address it ended up with.
// acceptPinned decides whether the daemon honours a requested address, which is
// how Docker behaves for one inside the subnet versus one outside it.
type fakeDaemon struct {
	server       *httptest.Server
	acceptPinned bool
	actualIP     string
	// pinnedRequested records the address the caller asked Docker for, if any.
	pinnedRequested string
	connects        int
}

func newFakeDaemon(t *testing.T, acceptPinned bool, actualIP string) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{acceptPinned: acceptPinned, actualIP: actualIP}
	d.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/connect"):
			var body struct {
				EndpointConfig *docker.EndpointSettings `json:"EndpointConfig"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode connect payload: %v", err)
			}
			d.connects++
			pinned := body.EndpointConfig != nil && body.EndpointConfig.IPAMConfig != nil
			if pinned {
				d.pinnedRequested = body.EndpointConfig.IPAMConfig.IPv4Address
				if !d.acceptPinned {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"message":"invalid IPv4 address for the network"}`))
					return
				}
				d.actualIP = body.EndpointConfig.IPAMConfig.IPv4Address
			}
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/json"):
			_, _ = w.Write([]byte(`{"Id":"` + testDockerID + `","NetworkSettings":{"Networks":{` +
				`"overcast_ecs":{"NetworkID":"net-ecs","IPAddress":"172.18.0.9"},` +
				`"overcast-vpc-x":{"NetworkID":"` + testVPCNetworkID + `","IPAddress":"` + d.actualIP + `"}}}}`))
		default:
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(d.server.Close)
	return d
}

// handlerWithDaemon returns a Handler whose Docker client talks to d.
func handlerWithDaemon(t *testing.T, d *fakeDaemon) *Handler {
	t.Helper()
	return &Handler{
		docker: docker.NewClient(strings.Replace(d.server.URL, "http://", "tcp://", 1), zap.NewNop()),
		log:    serviceutil.NewServiceLogger(zap.NewNop(), "ecs"),
	}
}

// awsvpcTask is a task carrying one ENI attachment at the allocated address.
func awsvpcTask(allocatedIP string) *Task {
	return &Task{Attachments: []Attachment{{
		Type:   "ElasticNetworkInterface",
		Status: "ATTACHING",
		Details: []KeyValuePair{
			{Name: "networkInterfaceId", Value: "eni-00000000"},
			{Name: "subnetId", Value: "subnet-1"},
			{Name: "privateIPv4Address", Value: allocatedIP},
		},
	}}}
}

func TestAttachTaskENI_asksDockerForTheAllocatedAddress(t *testing.T) {
	// Given: a task whose ENI was allocated 10.9.0.4, and a network that can
	// hand out that address.
	daemon := newFakeDaemon(t, true, "")
	h := handlerWithDaemon(t, daemon)
	task := awsvpcTask("10.9.0.4")

	// When: its container is attached to the VPC network.
	if err := h.attachTaskENI(context.Background(), task,
		awsvpcPlacement{networkID: testVPCNetworkID}, testDockerID, true); err != nil {
		t.Fatalf("attachTaskENI: %v", err)
	}

	// Then: Docker was asked for exactly the allocated address, so the container
	// holds the address the target group is registered with.
	if daemon.pinnedRequested != "10.9.0.4" {
		t.Errorf("Docker was asked for %q, want the allocated 10.9.0.4", daemon.pinnedRequested)
	}
	if got := taskTargetAddress(task); got != "10.9.0.4" {
		t.Errorf("task reports %q, want 10.9.0.4", got)
	}
}

func TestAttachTaskENI_reportsTheAddressDockerActuallyGave(t *testing.T) {
	// Given: a network that refuses the allocated address — already taken, or
	// outside its subnet — and hands out 10.9.0.2 instead.
	daemon := newFakeDaemon(t, false, "10.9.0.2")
	h := handlerWithDaemon(t, daemon)
	task := awsvpcTask("10.9.0.4")

	// When: its container is attached.
	if err := h.attachTaskENI(context.Background(), task,
		awsvpcPlacement{networkID: testVPCNetworkID}, testDockerID, true); err != nil {
		t.Fatalf("attachTaskENI: %v", err)
	}

	// Then: the task reports the address the container really has, not the one
	// that was asked for. Reporting the other one registers a load balancer
	// target that nothing answers on.
	if got := taskTargetAddress(task); got != "10.9.0.2" {
		t.Errorf("task reports %q, want the container's own 10.9.0.2", got)
	}
	if daemon.connects != 2 {
		t.Errorf("expected a retry without the pinned address, got %d connects", daemon.connects)
	}
}

func TestAttachTaskENI_leavesARemappedVPCsAddressAlone(t *testing.T) {
	// Given: a remapped VPC, where the Docker network deliberately sits on a
	// shadow CIDR and the API reports the user's own.
	daemon := newFakeDaemon(t, false, "100.64.0.2")
	h := handlerWithDaemon(t, daemon)
	task := awsvpcTask("10.9.0.4")

	// When: its container is attached.
	if err := h.attachTaskENI(context.Background(), task,
		awsvpcPlacement{networkID: testVPCNetworkID, remapped: true}, testDockerID, true); err != nil {
		t.Fatalf("attachTaskENI: %v", err)
	}

	// Then: the reported address is untouched — the shadow address is an
	// implementation detail remapped mode exists to hide.
	if got := taskTargetAddress(task); got != "10.9.0.4" {
		t.Errorf("task reports %q, want the unremapped 10.9.0.4", got)
	}
	if daemon.pinnedRequested != "" {
		t.Errorf("a remapped address should never be pinned, got %q", daemon.pinnedRequested)
	}
}

func TestAttachTaskENI_onlyTheFirstContainerCarriesTheENIAddress(t *testing.T) {
	// Given: a task's second container, which on AWS shares the task's single
	// ENI and cannot be given the address itself.
	daemon := newFakeDaemon(t, false, "10.9.0.7")
	h := handlerWithDaemon(t, daemon)
	task := awsvpcTask("10.9.0.4")

	// When: it is attached.
	if err := h.attachTaskENI(context.Background(), task,
		awsvpcPlacement{networkID: testVPCNetworkID}, testDockerID, false); err != nil {
		t.Fatalf("attachTaskENI: %v", err)
	}

	// Then: the task still reports the first container's address.
	if got := taskTargetAddress(task); got != "10.9.0.4" {
		t.Errorf("task reports %q, want the first container's 10.9.0.4", got)
	}
}
