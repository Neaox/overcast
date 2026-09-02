package containerendpoint

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
)

// listen_test.go covers choosing where a server containers connect back to
// listens, and which of its addresses they are told to dial.
//
// Two behaviours are under test and they pull in opposite directions. The bind
// set is a *narrowing*: before it, the Lambda Runtime API bound 0.0.0.0
// unconditionally, so an unauthenticated control channel for every Lambda
// container sat on whatever network the machine was attached to — so every case
// asserts what is not bound as much as what is. The container host is a
// *measurement*: since #1572 the ordering below only proposes, and a container
// actually connecting is what decides, so every case says which candidate the
// container reached rather than which one looked plausible.

// fakeListenClient is fakeNetworkClient plus network inspection, daemon
// identity, and the container-running surface the reachability probe needs.
type fakeListenClient struct {
	fakeNetworkClient
	inspectedNetwork string
	network          *docker.NetworkInspect
	networkErr       error
	daemonID         string
}

func (f *fakeListenClient) InspectNetwork(_ context.Context, nameOrID string) (*docker.NetworkInspect, error) {
	f.inspectedNetwork = nameOrID
	return f.network, f.networkErr
}

func (f *fakeListenClient) Info(context.Context) (*docker.SystemInfo, error) {
	return &docker.SystemInfo{ID: f.daemonID}, nil
}

// The container-runner half is never exercised through this fake — every test
// here injects resolveDeps.probe directly, so the ordering, the cache and the
// failure path are all decided without a daemon. It is implemented so the fake
// satisfies listenClient, which is the interface the real caller passes.
func (f *fakeListenClient) ImageExists(context.Context, string) (bool, error) { return true, nil }
func (f *fakeListenClient) PullImage(context.Context, string) error           { return nil }
func (f *fakeListenClient) CreateContainer(context.Context, string, *docker.CreateContainerRequest) (string, error) {
	return "", errors.New("not used")
}
func (f *fakeListenClient) StartContainer(context.Context, string) error { return nil }
func (f *fakeListenClient) WaitContainer(context.Context, string) (int, error) {
	return 0, nil
}
func (f *fakeListenClient) ContainerLogs(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (f *fakeListenClient) RemoveContainer(context.Context, string, bool) error { return nil }

// networkWithGateway builds an inspect result for a user-defined network whose
// IPAM pool has the given gateway.
func networkWithGateway(name, gateway string) *docker.NetworkInspect {
	return &docker.NetworkInspect{
		Name: name,
		IPAM: docker.NetworkIPAM{Config: []docker.NetworkIPAMConfig{{
			Subnet:  "172.19.0.0/16",
			Gateway: gateway,
		}}},
	}
}

// The environment probes resolveListen takes are shared with published_test.go:
// onHost and inContainer are declared there.

// bindableExcept accepts every address but the listed ones — the shape of a
// Docker Desktop host, where the daemon's gateway lives in a Linux VM and no
// listener on this kernel can hold it.
func bindableExcept(unbindable ...string) func(string) bool {
	return func(host string) bool { return !slices.Contains(unbindable, host) }
}

func bindableNone(string) bool { return false }

// reachableFrom builds a probe that reports the named candidates reachable and
// everything else refused, recording the order it was asked in. That order is
// the assertion most of these tests actually make: a candidate reached is a
// candidate no later one was tried for.
//
// A candidate is named by its host, or by "mode/host" where two candidates
// share a host — the Docker Desktop alias is advertised by both the
// docker-internal row and the wildcard fallback, and they are different
// answers.
type fakeProbe struct {
	reachable []string
	asked     []string
	err       string
}

func reachableFrom(hosts ...string) *fakeProbe {
	return &fakeProbe{reachable: hosts, err: "wget: can't connect to remote host: Connection refused"}
}

func (f *fakeProbe) fn(_ context.Context, c Candidate) Attempt {
	f.asked = append(f.asked, c.Mode+"/"+c.ContainerHost)
	if slices.Contains(f.reachable, c.ContainerHost) || slices.Contains(f.reachable, c.Mode+"/"+c.ContainerHost) {
		return Attempt{Mode: c.Mode, Host: c.ContainerHost, Reachable: true}
	}
	return Attempt{Mode: c.Mode, Host: c.ContainerHost, Error: f.err}
}

// deps assembles resolveDeps for a test, defaulting the parts a case does not
// care about.
func deps(inContainer func() bool, hostIP string, bindable func(string) bool, probe *fakeProbe) resolveDeps {
	d := resolveDeps{
		inContainer: inContainer,
		hostIP:      func() string { return hostIP },
		bindable:    bindable,
		daemonID:    func(context.Context) string { return "daemon-1" },
		now:         func() time.Time { return time.Unix(1700000000, 0) },
	}
	if probe != nil {
		d.probe = probe.fn
	}
	return d
}

func TestResolveListen_prefersTheNetworkGatewayOverEverythingElse(t *testing.T) {
	// Given: a native Linux daemon — the plane's gateway is an address this
	// kernel can bind — and a container that can reach it.
	dc := &fakeListenClient{network: networkWithGateway("overcast_control", "172.19.0.1")}
	probe := reachableFrom("172.19.0.1")

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"},
		deps(onHost, "192.168.1.20", bindableExcept(), probe))

	// Then: containers are pointed at the gateway and it is bound with loopback
	// — the machine's LAN address is left alone, which is the whole point: the
	// gateway is reachable from this machine's containers and from nowhere else.
	if got.ContainerHost != "172.19.0.1" {
		t.Errorf("ContainerHost = %q, want %q", got.ContainerHost, "172.19.0.1")
	}
	if !slices.Equal(got.BindHosts, []string{"172.19.0.1", "127.0.0.1"}) {
		t.Errorf("BindHosts = %v, want [172.19.0.1 127.0.0.1]", got.BindHosts)
	}
	if !got.Verified {
		t.Error("Verified = false, want true — a container reached it")
	}
	// And nothing after it was tried: the first candidate a container reaches
	// ends the search, so a native Linux host pays exactly one probe.
	if len(probe.asked) != 1 {
		t.Errorf("candidates tried = %v, want only the gateway", probe.asked)
	}
}

func TestResolveListen_prefersDockerInternalOverTheHostAddress(t *testing.T) {
	// Given: the reported bug (#1572). A Windows host with Docker Desktop: the
	// daemon's gateway is not bindable here, the host's own routable address
	// binds fine — and containers cannot reach it, because the firewall's
	// inbound default blocks this binary. host.docker.internal works.
	dc := &fakeListenClient{network: networkWithGateway("overcast_control", "172.19.0.1")}
	probe := reachableFrom(dockerInternalHost)

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"},
		deps(onHost, "192.168.8.19", bindableExcept("172.19.0.1"), probe))

	// Then: the alias is what containers dial. Before this change the host
	// address won on the strength of being bindable, and every invocation on
	// such a host died at INIT with exit 139.
	if got.ContainerHost != dockerInternalHost {
		t.Errorf("ContainerHost = %q, want %q", got.ContainerHost, dockerInternalHost)
	}
	if got.Mode != modeDockerInternal {
		t.Errorf("Mode = %q, want %q", got.Mode, modeDockerInternal)
	}
	// And it is still a narrowing, not the wildcard under another name: the
	// bind set is loopback (where Docker Desktop's VM route lands) plus the
	// host address, and nothing else.
	if slices.Contains(got.BindHosts, wildcardHost) {
		t.Errorf("BindHosts = %v, must not contain %q", got.BindHosts, wildcardHost)
	}
	if !slices.Contains(got.BindHosts, loopbackHost) {
		t.Errorf("BindHosts = %v, want %q in it", got.BindHosts, loopbackHost)
	}
	// And the host address was never even asked about, because the ordering
	// puts the alias first — that ordering is the fix.
	if slices.Contains(probe.asked, modeHost+"/192.168.8.19") {
		t.Errorf("candidates tried = %v, want the host address not reached", probe.asked)
	}
}

func TestResolveListen_fallsThroughToTheHostAddressWhenTheAliasIsUnreachable(t *testing.T) {
	// Given: a native Linux host with no usable plane gateway and no
	// host-gateway route for the alias, but a routable interface a container
	// can reach — the case host mode was always right for.
	dc := &fakeListenClient{networkErr: errors.New("no such network")}
	probe := reachableFrom("192.168.1.20")

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"},
		deps(onHost, "192.168.1.20", bindableExcept(), probe))

	// Then: host mode still wins — its precedence changed, not its validity.
	if got.ContainerHost != "192.168.1.20" || got.Mode != modeHost {
		t.Errorf("ContainerHost/Mode = %q/%q, want 192.168.1.20/%s", got.ContainerHost, got.Mode, modeHost)
	}
	if !slices.Equal(got.BindHosts, []string{"192.168.1.20", "127.0.0.1"}) {
		t.Errorf("BindHosts = %v, want [192.168.1.20 127.0.0.1]", got.BindHosts)
	}
	if got.Wildcard {
		t.Error("Wildcard = true, want false — a host address was resolved")
	}
}

func TestResolveListen_usesOurOwnAddressOnTheNetworkWhenContainerised(t *testing.T) {
	// Given: Overcast is itself a container on the plane. Its address there is
	// directly routable from siblings, and the gateway is nobody's answer.
	dc := &fakeListenClient{
		fakeNetworkClient: fakeNetworkClient{inspect: inspectOnNetwork("overcast_control", "172.19.0.5")},
		network:           networkWithGateway("overcast_control", "172.19.0.1"),
	}
	probe := reachableFrom("172.19.0.5")

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"},
		deps(inContainer, "192.168.1.20", bindableExcept(), probe))

	// Then: our address on the network is what containers dial and what is
	// bound, and it was the first thing tried.
	if got.ContainerHost != "172.19.0.5" {
		t.Errorf("ContainerHost = %q, want %q", got.ContainerHost, "172.19.0.5")
	}
	if !slices.Equal(got.BindHosts, []string{"172.19.0.5", "127.0.0.1"}) {
		t.Errorf("BindHosts = %v, want [172.19.0.5 127.0.0.1]", got.BindHosts)
	}
	if dc.connectedTo != "overcast_control" {
		t.Errorf("connected to %q, want %q", dc.connectedTo, "overcast_control")
	}
}

func TestResolveListen_candidateOrderIsTheDocumentedOne(t *testing.T) {
	// Given: a host where every candidate can be built and none is reachable,
	// so the whole list is walked.
	dc := &fakeListenClient{network: networkWithGateway("overcast_control", "172.19.0.1")}
	probe := reachableFrom()

	// When: the listen set is resolved.
	resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"},
		deps(onHost, "192.168.1.20", bindableExcept(), probe))

	// Then: the order is gateway, then the Docker Desktop alias, then the host
	// address, then the wildcard. It is asserted whole rather than pairwise
	// because the ordering *is* the fix — a later reshuffle that happens to
	// keep one pair in place would still reintroduce #1572.
	want := []string{
		modeGateway + "/172.19.0.1",
		modeDockerInternal + "/" + dockerInternalHost,
		modeHost + "/192.168.1.20",
		modeWildcard + "/" + dockerInternalHost,
	}
	if !slices.Equal(probe.asked, want) {
		t.Errorf("candidates tried:\n got %v\nwant %v", probe.asked, want)
	}
}

func TestResolveListen_saysSoWhenNothingIsReachable(t *testing.T) {
	// Given: a host where no candidate answers — the firewall blocks the binary
	// and the daemon has no route back either.
	dc := &fakeListenClient{network: networkWithGateway("overcast_control", "172.19.0.1")}
	probe := reachableFrom()

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"},
		deps(onHost, "192.168.1.20", bindableExcept(), probe))

	// Then: it is reported unreachable rather than presented as a working
	// configuration. This is the whole of #1572's second half: the address was
	// advertised, the log said "resolved container-reachable listen address",
	// and the only signal anyone got was exit 139.
	if !got.Unreachable {
		t.Error("Unreachable = false, want true")
	}
	if got.Verified {
		t.Error("Verified = true, want false")
	}
	// And every candidate is carried out, in order, with its reason — that list
	// is what the health advisory and the INIT-death diagnostic quote.
	if len(got.Attempts) != 4 {
		t.Fatalf("Attempts = %v, want one per candidate", AttemptStrings(got.Attempts))
	}
	for _, a := range got.Attempts {
		if a.Error == "" {
			t.Errorf("attempt %q carries no reason", a.Mode)
		}
	}
	// And something is still advertised: binding nothing would take Lambda from
	// "broken with an explanation" to "absent".
	if got.ContainerHost == "" || len(got.BindHosts) == 0 {
		t.Errorf("nothing advertised: host=%q binds=%v", got.ContainerHost, got.BindHosts)
	}
}

func TestResolveListen_detailNamesEveryCandidateAndTheFix(t *testing.T) {
	// Given: the attempts from a total failure, one refused and one timed out.
	attempts := []Attempt{
		{Mode: modeGateway, Host: "172.19.0.1", Error: "wget: can't connect to remote host: Connection refused"},
		{Mode: modeHost, Host: "192.168.8.19", Error: "wget: download timed out"},
	}

	// When: the advisory detail is composed.
	got := RuntimeAPIUnreachableDetail(attempts)

	// Then: every address tried is in it, with its own observed error, and so
	// is the fix. A user reading this in /_overcast/health has to be able to
	// act without reading any code.
	for _, want := range []string{"172.19.0.1", "192.168.8.19", "Connection refused", "timed out",
		"LAMBDA_RUNTIME_API_HOST=host.docker.internal", "firewall"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail does not mention %q:\n%s", want, got)
		}
	}
}

func TestResolveListen_remembersAVerifiedAnswerAndSkipsTheProbeNextTime(t *testing.T) {
	// Given: a state directory to remember it in, and a first run that probes.
	path := filepath.Join(t.TempDir(), hintFileName("overcast_control"))
	dc := &fakeListenClient{network: networkWithGateway("overcast_control", "172.19.0.1")}
	first := reachableFrom(dockerInternalHost)

	got := resolveListen(context.Background(), dc,
		ListenOptions{Network: "overcast_control", HintPath: path},
		deps(onHost, "192.168.8.19", bindableExcept("172.19.0.1"), first))
	if got.ContainerHost != dockerInternalHost {
		t.Fatalf("first run ContainerHost = %q, want %q", got.ContainerHost, dockerInternalHost)
	}

	// When: a second run starts against the same daemon and the same plane.
	second := reachableFrom()
	again := resolveListen(context.Background(), dc,
		ListenOptions{Network: "overcast_control", HintPath: path},
		deps(onHost, "192.168.8.19", bindableExcept("172.19.0.1"), second))

	// Then: the remembered answer is used and no container is started. Probing
	// costs a container start; paying it on every restart of a daemon whose
	// answer has not changed is a cost with nothing on the other side.
	if again.ContainerHost != dockerInternalHost {
		t.Errorf("second run ContainerHost = %q, want %q", again.ContainerHost, dockerInternalHost)
	}
	if len(second.asked) != 0 {
		t.Errorf("second run probed %v, want nothing", second.asked)
	}
	if !slices.Equal(again.BindHosts, got.BindHosts) {
		t.Errorf("second run BindHosts = %v, want the remembered %v", again.BindHosts, got.BindHosts)
	}
	if !again.Verified {
		t.Error("second run Verified = false, want true — the answer was established, just not again")
	}
}

func TestResolveListen_ignoresAHintFromAnotherDaemonOrPlane(t *testing.T) {
	// Given: a hint written against one daemon and one plane.
	path := filepath.Join(t.TempDir(), hintFileName("overcast_control"))
	writeHint(path, "daemon-1", "overcast_control",
		Candidate{Mode: modeHost, ContainerHost: "10.0.0.9", BindHosts: []string{"10.0.0.9"}}, time.Now(), zap.NewNop())

	cases := map[string]struct {
		daemon  string
		network string
	}{
		// The same binary pointed at a different daemon — Docker Desktop after
		// a native one, a DinD sidecar, a tcp:// endpoint — is a different
		// question, and the old answer is not evidence about it.
		"another daemon": {daemon: "daemon-2", network: "overcast_control"},
		// And a different control plane has a different gateway and a different
		// isolation, so the same reasoning applies.
		"another plane": {daemon: "daemon-1", network: "other_control"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dc := &fakeListenClient{networkErr: errors.New("no such network"), daemonID: tc.daemon}
			probe := reachableFrom("192.168.1.20")
			d := deps(onHost, "192.168.1.20", bindableExcept(), probe)
			d.daemonID = func(context.Context) string { return tc.daemon }

			// When/Then: the hint is not taken, and the probe runs.
			got := resolveListen(context.Background(), dc,
				ListenOptions{Network: tc.network, HintPath: path}, d)
			if got.ContainerHost == "10.0.0.9" {
				t.Error("the stale hint was used")
			}
			if len(probe.asked) == 0 {
				t.Error("no candidate was probed")
			}
		})
	}
}

func TestResolveListen_forgetsTheRememberedAnswerWhenNothingIsReachable(t *testing.T) {
	// Given: a remembered answer, and a run in which nothing is reachable —
	// the firewall posture changed under it.
	path := filepath.Join(t.TempDir(), hintFileName("overcast_control"))
	writeHint(path, "daemon-9", "overcast_control",
		Candidate{Mode: modeHost, ContainerHost: "10.0.0.9", BindHosts: []string{"10.0.0.9"}}, time.Now(), zap.NewNop())
	dc := &fakeListenClient{networkErr: errors.New("no such network")}
	d := deps(onHost, "192.168.1.20", bindableExcept(), reachableFrom())
	d.daemonID = func(context.Context) string { return "daemon-1" }

	// When: the listen set is resolved.
	resolveListen(context.Background(), dc,
		ListenOptions{Network: "overcast_control", HintPath: path}, d)

	// Then: the file is gone, so the next startup probes rather than inheriting
	// a verdict this run disproved.
	if readHint(path, "daemon-9", "overcast_control") != nil {
		t.Error("the remembered answer survived a run that found nothing reachable")
	}
}

func TestResolveListen_takesAPinnedAddressAsGiven(t *testing.T) {
	// Given: LAMBDA_RUNTIME_API_HOST set, on a host where the probe would have
	// chosen otherwise.
	dc := &fakeListenClient{network: networkWithGateway("overcast_control", "172.19.0.1")}
	probe := reachableFrom("172.19.0.1")

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc,
		ListenOptions{Network: "overcast_control", PinnedHost: dockerInternalHost},
		deps(onHost, "192.168.1.20", bindableExcept(), probe))

	// Then: the pin wins and nothing is probed. Somebody who sets this has a
	// reason the probe cannot see — an air-gapped host, a daemon that refuses
	// container creates — and second-guessing it would make the escape hatch
	// useless on the host that needs it most.
	if got.ContainerHost != dockerInternalHost || got.Mode != modePinned {
		t.Errorf("ContainerHost/Mode = %q/%q, want %s/%s", got.ContainerHost, got.Mode, dockerInternalHost, modePinned)
	}
	if len(probe.asked) != 0 {
		t.Errorf("probed %v, want nothing", probe.asked)
	}
	// And the derived bind set still covers the paths that address arrives on.
	for _, want := range []string{loopbackHost, "172.19.0.1", "192.168.1.20"} {
		if !slices.Contains(got.BindHosts, want) {
			t.Errorf("BindHosts = %v, want %q in it", got.BindHosts, want)
		}
	}
}

func TestResolveListen_withoutADaemonKeepsTheOrderingAndClaimsNothing(t *testing.T) {
	// Given: no way to run the probe at all.
	dc := &fakeListenClient{network: networkWithGateway("overcast_control", "172.19.0.1")}

	// When: the listen set is resolved with no probe.
	got := resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"},
		deps(onHost, "192.168.1.20", bindableExcept(), nil))

	// Then: the first candidate stands — the pre-#1572 answer, which is right
	// on every host where the ordering was already right — but nothing claims
	// it was established, and nothing is reported as broken either.
	if got.ContainerHost != "172.19.0.1" {
		t.Errorf("ContainerHost = %q, want the gateway", got.ContainerHost)
	}
	if got.Verified {
		t.Error("Verified = true, want false — nothing was measured")
	}
	if got.Unreachable {
		t.Error("Unreachable = true, want false — an unmeasured address is not an unreachable one")
	}
}

func TestResolveListen_bindsEveryInterfaceRatherThanStrandContainers(t *testing.T) {
	// Given: a host where nothing resolves — the network cannot be inspected
	// and no interface address is usable — and only the wildcard answers.
	dc := &fakeListenClient{networkErr: errors.New("no such network")}
	probe := reachableFrom(modeWildcard + "/" + dockerInternalHost)

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"},
		deps(onHost, "", bindableNone, probe))

	// Then: the wildcard, flagged as such. It is the only candidate that covers
	// an arrival path this process could not enumerate.
	if !got.Wildcard {
		t.Errorf("Wildcard = false with BindHosts %v, want the fallback", got.BindHosts)
	}
	if !slices.Equal(got.BindHosts, []string{"0.0.0.0"}) {
		t.Errorf("BindHosts = %v, want [0.0.0.0]", got.BindHosts)
	}
	if got.ContainerHost != dockerInternalHost {
		t.Errorf("ContainerHost = %q, want %q", got.ContainerHost, dockerInternalHost)
	}
}

func TestResolveListen_neverBindsTheWildcardAlongsideAResolvedAddress(t *testing.T) {
	// Given: each mode that resolves a specific address.
	cases := map[string]struct {
		dc            *fakeListenClient
		inContainer   func() bool
		hostIP        string
		wantContainer string
	}{
		"gateway": {
			dc:            &fakeListenClient{network: networkWithGateway("overcast_control", "172.19.0.1")},
			inContainer:   onHost,
			hostIP:        "192.168.1.20",
			wantContainer: "172.19.0.1",
		},
		"host": {
			dc:            &fakeListenClient{networkErr: errors.New("no such network")},
			inContainer:   onHost,
			hostIP:        "192.168.1.20",
			wantContainer: "192.168.1.20",
		},
		"container": {
			dc: &fakeListenClient{
				fakeNetworkClient: fakeNetworkClient{inspect: inspectOnNetwork("overcast_control", "172.19.0.5")},
			},
			inContainer:   inContainer,
			hostIP:        "192.168.1.20",
			wantContainer: "172.19.0.5",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// When: the listen set is resolved and that address is the reachable one.
			got := resolveListen(context.Background(), tc.dc, ListenOptions{Network: "overcast_control"},
				deps(tc.inContainer, tc.hostIP, bindableExcept(), reachableFrom(tc.wantContainer)))

			// Then: the wildcard is absent. Binding it next to a specific
			// address is not a belt-and-braces measure, it is the exposure the
			// specific address was chosen to avoid.
			if slices.Contains(got.BindHosts, wildcardHost) {
				t.Errorf("BindHosts = %v, must not contain %q", got.BindHosts, wildcardHost)
			}
			if got.ContainerHost != tc.wantContainer {
				t.Errorf("ContainerHost = %q, want %q", got.ContainerHost, tc.wantContainer)
			}
			// And loopback is always there: it costs nothing, and it is what a
			// developer or a test on this machine reaches for.
			if !slices.Contains(got.BindHosts, loopbackHost) {
				t.Errorf("BindHosts = %v, want %q in it", got.BindHosts, loopbackHost)
			}
		})
	}
}

func TestResolveListen_ignoresAContainerModeAddressItCannotBind(t *testing.T) {
	// Given: Overcast reports an address on the network that this kernel will
	// not accept a listener on — a stale inspect result, or an address that
	// belongs to a namespace we are not in.
	dc := &fakeListenClient{
		fakeNetworkClient: fakeNetworkClient{inspect: inspectOnNetwork("overcast_control", "172.19.0.5")},
	}
	probe := reachableFrom("172.19.0.5")

	// When: the listen set is resolved.
	resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"},
		deps(inContainer, "192.168.1.20", bindableExcept("172.19.0.5"), probe))

	// Then: it is never even offered as a candidate. Telling a container to
	// dial an address nothing here can listen on hangs it until the invocation
	// times out, which reads as a broken function rather than a network
	// mistake — bindability is still the right filter for *proposing*, it is
	// only the wrong one for deciding.
	if slices.Contains(probe.asked, modeContainer+"/172.19.0.5") {
		t.Errorf("candidates tried = %v, want the unbindable address absent", probe.asked)
	}
}

func TestNetworkGateway_declinesGatewaysNoContainerCouldDial(t *testing.T) {
	cases := map[string]*docker.NetworkInspect{
		// The "host" and "none" networks, and any network created without an
		// IPAM pool: no gateway is not an error, it means the mode is n/a.
		"no IPAM pool": {Name: "host"},
		"empty gateway": {Name: "overcast_control", IPAM: docker.NetworkIPAM{
			Config: []docker.NetworkIPAMConfig{{Subnet: "172.19.0.0/16"}}}},
		"unparseable":  networkWithGateway("overcast_control", "not-an-ip"),
		"loopback":     networkWithGateway("overcast_control", "127.0.0.1"),
		"unspecified":  networkWithGateway("overcast_control", "0.0.0.0"),
		"link-local":   networkWithGateway("overcast_control", "169.254.1.1"),
		"IPv6 gateway": networkWithGateway("overcast_control", "fd00::1"),
	}

	for name, inspect := range cases {
		t.Run(name, func(t *testing.T) {
			// Given: a network whose gateway is unusable.
			dc := &fakeListenClient{network: inspect}

			// When/Then: no gateway is offered, so resolution moves on rather
			// than binding something arbitrary.
			if got := networkGateway(context.Background(), dc, "overcast_control"); got != "" {
				t.Errorf("networkGateway() = %q, want \"\"", got)
			}
		})
	}
}

func TestNetworkGateway_withoutADaemonOrANetwork(t *testing.T) {
	// Given: no Docker client, or no configured network — a service that
	// manages its own networks passes an empty name.
	// When/Then: neither panics, and neither invents a gateway.
	if got := networkGateway(context.Background(), nil, "overcast_control"); got != "" {
		t.Errorf("networkGateway(nil client) = %q, want \"\"", got)
	}
	if got := networkGateway(context.Background(), &fakeListenClient{}, ""); got != "" {
		t.Errorf("networkGateway(no network) = %q, want \"\"", got)
	}
}

func TestNativeLinuxDaemon_trueWhenTheDefaultBridgeGatewayIsOurs(t *testing.T) {
	// Given: a native Linux daemon — its "bridge" network's gateway is an
	// address this kernel can bind.
	dc := &fakeListenClient{network: networkWithGateway("bridge", "172.17.0.1")}

	// When/Then: the daemon is reported native. Bindability is the right
	// question here and only here: what it decides is whether an `--internal`
	// control plane would still have an on-link address for containers to dial,
	// which is a property of the bridge rather than of any path to this process.
	if !nativeLinuxDaemon(context.Background(), dc, bindableExcept()) {
		t.Error("nativeLinuxDaemon() = false, want true")
	}
	if dc.inspectedNetwork != "bridge" {
		t.Errorf("inspected network %q, want %q", dc.inspectedNetwork, "bridge")
	}
}

func TestNativeLinuxDaemon_falseOnDockerDesktop(t *testing.T) {
	// Given: Docker Desktop — "bridge" reports a gateway, but it belongs to the
	// daemon's VM and cannot be bound here.
	dc := &fakeListenClient{network: networkWithGateway("bridge", "172.17.0.1")}

	// When/Then: the daemon is not reported native.
	if nativeLinuxDaemon(context.Background(), dc, bindableExcept("172.17.0.1")) {
		t.Error("nativeLinuxDaemon() = true, want false")
	}
}

func TestNativeLinuxDaemon_falseWhenTheDefaultBridgeCannotBeInspected(t *testing.T) {
	// Given: no default network to ask — a minimal or rootless daemon, or no
	// client at all. The fact cannot be established, so the safe answer is no
	// rather than a guess: getting this wrong strands every Lambda invocation.
	dc := &fakeListenClient{networkErr: errors.New("no such network")}

	// When/Then: undetermined reads as false.
	if nativeLinuxDaemon(context.Background(), dc, bindableExcept()) {
		t.Error("nativeLinuxDaemon() = true, want false")
	}
	if nativeLinuxDaemon(context.Background(), nil, bindableExcept()) {
		t.Error("nativeLinuxDaemon(nil client) = true, want false")
	}
}

func TestBindableHost_answersFromAnActualBind(t *testing.T) {
	// Given: loopback, which every platform this runs on can bind.
	// When/Then: the probe says so.
	if !bindableHost("127.0.0.1") {
		t.Error("bindableHost(127.0.0.1) = false, want true")
	}

	// And: TEST-NET-1 (RFC 5737), which is not an address of this machine.
	// The probe is what keeps a Docker Desktop gateway — reported by the
	// daemon, unbindable here — out of the candidate list.
	if bindableHost("192.0.2.1") {
		t.Error("bindableHost(192.0.2.1) = true, want false")
	}
}

// unavailableProbe reports that the probe could not be run at all — an
// air-gapped host with no cached busybox, or a daemon refusing creates.
type unavailableProbe struct{ asked []string }

func (u *unavailableProbe) fn(_ context.Context, c Candidate) Attempt {
	u.asked = append(u.asked, c.Mode+"/"+c.ContainerHost)
	return Attempt{
		Mode: c.Mode, Host: c.ContainerHost,
		Unavailable: true,
		Error:       "probe could not run: pull busybox:1.36: no such host",
	}
}

func TestResolveListen_aProbeThatCouldNotRunIsNotAnUnreachableAddress(t *testing.T) {
	// Given: a host where every candidate comes back "could not ask" rather
	// than "asked and got nothing".
	dc := &fakeListenClient{network: networkWithGateway("overcast_control", "172.19.0.1")}
	probe := &unavailableProbe{}
	d := deps(onHost, "192.168.1.20", bindableExcept(), nil)
	d.probe = probe.fn

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"}, d)

	// Then: nothing is reported broken. Calling this unreachable would fire the
	// only critical advisory Overcast has, degrade health, append a firewall
	// paragraph to every InitError, and silently widen the bind set to every
	// interface — on a machine whose Lambda may work perfectly.
	if got.Unreachable {
		t.Error("Unreachable = true, want false — nothing was measured")
	}
	if got.Verified {
		t.Error("Verified = true, want false — nothing was measured")
	}
	// And the ordering stands on its own, exactly as it does with no daemon:
	// the best candidate, not the wildcard the failure path advertises.
	if got.Mode != modeGateway || got.ContainerHost != "172.19.0.1" {
		t.Errorf("Mode/host = %q/%q, want the ordering to stand", got.Mode, got.ContainerHost)
	}
	if slices.Contains(got.BindHosts, wildcardHost) {
		t.Errorf("BindHosts = %v, must not silently widen to the wildcard", got.BindHosts)
	}
}

func TestResolveListen_aProbeThatCouldNotRunKeepsTheRememberedAnswer(t *testing.T) {
	// Given: an answer measured by an earlier run, and a run today that cannot
	// probe at all — the daemon it would ask is a different one, so the hint
	// does not short-circuit.
	path := filepath.Join(t.TempDir(), hintFileName("overcast_control"))
	writeHint(path, "daemon-earlier", "overcast_control",
		Candidate{Mode: modeHost, ContainerHost: "10.0.0.9", BindHosts: []string{"10.0.0.9"}},
		time.Now(), zap.NewNop())
	dc := &fakeListenClient{networkErr: errors.New("no such network")}
	d := deps(onHost, "192.168.1.20", bindableExcept(), nil)
	d.probe = (&unavailableProbe{}).fn
	d.daemonID = func(context.Context) string { return "daemon-today" }

	// When: the listen set is resolved.
	resolveListen(context.Background(), dc,
		ListenOptions{Network: "overcast_control", HintPath: path}, d)

	// Then: the earlier measurement survives. A run that established nothing
	// has no standing to discard what an earlier run actually measured.
	if readHint(path, "daemon-earlier", "overcast_control") == nil {
		t.Error("the remembered answer was discarded by a run that measured nothing")
	}
}

func TestResolveListen_oneRealMeasurementIsEnoughToCallItUnreachable(t *testing.T) {
	// Given: a host where one candidate was genuinely measured and refused, and
	// the rest could not be asked.
	dc := &fakeListenClient{network: networkWithGateway("overcast_control", "172.19.0.1")}
	d := deps(onHost, "192.168.1.20", bindableExcept(), nil)
	first := true
	d.probe = func(_ context.Context, c Candidate) Attempt {
		if first {
			first = false
			return Attempt{Mode: c.Mode, Host: c.ContainerHost, Error: "wget: download timed out"}
		}
		return Attempt{Mode: c.Mode, Host: c.ContainerHost, Unavailable: true, Error: "probe could not run: refused"}
	}

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"}, d)

	// Then: it is a real verdict. The bar is evidence, not unanimity — a host
	// that dropped the packets on the one address it could be asked about has
	// told us something, and staying quiet about it is the bug this fixes.
	if !got.Unreachable {
		t.Error("Unreachable = false, want true — one candidate was measured and failed")
	}
}

func TestResolveListen_anIncompleteWalkIsNotAVerdict(t *testing.T) {
	// Given: a probe that measures the first candidate and then burns the whole
	// budget, so later candidates never get their turn.
	dc := &fakeListenClient{network: networkWithGateway("overcast_control", "172.19.0.1")}
	d := deps(onHost, "192.168.1.20", bindableExcept(), nil)
	d.budget = 50 * time.Millisecond
	d.probe = func(ctx context.Context, c Candidate) Attempt {
		// Exhaust the caller budget the way a hanging daemon call would.
		if ctx.Err() == nil {
			<-ctx.Done()
		}
		return Attempt{Mode: c.Mode, Host: c.ContainerHost, Error: "wget: download timed out"}
	}

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, ListenOptions{Network: "overcast_control"}, d)

	// Then: no verdict. Candidates left untried may well have answered, and one
	// of them is the row that fixes the platform this whole change is for.
	if got.Unreachable {
		t.Errorf("Unreachable = true after only %d of the candidates were tried, want false",
			len(got.Attempts))
	}
	if len(got.Attempts) == 0 {
		t.Error("no candidate was attempted at all")
	}
}
