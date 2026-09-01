package main

// docker_backend.go — `overcast start --docker`'s backend: runs the emulator
// as a container instead of spawning `overcast serve` natively (cmd_start.go's
// startNative). Shares the instance registry (instances.go) with the native
// backend; only the "how do I start/stop/check/log this thing" mechanics
// differ, dispatched by instanceRecord.Backend == "docker".
//
// Prior art: scripts/run-test-instance.sh and .ps1 already solve free-port
// scanning, the OVERCAST_*/AWS_* env allow-list, and 127.0.0.1-only
// publishing for a throwaway test instance. This backend reuses those same
// choices for a *named, persistent* instance a user manages with
// start/stop/restart/logs — the differences are that the default port pair
// is the standard 4566/4567 (this is meant to be someone's real running
// instance, not a side-by-side test one) and the container is created
// without --rm, so `overcast stop` controls its removal explicitly (see
// stopDockerInstance in cmd_stop.go) rather than it vanishing the moment it
// stops.
//
// ---------------------------------------------------------------------------
// No arbitrary docker flag passthrough
// ---------------------------------------------------------------------------
// Exactly like run-test-instance.sh/.ps1 (see their header comments), the
// flags this backend exposes — --image, --channel, --data-volume,
// --mount-docker-socket — are an allow-list, not a passthrough. `overcast
// start --docker` runs a container derived entirely from named, validated
// options; there is no "extra docker args" escape hatch. The reasoning is
// identical: a passthrough would make any permission grant that names this
// command equivalent to a blanket `docker run` grant, --privileged included.
//
// ---------------------------------------------------------------------------
// The exec seam
// ---------------------------------------------------------------------------
// Every docker invocation in this file goes through dockerRun, a package var
// tests replace with a fake. This repo has hard-won history of docker-gated
// tests silently skipping on this machine (SkipWithoutDocker always skips —
// see the memory notes); dockerRun exists so this backend's own tests never
// need a real daemon, real Docker Desktop, or any container at all — they
// assert on the argv this backend assembles, not on what a real `docker`
// binary does with it.
//
// Production wires dockerRun to shell out to the real `docker` CLI (never
// the daemon's HTTP socket directly): the same choice run-test-instance made,
// so whatever `docker` binary and context/auth config is on the caller's
// PATH is what actually runs the container — no separate credential or
// socket-path story to keep in sync with the user's own docker setup.
import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// dockerRun is the seam every docker invocation in this file goes through.
// Production runs the real `docker` CLI; tests replace this var with a fake
// that returns canned output, so nothing here ever requires a real daemon.
// CombinedOutput (stdout+stderr together) is used because a failing `docker`
// invocation's useful diagnostic is almost always on stderr (image not
// found, daemon unreachable, name already in use) and callers here only ever
// want a single human-readable error, never to distinguish the streams.
var dockerRun = func(args ...string) (string, error) {
	out, err := exec.Command("docker", args...).CombinedOutput() //nolint:gosec // "docker" resolves via PATH exactly like run-test-instance.sh/.ps1; args are built entirely from this file's own allow-listed flags, never arbitrary passthrough.
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// dockerImageRepo is the only image repository this backend ever runs — the
// full console image (web UI included), matching what `overcast start`
// (native) always launches. --image overrides it entirely; there is no flag
// to switch to overcast-slim, since a CLI-managed instance is exactly the
// case the console image is for.
const dockerImageRepo = "ghcr.io/neaox/overcast"

// resolveDockerImage applies --image > --channel > version-pinned-default
// precedence for `overcast start --docker`'s image reference.
//
// The default is pinned to this CLI's own version
// (ghcr.io/neaox/overcast:<version>), not a floating tag like :latest or
// :alpha. That is a deliberate departure from tools like LocalStack, which
// float :latest and document no supported CLI-to-emulator version pairing at
// all: a floating default silently lets the CLI you installed six months ago
// start a container built yesterday, and any behavior drift between the two
// is indistinguishable from a real bug until someone thinks to check image
// versions by hand. Pinning to the CLI's own version means `overcast start
// --docker` always reproduces the exact emulator this binary was built
// against, and the only way to run something else is to say so explicitly
// via --image or --channel.
//
// cliVersion == "dev" is the one case with no matching version tag at all —
// an unreleased build straight from `go build` — so it falls back to the
// :alpha channel, with a one-line note explaining why (out is normally
// cmd.OutOrStdout()).
func resolveDockerImage(imageFlag, channelFlag, cliVersion string, note func(string)) (string, error) {
	if imageFlag != "" {
		return imageFlag, nil
	}
	if channelFlag != "" {
		switch channelFlag {
		case "alpha", "beta", "latest":
			return dockerImageRepo + ":" + channelFlag, nil
		default:
			return "", fmt.Errorf("--channel %q: want alpha, beta, or latest", channelFlag)
		}
	}
	if cliVersion == "dev" {
		if note != nil {
			note(fmt.Sprintf("overcast: this is an unreleased build (version %q has no matching image tag) — defaulting to %s:alpha; pass --image or --channel to pick a specific build", cliVersion, dockerImageRepo))
		}
		return dockerImageRepo + ":alpha", nil
	}
	return dockerImageRepo + ":" + cliVersion, nil
}

// dockerChannels are the completion candidates for --channel.
var dockerChannels = []string{"alpha", "beta", "latest"}

// portFree reports whether port is free to bind on 127.0.0.1, by actually
// attempting to listen on it and closing immediately — the same technique
// run-test-instance.ps1 falls back to when Get-NetTCPConnection is
// unavailable, applied here as the only technique since this backend has no
// ss/netstat parsing to fall back from.
func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// resolveDockerPorts decides the API/UI port pair `overcast start --docker`
// publishes. explicit is true when the caller actually passed --port and/or
// --ui-port (cmd.Flags().Changed) rather than leaving them at their cobra
// defaults; restart also passes explicit=true, since it means to reproduce
// the exact same instance rather than pick fresh ports.
//
//   - explicit: both ports are used as given (error if either is busy).
//     uiPort == 0 keeps the native "0 = disable the web console" meaning —
//     it is neither checked nor published.
//   - not explicit: scan upward from the standard 4566/4567, mirroring
//     run-test-instance's adjacent-pair scan, but starting AT the standard
//     ports rather than skipping them — unlike that script's throwaway test
//     instances, this is meant to become the user's real instance, so
//     landing on 4566/4567 when they're free is the whole point.
func resolveDockerPorts(port, uiPort int, explicit bool) (int, int, error) {
	if explicit {
		if !portFree(port) {
			return 0, 0, fmt.Errorf("port %d is already in use", port)
		}
		if uiPort != 0 && !portFree(uiPort) {
			return 0, 0, fmt.Errorf("ui-port %d is already in use", uiPort)
		}
		return port, uiPort, nil
	}
	for p := 4566; p < 65000; p += 2 {
		ui := p + 1
		if portFree(p) && portFree(ui) {
			return p, ui, nil
		}
	}
	return 0, 0, errors.New("no free port pair found from 4566 upward")
}

// checkDockerAvailable gives a crisp, specific error for the two ways
// --docker can't work: no docker CLI on PATH, or a CLI that's there but
// can't reach a daemon (Docker Desktop not running, wrong context, etc.).
// Both are checked up front so a caller sees the actual problem rather than
// a raw `docker run` failure several steps into starting the instance.
func checkDockerAvailable() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("--docker requires the docker CLI on PATH — install Docker Desktop (or the docker CLI) and make sure `docker` is runnable")
	}
	if _, err := dockerRun("version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("--docker requires a reachable Docker daemon: %w — is Docker Desktop running?", err)
	}
	return nil
}

// dockerContainerName is the container name `overcast start --docker`
// creates: overcast-<instance name>, so `docker ps`/`docker logs` output is
// self-explanatory without consulting the registry, and a stray manual
// `docker rm overcast-<name>` is an obvious, discoverable escape hatch.
func dockerContainerName(instanceName string) string {
	return "overcast-" + instanceName
}

// startDocker is startBackend's "docker" case (dispatched from cmd_start.go).
// opts.image must already be resolved (see resolveDockerImage) — restart
// reuses the persisted instanceRecord.Image directly, and a fresh `overcast
// start --docker` resolves it once in cmd_start.go's RunE before calling
// here, so the one-line "unreleased build" note (resolveDockerImage's note
// callback) is only ever printed once per invocation, not on every restart.
func startDocker(opts startOptions) (instanceRecord, error) {
	if opts.image == "" {
		return instanceRecord{}, errors.New("internal error: startDocker requires a resolved image (opts.image is empty)")
	}
	if err := checkDockerAvailable(); err != nil {
		return instanceRecord{}, err
	}

	port, uiPort, err := resolveDockerPorts(opts.port, opts.uiPort, opts.portsExplicit)
	if err != nil {
		return instanceRecord{}, err
	}

	args := []string{"run", "-d", "--name", dockerContainerName(opts.name),
		"-p", fmt.Sprintf("127.0.0.1:%d:4566", port),
	}
	if uiPort != 0 {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:4567", uiPort))
	}
	if opts.state != "" {
		args = append(args, "-e", "OVERCAST_STATE="+opts.state)
	}
	// Sorted for deterministic argv — mirrors buildChildEnv's (cmd_start.go)
	// reason: tests asserting on the assembled args aren't at the mercy of
	// Go's randomized map iteration.
	names := make([]string, 0, len(opts.env))
	for k := range opts.env {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		args = append(args, "-e", k+"="+opts.env[k])
	}
	if opts.dataVolume != "" {
		// OVERCAST_DATA_DIR=/data is already the image's own baked-in
		// default (see the Dockerfile's ENV block) — set explicitly anyway
		// so the container's environment states the intent plainly rather
		// than relying on an image-internal default a reader of `docker
		// inspect` would have to already know about.
		args = append(args, "-v", opts.dataVolume+":/data", "-e", "OVERCAST_DATA_DIR=/data")
	}
	if opts.mountDockerSocket {
		args = append(args, "-v", "/var/run/docker.sock:/var/run/docker.sock")
	}
	args = append(args, opts.image)

	out, err := dockerRun(args...)
	if err != nil {
		return instanceRecord{}, fmt.Errorf("docker run: %w", err)
	}
	containerID := lastNonEmptyLine(out)
	if containerID == "" {
		return instanceRecord{}, errors.New("docker run reported no container id")
	}

	return instanceRecord{
		Name:              opts.name,
		Backend:           "docker",
		Port:              port,
		UIPort:            uiPort,
		Endpoint:          fmt.Sprintf("http://127.0.0.1:%d", port), // 127.0.0.1, not localhost — see buildInstanceRecord's comment in cmd_start.go; the same rationale applies here.
		State:             opts.state,
		ContainerID:       containerID,
		Image:             opts.image,
		DataVolume:        opts.dataVolume,
		MountDockerSocket: opts.mountDockerSocket,
		StartedAt:         time.Now().UTC(),
		Version:           version,
		Env:               opts.env,
	}, nil
}

// dockerContainerRunning reports rec's container's live state via `docker
// inspect -f {{.State.Running}}`, through the dockerRun seam. Used by
// instanceRunning and instanceLifecycleState (instances.go); a failed
// inspect (container removed out of band, daemon down, docker missing) is
// returned as an error rather than silently coerced to "not running", so
// instanceLifecycleState can report "unknown" instead of a false "stopped".
func dockerContainerRunning(containerID string) (bool, error) {
	if containerID == "" {
		return false, errors.New("no container id recorded for this instance")
	}
	out, err := dockerRun("inspect", "-f", "{{.State.Running}}", containerID)
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected `docker inspect` output %q", strings.TrimSpace(out))
	}
}

// lastNonEmptyLine returns the last non-blank line of s, trimmed. `docker
// run -d` normally prints exactly one line (the container id) on success,
// but when the image is not already present locally it first prints
// "Unable to find image ... locally" plus the pull's progress output — all
// of that arrives through the same CombinedOutput stream dockerRun reads,
// ahead of the id (docker pulls synchronously before creating the
// container, so the id is reliably the last thing written). Taking the
// whole trimmed output as the id, as an earlier version of this function
// did, silently stored a multi-line pull log as the "container id" the very
// first time this ran against an image not yet pulled — this is that fix.
func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// shortContainerID trims a container id to docker's own conventional
// 12-character short form for display, tolerating an id shorter than that
// (should not happen with a real docker daemon, but a fake in a test might
// return one).
func shortContainerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
