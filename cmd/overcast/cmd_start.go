package main

// cmd_start.go — `overcast start`. Spawns `overcast serve` as a detached
// background process (or, with --docker, runs it as a container — see
// docker_backend.go) and records it in the instance registry (instances.go)
// so `overcast stop`/`overcast restart` (cmd_stop.go) can find it again.
//
// startBackend's dispatch and the "backend" field on the saved record exist
// so each backend is a self-contained case here rather than a parallel start
// path living elsewhere; startNative is this file's case, startDocker
// (docker_backend.go) is the other.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a background overcast instance",
		Args:  cobra.NoArgs,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			port, _ := cmd.Flags().GetInt("port")
			uiPort, _ := cmd.Flags().GetInt("ui-port")
			state, _ := cmd.Flags().GetString("state")
			dataDir, _ := cmd.Flags().GetString("data-dir")
			envFlags, _ := cmd.Flags().GetStringArray("env")
			noWait, _ := cmd.Flags().GetBool("no-wait")
			timeout, _ := cmd.Flags().GetDuration("timeout")
			useDocker, _ := cmd.Flags().GetBool("docker")
			image, _ := cmd.Flags().GetString("image")
			channel, _ := cmd.Flags().GetString("channel")
			dataVolume, _ := cmd.Flags().GetString("data-volume")
			mountDockerSocket, _ := cmd.Flags().GetBool("mount-docker-socket")

			if err := validateDockerOnlyFlags(useDocker, image, channel, dataVolume, mountDockerSocket); err != nil {
				return err
			}

			envMap, err := parseStartEnv(envFlags)
			if err != nil {
				return err
			}

			// A dead record (its process/container no longer running) is
			// replaced silently below by the eventual saveInstance call;
			// only a live one is refused.
			if existing, err := loadInstance(name); err == nil && instanceRunning(existing) {
				return fmt.Errorf("instance %q is already running — run `overcast stop %s` first", name, name)
			}

			backend := "native"
			opts := startOptions{
				name:    name,
				port:    port,
				uiPort:  uiPort,
				state:   state,
				dataDir: dataDir,
				env:     envMap,
			}
			if useDocker {
				backend = "docker"
				resolvedImage, err := resolveDockerImage(image, channel, version, func(note string) {
					fmt.Fprintln(cmd.OutOrStdout(), note)
				})
				if err != nil {
					return err
				}
				opts.portsExplicit = cmd.Flags().Changed("port") || cmd.Flags().Changed("ui-port")
				opts.image = resolvedImage
				opts.dataVolume = dataVolume
				opts.mountDockerSocket = mountDockerSocket
			}

			rec, err := startBackend(backend, opts)
			if err != nil {
				return err
			}
			if err := saveInstance(rec); err != nil {
				return fmt.Errorf("instance %q started but saving its registry record failed: %w", name, err)
			}

			printInstanceStarted(cmd, rec)

			if noWait {
				return nil
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := waitForHealthy(ctx, rec.Endpoint, 500*time.Millisecond); err != nil {
				return fmt.Errorf("instance %q did not become healthy within %s: %w — see %s for the daemon's own log", name, timeout, err, rec.LogFile)
			}
			return nil
		},
	}
	cmd.Flags().String("name", "default", "instance name")
	_ = cmd.RegisterFlagCompletionFunc("name", completeInstanceNames)
	cmd.Flags().Int("port", 4566, "API port (env: OVERCAST_PORT)")
	cmd.Flags().Int("ui-port", defaultUIPort, "web UI port (0 = disable)")
	cmd.Flags().String("state", "", "state backend passthrough (env: OVERCAST_STATE; empty = daemon default)")
	cmd.Flags().String("data-dir", "", "data directory passthrough (env: OVERCAST_DATA_DIR; empty = daemon default)")
	cmd.Flags().StringArray("env", nil, "extra OVERCAST_*/AWS_* environment variable for the daemon, as KEY=VALUE (repeatable)")
	cmd.Flags().Bool("no-wait", false, "return immediately instead of waiting for the instance to become healthy")
	cmd.Flags().Duration("timeout", 60*time.Second, "how long to wait for the instance to become healthy")

	// --docker and its options: see docker_backend.go for what each does and
	// why there is no further docker run passthrough.
	cmd.Flags().Bool("docker", false, "run the instance as a Docker container instead of a native process")
	cmd.Flags().String("image", "", "docker image to run, full override (only with --docker; default: this CLI's own version)")
	cmd.Flags().String("channel", "", "docker image channel: alpha, beta, or latest (only with --docker; default: pinned to this CLI's version)")
	_ = cmd.RegisterFlagCompletionFunc("channel", completeDockerChannels)
	cmd.Flags().String("data-volume", "", "docker named volume to mount at /data (only with --docker)")
	cmd.Flags().Bool("mount-docker-socket", false, "bind-mount the host docker socket into the container, for Lambda/ECS sibling containers (only with --docker)")
	return cmd
}

// completeDockerChannels is --channel's shell-completion source: the fixed,
// small set of valid values (see resolveDockerImage, docker_backend.go).
func completeDockerChannels(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return dockerChannels, cobra.ShellCompDirectiveNoFileComp
}

// validateDockerOnlyFlags refuses --image/--channel/--data-volume/
// --mount-docker-socket when --docker was not given: each is meaningless for
// the native backend, and silently ignoring them would let a typo'd
// `--dokcer` (say) look like it worked while quietly running native instead
// of the container the caller actually asked for.
func validateDockerOnlyFlags(docker bool, image, channel, dataVolume string, mountDockerSocket bool) error {
	if docker {
		return nil
	}
	switch {
	case image != "":
		return errors.New("--image requires --docker")
	case channel != "":
		return errors.New("--channel requires --docker")
	case dataVolume != "":
		return errors.New("--data-volume requires --docker")
	case mountDockerSocket:
		return errors.New("--mount-docker-socket requires --docker")
	}
	return nil
}

// printInstanceStarted prints the summary a caller needs right after start
// (or restart) succeeds: where the instance is, where its log is, and how
// to point AWS tools at it. Shared by cmd_stop.go's restart, whose success
// message is identical apart from the verb. Backend-aware: a docker instance
// has a container id instead of a pid and no daemon.log of its own —
// `overcast logs` reads from the container either way, so that's what's
// pointed at.
func printInstanceStarted(cmd *cobra.Command, rec instanceRecord) {
	out := cmd.OutOrStdout()
	if rec.Backend == "docker" {
		fmt.Fprintf(out, "overcast %q started (container %s)\n", rec.Name, shortContainerID(rec.ContainerID))
	} else {
		fmt.Fprintf(out, "overcast %q started (pid %d)\n", rec.Name, rec.PID)
	}
	fmt.Fprintf(out, "  endpoint: %s\n", rec.Endpoint)
	if rec.UIPort != 0 {
		fmt.Fprintf(out, "  web console: http://127.0.0.1:%d\n", rec.UIPort)
	}
	if rec.Backend == "docker" {
		fmt.Fprintf(out, "  logs: overcast logs %s\n", rec.Name)
	} else {
		fmt.Fprintf(out, "  log: %s\n", rec.LogFile)
	}
	fmt.Fprintln(out, `  run 'eval "$(overcast env)"' (PowerShell: overcast env | iex) to point AWS tools here`)
}

// parseStartEnv validates each --env KEY=VALUE flag against an allow-list:
// only OVERCAST_* and AWS_* names may reach the spawned daemon's
// environment, mirroring scripts/run-test-instance.ps1's -EnvVar (see that
// script's header comment). The daemon is unauthenticated and, with the
// docker backend (docker_backend.go), capable of starting containers on the
// caller's behalf, so `overcast start --env` must not become a way to
// inject arbitrary environment (PATH, LD_PRELOAD, …) into a process this
// CLI spawns.
func parseStartEnv(flags []string) (map[string]string, error) {
	env := make(map[string]string, len(flags))
	for _, kv := range flags {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("--env %q: want KEY=VALUE", kv)
		}
		if !strings.HasPrefix(name, "OVERCAST_") && !strings.HasPrefix(name, "AWS_") {
			return nil, fmt.Errorf("--env %q: only OVERCAST_* and AWS_* variables are allowed", kv)
		}
		env[name] = value
	}
	return env, nil
}

// startOptions collects the resolved, already-validated parameters for
// spawning one instance, independent of which backend ends up running it.
// The fields below dataDir are native-only; the fields below env are
// docker-only (see startDocker, docker_backend.go) — each backend ignores
// the fields that aren't its own.
type startOptions struct {
	name    string
	port    int
	uiPort  int
	state   string
	dataDir string
	env     map[string]string

	// portsExplicit: true when port/uiPort were explicitly requested (error
	// if busy) rather than left at their defaults (scan for a free pair).
	// See resolveDockerPorts (docker_backend.go).
	portsExplicit     bool
	image             string // already-resolved image reference; see resolveDockerImage.
	dataVolume        string
	mountDockerSocket bool
}

// startBackend dispatches by backend name: "native" (this file) or "docker"
// (docker_backend.go). Each backend is a self-contained case here rather
// than a parallel switch living somewhere else.
func startBackend(backend string, opts startOptions) (instanceRecord, error) {
	switch backend {
	case "native":
		return startNative(opts)
	case "docker":
		return startDocker(opts)
	default:
		return instanceRecord{}, fmt.Errorf("unknown backend %q", backend)
	}
}

// startNative spawns `overcast serve` as a detached child process: this
// binary re-invoked via os.Executable(), put in its own session/process
// group so it survives this process exiting (setDetachAttrs — see
// detach_unix.go/detach_windows.go), with stdout+stderr redirected to
// daemon.log. Returns the registry record for the caller to save; a save
// failure after a successful spawn is reported accurately by RunE because
// the spawn and the save are two separate steps here.
func startNative(opts startOptions) (instanceRecord, error) {
	base, err := instancesBaseDir()
	if err != nil {
		return instanceRecord{}, err
	}
	dir := instanceDir(base, opts.name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return instanceRecord{}, fmt.Errorf("create instance directory %s: %w", dir, err)
	}
	logFile := filepath.Join(dir, "daemon.log")

	exe, err := os.Executable()
	if err != nil {
		return instanceRecord{}, fmt.Errorf("resolve overcast binary path: %w", err)
	}

	logHandle, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return instanceRecord{}, fmt.Errorf("open log file %s: %w", logFile, err)
	}
	// cmd.Start() duplicates this fd for the child (fork+exec on unix,
	// handle inheritance via CreateProcess on windows); our copy is not
	// needed once that has happened, whether or not Start succeeds.
	defer logHandle.Close() //nolint:errcheck

	cmd := exec.Command(exe, "serve", "--ui-port", strconv.Itoa(opts.uiPort)) //nolint:gosec // exe is os.Executable() (this binary); args are our own constructed flags, not attacker input.
	cmd.Env = buildChildEnv(opts)
	cmd.Stdout = logHandle
	cmd.Stderr = logHandle
	setDetachAttrs(cmd)

	if err := cmd.Start(); err != nil {
		return instanceRecord{}, fmt.Errorf("spawn overcast serve: %w", err)
	}
	// Detached: Release rather than Wait, so this process exiting doesn't
	// try to reap or wait on a child meant to keep running after it.
	_ = cmd.Process.Release()

	return buildInstanceRecord(opts, cmd.Process.Pid, logFile, time.Now().UTC()), nil
}

// buildInstanceRecord assembles the registry record for a freshly spawned
// native instance. Split out from startNative so record construction is
// testable without actually spawning a process.
func buildInstanceRecord(opts startOptions, pid int, logFile string, startedAt time.Time) instanceRecord {
	return instanceRecord{
		Name:      opts.name,
		Backend:   "native",
		PID:       pid,
		Port:      opts.port,
		UIPort:    opts.uiPort,
		Endpoint:  fmt.Sprintf("http://127.0.0.1:%d", opts.port), // 127.0.0.1, not localhost: this box has demonstrated a multi-second stall when a client resolves localhost to ::1 first against an IPv4-only listener.
		DataDir:   opts.dataDir,
		State:     opts.state,
		LogFile:   logFile,
		StartedAt: startedAt,
		Version:   version,
		Env:       opts.env,
	}
}

// buildChildEnv assembles the spawned daemon's environment: this process's
// own environment (so PATH, HOME, etc. are inherited normally), the
// resolved OVERCAST_PORT, the --state/--data-dir passthroughs when given,
// and the already allow-list-validated --env entries — applied last so they
// can override any of the above.
func buildChildEnv(opts startOptions) []string {
	env := os.Environ()
	env = append(env, "OVERCAST_PORT="+strconv.Itoa(opts.port))
	if opts.state != "" {
		env = append(env, "OVERCAST_STATE="+opts.state)
	}
	if opts.dataDir != "" {
		env = append(env, "OVERCAST_DATA_DIR="+opts.dataDir)
	}
	// Sorted for deterministic ordering — mainly so tests asserting on the
	// resulting slice aren't at the mercy of Go's randomized map iteration.
	names := make([]string, 0, len(opts.env))
	for k := range opts.env {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		env = append(env, k+"="+opts.env[k])
	}
	return env
}
