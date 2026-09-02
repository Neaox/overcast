package containerendpoint

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
)

// reachability_dialer_test.go covers dockerDialer against a fake daemon.
//
// It is the only code in this package that talks to Docker, and it had no
// automated coverage at all — which is exactly how the probe command came to
// exit with the status of its `echo` rather than its `wget`. Every real failure
// then landed in the "connected to something else" arm and the container's own
// words, the one thing the advisory and the docs promise to quote, were thrown
// away. Nothing but the opt-in live test could have caught it, and that takes
// the reachable path on any healthy host.

// fakeRunner records what was asked of the daemon and answers with a canned
// container run.
type fakeRunner struct {
	imagePresent bool
	pulled       int
	created      *docker.CreateContainerRequest
	createdName  string
	createErr    error
	startErr     error
	exitCode     int
	waitErr      error
	logs         []byte
	removed      []string
	removeForced bool
}

func (f *fakeRunner) ImageExists(context.Context, string) (bool, error) {
	return f.imagePresent, nil
}

func (f *fakeRunner) PullImage(context.Context, string) error {
	f.pulled++
	f.imagePresent = true
	return nil
}

func (f *fakeRunner) CreateContainer(_ context.Context, name string, req *docker.CreateContainerRequest) (string, error) {
	f.createdName, f.created = name, req
	if f.createErr != nil {
		return "", f.createErr
	}
	return "probe-container-id", nil
}

func (f *fakeRunner) StartContainer(context.Context, string) error { return f.startErr }

func (f *fakeRunner) WaitContainer(context.Context, string) (int, error) {
	return f.exitCode, f.waitErr
}

func (f *fakeRunner) ContainerLogs(context.Context, string, string) ([]byte, error) {
	return f.logs, nil
}

func (f *fakeRunner) RemoveContainer(_ context.Context, id string, force bool) error {
	f.removed = append(f.removed, id)
	f.removeForced = force
	return nil
}

// framed wraps a payload the way a non-TTY container log arrives: an 8-byte
// header of stream type, three zero bytes and a big-endian length.
func framed(stream byte, payload string) []byte {
	out := []byte{stream, 0, 0, 0, 0, 0, 0, byte(len(payload))}
	return append(out, payload...)
}

func TestDockerDialer_runsTheProbeOnThePlaneWithTheHostGatewayEntry(t *testing.T) {
	// Given: a daemon that already holds busybox.
	f := &fakeRunner{imagePresent: true, logs: framed(1, "ok\nprobe-exit=0\n")}

	// When: a candidate is dialled.
	_, unavailable, err := dockerDialer(f, "overcast_control", zap.NewNop())(
		context.Background(), "host.docker.internal:12345")

	// Then: it succeeded, and no pull was needed.
	if unavailable || err != nil {
		t.Fatalf("dial() = unavailable=%v err=%v, want a clean success", unavailable, err)
	}
	if f.pulled != 0 {
		t.Errorf("pulled %d times, want 0 — the image was already present", f.pulled)
	}

	// And the container ran where the answer is actually for: on the control
	// plane, with the same host-gateway entry real Lambda containers get —
	// without which host.docker.internal would not resolve off Docker Desktop,
	// and this candidate would be reported unreachable on every Linux host.
	if f.created == nil || f.created.HostConfig == nil {
		t.Fatal("no container was created")
	}
	if got := f.created.HostConfig.NetworkMode; got != "overcast_control" {
		t.Errorf("NetworkMode = %q, want the control plane", got)
	}
	if !slices.Contains(f.created.HostConfig.ExtraHosts, dockerInternalHost+":host-gateway") {
		t.Errorf("ExtraHosts = %v, want the host-gateway entry", f.created.HostConfig.ExtraHosts)
	}
	if got := f.created.ContainerConfig.Image; got != docker.UtilityImage {
		t.Errorf("image = %q, want %q", got, docker.UtilityImage)
	}

	// And the address it was told to fetch is the one it was handed.
	cmd := strings.Join(f.created.ContainerConfig.Cmd, " ")
	if !strings.Contains(cmd, "http://host.docker.internal:12345/") {
		t.Errorf("probe command does not dial the candidate: %s", cmd)
	}

	// And the throwaway container is removed — one named after a timestamp is
	// litter nothing else will ever sweep.
	if !slices.Contains(f.removed, "probe-container-id") {
		t.Errorf("removed = %v, want the probe container gone", f.removed)
	}
}

func TestDockerDialer_theShellReportsWgetsExitStatusNotEchos(t *testing.T) {
	// Given: the probe command as it is actually built.
	f := &fakeRunner{imagePresent: true, logs: framed(1, "ok\nprobe-exit=0\n")}
	_, _, _ = dockerDialer(f, "overcast_control", zap.NewNop())(context.Background(), "10.0.0.1:1")
	cmd := f.created.ContainerConfig.Cmd[len(f.created.ContainerConfig.Cmd)-1]

	// Then: it captures wget's status before echoing, and exits with it.
	// `sh -c` exits with the status of its *last* command, so echoing the
	// marker first makes every probe container exit 0 whatever wget did —
	// which silently deletes the refused-vs-timed-out diagnosis the advisory,
	// the docs and the changelog all rest on.
	if !strings.Contains(cmd, "rc=$?") || !strings.Contains(cmd, "exit $rc") {
		t.Errorf("probe command does not propagate wget's exit status: %s", cmd)
	}
	if strings.Contains(cmd, "echo probe-exit=$?") {
		t.Errorf("probe command echoes $? directly — that is echo's status, not wget's: %s", cmd)
	}
}

func TestDockerDialer_surfacesTheContainersOwnReason(t *testing.T) {
	cases := map[string]struct{ log, want string }{
		// The firewall signature: packets dropped on the way in.
		"timed out": {log: "wget: download timed out\nprobe-exit=1\n", want: "download timed out"},
		// Nothing listening on an address that did answer at the IP layer.
		"refused": {
			log:  "wget: cannot connect to remote host (192.168.8.19): Connection refused\nprobe-exit=1\n",
			want: "Connection refused",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// Given: a probe container that failed and said why.
			f := &fakeRunner{imagePresent: true, exitCode: 1, logs: framed(2, tc.log)}

			// When: the candidate is dialled.
			out, unavailable, err := dockerDialer(f, "overcast_control", zap.NewNop())(
				context.Background(), "192.168.8.19:12345")

			// Then: it is a failure of the address, not of the probe, and the
			// container's own words come back to be quoted. This is the text the
			// whole diagnosis is built from: a refusal and a timeout mean
			// different things and need different fixes.
			if unavailable {
				t.Error("unavailable = true, want false — the probe ran, the address failed")
			}
			if err == nil {
				t.Error("err = nil, want the failure reported")
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output = %q, want %q in it", out, tc.want)
			}

			// And the container is still cleaned up on the failure path.
			if !slices.Contains(f.removed, "probe-container-id") {
				t.Errorf("removed = %v, want the probe container gone", f.removed)
			}
			if !f.removeForced {
				t.Error("RemoveContainer(force=false) — a probe container that is still running would be left behind")
			}
		})
	}
}

func TestDockerDialer_reportsUnavailableRatherThanUnreachable(t *testing.T) {
	cases := map[string]*fakeRunner{
		"create refused": {imagePresent: true, createErr: errors.New("daemon refused the create")},
		"start refused":  {imagePresent: true, startErr: errors.New("no such image")},
		"wait failed":    {imagePresent: true, waitErr: errors.New("daemon went away")},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			// When: the candidate is dialled on a daemon that cannot run it.
			_, unavailable, err := dockerDialer(f, "overcast_control", zap.NewNop())(
				context.Background(), "10.0.0.1:1")

			// Then: unavailable, not unreachable. The difference decides whether
			// Overcast fires its only critical advisory, degrades health and
			// widens the bind set on a host whose Lambda may work perfectly.
			if !unavailable {
				t.Errorf("unavailable = false (err=%v), want true — the probe could not run", err)
			}
			if err == nil {
				t.Error("err = nil, want the reason")
			}
		})
	}
}

func TestDockerDialer_withoutADaemonIsUnavailable(t *testing.T) {
	// Given/When: no client at all.
	_, unavailable, err := dockerDialer(nil, "overcast_control", zap.NewNop())(
		context.Background(), "10.0.0.1:1")

	// Then: a missing measurement, never a verdict about the address.
	if !unavailable || err == nil {
		t.Errorf("dial() = unavailable=%v err=%v, want unavailable with a reason", unavailable, err)
	}
}

func TestDockerDialer_pullsTheImageWhenItIsMissing(t *testing.T) {
	// Given: a daemon without busybox.
	f := &fakeRunner{imagePresent: false, logs: framed(1, "ok\nprobe-exit=0\n")}

	// When: a candidate is dialled.
	if _, unavailable, err := dockerDialer(f, "overcast_control", zap.NewNop())(
		context.Background(), "10.0.0.1:1"); unavailable || err != nil {
		t.Fatalf("dial() = unavailable=%v err=%v, want success after a pull", unavailable, err)
	}

	// Then: it was fetched, once.
	if f.pulled != 1 {
		t.Errorf("pulled %d times, want 1", f.pulled)
	}
}

// TestDockerDialer_endToEndThroughProbeCandidate is the seam the two halves meet
// at: dockerDialer's answer, read by probeCandidate, becoming the Attempt the
// advisory quotes. Both defects this file exists for lived precisely here — each
// half was defensible alone.
func TestDockerDialer_endToEndThroughProbeCandidate(t *testing.T) {
	t.Run("a firewalled address keeps its reason", func(t *testing.T) {
		f := &fakeRunner{imagePresent: true, exitCode: 1,
			logs: framed(2, "wget: download timed out\nprobe-exit=1\n")}
		c := Candidate{Mode: modeHost, ContainerHost: "192.168.8.19", BindHosts: []string{loopbackHost}}

		got := probeCandidate(context.Background(), c, dockerDialer(f, "overcast_control", zap.NewNop()))

		if got.Reachable || got.Unavailable {
			t.Fatalf("Attempt = %+v, want a measured failure", got)
		}
		if !strings.Contains(got.Error, "download timed out") {
			t.Errorf("Error = %q, want the container's own reason", got.Error)
		}
	})

	t.Run("a daemon that cannot run the probe is not a verdict", func(t *testing.T) {
		f := &fakeRunner{imagePresent: true, createErr: errors.New("refused")}
		c := Candidate{Mode: modeHost, ContainerHost: "192.168.8.19", BindHosts: []string{loopbackHost}}

		got := probeCandidate(context.Background(), c, dockerDialer(f, "overcast_control", zap.NewNop()))

		if !got.Unavailable {
			t.Fatalf("Attempt = %+v, want Unavailable — nothing was measured", got)
		}
	})
}
