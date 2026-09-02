package containerendpoint

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
)

// reachability_test.go covers the machinery listen_test.go injects past: the
// bind-and-connect probe, what it makes of a container's answer, and the
// remembered verdict.
//
// The deciding fact everywhere here is the *accept* on this side. A probe that
// trusted the container's own exit code would be one more thing standing
// between the question and the answer, which is the shape of the bug the whole
// change is about.

// dialLoopback stands in for the container: it opens a real connection to the
// probe listener on loopback, which is what a reachable candidate looks like
// from here whatever address the container was given.
func dialLoopback(_ context.Context, addr string) (string, bool, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", true, err
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 2*time.Second)
	if err != nil {
		return "wget: can't connect to remote host: Connection refused", false, err
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
	buf := make([]byte, 64)
	_, _ = conn.Read(buf)
	return "ok", false, nil
}

func TestProbeCandidate_reachableWhenAContainerActuallyConnects(t *testing.T) {
	// Given: a candidate this machine can bind, and a container that connects.
	c := Candidate{Mode: modeGateway, ContainerHost: "127.0.0.1", BindHosts: []string{loopbackHost}}

	// When: it is probed.
	got := probeCandidate(context.Background(), c, dialLoopback)

	// Then: reachable, with no error to explain.
	if !got.Reachable {
		t.Errorf("Reachable = false (%q), want true", got.Error)
	}
	if got.Mode != modeGateway || got.Host != "127.0.0.1" {
		t.Errorf("Attempt identifies %q/%q, want %s/127.0.0.1", got.Mode, got.Host, modeGateway)
	}
}

func TestProbeCandidate_unreachableCarriesTheContainersOwnReason(t *testing.T) {
	// Given: a container that cannot connect and says why. "Connection refused"
	// and "download timed out" are different diagnoses — nothing listening vs.
	// packets dropped on the way in — and the text is what lets the advisory
	// say which.
	c := Candidate{Mode: modeHost, ContainerHost: "192.168.8.19", BindHosts: []string{loopbackHost}}
	dial := func(context.Context, string) (string, bool, error) {
		return "wget: download timed out", false, errors.New("probe container exited 1")
	}

	// When: it is probed.
	got := probeCandidate(context.Background(), c, dial)

	// Then: not reachable, and the container's own words are kept.
	if got.Reachable {
		t.Error("Reachable = true, want false")
	}
	if !strings.Contains(got.Error, "download timed out") {
		t.Errorf("Error = %q, want the container's reason", got.Error)
	}
}

func TestProbeCandidate_doesNotTrustAContainerThatReachedSomethingElse(t *testing.T) {
	// Given: a dial that reports success while nothing arrives here — a proxy,
	// a captive portal, or an address some other process answered on.
	c := Candidate{Mode: modeHost, ContainerHost: "192.168.8.19", BindHosts: []string{loopbackHost}}
	dial := func(context.Context, string) (string, bool, error) { return "ok", false, nil }

	// When: it is probed.
	got := probeCandidate(context.Background(), c, dial)

	// Then: not reachable. The accept on this side is the only fact that
	// decides — the whole point is to stop believing a claim nothing measured.
	if got.Reachable {
		t.Error("Reachable = true, want false — nothing reached this process")
	}
	if !strings.Contains(got.Error, "not to this process") {
		t.Errorf("Error = %q, want it to name what actually happened", got.Error)
	}
}

func TestProbeCandidate_separatesAProbeThatCouldNotRunFromAnUnreachableAddress(t *testing.T) {
	// Given: a daemon that cannot run the probe at all — no image, or a create
	// that was refused.
	c := Candidate{Mode: modeHost, ContainerHost: "192.168.8.19", BindHosts: []string{loopbackHost}}
	dial := func(context.Context, string) (string, bool, error) {
		return "", true, errors.New("pull busybox:1.36: no such host")
	}

	// When: it is probed.
	got := probeCandidate(context.Background(), c, dial)

	// Then: not reachable, but the reason says the probe did not run rather
	// than that the address failed. Reading one as the other would turn an
	// offline machine into a false "no Lambda can run" advisory.
	if got.Reachable {
		t.Error("Reachable = true, want false")
	}
	if !strings.HasPrefix(got.Error, "probe could not run") {
		t.Errorf("Error = %q, want it to say the probe did not run", got.Error)
	}
}

func TestProbeCandidate_unbindableCandidateIsNotReachable(t *testing.T) {
	// Given: a candidate whose primary bind host is not an address of this
	// machine — TEST-NET-1 (RFC 5737).
	c := Candidate{Mode: modeHost, ContainerHost: "192.0.2.1", BindHosts: []string{"192.0.2.1"}}
	called := false
	dial := func(context.Context, string) (string, bool, error) {
		called = true
		return "", false, nil
	}

	// When: it is probed.
	got := probeCandidate(context.Background(), c, dial)

	// Then: it fails on the bind, no container is started, and the error names
	// the bind rather than the network.
	if got.Reachable {
		t.Error("Reachable = true, want false")
	}
	if called {
		t.Error("a container was started for a candidate that could not be bound")
	}
	if !strings.Contains(got.Error, "could not bind") {
		t.Errorf("Error = %q, want it to name the bind", got.Error)
	}
}

func TestListenProbe_bindsEveryHostOnOneSharedPort(t *testing.T) {
	// Given: loopback twice — the second bind has to join the port the first
	// settled, not take one of its own, or the container would be pointed at an
	// address that is listening on a different number.
	pl, err := listenProbe([]string{loopbackHost, loopbackHost})
	if err != nil {
		t.Fatalf("listenProbe() error = %v", err)
	}
	defer pl.Close()

	for _, ln := range pl.lns {
		if got := ln.Addr().(*net.TCPAddr).Port; got != pl.port {
			t.Errorf("listener on port %d, want the shared %d", got, pl.port)
		}
	}
}

func TestListenProbe_survivesASecondaryBindItCannotHold(t *testing.T) {
	// Given: loopback (bindable) followed by an address this machine does not
	// have. A candidate is still testable on the addresses that did bind, and a
	// host that cannot hold one of its own interfaces is exactly the situation
	// the probe exists to find out about.
	pl, err := listenProbe([]string{loopbackHost, "192.0.2.1"})
	if err != nil {
		t.Fatalf("listenProbe() error = %v", err)
	}
	defer pl.Close()
	if len(pl.lns) != 1 {
		t.Errorf("bound %d listeners, want just the one that could be held", len(pl.lns))
	}
}

func TestListenProbe_failsWhenThePrimaryCannotBeBound(t *testing.T) {
	// Given: a primary bind host this machine does not have. It is the address
	// closest to what containers were told to dial, so its failure is the
	// candidate's failure.
	if _, err := listenProbe([]string{"192.0.2.1", loopbackHost}); err == nil {
		t.Error("listenProbe() error = nil, want the primary bind to decide")
	}
	if _, err := listenProbe(nil); err == nil {
		t.Error("listenProbe(nil) error = nil, want an error")
	}
}

func TestProbeOutput_keepsTheClientsErrorAndDropsTheBookkeeping(t *testing.T) {
	const nl = "\n"
	cases := map[string]struct{ raw, want string }{
		"a refusal": {
			raw:  "wget: can't connect to remote host (192.168.8.19): Connection refused" + nl + "probe-exit=1" + nl,
			want: "wget: can't connect to remote host (192.168.8.19): Connection refused",
		},
		"a timeout": {
			raw:  "wget: download timed out" + nl + "probe-exit=1" + nl,
			want: "wget: download timed out",
		},
		// A success prints the body and the zero exit, neither of which is a
		// reason for anything — an empty result is the honest one.
		"a success": {raw: "ok" + nl + "probe-exit=0" + nl, want: ""},
		// A log the daemon never handed over carries no marker, so the raw text
		// is kept rather than being reported as "nothing useful was said".
		"no marker at all": {raw: "Error response from daemon" + nl, want: "Error response from daemon"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := probeOutput(tc.raw); got != tc.want {
				t.Errorf("probeOutput() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProbeOutput_readsTheStreamAsTheDaemonFramesIt(t *testing.T) {
	// Given: a stderr line as Docker actually delivers it — an 8-byte header
	// (stream type, three zero bytes, a big-endian length) in front of the
	// payload, because the probe container has no TTY.
	line := "wget: download timed out\n"
	framed := []byte{2, 0, 0, 0, 0, 0, 0, byte(len(line))}
	framed = append(framed, line...)

	// When: it goes through the same two steps the real path uses.
	got := probeOutput(string(docker.DemuxStream(framed)))

	// Then: the reason comes out legible. Skipping the de-frame would put
	// control bytes into a health advisory, where a user reads them.
	if got != "wget: download timed out" {
		t.Errorf("probeOutput(DemuxStream(...)) = %q, want the bare reason", got)
	}
}

func TestHintPath_isEmptyWithoutADataDirectory(t *testing.T) {
	// Given: memory mode, or no data directory configured.
	// When/Then: there is nowhere to remember it, and that is not an error —
	// the probe simply runs at every startup.
	if got := HintPath(""); got != "" {
		t.Errorf("HintPath(empty) = %q, want empty", got)
	}
	if got := HintPath("/data"); got != filepath.Join("/data", hintFileName) {
		t.Errorf("HintPath() = %q, want it under the data dir", got)
	}
}

func TestHint_roundTripsAndRejectsWhatItCannotTrust(t *testing.T) {
	path := filepath.Join(t.TempDir(), hintFileName)
	c := Candidate{Mode: modeDockerInternal, ContainerHost: dockerInternalHost,
		BindHosts: []string{loopbackHost, "192.168.8.19"}}
	writeHint(path, "daemon-1", "overcast_control", c, time.Now())

	// Given the matching daemon and plane: the candidate comes back whole, bind
	// set included — the pair travels together or the answer is a silent
	// misconfiguration.
	got := readHint(path, "daemon-1", "overcast_control")
	if got == nil {
		t.Fatal("readHint() = nil, want the remembered candidate")
	}
	if got.ContainerHost != c.ContainerHost || strings.Join(got.BindHosts, ",") != strings.Join(c.BindHosts, ",") {
		t.Errorf("readHint() = %+v, want %+v", *got, c)
	}

	// And every way of not being trustworthy is a miss rather than an error: a
	// hint is an optimisation, and one that cannot be read costs a probe.
	if readHint(path, "", "overcast_control") != nil {
		t.Error("a hint was used with no daemon identity to key it on")
	}
	if readHint("", "daemon-1", "overcast_control") != nil {
		t.Error("a hint was read from nowhere")
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if readHint(path, "daemon-1", "overcast_control") != nil {
		t.Error("an unparseable hint was used")
	}

	// And forgetting it is idempotent — it runs from a failure path.
	ForgetHint(path)
	ForgetHint(path)
	if readHint(path, "daemon-1", "overcast_control") != nil {
		t.Error("the hint survived ForgetHint")
	}
}

func TestWriteHint_keepsNothingItCannotKeyOn(t *testing.T) {
	// Given: no daemon identity — a daemon whose /info could not be read.
	path := filepath.Join(t.TempDir(), hintFileName)

	// When: a verified candidate is recorded.
	writeHint(path, "", "overcast_control",
		Candidate{Mode: modeHost, ContainerHost: "10.0.0.1", BindHosts: []string{"10.0.0.1"}}, time.Now())

	// Then: nothing is written. A hint that cannot be keyed cannot be
	// invalidated when the daemon changes, which makes it worse than no hint.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a hint was written with no daemon to key it on (stat err = %v)", err)
	}
}

// ---- Live, against a real daemon ------------------------------------------

// TestReachability_live runs the whole probe against the real Docker daemon on
// this machine — the only test that proves the busybox command, the
// stream-framed log, the host-gateway entry and the accept side actually fit
// together, all of which a fake dialer necessarily assumes.
//
// Opt-in, behind the same variable as the other real-daemon network tests
// (#1575): it starts containers, pulls an image on a cold machine, and creates
// a network, none of which belongs in a default `go test ./...`.
func TestReachability_live(t *testing.T) {
	if os.Getenv("OVERCAST_DOCKER_NETWORK_TESTS") == "" {
		t.Skip("set OVERCAST_DOCKER_NETWORK_TESTS=1 to run the real-daemon reachability probe " +
			"(it starts containers and creates a Docker network)")
	}
	dc := docker.NewClient(config.DefaultDockerSocket(), zap.NewNop())
	if !dc.Available(5 * time.Second) {
		t.Skip("Docker not available, skipping the real-daemon reachability probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)

	network := fmt.Sprintf("overcast-reach-test-%d", time.Now().UnixNano())
	if _, err := dc.CreateNetwork(ctx, network); err != nil {
		t.Skipf("could not create a test network (the daemon's address pools may be full): %v", err)
	}
	t.Cleanup(func() { _ = dc.RemoveNetwork(context.Background(), network) })

	// When: the whole listen set is resolved on this host, for real.
	got := ResolveListen(ctx, dc, ListenOptions{Network: network, Logger: zap.NewNop()})

	// Then: something is always advertised — a caller puts this in an env var,
	// and an unset one degrades worse than a wrong one.
	if got.ContainerHost == "" || len(got.BindHosts) == 0 {
		t.Fatalf("nothing advertised: %+v", got)
	}
	t.Logf("mode=%s host=%s binds=%v verified=%v unreachable=%v",
		got.Mode, got.ContainerHost, got.BindHosts, got.Verified, got.Unreachable)
	for _, a := range got.Attempts {
		t.Logf("  candidate %s", a)
	}

	// And the verdict is one thing or the other: an address a container reached,
	// or a documented failure. Both at once, or neither, means the probe did not
	// actually run.
	if got.Verified && got.Unreachable {
		t.Error("Verified and Unreachable are both set")
	}
	if !got.Verified && !got.Unreachable {
		t.Error("neither verified nor unreachable — the probe did not run; check the daemon can pull busybox")
	}

	// And nothing is left behind: the probe containers are removed even on the
	// failure path, because one named after a timestamp is litter nothing else
	// will ever sweep.
	list, err := dc.ListContainers(ctx, docker.ServiceCore)
	if err == nil {
		for _, c := range list {
			for _, n := range c.Names {
				if strings.Contains(n, "overcast-reachability-") {
					t.Errorf("probe container left behind: %s", n)
				}
			}
		}
	}
}
