package containerendpoint

import (
	"context"
	"errors"
	"testing"

	"github.com/Neaox/overcast/internal/docker"
)

// published_test.go covers recovering the host port Overcast is published on.
// A container started with `-p 4580:4566` listens on 4566 and has no way to
// know host callers arrive on 4580, so handing the web UI the listen port
// points the browser at a closed port.

// fakeInspectClient stands in for *docker.Client, returning a canned inspect
// result and recording the container it was asked about.
type fakeInspectClient struct {
	inspectedID string
	inspect     *docker.ContainerInspect
	inspectErr  error
}

func (f *fakeInspectClient) InspectContainer(_ context.Context, id string) (*docker.ContainerInspect, error) {
	f.inspectedID = id
	return f.inspect, f.inspectErr
}

// inspectWithPorts builds an inspect result publishing the given
// "containerPort/proto" → host port bindings.
func inspectWithPorts(bindings map[string][]docker.PortBinding) *docker.ContainerInspect {
	ci := &docker.ContainerInspect{}
	ci.NetworkSettings.Ports = bindings
	return ci
}

func inContainer() bool { return true }
func onHost() bool      { return false }

func TestPublishedPort_containerised_returnsRemappedHostPort(t *testing.T) {
	// Given Overcast runs in a container published as -p 4580:4566
	dc := &fakeInspectClient{inspect: inspectWithPorts(map[string][]docker.PortBinding{
		"4566/tcp": {{HostIP: "0.0.0.0", HostPort: "4580"}},
		"4567/tcp": {{HostIP: "0.0.0.0", HostPort: "4581"}},
	})}

	// When the published port for the internal API port is resolved
	got := publishedPort(context.Background(), dc, 4566, nil, inContainer)

	// Then it is the host port, not the port we listen on
	if got != 4580 {
		t.Errorf("publishedPort = %d, want 4580", got)
	}
}

func TestPublishedPort_notContainerised_returnsZero(t *testing.T) {
	// Given a native binary — Docker must not be consulted at all
	dc := &fakeInspectClient{inspect: inspectWithPorts(map[string][]docker.PortBinding{
		"4566/tcp": {{HostIP: "0.0.0.0", HostPort: "4580"}},
	})}

	// When the published port is resolved
	got := publishedPort(context.Background(), dc, 4566, nil, onHost)

	// Then it reports "no better answer" so the caller keeps its configured port
	if got != 0 {
		t.Errorf("publishedPort = %d, want 0 for a non-containerised process", got)
	}
	if dc.inspectedID != "" {
		t.Errorf("inspected container %q; a native binary must not call Docker", dc.inspectedID)
	}
}

func TestPublishedPort_sameHostAndContainerPort(t *testing.T) {
	// Given the conventional -p 4566:4566 mapping
	dc := &fakeInspectClient{inspect: inspectWithPorts(map[string][]docker.PortBinding{
		"4566/tcp": {{HostIP: "0.0.0.0", HostPort: "4566"}},
	})}

	got := publishedPort(context.Background(), dc, 4566, nil, inContainer)

	// Then the answer is the same port — no behaviour change for the default setup
	if got != 4566 {
		t.Errorf("publishedPort = %d, want 4566", got)
	}
}

func TestPublishedPort_portNotPublished_returnsZero(t *testing.T) {
	// Given a container whose API port is not published (e.g. --network host,
	// or reached only over a container network)
	dc := &fakeInspectClient{inspect: inspectWithPorts(map[string][]docker.PortBinding{
		"4567/tcp": {{HostIP: "0.0.0.0", HostPort: "4581"}},
	})}

	got := publishedPort(context.Background(), dc, 4566, nil, inContainer)

	if got != 0 {
		t.Errorf("publishedPort = %d, want 0 when the port is not published", got)
	}
}

func TestPublishedPort_inspectFails_returnsZero(t *testing.T) {
	// Given the Docker socket is not mounted
	dc := &fakeInspectClient{inspectErr: errors.New("dial /var/run/docker.sock: no such file")}

	got := publishedPort(context.Background(), dc, 4566, nil, inContainer)

	// Then we fall back rather than guessing
	if got != 0 {
		t.Errorf("publishedPort = %d, want 0 when Docker is unreachable", got)
	}
}

func TestPublishedPort_nilClientOrBadPort_returnsZero(t *testing.T) {
	if got := publishedPort(context.Background(), nil, 4566, nil, inContainer); got != 0 {
		t.Errorf("publishedPort(nil client) = %d, want 0", got)
	}
	dc := &fakeInspectClient{inspect: inspectWithPorts(nil)}
	if got := publishedPort(context.Background(), dc, 0, nil, inContainer); got != 0 {
		t.Errorf("publishedPort(port 0) = %d, want 0", got)
	}
}

func TestPublishedPort_ipv6BindingOnly(t *testing.T) {
	// Given Docker listed only the IPv6 binding for the mapping
	dc := &fakeInspectClient{inspect: inspectWithPorts(map[string][]docker.PortBinding{
		"4566/tcp": {{HostIP: "::", HostPort: "4590"}},
	})}

	got := publishedPort(context.Background(), dc, 4566, nil, inContainer)

	// Then the host port is still recovered — either family answers the question
	if got != 4590 {
		t.Errorf("publishedPort = %d, want 4590", got)
	}
}
