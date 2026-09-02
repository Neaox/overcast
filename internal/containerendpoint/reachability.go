package containerendpoint

// reachability.go establishes the fact listen.go used to assume.
//
// The question that decides whether any Lambda can run is "can a container on
// this machine open a connection to the address we are about to advertise".
// Until #1572 nothing asked it. `bindableHost` asked a different one — "can
// this process listen on this address" — and `narrowedTo` logged the answer as
// "resolved container-reachable listen address", which is a claim about a
// property that was never tested.
//
// The two come apart on the most common developer platform there is. On a
// Windows host with Docker Desktop, the host's own routable interface address
// binds fine and containers cannot reach it, because Windows Firewall's
// inbound default blocks a freshly built binary until somebody clicks Allow.
// `host.docker.internal` works in exactly that situation — Docker Desktop backs
// it with a VM-side route that does not take the filtered path. So every fresh
// `overcast.exe` on such a host got silent, total Lambda failure: every
// invocation stranded at INIT and the runtime exited 139, with nothing anywhere
// saying the address was unreachable.
//
// The fix is to measure it. Each candidate address is bound for real, a
// throwaway busybox container is started on the control plane, and it is asked
// to fetch a URL from that address. The first candidate a container actually
// reaches is the one containers are told to dial. One container start at boot,
// paid once per daemon (see the persisted hint below), for the fact that
// decides whether any function can run at all.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
)

// Candidate is one answer to "where do containers dial", paired with the local
// addresses that answer requires this process to be listening on.
//
// The pair travels together because a bind set derived separately from the
// address containers were handed is a silent misconfiguration the first time
// either changes — the same reason ResolveListen returns both.
type Candidate struct {
	// Mode names the reasoning that produced this candidate, for the log and
	// the health report: "container", "gateway", "docker-internal", "host",
	// "wildcard", or "pinned".
	Mode string
	// ContainerHost is what containers are told to dial.
	ContainerHost string
	// BindHosts are the local addresses that has to be listening on, most
	// important first.
	BindHosts []string
}

// Attempt records what one candidate did when a container tried to reach it.
// Carried out of the probe rather than only logged, because the advisory that
// explains a total failure has to name every address tried and what each did.
type Attempt struct {
	// Mode and Host identify the candidate — see Candidate.
	Mode string `json:"mode"`
	Host string `json:"host"`
	// Reachable is the only fact that decides anything: a container opened a
	// connection to Host and this process accepted it.
	Reachable bool `json:"reachable"`
	// Error is what the probe container reported when it could not connect —
	// "Connection refused" and "download timed out" are different diagnoses
	// (nothing listening vs. packets dropped on the way in), so the text is
	// kept verbatim rather than flattened to a boolean.
	Error string `json:"error,omitempty"`

	// Unavailable means the probe could not be *run* — no image, a daemon
	// that refused the create, no client at all — as opposed to running and
	// finding the address unreachable.
	//
	// The two must not be conflated, and keeping them apart is why this is a
	// field rather than a prefix on Error. An address nothing could reach is
	// a critical fault on this host; an address nobody could ask about is a
	// missing measurement, and reporting it as the first fires the loudest
	// advisory Overcast has at an air-gapped machine whose Lambda works.
	Unavailable bool `json:"unavailable,omitempty"`
}

// String renders one attempt for a log field or an advisory line.
func (a Attempt) String() string {
	if a.Reachable {
		return a.Mode + " " + a.Host + ": reachable"
	}
	reason := a.Error
	if reason == "" {
		reason = "no connection arrived"
	}
	if a.Unavailable {
		return a.Mode + " " + a.Host + ": not measured — " + reason
	}
	return a.Mode + " " + a.Host + ": " + reason
}

// AttemptStrings renders a whole attempt list, in the order they were tried.
func AttemptStrings(attempts []Attempt) []string {
	out := make([]string, 0, len(attempts))
	for _, a := range attempts {
		out = append(out, a.String())
	}
	return out
}

const (
	// probeDialSeconds is how long the probe container waits for its
	// connection. Long enough that a slow VM hop is not read as a block, short
	// enough that four dead candidates cost less than half a minute.
	probeDialSeconds = 4

	// probeContainerTimeout bounds one candidate's container end to end —
	// create, start, wait, logs — so a daemon that accepts the create and then
	// stops answering cannot hold Lambda's startup open.
	probeContainerTimeout = 20 * time.Second

	// probeTotalBudget caps the whole candidate walk, not each step of it.
	//
	// The per-candidate timeout bounds one bad daemon call; nothing bounded the
	// sum, so a daemon that accepts every create and then hangs cost five times
	// that before Lambda could serve anything — and an Invoke arriving during
	// the probe parks for all of it, because the runtime registry does not
	// settle until the wiring finishes. The ordinary path spends one candidate
	// and a second or two, so this only ever bites the pathological one.
	//
	// Running out is not a verdict. Candidates left untried mean the walk is
	// incomplete, and resolveListen will not call an address unreachable on an
	// incomplete walk — it falls back to the ordering, exactly as it does when
	// there is no daemon to probe with.
	probeTotalBudget = 45 * time.Second

	// probeImagePullTimeout bounds fetching busybox, and is spent outside every
	// other clock here — outside probeContainerTimeout, and outside
	// probeTotalBudget. Nesting it inside either makes it unreachable: a cold
	// pull on a slow link cut off at 20 s would be retried and re-truncated once
	// per candidate, and one cut off at 45 s exhausts the whole walk, so the
	// image never arrives however long this says. It was nested inside the total
	// budget until #1586, which is why the 60 s could not be reached.
	//
	// The pull therefore happens once, before the walk, against the caller's own
	// context — see prepareProbe in listen.go.
	probeImagePullTimeout = 60 * time.Second

	// probeAcceptGrace is how long the accept side is given after the probe
	// container has exited. The container's own exit is the later event in
	// every ordinary case; this only covers the daemon reporting it before the
	// connection has been handed to us.
	probeAcceptGrace = 250 * time.Millisecond
)

// runnerClient is the slice of *docker.Client the reachability probe needs.
// Separate from listenClient so a test can supply a container runner without
// also implementing network inspection.
type runnerClient interface {
	ImageExists(ctx context.Context, image string) (bool, error)
	PullImage(ctx context.Context, image string) error
	CreateContainer(ctx context.Context, name string, req *docker.CreateContainerRequest) (string, error)
	StartContainer(ctx context.Context, id string) error
	WaitContainer(ctx context.Context, id string) (int, error)
	ContainerLogs(ctx context.Context, id string, tail string) ([]byte, error)
	RemoveContainer(ctx context.Context, id string, force bool) error
}

// dialFromContainer asks a container on the plane to open a connection to addr
// and returns what it saw. A nil error means the container's own client
// reported success; the deciding fact is still the accept on this side, which
// probeCandidate checks — a container that reports success while nothing
// arrived here is not evidence of anything.
//
// The second return distinguishes "the container could not connect" (err
// non-nil, output carries the reason) from "the probe could not be run at all"
// (unavailable true) — the second must never be read as an unreachable
// address, because it is not a fact about the address.
type dialFromContainer func(ctx context.Context, addr string) (output string, unavailable bool, err error)

// probeCandidate binds c's local addresses on one shared ephemeral port,
// serves a trivial HTTP 200 there, and asks a container to fetch it.
//
// The ephemeral port rather than the Runtime API's real one is deliberate and
// safe: every cause this exists to catch is port-independent. Windows Firewall
// rules are per-program, Docker Desktop's host route is per-address, and an
// `--internal` bridge severs a path rather than a port. Probing on the real
// port would mean binding it before knowing whether to keep it, which is a
// worse trade — a failed candidate would leave the port taken mid-decision.
func probeCandidate(ctx context.Context, c Candidate, dial dialFromContainer) Attempt {
	a := Attempt{Mode: c.Mode, Host: c.ContainerHost}

	pl, err := listenProbe(c.BindHosts)
	if err != nil {
		a.Error = "could not bind " + strings.Join(c.BindHosts, ", ") + ": " + err.Error()
		return a
	}
	defer pl.Close()

	output, unavailable, dialErr := dial(ctx, net.JoinHostPort(c.ContainerHost, strconv.Itoa(pl.port)))
	if unavailable {
		a.Unavailable = true
		a.Error = "probe could not run: " + dialErr.Error()
		return a
	}
	if pl.arrived(probeAcceptGrace) {
		a.Reachable = true
		return a
	}
	switch {
	case output != "":
		// The container's own words, whenever it said anything. Deliberately
		// not gated on dialErr: the exit code is plumbed through a shell and a
		// daemon, and a diagnosis that depends on that plumbing is one that
		// silently degrades to nothing when it changes. probeOutput already
		// drops the marker and the served body, so a clean success still
		// yields "" and still lands in the default arm below.
		a.Error = output
	case dialErr != nil:
		a.Error = dialErr.Error()
	default:
		// Nothing to quote and no error: the container's client reported
		// success and nothing reached us. Rare, and worth its own wording
		// rather than being folded into a timeout — something between the two
		// answered on the address's behalf.
		a.Error = "the probe container connected to something, but not to this process"
	}
	return a
}

// probeListener is the accept side of one candidate: every bind host on one
// shared ephemeral port, answering anything that connects with an HTTP 200.
type probeListener struct {
	lns      []net.Listener
	port     int
	accepted atomic.Bool
	arrivals chan struct{}
}

// listenProbe binds hosts on a single ephemeral port.
//
// The first host settles the port and its bind is the one that has to succeed —
// it is the address closest to what containers were told to dial. A secondary
// bind that fails is not fatal: the candidate is still testable on the
// addresses that did bind, and a host that cannot hold one of its own
// interfaces is exactly the situation the probe exists to find out about.
func listenProbe(hosts []string) (*probeListener, error) {
	if len(hosts) == 0 {
		return nil, errors.New("no bind hosts")
	}
	pl := &probeListener{arrivals: make(chan struct{}, 1)}
	for i, host := range hosts {
		port := pl.port
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			if i == 0 {
				return nil, err
			}
			continue
		}
		if pl.port == 0 {
			pl.port = ln.Addr().(*net.TCPAddr).Port
		}
		pl.lns = append(pl.lns, ln)
	}
	if len(pl.lns) == 0 {
		return nil, errors.New("no bind host could be listened on")
	}
	for _, ln := range pl.lns {
		go pl.serve(ln)
	}
	return pl, nil
}

// serve answers one listener. Anything that connects is recorded and given a
// 200: the container's client has to see a well-formed answer, or a working
// path would be reported as a failure by its own error handling.
func (p *probeListener) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		p.accepted.Store(true)
		select {
		case p.arrivals <- struct{}{}:
		default:
		}
		go func() {
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			// Read and discard the request line; busybox wget will not read
			// the response until it has finished writing the request.
			buf := make([]byte, 1024)
			_, _ = conn.Read(buf)
			_, _ = conn.Write([]byte("HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 3\r\nConnection: close\r\n\r\nok\n"))
		}()
	}
}

// arrived reports whether anything connected, waiting up to grace for a
// connection the daemon has already told us about but that has not reached the
// accept loop yet.
func (p *probeListener) arrived(grace time.Duration) bool {
	if p.accepted.Load() {
		return true
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-p.arrivals:
		return true
	case <-timer.C:
		return p.accepted.Load()
	}
}

// Close stops every listener.
func (p *probeListener) Close() {
	for _, ln := range p.lns {
		_ = ln.Close()
	}
}

// probeExtraHosts is the one /etc/hosts entry the probe container needs, and it
// is not incidental: `host.docker.internal` only resolves off Docker Desktop
// because Mapper.ExtraHosts pins it to Docker's "host-gateway" for every real
// Lambda container. A probe run without it would report the docker-internal
// candidate unreachable on every native Linux host — measuring the probe's own
// container rather than the one the answer is for.
var probeExtraHosts = []string{dockerInternalHost + ":host-gateway"}

// dockerDialer builds a dialFromContainer backed by a real daemon: one
// throwaway busybox container per candidate, on the plane the Lambda containers
// themselves are created on, with the same host-gateway entry they get.
func dockerDialer(dc runnerClient, network string, logger *zap.Logger) dialFromContainer {
	return func(ctx context.Context, addr string) (string, bool, error) {
		if dc == nil {
			return "", true, errors.New("no Docker client")
		}
		// The image is already here: resolveListen pulls it once, before the
		// walk's clock starts, so nothing in this function competes with the
		// pull for the candidate's time. See prepareProbe and
		// probeImagePullTimeout.
		ctx, cancel := context.WithTimeout(ctx, probeContainerTimeout)
		defer cancel()

		name := fmt.Sprintf("overcast-reachability-%d", time.Now().UnixNano())
		req := &docker.CreateContainerRequest{
			ContainerConfig: &docker.ContainerConfig{
				Image: docker.UtilityImage,
				// `rc=$?` before the echo, and an explicit `exit $rc`: `sh -c`
				// exits with the status of its *last* command, so echoing the
				// marker first would make every probe container exit 0 whatever
				// wget did — and the whole diagnosis rests on telling a refusal
				// from a timeout. Without `-q` wget writes a progress line to
				// stderr that would drown the one line worth quoting.
				Cmd: []string{"sh", "-c", fmt.Sprintf(
					"wget -q -T %d -O - http://%s/ 2>&1; rc=$?; echo %s$rc; exit $rc",
					probeDialSeconds, addr, probeExitMarker)},
				Labels: docker.ManagedLabels(docker.ServiceCore, "runtime-api-reachability"),
			},
			HostConfig: &docker.HostConfig{
				NetworkMode: network,
				ExtraHosts:  probeExtraHosts,
			},
		}
		id, err := dc.CreateContainer(ctx, name, req)
		if err != nil {
			return "", true, fmt.Errorf("create probe container: %w", err)
		}
		defer func() {
			// A detached context: the probe's own deadline may already have
			// expired, and a container left behind is litter on the user's
			// daemon that nothing else will sweep by name.
			rmCtx, rmCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer rmCancel()
			if rmErr := dc.RemoveContainer(rmCtx, id, true); rmErr != nil && logger != nil {
				logger.Debug("reachability probe container not removed",
					zap.String("container", id), zap.Error(rmErr))
			}
		}()

		if err := dc.StartContainer(ctx, id); err != nil {
			return "", true, fmt.Errorf("start probe container: %w", err)
		}
		code, err := dc.WaitContainer(ctx, id)
		if err != nil {
			return "", true, fmt.Errorf("wait for probe container: %w", err)
		}
		out := ""
		if logs, logErr := dc.ContainerLogs(ctx, id, "20"); logErr == nil {
			out = probeOutput(string(docker.DemuxStream(logs)))
		}
		if code == 0 {
			return out, false, nil
		}
		return out, false, fmt.Errorf("probe container exited %d", code)
	}
}

// probeExitMarker is what the probe command echoes after its client exits, so
// a de-framed log that carries it can be told from one the daemon truncated or
// never handed over.
const probeExitMarker = "probe-exit="

// probeOutput reduces the container's de-framed log to the part worth quoting:
// the client's own error, if it printed one.
//
// busybox wget names its failures precisely — "Connection refused" from a host
// that answered, "download timed out" from one that dropped the packets — and
// that distinction is the difference between "nothing is listening there" and
// "a firewall is in the way", which is the sentence the advisory has to be able
// to write.
//
// The probe's own bookkeeping and the body the listener serves are dropped:
// they say the command ran, which is not a reason for anything. A run whose
// whole output is bookkeeping therefore yields "" rather than the raw text — an
// empty reason reads as "the container said nothing useful", which is true,
// where quoting a success line beside a failure verdict would not be.
func probeOutput(raw string) string {
	var kept []string
	sawMarker := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, probeExitMarker) {
			sawMarker = true
			continue
		}
		if line == "" || line == "ok" {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		if sawMarker {
			return ""
		}
		return strings.TrimSpace(raw)
	}
	return strings.Join(kept, "; ")
}

// ensureProbeImage makes sure busybox is on the daemon, pulling it only when it
// is not already there.
//
// It is docker.UtilityImage — the image Overcast already runs its own
// infrastructure containers from (EFS's materializer, ECS's awsvpc namespace
// container) — so a machine that has run either has it cached, and one that has
// not pays a ~2 MB pull once.
func ensureProbeImage(ctx context.Context, dc runnerClient) error {
	lookCtx, lookCancel := context.WithTimeout(ctx, 10*time.Second)
	present, err := dc.ImageExists(lookCtx, docker.UtilityImage)
	lookCancel()
	if err == nil && present {
		return nil
	}
	pullCtx, cancel := context.WithTimeout(ctx, probeImagePullTimeout)
	defer cancel()
	if err := dc.PullImage(pullCtx, docker.UtilityImage); err != nil {
		return fmt.Errorf("pull %s for the reachability probe: %w", docker.UtilityImage, err)
	}
	return nil
}

// ---- The remembered answer ------------------------------------------------

// hint is the probe's verdict, remembered across restarts.
//
// Keyed by daemon identity rather than by host: the answer is a property of the
// pair (this process, that daemon), and the same binary against a different
// daemon — a native one after a Docker Desktop one, a DinD sidecar, a tcp://
// endpoint — is a different question with a different answer. A hint whose
// daemon does not match the one in front of us is ignored rather than trusted.
type hint struct {
	// Version is hintFileVersion — see there for why a cache carries a schema.
	Version int `json:"version"`
	// Daemon is docker.SystemInfo.ID.
	Daemon string `json:"daemon"`
	// Network is the plane the probe ran on: a hint from a different control
	// plane says nothing about this one.
	Network string `json:"network"`
	// Mode, ContainerHost and BindHosts are the candidate that won.
	Mode          string   `json:"mode"`
	ContainerHost string   `json:"containerHost"`
	BindHosts     []string `json:"bindHosts"`
	// ProbedAt is when, for the log line that says the probe was skipped.
	ProbedAt time.Time `json:"probedAt"`
}

// hintFileVersion is the schema of the record below. A reader that finds
// anything else ignores the file and re-probes, which is the same cost as a
// miss — so a future field change never has to be inferred from what happens
// to survive json.Unmarshal.
const hintFileVersion = 1

// hintFileName builds the file name for one control plane.
//
// One file *per plane*, not one for the data directory. Two Overcast instances
// sharing the default data dir with different OVERCAST_NETWORK values is a
// documented configuration (docs/configuration.md § Running two instances on
// one host), and a single file makes them fight: each rejects the other's
// record on the network check, re-probes, and overwrites it, for ever. The
// record still names the plane it is about, so a file read for the wrong one is
// caught either way — the name is what stops the two from thrashing.
func hintFileName(network string) string {
	if network == "" {
		return "runtime-api-host.json"
	}
	return "runtime-api-host-" + hintFileSafe(network) + ".json"
}

// hintFileSafe reduces a network name to something safe in a file name. Docker
// network names are already conservative, but OVERCAST_NETWORK is free text and
// a path separator in it must not escape the data directory.
func hintFileSafe(network string) string {
	var b strings.Builder
	for _, r := range network {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// HintPath returns where the remembered answer for one control plane lives
// under a given state directory, or "" when there is nowhere to keep it (no
// data directory configured). Exported so the caller passes a path rather than
// this package growing a dependency on config.
func HintPath(dataDir, network string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, hintFileName(network))
}

// readHint returns the remembered candidate when the file names this daemon
// and this network, and nil otherwise. Every failure is a miss: a hint is an
// optimisation, and one that cannot be read costs a probe rather than a
// startup.
func readHint(path, daemon, network string) *Candidate {
	if path == "" || daemon == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var h hint
	if err := json.Unmarshal(data, &h); err != nil {
		return nil
	}
	if h.Version != hintFileVersion {
		return nil
	}
	if h.Daemon != daemon || h.Network != network || h.ContainerHost == "" || len(h.BindHosts) == 0 {
		return nil
	}
	return &Candidate{Mode: h.Mode, ContainerHost: h.ContainerHost, BindHosts: h.BindHosts}
}

// writeHint records a verified candidate.
//
// The directory is created rather than assumed. cfg.DataDir is always set, but
// only the hybrid and WAL state backends actually create it — under
// OVERCAST_STATE=memory, which config resolves to automatically whenever SQLite
// is unavailable, nothing does. Without the MkdirAll every write there fails
// silently and every startup re-probes, which is precisely the promise the docs
// and the changelog make ("only the first startup pays for it").
//
// Written to a temp file and renamed, so a reader never sees half a record: a
// torn file is only a miss, but it is a miss with no explanation, and rename is
// one line.
//
// Still best effort. A data directory that cannot be written is somebody else's
// problem to report, and failing startup over a cache would be the wrong trade —
// but it is logged at debug rather than swallowed, so "why does it probe every
// time" is answerable without a debugger.
func writeHint(path, daemon, network string, c Candidate, now time.Time, logger *zap.Logger) {
	if path == "" || daemon == "" {
		return
	}
	note := func(err error) {
		if logger != nil {
			logger.Debug("could not remember the Runtime API probe result — the next startup will probe again",
				zap.String("path", path), zap.Error(err))
		}
	}
	data, err := json.Marshal(hint{
		Version:       hintFileVersion,
		Daemon:        daemon,
		Network:       network,
		Mode:          c.Mode,
		ContainerHost: c.ContainerHost,
		BindHosts:     c.BindHosts,
		ProbedAt:      now.UTC(),
	})
	if err != nil {
		note(err)
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		note(err)
		return
	}
	tmp, err := os.CreateTemp(dir, ".runtime-api-host-*.tmp")
	if err != nil {
		note(err)
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		note(err)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		note(err)
		return
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		note(err)
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		note(err)
	}
}

// ForgetHint drops the remembered answer, so the next startup probes again.
//
// Called when the remembered address is proved wrong at the only moment that
// can prove it: a Lambda container that died during INIT having never opened a
// connection to its Runtime API endpoint. A hint that survived that would make
// the failure permanent across restarts, which is worse than the bug this
// package is fixing.
func ForgetHint(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// daemonIdentity returns the daemon's own ID, or "" when it cannot be
// established — in which case nothing is remembered, because a hint that cannot
// be keyed cannot be invalidated when the daemon changes.
func daemonIdentity(ctx context.Context, dc interface {
	Info(ctx context.Context) (*docker.SystemInfo, error)
}) string {
	if dc == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := dc.Info(ctx)
	if err != nil || info == nil {
		return ""
	}
	return info.ID
}
