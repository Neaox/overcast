package main

// cmd_stop.go — `overcast stop` and `overcast restart`. Both operate on the
// instance registry (instances.go): stop asks the named instance's process
// to exit and removes its record; restart replays the same start using the
// record's saved configuration, so the arguments the original `overcast
// start` was given don't need to be remembered or re-typed.

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop [name]",
		Short: "Stop a background overcast instance",
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeRunningInstanceNames(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return stopInstance(cmd, instanceNameArg(args))
		},
	}
}

func newRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart [name]",
		Short: "Restart a background overcast instance with its saved configuration",
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeInstanceNames(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := instanceNameArg(args)

			rec, err := loadInstance(name)
			if err != nil {
				return err
			}
			if instanceRunning(rec) {
				if err := stopInstance(cmd, name); err != nil {
					return err
				}
			}

			noWait, _ := cmd.Flags().GetBool("no-wait")
			timeout, _ := cmd.Flags().GetDuration("timeout")

			newRec, err := startBackend(rec.Backend, startOptionsFromRecord(rec))
			if err != nil {
				return err
			}
			if err := saveInstance(newRec); err != nil {
				return fmt.Errorf("instance %q restarted but saving its registry record failed: %w", name, err)
			}

			printInstanceStarted(cmd, newRec)

			if noWait {
				return nil
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := waitForHealthy(ctx, newRec.Endpoint, 500*time.Millisecond); err != nil {
				return fmt.Errorf("instance %q did not become healthy within %s: %w — see %s for the daemon's own log", name, timeout, err, newRec.LogFile)
			}
			return nil
		},
	}
	cmd.Flags().Bool("no-wait", false, "return immediately instead of waiting for the instance to become healthy")
	cmd.Flags().Duration("timeout", 60*time.Second, "how long to wait for the instance to become healthy")
	return cmd
}

// instanceNameArg resolves the shared [name] positional argument stop and
// restart both take, defaulting to "default" like `overcast start --name`.
func instanceNameArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return "default"
}

// startOptionsFromRecord rebuilds the parameters `overcast start` was given
// from a saved record, so `overcast restart` reproduces the same instance
// rather than falling back to flag defaults. See instanceRecord.Env's doc
// comment (instances.go) for why secrets should not have been passed via
// --env in the first place — restart carries over exactly what was saved,
// nothing more.
//
// portsExplicit is set only when rec.Backend is "docker" (native ignores
// it): restart means to reproduce the exact same instance, including its
// port pair, so resolveDockerPorts (docker_backend.go) must error if either
// port is now busy rather than silently scanning for a fresh pair.
func startOptionsFromRecord(rec instanceRecord) startOptions {
	return startOptions{
		name:              rec.Name,
		port:              rec.Port,
		uiPort:            rec.UIPort,
		state:             rec.State,
		dataDir:           rec.DataDir,
		env:               rec.Env,
		portsExplicit:     rec.Backend == "docker",
		image:             rec.Image,
		dataVolume:        rec.DataVolume,
		mountDockerSocket: rec.MountDockerSocket,
	}
}

// stopInstance implements `overcast stop`'s body, shared with restart: look
// up the record, ask a running process/container to exit, and remove the
// record either way. Stopping an already-stopped instance is not an error;
// it just cleans up the stale record and says so, per the brief.
//
// Backend-specific from here: native asks the pid to exit gracefully then
// forces it (sendTerminate in detach_unix.go/detach_windows.go, below);
// docker is stopDockerInstance, just below this function.
func stopInstance(cmd *cobra.Command, name string) error {
	out := cmd.OutOrStdout()

	rec, err := loadInstance(name)
	if err != nil {
		return err
	}

	if !instanceRunning(rec) {
		if err := removeInstance(name); err != nil {
			return fmt.Errorf("instance %q was not running; removing its stale record failed: %w", name, err)
		}
		fmt.Fprintf(out, "instance %q was not running; removed its stale record\n", name)
		return nil
	}

	if rec.Backend == "docker" {
		return stopDockerInstance(out, name, rec)
	}

	proc, err := os.FindProcess(rec.PID)
	if err != nil {
		return fmt.Errorf("instance %q: %w", name, err)
	}
	if err := sendTerminate(proc); err != nil {
		return fmt.Errorf("instance %q: signal pid %d: %w", name, rec.PID, err)
	}
	waitForExit(rec, 10*time.Second)

	if instanceRunning(rec) {
		// Grace period elapsed without the process exiting — force it. On
		// Windows sendTerminate already called Kill(), so reaching here
		// means that somehow didn't take; on unix this is the SIGKILL that
		// follows an unheeded SIGTERM.
		_ = proc.Kill()
		waitForExit(rec, 10*time.Second)
	}

	if err := removeInstance(name); err != nil {
		return fmt.Errorf("instance %q stopped but removing its record failed: %w", name, err)
	}
	fmt.Fprintf(out, "instance %q stopped\n", name)
	return nil
}

// stopDockerInstance is stopInstance's "docker" branch: `docker stop`
// (default grace period — same as a bare `docker stop` with no --time), then
// `docker rm` it, since this backend created the container without --rm and
// so owns its removal (see docker_backend.go's file comment). Both go
// through the dockerRun exec seam. removeInstance always runs, even if the
// docker calls themselves failed, so a container that somehow can't be
// reached still doesn't leave a permanently-refusing "already running"
// record behind — the same "clean up the record regardless" behavior the
// native path has via its own force-kill fallback.
func stopDockerInstance(out io.Writer, name string, rec instanceRecord) error {
	stopErr, rmErr := error(nil), error(nil)
	if _, err := dockerRun("stop", rec.ContainerID); err != nil {
		stopErr = err
	} else if _, err := dockerRun("rm", rec.ContainerID); err != nil {
		rmErr = err
	}

	if err := removeInstance(name); err != nil {
		return fmt.Errorf("instance %q: stopping the container failed and removing its registry record also failed: %w", name, err)
	}
	switch {
	case stopErr != nil:
		return fmt.Errorf("instance %q: docker stop %s: %w (registry record removed anyway)", name, rec.ContainerID, stopErr)
	case rmErr != nil:
		return fmt.Errorf("instance %q: container stopped but docker rm %s failed: %w (registry record removed anyway)", name, rec.ContainerID, rmErr)
	}
	fmt.Fprintf(out, "instance %q stopped\n", name)
	return nil
}

// waitForExit polls instanceRunning until rec's process is gone or timeout
// elapses, whichever comes first.
func waitForExit(rec instanceRecord, timeout time.Duration) {
	const pollInterval = 100 * time.Millisecond
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && instanceRunning(rec) {
		time.Sleep(pollInterval)
	}
}
