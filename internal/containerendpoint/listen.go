package containerendpoint

// listen.go answers resolve.go's question from the other side.
//
// Resolve says which address a container should dial. That is the whole answer
// for a server bound to every interface, which is what Overcast's own API is by
// default — whatever the container reaches us on, something is listening. A
// server that picks its own bind addresses needs the pair: the address
// containers dial *and* the local addresses that has to be listening on for the
// dial to arrive. The Lambda Runtime API is the case in point — nothing off this
// machine has any business reaching it, but Lambda containers connect back to it
// over the control plane, so loopback alone would strand every invocation.
//
// The two are resolved together, in one function, because a bind set derived
// separately from the address containers were handed is a silent misconfiguration
// the first time either changes.
//
// The host-type reasoning below is the *ordering*. What decides is
// reachability: each candidate is bound and a container is asked to connect to
// it (reachability.go). Before #1572 the deciding fact was bindability, which
// is a different property and diverges from this one on every Windows host
// whose firewall has not been told about the binary — see that file's header.

import (
	"context"
	"net"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
)

// loopbackHost is always in a narrowed bind set: it costs nothing, it is
// reachable from nowhere else, and it is what a developer or a test on this
// machine reaches for.
const loopbackHost = "127.0.0.1"

// wildcardHost binds every interface — the pre-narrowing behaviour, kept as the
// last resort for a host whose reachable address could not be established.
const wildcardHost = "0.0.0.0"

// Candidate modes, in the order candidates builds them.
const (
	modeContainer      = "container"
	modeGateway        = "gateway"
	modeDockerInternal = "docker-internal"
	modeHost           = "host"
	modeWildcard       = "wildcard"
	modePinned         = "pinned"
	modeHinted         = "hinted"
)

// Listen is where a container-facing server should listen and what containers
// should be told to dial.
type Listen struct {
	// ContainerHost is the host containers use to reach the server. Never
	// empty: callers put it in an env var, and a wrong-but-plausible address
	// degrades better than an unset one.
	ContainerHost string

	// BindHosts are the local addresses to listen on, most important first —
	// the caller binds BindHosts[0] to settle a port of 0, so ContainerHost
	// leads whenever it is bindable here.
	BindHosts []string

	// Wildcard reports that BindHosts is the every-interface fallback rather
	// than a resolved set, so the caller can say so rather than looking narrow
	// when it is not.
	Wildcard bool

	// Mode names which candidate was taken — see the mode constants.
	Mode string

	// Verified reports that a container actually opened a connection to
	// ContainerHost. False means the answer is the old bindability ordering
	// with nothing measured, which is not the same as a failure: a daemon that
	// cannot run the probe at all (no image, create refused) still gets a
	// working stack on every host where the ordering was already right.
	Verified bool

	// Unreachable is the loud case: the probe ran, every candidate was tried,
	// and no container could open a connection to any of them. Nothing works on
	// this host until something changes, and saying so beats advertising an
	// address that strands every invocation at INIT with exit 139.
	Unreachable bool

	// Attempts is what each candidate did, in the order tried — the evidence
	// the health advisory and the INIT-death diagnostic quote.
	Attempts []Attempt
}

// listenClient is the slice of *docker.Client that ResolveListen needs.
type listenClient interface {
	networkClient
	runnerClient
	InspectNetwork(ctx context.Context, nameOrID string) (*docker.NetworkInspect, error)
	Info(ctx context.Context) (*docker.SystemInfo, error)
}

// ListenOptions are the inputs ResolveListen needs beyond the daemon itself.
type ListenOptions struct {
	// Network is the plane containers are created on — both the network the
	// gateway candidate is read from and the one the probe container joins.
	Network string

	// PinnedHost is LAMBDA_RUNTIME_API_HOST when the operator named an
	// address. It skips both the ordering and the probe: somebody who pins
	// this has a reason the probe cannot see, and second-guessing it would
	// make the escape hatch useless on the host that needs it most.
	PinnedHost string

	// HintPath is where a verified answer is remembered across restarts, from
	// HintPath(cfg.DataDir, Network). Empty disables both reading and writing
	// it — which is what a caller with no data directory at all gets, not what
	// any particular state backend gets: cfg.DataDir is always set, and
	// writeHint creates the directory rather than assuming a backend made it.
	HintPath string

	// Logger, optional.
	Logger *zap.Logger
}

// ResolveListen returns the address containers on the plane dial to reach a
// server on this host, and the local addresses that server must bind for it to
// work — established by having a container connect to each candidate in turn,
// not by asking whether this process can bind it.
func ResolveListen(ctx context.Context, dc listenClient, opts ListenOptions) Listen {
	var probe func(context.Context, Candidate) Attempt
	var info infoClient
	if dc != nil {
		dial := dockerDialer(dc, opts.Network, opts.Logger)
		probe = func(c context.Context, cand Candidate) Attempt { return probeCandidate(c, cand, dial) }
		info = dc
	}
	return resolveListen(ctx, dc, opts, resolveDeps{
		inContainer: runningInContainer,
		hostIP:      hostReachableIP,
		bindable:    bindableHost,
		probe:       probe,
		daemonID:    func(c context.Context) string { return daemonIdentity(c, info) },
		now:         time.Now,
	})
}

// infoClient is the one method daemonIdentity needs.
type infoClient interface {
	Info(ctx context.Context) (*docker.SystemInfo, error)
}

// resolveDeps are the environment facts resolveListen consults, injected so the
// decision is testable without a Docker daemon, a particular host interface
// layout, or the privilege to bind a given address.
type resolveDeps struct {
	inContainer func() bool
	hostIP      func() string
	bindable    func(string) bool
	// probe binds one candidate and asks a container to connect to it,
	// returning what happened. Nil when there is no daemon to run it on.
	probe    func(context.Context, Candidate) Attempt
	daemonID func(context.Context) string
	now      func() time.Time
	// budget caps the whole candidate walk. Zero means probeTotalBudget; a
	// test sets it short so exercising the exhaustion path costs milliseconds
	// rather than the real budget.
	budget time.Duration
}

// resolveListen is ResolveListen with its environment probes injected.
func resolveListen(ctx context.Context, dc listenClient, opts ListenOptions, deps resolveDeps) Listen {
	logger := opts.Logger

	// A pinned address is taken as given — see ListenOptions.PinnedHost.
	if opts.PinnedHost != "" {
		c := pinnedCandidate(ctx, dc, opts, deps)
		if logger != nil {
			logger.Info("Runtime API address pinned by configuration",
				zap.String("host", c.ContainerHost),
				zap.Strings("bind", c.BindHosts))
		}
		return listenOf(c, false, nil)
	}

	list := candidates(ctx, dc, opts.Network, deps)

	// With no way to run the probe there is nothing to decide by, so the
	// ordering stands on its own — exactly the pre-#1572 behaviour, but said
	// honestly rather than logged as an established fact.
	if deps.probe == nil {
		c := list[0]
		if logger != nil {
			logger.Warn("Runtime API address chosen without a reachability check — no Docker client to run one",
				zap.String("mode", c.Mode), zap.String("host", c.ContainerHost))
		}
		return listenOf(c, false, nil)
	}

	daemon := ""
	if deps.daemonID != nil {
		daemon = deps.daemonID(ctx)
	}
	if c := readHint(opts.HintPath, daemon, opts.Network); c != nil {
		if logger != nil {
			logger.Info("Runtime API address taken from the remembered probe result",
				zap.String("mode", c.Mode),
				zap.String("host", c.ContainerHost),
				zap.String("daemon", daemon))
		}
		out := listenOf(*c, true, nil)
		out.Mode = modeHinted + ":" + c.Mode
		return out
	}

	// One clock over the whole walk — see probeTotalBudget.
	budget := deps.budget
	if budget <= 0 {
		budget = probeTotalBudget
	}
	probeCtx, cancelProbe := context.WithTimeout(ctx, budget)
	defer cancelProbe()

	attempts := make([]Attempt, 0, len(list))
	for i, c := range list {
		if i > 0 && probeCtx.Err() != nil {
			if logger != nil {
				logger.Warn("stopped probing Runtime API candidates — the budget ran out",
					zap.Int("tried", len(attempts)),
					zap.Int("candidates", len(list)),
					zap.Duration("budget", budget))
			}
			break
		}
		a := deps.probe(probeCtx, c)
		attempts = append(attempts, a)
		if !a.Reachable {
			if logger != nil {
				logger.Debug("Runtime API candidate not reachable from a container",
					zap.String("mode", c.Mode), zap.String("host", c.ContainerHost),
					zap.String("error", a.Error))
			}
			continue
		}
		if logger != nil {
			logger.Info("resolved container-reachable listen address",
				zap.String("mode", c.Mode),
				zap.String("network", opts.Network),
				zap.String("host", c.ContainerHost),
				zap.Strings("bind", c.BindHosts),
				zap.Int("candidates_tried", len(attempts)))
		}
		writeHint(opts.HintPath, daemon, opts.Network, c, deps.now(), logger)
		return listenOf(c, true, attempts)
	}

	// Nothing answered. Before calling that a fault, check that anything was
	// actually *asked*: a host with no cached busybox and no registry, or a
	// daemon refusing creates, returns "unavailable" for every candidate, and
	// that is a missing measurement rather than a broken host. Reporting it as
	// the latter fires the loudest advisory Overcast has, degrades health,
	// appends a firewall paragraph to every InitError, and — because the last
	// candidate is the wildcard — silently widens the bind set, all on a
	// machine whose Lambda may work perfectly.
	measured := false
	for _, a := range attempts {
		if !a.Unavailable {
			measured = true
			break
		}
	}
	// An incomplete walk is not a verdict either: the budget ran out before
	// every candidate had its turn, so one of the ones left untried may well
	// have answered.
	complete := len(attempts) == len(list)
	if !measured || !complete {
		c := list[0]
		if logger != nil {
			reason := "the probe could not run"
			if !complete {
				reason = "the probe budget ran out before every candidate was tried"
			}
			logger.Warn("Runtime API address chosen without a reachability check — "+reason,
				zap.String("mode", c.Mode),
				zap.String("host", c.ContainerHost),
				zap.Strings("tried", AttemptStrings(attempts)))
		}
		// Same answer as the no-daemon path: the ordering stands on its own,
		// nothing claims it was established, and nothing is reported broken.
		// The hint is left alone — a run that established nothing has no
		// standing to discard what an earlier run measured.
		return listenOf(c, false, attempts)
	}

	// Every candidate was tried, at least one for real, and none answered. The
	// last one is still what gets advertised — binding nothing would take
	// Lambda from "broken with an explanation" to "absent" — but Unreachable
	// says the address is not believed, and every surface that reports it says
	// so too.
	last := list[len(list)-1]
	out := listenOf(last, false, attempts)
	out.Unreachable = true
	if logger != nil {
		logger.Error("no Runtime API address is reachable from a container — every Lambda invocation will fail during INIT",
			zap.Strings("tried", AttemptStrings(attempts)),
			zap.String("advertising", last.ContainerHost),
			zap.String("fix", RuntimeAPIUnreachableFix))
	}
	// A stale hint cannot be the reason (it short-circuits above), but a hint
	// written by an earlier boot on a host whose firewall posture has since
	// changed can be: drop it so the next startup re-probes rather than
	// inheriting an answer this run just disproved.
	ForgetHint(opts.HintPath)
	return out
}

// listenOf turns the chosen candidate into the answer callers use.
func listenOf(c Candidate, verified bool, attempts []Attempt) Listen {
	return Listen{
		ContainerHost: c.ContainerHost,
		BindHosts:     c.BindHosts,
		Wildcard:      len(c.BindHosts) == 1 && c.BindHosts[0] == wildcardHost,
		Mode:          c.Mode,
		Verified:      verified,
		Attempts:      attempts,
	}
}

// candidates returns the addresses to try, best first. Never empty: the last
// entry is the every-interface fallback, which is the only answer that cannot
// be ruled out in advance.
//
// The order is the host-type reasoning this file has always carried, with one
// row inserted — and that row is the fix:
//
//  1. **container** — Overcast's own address on the plane, when Overcast is
//     itself containerised. Directly routable from siblings on it, and it is
//     what Resolve hands them.
//  2. **gateway** — the plane's own gateway. This host's address on that
//     bridge, reachable from this machine's containers and from nowhere else,
//     and on-link for every container attached, so it keeps working when a
//     function joins a VPC network that takes over the default route. A native
//     Linux daemon lands here and never pays for the rows below.
//  3. **docker-internal** — `host.docker.internal`, bound on loopback and on
//     whatever else this kernel can hold. Docker Desktop backs the name with a
//     VM-side route that arrives on the host's loopback, which is the path a
//     Windows Firewall inbound rule does not filter — so this is the row that
//     works on the host where row 4 binds fine and reaches nothing (#1572).
//  4. **host** — the host's own routable interface address. Still here, and
//     still correct on plenty of hosts; it is only its *precedence* over row 3
//     that was wrong, because bindability made it look established.
//  5. **wildcard** — every interface, advertising `host.docker.internal`. The
//     pre-narrowing behaviour, kept because it is the only candidate that
//     covers an arrival path this process could not enumerate.
func candidates(ctx context.Context, dc listenClient, network string, deps resolveDeps) []Candidate {
	var list []Candidate
	containerised := deps.inContainer()

	if containerised && dc != nil && network != "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			nctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			ip := networkIP(nctx, dc, network, hostname)
			cancel()
			if ip != "" && deps.bindable(ip) {
				list = append(list, Candidate{
					Mode: modeContainer, ContainerHost: ip,
					BindHosts: []string{ip, loopbackHost},
				})
			}
		}
	}

	gateway := ""
	hostIP := ""
	if !containerised {
		if g := networkGateway(ctx, dc, network); g != "" && deps.bindable(g) {
			gateway = g
			list = append(list, Candidate{
				Mode: modeGateway, ContainerHost: g,
				BindHosts: []string{g, loopbackHost},
			})
		}
		if ip := deps.hostIP(); ip != "" && deps.bindable(ip) {
			hostIP = ip
		}
	}

	// The alias, bound on every local address it could plausibly arrive on:
	// loopback (Docker Desktop's VM route lands there), the bridge gateway
	// (what "host-gateway" resolves to on native Linux), and the host's own
	// interface. Narrow rather than wildcard, so this candidate is a real
	// narrowing and not the fallback under another name.
	internalBinds := []string{loopbackHost}
	if gateway != "" {
		internalBinds = append(internalBinds, gateway)
	}
	if hostIP != "" {
		internalBinds = append(internalBinds, hostIP)
	}
	list = append(list, Candidate{
		Mode: modeDockerInternal, ContainerHost: dockerInternalHost, BindHosts: internalBinds,
	})

	if hostIP != "" {
		list = append(list, Candidate{
			Mode: modeHost, ContainerHost: hostIP,
			BindHosts: []string{hostIP, loopbackHost},
		})
	}

	list = append(list, Candidate{
		Mode: modeWildcard, ContainerHost: dockerInternalHost, BindHosts: []string{wildcardHost},
	})
	return list
}

// pinnedCandidate builds the answer for LAMBDA_RUNTIME_API_HOST=<address>.
//
// The address itself is bound when this kernel can hold it — an IP the operator
// named is usually one of ours — and loopback always. A name (they may well
// pin `host.docker.internal`) is not bindable, so the derived set stands in,
// exactly as the docker-internal candidate's does.
func pinnedCandidate(ctx context.Context, dc listenClient, opts ListenOptions, deps resolveDeps) Candidate {
	binds := []string{}
	if deps.bindable(opts.PinnedHost) {
		binds = append(binds, opts.PinnedHost)
	}
	binds = append(binds, loopbackHost)
	if !deps.inContainer() {
		if g := networkGateway(ctx, dc, opts.Network); g != "" && g != opts.PinnedHost && deps.bindable(g) {
			binds = append(binds, g)
		}
		if ip := deps.hostIP(); ip != "" && ip != opts.PinnedHost && deps.bindable(ip) {
			binds = append(binds, ip)
		}
	}
	return Candidate{Mode: modePinned, ContainerHost: opts.PinnedHost, BindHosts: binds}
}

// RuntimeAPIUnreachableFix is what to change on a host where no candidate is
// reachable. It names both causes measured in the field and the exact
// remediation for each, because the symptom (exit 139) names neither.
const RuntimeAPIUnreachableFix = "allow inbound connections to this overcast binary through the host " +
	"firewall (on Windows, a freshly built binary is blocked by default until an allow rule exists for " +
	"its exact path), or pin the address with LAMBDA_RUNTIME_API_HOST=host.docker.internal, or run " +
	"Overcast itself as a container (`overcast start --docker --mount-docker-socket`) so it sits on the " +
	"same network as the functions"

// RuntimeAPIUnreachableDetail composes the whole explanation from a probe's
// attempts: what was tried, what each did, and what to change.
//
// One function, used by the log line, /_overcast/health and the console
// advisory, so the three cannot say different things about the same failure.
func RuntimeAPIUnreachableDetail(attempts []Attempt) string {
	tried := "no candidate address could be built"
	if len(attempts) > 0 {
		tried = strings.Join(AttemptStrings(attempts), "; ")
	}
	return "No address Overcast can bind is reachable from a container on this machine, so every Lambda " +
		"invocation will strand at INIT and the runtime will exit 139. Tried, in order: " + tried +
		". A \"Connection refused\" means nothing was listening; a timeout means the packets were dropped " +
		"on the way in, which on Windows is the firewall's inbound default for a binary with no allow " +
		"rule. Fix: " + RuntimeAPIUnreachableFix + "."
}

// networkGateway returns the IPv4 gateway of a Docker network, or "" when the
// network has none, cannot be inspected, or names an address no container could
// usefully dial. A network created without IPAM configuration (the "host" and
// "none" networks among them) has no gateway, which is not an error — it means
// this mode does not apply.
func networkGateway(ctx context.Context, dc listenClient, network string) string {
	if dc == nil || network == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	info, err := dc.InspectNetwork(ctx, network)
	if err != nil || info == nil {
		return ""
	}
	for _, pool := range info.IPAM.Config {
		if ip := net.ParseIP(pool.Gateway); ip != nil && usableHostIP(ip) {
			return pool.Gateway
		}
	}
	return ""
}

// dockerDefaultBridge is Docker's own built-in network, present on every
// installation before Overcast creates anything of its own — including on
// Overcast's very first run, when a plane it is still deciding how to create
// does not exist yet to be inspected.
const dockerDefaultBridge = "bridge"

// NativeLinuxDaemon reports whether the Docker daemon appears to run on this
// same kernel — true on a native Linux daemon, false when it does not
// (including Docker Desktop, whose daemon lives inside a VM this process
// cannot bind a gateway address on) or when the fact cannot be established at
// all (no client, no "bridge" network to inspect).
//
// It answers the same question the gateway row vs. the host row answers in
// candidates — is a bridge network's gateway an address this kernel can bind —
// but against Docker's own default network rather than one Overcast manages:
// the fact under test (whose kernel is this) does not depend on which network
// is asked about, and "bridge" is guaranteed to exist already, unlike a plane a
// caller is still deciding whether to create `--internal`.
//
// Bindability is the right question *here*, and only here. What it decides is
// whether an `--internal` control plane would still have an on-link address for
// containers to dial, which is a property of the bridge rather than of any path
// to this process — see dataplane.runtimeAPIReachableOnInternalPlane. #1572 is
// about the other caller, which asked this and reported the answer as
// reachability.
func NativeLinuxDaemon(ctx context.Context, dc *docker.Client) bool {
	if dc == nil {
		return nativeLinuxDaemon(ctx, nil, bindableHost)
	}
	return nativeLinuxDaemon(ctx, dc, bindableHost)
}

// nativeLinuxDaemon is NativeLinuxDaemon with its bindability probe injected,
// so it is testable without a Docker daemon or a particular host network
// layout.
func nativeLinuxDaemon(ctx context.Context, dc listenClient, bindable func(string) bool) bool {
	gateway := networkGateway(ctx, dc, dockerDefaultBridge)
	return gateway != "" && bindable(gateway)
}

// bindableHost reports whether this kernel can listen on host.
//
// It orders the candidates and it answers NativeLinuxDaemon. It no longer
// *decides* which address containers are told to dial — that is what #1572 was
// about: owning an address is not the same as being reachable on it, and on a
// Windows host with the default firewall posture the two disagree in the
// direction that breaks every function.
//
// Port 0 asks for any free port, so this proves the address is local without
// depending on the port the caller is about to want — which is settled, and
// reported honestly, by the real listen.
func bindableHost(host string) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
